package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// LiveBackend is the subset of live.Repo realtime needs for danmaku
// persistence, viewer-count maintenance, and ban enforcement. Defined
// as an interface here so we don't import live (which would re-import
// us and create a cycle).
type LiveBackend interface {
	InsertDanmaku(ctx context.Context, roomID, senderID int64, content, color string) error
	SetViewerCount(ctx context.Context, roomID int64, count int, bumpTotal bool) error
	IsBanned(ctx context.Context, roomID, userID int64) (banned, isKick bool, err error)
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

type Handler struct {
	issuer *auth.Issuer
	bus    *wsbus.Bus
	log    *slog.Logger
	live   LiveBackend // optional — nil disables persistence + ban check
	up     websocket.Upgrader

	// liveSubs[roomId] = set of userIDs currently subscribed to that
	// live room's danmaku channel. Guarded by liveMu. Cleared lazily
	// when a user disconnects (we keep stale entries; bus.Publish is a
	// no-op for absent users so it's harmless).
	liveMu   sync.Mutex
	liveSubs map[string]map[int64]struct{}
}

func NewHandler(issuer *auth.Issuer, bus *wsbus.Bus, log *slog.Logger, live LiveBackend) *Handler {
	return &Handler{
		issuer:   issuer,
		bus:      bus,
		log:      log,
		live:     live,
		liveSubs: make(map[string]map[int64]struct{}),
		up: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// MVP: allow all origins. Tighten before production.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (h *Handler) Register(r *gin.Engine) {
	r.GET("/ws", h.handle)
}

type clientEnvelope struct {
	MsgID   string          `json:"msgId"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// callPayload covers the `to` field for routing. The rest of the body
// (sdp, candidate, etc.) is re-emitted opaquely so the server stays
// signaling-agnostic.
type callPayload struct {
	To string `json:"to"`
}

type serverEnvelope struct {
	MsgID   string `json:"msgId,omitempty"`
	AckID   string `json:"ackId,omitempty"`
	Type    string `json:"type"`
	TS      int64  `json:"ts"`
	Payload any    `json:"payload,omitempty"`
}

func (h *Handler) handle(c *gin.Context) {
	tokenStr := c.Query("token")
	if tokenStr == "" {
		c.String(http.StatusUnauthorized, "missing token")
		return
	}
	claims, err := h.issuer.Parse(tokenStr)
	if err != nil {
		c.String(http.StatusUnauthorized, "invalid token")
		return
	}
	userID := claims.UserID

	conn, err := h.up.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Warn("ws upgrade failed", "err", err)
		return
	}
	h.log.Info("ws connected", "userID", userID, "ip", c.ClientIP())

	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	ch, unsub := h.bus.Subscribe(userID)
	defer unsub()

	stop := make(chan struct{})
	go h.readLoop(conn, userID, stop)
	h.writeLoop(conn, ch, stop)

	h.log.Info("ws disconnected", "userID", userID)
}

func (h *Handler) readLoop(conn *websocket.Conn, userID int64, stop chan struct{}) {
	defer close(stop)
	defer conn.Close()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			h.log.Info("ws read closed", "userID", userID, "err", err.Error())
			return
		}
		var env clientEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		switch env.Type {
		case "ping":
			h.bus.Publish(userID, wsbus.Event{Type: "pong", Payload: map[string]any{"ackId": env.MsgID}})
		case "call.invite", "call.accept", "call.reject", "call.signal", "call.end":
			h.relayCall(userID, env)
		case "live.subscribe":
			h.handleLiveSubscribe(userID, env)
		case "live.unsubscribe":
			h.handleLiveUnsubscribe(userID, env)
		case "live.danmaku.send":
			h.handleLiveDanmaku(userID, env)
		case "typing.start", "typing.stop":
			h.handleTyping(userID, env)
		default:
			// MVP: chat messages go over REST POST /api/v1/messages.
			// WS is push-only for chat. Ignore unknown control types.
		}
	}
}

// relayCall forwards a call control message from senderID to the user
// referenced by payload.to. The payload is augmented with `from` so the
// peer knows who's calling. Server has no concept of "call state".
func (h *Handler) relayCall(senderID int64, env clientEnvelope) {
	var route callPayload
	if err := json.Unmarshal(env.Payload, &route); err != nil || route.To == "" {
		return
	}
	toID, err := parseInt64(route.To)
	if err != nil || toID <= 0 {
		return
	}
	// Inject `from` into the payload so the receiver can answer.
	var asMap map[string]any
	if err := json.Unmarshal(env.Payload, &asMap); err != nil {
		return
	}
	asMap["from"] = senderIDString(senderID)
	h.bus.Publish(toID, wsbus.Event{Type: env.Type, Payload: asMap})
}

func parseInt64(s string) (int64, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("bad digit")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func senderIDString(id int64) string {
	return fmt.Sprintf("%d", id)
}

// --- live danmaku --------------------------------------------------
//
// Subscription model: a viewer client sends `live.subscribe {roomId}` when
// it opens a stream and `live.unsubscribe {roomId}` when it leaves. Any
// `live.danmaku.send {roomId, text}` is fanned out as `live.danmaku.recv`
// to every subscriber of that room.
//
// We also:
//   - count unique viewers per room (size of the per-room user set) and
//     broadcast `live.viewer.count` whenever it changes.
//   - persist danmaku to PG so late joiners can fetch recent history.
//   - check the room's ban list before accepting a danmaku.

type liveSubPayload struct {
	RoomID string `json:"roomId"`
}

type liveDanmakuSendPayload struct {
	RoomID string `json:"roomId"`
	Text   string `json:"text"`
	Color  string `json:"color,omitempty"`
}

func (h *Handler) handleLiveSubscribe(userID int64, env clientEnvelope) {
	var p liveSubPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
		return
	}
	h.liveMu.Lock()
	if h.liveSubs[p.RoomID] == nil {
		h.liveSubs[p.RoomID] = make(map[int64]struct{})
	}
	_, alreadySubscribed := h.liveSubs[p.RoomID][userID]
	h.liveSubs[p.RoomID][userID] = struct{}{}
	count := len(h.liveSubs[p.RoomID])
	targets := h.snapshotSubsLocked(p.RoomID)
	h.liveMu.Unlock()

	h.persistViewerCount(p.RoomID, count, !alreadySubscribed)
	h.broadcastViewerCount(targets, p.RoomID, count)
}

func (h *Handler) handleLiveUnsubscribe(userID int64, env clientEnvelope) {
	var p liveSubPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
		return
	}
	h.liveMu.Lock()
	subs, ok := h.liveSubs[p.RoomID]
	if !ok {
		h.liveMu.Unlock()
		return
	}
	_, wasSubscribed := subs[userID]
	delete(subs, userID)
	count := len(subs)
	if count == 0 {
		delete(h.liveSubs, p.RoomID)
	}
	targets := h.snapshotSubsLocked(p.RoomID)
	h.liveMu.Unlock()

	if wasSubscribed {
		h.persistViewerCount(p.RoomID, count, false)
		h.broadcastViewerCount(targets, p.RoomID, count)
	}
}

func (h *Handler) handleLiveDanmaku(userID int64, env clientEnvelope) {
	var p liveDanmakuSendPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
		return
	}
	text := p.Text
	if len(text) == 0 || len(text) > 200 {
		return // skip empty / oversized; full sanitisation is a later concern
	}

	// Ban check before doing any work.
	if h.live != nil {
		if roomID, err := strconv.ParseInt(p.RoomID, 10, 64); err == nil {
			banned, _, _ := h.live.IsBanned(context.Background(), roomID, userID)
			if banned {
				h.bus.Publish(userID, wsbus.Event{
					Type:    "live.danmaku.rejected",
					Payload: map[string]any{"roomId": p.RoomID, "reason": "banned"},
				})
				return
			}
			// Persist (best effort — failure doesn't block broadcast).
			_ = h.live.InsertDanmaku(context.Background(), roomID, userID, text, p.Color)
		}
	}

	h.liveMu.Lock()
	targets := h.snapshotSubsLocked(p.RoomID)
	h.liveMu.Unlock()

	payload := map[string]any{
		"roomId":   p.RoomID,
		"text":     text,
		"color":    p.Color,
		"senderId": fmt.Sprintf("%d", userID),
		"ts":       time.Now().UnixMilli(),
	}
	for _, uid := range targets {
		if uid == userID {
			// Sender does its own optimistic render; skip echo to avoid dupes.
			continue
		}
		h.bus.Publish(uid, wsbus.Event{Type: "live.danmaku.recv", Payload: payload})
	}
}

// =============== Typing indicator (forward-only) ===============
//
// Clients send {type:"typing.start"|"typing.stop", conversationId, recipientIds[]}
// and we fan out to each recipient. We don't auto-look-up conversation
// membership server-side — the client already knows it (friend pair /
// group members / channel viewers). Trust is OK because typing is a
// pure UX hint with no security implication; a malicious client at
// worst spams "X is typing" to people they could already message anyway.

type typingPayload struct {
	ConversationID string   `json:"conversationId"`
	RecipientIDs   []string `json:"recipientIds"`
}

func (h *Handler) handleTyping(userID int64, env clientEnvelope) {
	var p typingPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.ConversationID == "" {
		return
	}
	payload := map[string]any{
		"conversationId": p.ConversationID,
		"senderId":       fmt.Sprintf("%d", userID),
		"ts":             time.Now().UnixMilli(),
	}
	for _, ridStr := range p.RecipientIDs {
		rid, err := strconv.ParseInt(ridStr, 10, 64)
		if err != nil || rid == userID {
			continue
		}
		h.bus.Publish(rid, wsbus.Event{Type: env.Type, Payload: payload})
	}
}

// LiveViewerIDs returns the set of user IDs currently subscribed to the
// room's danmaku channel — used by live.viewerList to render "who's
// watching". Snapshot copies under the mutex so the caller can iterate
// safely.
func (h *Handler) LiveViewerIDs(roomID string) []int64 {
	h.liveMu.Lock()
	defer h.liveMu.Unlock()
	return h.snapshotSubsLocked(roomID)
}

// snapshotSubsLocked returns a slice copy of subscriber IDs for p.RoomID.
// Caller MUST hold h.liveMu.
func (h *Handler) snapshotSubsLocked(roomID string) []int64 {
	subs := h.liveSubs[roomID]
	out := make([]int64, 0, len(subs))
	for uid := range subs {
		out = append(out, uid)
	}
	return out
}

func (h *Handler) persistViewerCount(roomIDStr string, count int, bumpTotal bool) {
	if h.live == nil {
		return
	}
	roomID, err := strconv.ParseInt(roomIDStr, 10, 64)
	if err != nil {
		return
	}
	_ = h.live.SetViewerCount(context.Background(), roomID, count, bumpTotal)
}

func (h *Handler) broadcastViewerCount(targets []int64, roomID string, count int) {
	payload := map[string]any{"roomId": roomID, "count": count}
	for _, uid := range targets {
		h.bus.Publish(uid, wsbus.Event{Type: "live.viewer.count", Payload: payload})
	}
}

func (h *Handler) writeLoop(conn *websocket.Conn, ch <-chan wsbus.Event, stop chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer conn.Close()

	for {
		select {
		case <-stop:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			env := serverEnvelope{Type: ev.Type, TS: time.Now().UnixMilli(), Payload: ev.Payload}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(env); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
