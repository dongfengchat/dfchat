package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
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

// RelayBackend gates peer-to-peer WS-relayed messages (WebRTC signaling
// and typing). Returns true if `from` is allowed to send a relayed
// message to `to`. Implementation should check: shared friendship, or
// shared group membership. Keeps WebRTC sessions from being initiated
// by arbitrary accounts (spam) and stops typing-state leakage to
// strangers.
type RelayBackend interface {
	CanRelay(ctx context.Context, from, to int64) (bool, error)
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second

	// Hard caps to keep one user / one bad client from exhausting server
	// resources. Tuned generous-but-not-stupid for a desktop chat client
	// that typically maintains 1 connection + a handful of subscribed
	// live rooms.
	maxConnsPerUser  = 5    // Electron + multi-device + a couple reconnect-laggers
	maxRoomsPerConn  = 50   // far more than anyone realistically watches
	wsInboundRPS     = 20.0 // sustained inbound messages per second
	wsInboundBurst   = 40   // brief spike allowance
	wsReadLimitBytes = 64 * 1024

	// Close codes for graceful client UX.
	closeCodeTokenExpired = 4401
	closeCodeTooManyConns = 4429
)

type Handler struct {
	issuer         *auth.Issuer
	bus            *wsbus.Bus
	log            *slog.Logger
	live           LiveBackend  // optional — nil disables persistence + ban check
	relay          RelayBackend // optional — nil disables peer-relay gating (dev)
	allowedOrigins []string     // exact-match list; "*" allows all
	up             websocket.Upgrader

	// liveSubs[roomId] = set of userIDs currently subscribed. Guarded
	// by liveMu. Cleaned on disconnect via the per-conn rooms set.
	liveMu   sync.Mutex
	liveSubs map[string]map[int64]struct{}

	// Per-user connection accounting for the cap.
	connMu    sync.Mutex
	connCount map[int64]int
}

func NewHandler(issuer *auth.Issuer, bus *wsbus.Bus, log *slog.Logger, live LiveBackend, relay RelayBackend, allowedOrigins []string) *Handler {
	h := &Handler{
		issuer:         issuer,
		bus:            bus,
		log:            log,
		live:           live,
		relay:          relay,
		allowedOrigins: allowedOrigins,
		liveSubs:       make(map[string]map[int64]struct{}),
		connCount:      make(map[int64]int),
	}
	h.up = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     h.checkOrigin,
	}
	return h
}

// checkOrigin enforces an allowlist on the WebSocket upgrade. Without
// this, a malicious web page could open a WS as a victim's authenticated
// browser (cross-site WebSocket hijacking). Electron's renderer process
// uses `file://` origin so we explicitly accept that. The allowlist
// matches our HTTP CORS config plus the Electron origin.
func (h *Handler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients (curl, Go tests) don't send Origin; we
		// trust those because they don't have ambient-credential drives.
		return true
	}
	for _, allowed := range h.allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	// Always accept the Electron renderer's null/file origin.
	if origin == "null" || strings.HasPrefix(origin, "file://") {
		return true
	}
	return false
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

// conn wraps a single websocket connection plus its per-connection
// bookkeeping (rate limiter, subscribed rooms, token expiry). Each
// connection has exactly one read goroutine and one write goroutine.
type conn struct {
	ws         *websocket.Conn
	userID     int64
	expiresAt  time.Time
	rooms      map[string]struct{} // rooms this conn has subscribed to
	roomsMu    sync.Mutex
	limiter    *rate.Limiter
	closed     atomic.Bool
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
	expiresAt := claims.ExpiresAt.Time

	// Enforce per-user connection cap BEFORE upgrade so we don't waste
	// upgrade-cost on a connection we'll immediately reject.
	if !h.acquireConnSlot(userID) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"code":    42910,
			"message": "too many active connections for this account",
		})
		return
	}

	ws, err := h.up.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.releaseConnSlot(userID)
		h.log.Warn("ws upgrade failed", "err", err)
		return
	}
	h.log.Info("ws connected", "userID", userID, "ip", c.ClientIP())

	ws.SetReadLimit(wsReadLimitBytes)
	_ = ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	cn := &conn{
		ws:        ws,
		userID:    userID,
		expiresAt: expiresAt,
		rooms:     make(map[string]struct{}),
		limiter:   rate.NewLimiter(rate.Limit(wsInboundRPS), wsInboundBurst),
	}

	ch, unsub := h.bus.Subscribe(userID)

	// Ensure full cleanup whatever path the connection dies on.
	defer func() {
		cn.closed.Store(true)
		unsub()
		h.cleanupConnRooms(cn)
		h.releaseConnSlot(userID)
		_ = ws.Close()
		h.log.Info("ws disconnected", "userID", userID)
	}()

	stop := make(chan struct{})
	go h.readLoop(cn, stop)
	h.writeLoop(cn, ch, stop)
}

// acquireConnSlot tries to register one more connection for userID,
// rejecting if they're already at the per-user cap. Returns false on
// rejection.
func (h *Handler) acquireConnSlot(userID int64) bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[userID] >= maxConnsPerUser {
		return false
	}
	h.connCount[userID]++
	return true
}

func (h *Handler) releaseConnSlot(userID int64) {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[userID] > 0 {
		h.connCount[userID]--
		if h.connCount[userID] == 0 {
			delete(h.connCount, userID)
		}
	}
}

// cleanupConnRooms removes this connection's user from every live-room
// subscriber set it joined. Without this, leaving / crashing clients
// keep inflating the viewer count of rooms they're no longer watching,
// and the room map grows monotonically.
func (h *Handler) cleanupConnRooms(cn *conn) {
	cn.roomsMu.Lock()
	rooms := make([]string, 0, len(cn.rooms))
	for r := range cn.rooms {
		rooms = append(rooms, r)
	}
	cn.rooms = nil
	cn.roomsMu.Unlock()

	for _, roomID := range rooms {
		// Reuse the unsubscribe path so viewer-count is broadcast
		// correctly to remaining subscribers.
		h.removeLiveSub(cn.userID, roomID)
	}
}

func (h *Handler) removeLiveSub(userID int64, roomID string) {
	h.liveMu.Lock()
	subs, ok := h.liveSubs[roomID]
	if !ok {
		h.liveMu.Unlock()
		return
	}
	_, wasSubscribed := subs[userID]
	delete(subs, userID)
	count := len(subs)
	if count == 0 {
		delete(h.liveSubs, roomID)
	}
	targets := h.snapshotSubsLocked(roomID)
	h.liveMu.Unlock()

	if wasSubscribed {
		h.persistViewerCount(roomID, count, false)
		h.broadcastViewerCount(targets, roomID, count)
	}
}

func (h *Handler) readLoop(cn *conn, stop chan struct{}) {
	defer close(stop)
	for {
		_, raw, err := cn.ws.ReadMessage()
		if err != nil {
			h.log.Info("ws read closed", "userID", cn.userID, "err", err.Error())
			return
		}
		// Per-connection inbound rate limit. Blocks scripted flooders
		// from amplifying via fan-out (typing / danmaku) and keeps the
		// readLoop responsive for legitimate clients.
		if !cn.limiter.Allow() {
			// Don't drop the connection — just skip this frame. A
			// flood will hit it repeatedly; eventually the client
			// either backs off or the OS-level write timeout closes us.
			continue
		}
		var env clientEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		switch env.Type {
		case "ping":
			h.bus.Publish(cn.userID, wsbus.Event{Type: "pong", Payload: map[string]any{"ackId": env.MsgID}})
		case "call.invite", "call.accept", "call.reject", "call.signal", "call.end":
			h.relayCall(cn.userID, env)
		case "live.subscribe":
			h.handleLiveSubscribe(cn, env)
		case "live.unsubscribe":
			h.handleLiveUnsubscribe(cn, env)
		case "live.danmaku.send":
			h.handleLiveDanmaku(cn.userID, env)
		case "typing.start", "typing.stop":
			h.handleTyping(cn.userID, env)
		default:
			// Chat messages go over REST POST /api/v1/messages.
			// WS is push-only for chat. Ignore unknown control types.
		}
	}
}

// relayCall forwards a call control message from senderID to the user
// referenced by payload.to. The payload is augmented with `from` so the
// peer knows who's calling. Gated on the RelayBackend's CanRelay check
// to prevent unsolicited WebRTC signaling from strangers — without it,
// any authenticated account could spam call-invites at any other id.
func (h *Handler) relayCall(senderID int64, env clientEnvelope) {
	var route callPayload
	if err := json.Unmarshal(env.Payload, &route); err != nil || route.To == "" {
		return
	}
	toID, err := parseInt64(route.To)
	if err != nil || toID <= 0 {
		return
	}
	if h.relay != nil {
		ok, _ := h.relay.CanRelay(context.Background(), senderID, toID)
		if !ok {
			// Bounce back to the caller so the client can surface
			// "you can only call friends" rather than silent timeout.
			h.bus.Publish(senderID, wsbus.Event{
				Type:    "call.rejected",
				Payload: map[string]any{"to": route.To, "reason": "not_allowed"},
			})
			return
		}
	}
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

type liveSubPayload struct {
	RoomID string `json:"roomId"`
}

type liveDanmakuSendPayload struct {
	RoomID string `json:"roomId"`
	Text   string `json:"text"`
	Color  string `json:"color,omitempty"`
}

func (h *Handler) handleLiveSubscribe(cn *conn, env clientEnvelope) {
	var p liveSubPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
		return
	}

	// Per-connection cap on the size of the rooms set. Stops a malicious
	// client from stuffing the in-memory map with millions of fake room
	// ids to balloon memory + broadcast fanout.
	cn.roomsMu.Lock()
	if len(cn.rooms) >= maxRoomsPerConn {
		cn.roomsMu.Unlock()
		return
	}
	_, already := cn.rooms[p.RoomID]
	cn.rooms[p.RoomID] = struct{}{}
	cn.roomsMu.Unlock()

	h.liveMu.Lock()
	if h.liveSubs[p.RoomID] == nil {
		h.liveSubs[p.RoomID] = make(map[int64]struct{})
	}
	_, alreadySubscribed := h.liveSubs[p.RoomID][cn.userID]
	h.liveSubs[p.RoomID][cn.userID] = struct{}{}
	count := len(h.liveSubs[p.RoomID])
	targets := h.snapshotSubsLocked(p.RoomID)
	h.liveMu.Unlock()

	// Only persist + broadcast on the first sub from this user across
	// any of their connections — saves a write per noisy reconnect.
	if !already && !alreadySubscribed {
		h.persistViewerCount(p.RoomID, count, true)
		h.broadcastViewerCount(targets, p.RoomID, count)
	}
}

func (h *Handler) handleLiveUnsubscribe(cn *conn, env clientEnvelope) {
	var p liveSubPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
		return
	}
	cn.roomsMu.Lock()
	delete(cn.rooms, p.RoomID)
	cn.roomsMu.Unlock()
	h.removeLiveSub(cn.userID, p.RoomID)
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
			continue
		}
		h.bus.Publish(uid, wsbus.Event{Type: "live.danmaku.recv", Payload: payload})
	}
}

// =============== Typing indicator (forward-only) ===============
//
// Typing pings are gated on the same RelayBackend the WebRTC code uses
// (friend or shared-group), so a malicious client can't poke "X is
// typing" at arbitrary user ids. Without that gate the typing channel
// becomes a casual user-id existence oracle.

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
		if h.relay != nil {
			ok, _ := h.relay.CanRelay(context.Background(), userID, rid)
			if !ok {
				continue
			}
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

func (h *Handler) writeLoop(cn *conn, ch <-chan wsbus.Event, stop chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	// Token-expiry timer: when the access token's exp passes, we close
	// the socket with a distinct 4401 close code. The client knows to
	// refresh + reopen rather than tight-looping on the same dead token.
	// Skip the timer if exp is zero (shouldn't happen — defensive).
	var expireC <-chan time.Time
	if !cn.expiresAt.IsZero() {
		d := time.Until(cn.expiresAt)
		if d < 0 {
			d = 0
		}
		t := time.NewTimer(d)
		defer t.Stop()
		expireC = t.C
	}

	for {
		select {
		case <-stop:
			return
		case <-expireC:
			h.log.Info("ws token expired, closing", "userID", cn.userID)
			_ = cn.ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(closeCodeTokenExpired, "token expired, please refresh"),
				time.Now().Add(time.Second))
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			env := serverEnvelope{Type: ev.Type, TS: time.Now().UnixMilli(), Payload: ev.Payload}
			_ = cn.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cn.ws.WriteJSON(env); err != nil {
				// Slow / dead consumer — drop the connection so the
				// publisher channel can drain rather than back-pressuring
				// other broadcasts (e.g. live danmaku fan-out).
				return
			}
		case <-ticker.C:
			_ = cn.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cn.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
