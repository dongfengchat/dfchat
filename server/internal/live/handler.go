package live

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
)

// ViewerSource lets the live handler ask realtime "who's watching room X
// right now" without importing the realtime package. *realtime.Handler
// satisfies this (it owns the per-room subscriber set anyway).
type ViewerSource interface {
	LiveViewerIDs(roomID string) []int64
}

type Handler struct {
	repo        *Repo
	issuer      *auth.Issuer
	bus         *wsbus.Bus   // fan-out go-live / room-updated to followers + viewers
	viewers     ViewerSource // nil-safe; nil disables /viewers endpoint
	rtmpURL     string       // e.g. rtmp://dfchat.chat/live   (shown to streamer)
	hlsURL      string       // e.g. https://dfchat.chat/hls    (shown to viewer)
	srsSecret   string       // shared secret used by SRS hooks (configured in srs.conf)
	srsInternal string       // internal SRS HTTP base, e.g. http://srs:8080 — used to fetch raw m3u8
}

func NewHandler(repo *Repo, issuer *auth.Issuer, bus *wsbus.Bus, rtmpURL, hlsURL, srsSecret, srsInternal string) *Handler {
	return &Handler{repo: repo, issuer: issuer, bus: bus, rtmpURL: rtmpURL, hlsURL: hlsURL, srsSecret: srsSecret, srsInternal: srsInternal}
}

// AttachViewerSource is called from main.go *after* realtime is built — the
// live handler is constructed first because realtime needs liveRepo as
// its LiveBackend, so we wire the back-reference after both exist.
func (h *Handler) AttachViewerSource(v ViewerSource) { h.viewers = v }

func (h *Handler) Register(rg *gin.RouterGroup) {
	// Public — only the listings + non-test-room detail. Anything that
	// touches a SPECIFIC room (viewers, danmaku history, recordings)
	// used to be public and leaked test-mode privacy; now they all go
	// through an auth-gated handler that also re-checks is_test.
	rg.GET("/live/rooms", h.listLive)
	rg.GET("/live/scheduled", h.listScheduled)
	rg.GET("/live/rooms/:id", h.publicDetail)

	// Authed — anything per-room beyond the public detail.
	g := rg.Group("/live")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("/rooms/:id/recordings", h.recordings)
	g.GET("/rooms/:id/danmaku", h.recentDanmaku)
	g.GET("/rooms/:id/viewers", h.viewerList)

	// Owner-only writes.
	g.POST("/rooms", h.create)
	g.GET("/rooms/:id/owner", h.ownerDetail) // includes stream key + URLs
	g.PATCH("/rooms/:id", h.update)
	g.PATCH("/rooms/:id/visibility", h.setVisibility) // 试播 ↔ 公开
	g.PATCH("/rooms/:id/schedule", h.setSchedule)     // 直播预告
	g.POST("/rooms/:id/rotate-key", h.rotateKey)
	g.POST("/rooms/:id/stop", h.stopLive) // owner force-stop
	g.PATCH("/rooms/:id/chat-settings", h.updateChatSettings)
	g.POST("/rooms/:id/pin-danmaku", h.pinDanmaku)
	g.DELETE("/rooms/:id/pin-danmaku", h.unpinDanmaku)
	g.DELETE("/rooms/:id", h.deleteRoom)
	g.GET("/mine", h.listMine)

	// Followers (any authed user)
	g.POST("/rooms/:id/follow", h.follow)
	g.DELETE("/rooms/:id/follow", h.unfollow)
	g.GET("/rooms/:id/follow", h.followStatus)

	// Bans (owner moderates)
	g.POST("/rooms/:id/bans", h.banUser)
	g.DELETE("/rooms/:id/bans/:userId", h.unbanUser)

	// Recordings (owner deletes)
	g.DELETE("/recordings/:recId", h.deleteRecording)

	// SRS HTTP hooks — protected by:
	//   1. nginx /api/v1/live/srs-hook/ allowlist (docker bridge IPs only)
	//   2. constant-time shared-secret path comparison (LIVE_SRS_SECRET)
	// Outside callers should always get 404 at nginx; the handler is a
	// belt-and-braces.
	rg.POST("/live/srs-hook/:secret", h.srsHook)

	// Internal HLS auth + playlist proxy. Reachable only via nginx
	// auth_request / proxy_pass from the public /hls/ block; nginx
	// restricts the path so external callers never see it. No JWT.
	rg.GET("/live/play-auth", h.playAuth)
	rg.GET("/live/play-m3u8", h.playM3U8)
}

// accessRoomForAuthed loads the room and refuses if it's test-mode and
// the caller isn't the owner. Centralizes the "private rooms shouldn't
// leak via per-room endpoints" check. Returns the room on success or
// writes an error response and returns nil.
func (h *Handler) accessRoomForAuthed(c *gin.Context, callerID, roomID int64) *Room {
	rm, err := h.repo.FindByID(c.Request.Context(), roomID)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return nil
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return nil
	}
	if rm.IsTest && rm.OwnerID != callerID {
		// Same "pretend it doesn't exist" treatment as publicDetail so
		// test-mode existence isn't probable by id-enumeration.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return nil
	}
	return rm
}

// --- public ---

func (h *Handler) listLive(c *gin.Context) {
	// Optional filters for Discover search / category pills. Empty
	// values disable each filter independently. Limit defaults to 30
	// (capped at 100 server-side) so a malicious client can't ask for
	// a million rooms in one go.
	limit, _ := strconv.Atoi(c.Query("limit"))
	rooms, err := h.repo.ListLive(c.Request.Context(), ListLiveFilter{
		Q:        c.Query("q"),
		Category: c.Query("category"),
		Limit:    limit,
	})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": rooms, "hlsBase": h.hlsURL})
}

func (h *Handler) publicDetail(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	rm, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Test-mode rooms are private — pretend they don't exist on the public
	// endpoint. The owner has their own /rooms/:id/owner endpoint to peek
	// at their own preview stream.
	if rm.IsTest {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	// Compute the playback URL first (it needs the key), then strip the
	// key from the response. The key still leaks via the URL itself, but
	// that's how HLS works — segment URLs reference it. Hardening that
	// would need on_play hooks + signed segment tokens, future work.
	playbackURL := h.playbackURLFor(rm)
	rm.StreamKey = ""
	c.JSON(http.StatusOK, gin.H{
		"room":        rm,
		"playbackUrl": playbackURL,
	})
}

func (h *Handler) recordings(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if h.accessRoomForAuthed(c, uid, id) == nil {
		return
	}
	recs, err := h.repo.Recordings(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recordings": recs})
}

// --- owner ---

type createReq struct {
	Title    string `json:"title"`
	Category string `json:"category"`
}

// maxRoomsPerOwner caps how many live rooms a single account can own
// at once. Without this, one user can create thousands of rooms (each
// gets a stream key, a follower table entry on join, scheduled-reminder
// fanout potential) — both spam vector and disk footprint. 10 is
// generous (legit creators rarely keep more than 3-4 stale rooms).
const maxRoomsPerOwner = 10

func (h *Handler) create(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req createReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80020, "message": "invalid body"})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len(title) > 128 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80021, "message": "title 1-128 chars"})
		return
	}
	if cat := strings.TrimSpace(req.Category); len(cat) > 32 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80027, "message": "category too long"})
		return
	}
	// Per-owner concurrent room cap. ListMine is the source of truth
	// for "rooms this user owns" — we count them rather than keep a
	// counter, since rooms get deleted and we don't want drift.
	mine, err := h.repo.ListMine(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if len(mine) >= maxRoomsPerOwner {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 80029, "message": fmt.Sprintf("最多 %d 个直播间，请先删除一些旧的", maxRoomsPerOwner)})
		return
	}
	rm, err := h.repo.Create(c.Request.Context(), uid, title, strings.TrimSpace(req.Category))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"room":        rm,
		"rtmpUrl":     fmt.Sprintf("%s/%s", h.rtmpURL, rm.StreamKey),
		"playbackUrl": h.playbackURLFor(rm),
	})
}

// isOurMediaURL enforces that user-provided media URLs (cover, avatar)
// resolve to our own MinIO public host. Stops an owner from setting a
// cover_url like https://evil.example/track.gif which would silently
// beacon every viewer's IP / user-agent to a third party.
func isOurMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	// Accept files.dfchat.chat (prod) and any localhost-y host (dev).
	host := strings.ToLower(u.Hostname())
	if host == "files.dfchat.chat" {
		return true
	}
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "minio") {
		return true
	}
	return false
}

// ownerDetail returns the full room incl. stream key + push/pull URLs.
// Only the owner of the room may call it.
func (h *Handler) ownerDetail(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	rm, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if rm.OwnerID != uid {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"room":        rm,
		"rtmpUrl":     fmt.Sprintf("%s/%s", h.rtmpURL, rm.StreamKey),
		"playbackUrl": h.playbackURLFor(rm),
	})
}

type updateReq struct {
	Title    *string `json:"title"`
	Category *string `json:"category"`
	CoverURL *string `json:"coverUrl"`
}

func (h *Handler) update(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req updateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || len(t) > 128 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80021, "message": "title 1-128 chars"})
			return
		}
		req.Title = &t
	}
	if req.Category != nil && len(*req.Category) > 32 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80027, "message": "category too long"})
		return
	}
	// Cover URL must point at our public MinIO host so it can't be used
	// as a tracker pixel pointing at attacker infra. Empty / null is
	// fine (removes the cover).
	if req.CoverURL != nil && *req.CoverURL != "" {
		if !isOurMediaURL(*req.CoverURL) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80028, "message": "封面图必须来自 files.dfchat.chat"})
			return
		}
	}
	rm, err := h.repo.UpdateMeta(c.Request.Context(), id, uid, req.Title, req.Category, req.CoverURL)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastRoomUpdated(c.Request.Context(), rm)
	c.JSON(http.StatusOK, gin.H{"room": rm})
}

type visibilityReq struct {
	// IsTest=true → host-only preview, hidden from discover.
	// IsTest=false → public, appears in /live/rooms.
	IsTest *bool `json:"isTest"`
}

func (h *Handler) setVisibility(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req visibilityReq
	if err := c.ShouldBindJSON(&req); err != nil || req.IsTest == nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80023, "message": "isTest (bool) required"})
		return
	}
	rm, err := h.repo.SetVisibility(c.Request.Context(), id, uid, *req.IsTest)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastRoomUpdated(c.Request.Context(), rm)
	c.JSON(http.StatusOK, gin.H{"room": rm})
}

func (h *Handler) rotateKey(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	newKey, err := h.repo.RotateStreamKey(c.Request.Context(), id, uid)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"streamKey": newKey,
		"rtmpUrl":   fmt.Sprintf("%s/%s", h.rtmpURL, newKey),
	})
}

func (h *Handler) deleteRoom(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id, uid); err != nil {
		if errors.Is(err, ErrNotOwner) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Tell any active viewers their room is gone so the player tears
	// down + the danmaku WS unsubscribes cleanly. The realtime handler
	// already cleans liveSubs on disconnect, but viewers still on the
	// page would otherwise keep polling HLS for a 404.
	h.broadcastRoomDeleted(c.Request.Context(), id)
	c.Status(http.StatusNoContent)
}

// stopLive lets the owner manually mark a broadcast ended without
// relying on SRS to notice the publisher disappeared. Critical for
// "OBS crashed silently" — without this the room stays status=1
// (live) forever and shows up in Discover with viewer_count stale.
//
// Doesn't actually disconnect the SRS publisher (we'd need to call
// SRS HTTP API for that, future work). Just flips the DB state and
// fans out a "live ended" event. If SRS still has a publisher pushing,
// the next on_publish hook will re-set status=1 — which is fine, it
// just means the broadcast continues and the manual stop was a no-op.
func (h *Handler) stopLive(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	rm, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if rm.OwnerID != uid {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	_ = h.repo.SetEnded(c.Request.Context(), id)
	_, _ = h.repo.ReleasePublisher(c.Request.Context(), id) // rotate key
	if h.bus != nil && !rm.IsTest {
		h.notifyFollowersOffline(c.Request.Context(), rm)
	}
	c.Status(http.StatusNoContent)
}

// === Tier-C chat moderation: settings + pinned danmaku ============

type chatSettingsReq struct {
	SlowModeSeconds     *int  `json:"slowModeSeconds"`
	ChatSubscribersOnly *bool `json:"chatSubscribersOnly"`
}

// updateChatSettings flips slow-mode and subscriber-only in one PATCH.
// Owner-only. Both fields are optional; nil = leave as-is.
func (h *Handler) updateChatSettings(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req chatSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	// Server-side clamp to match the migration's CHECK; gives a clear
	// 400 instead of a SQL error from the DB.
	if req.SlowModeSeconds != nil {
		s := *req.SlowModeSeconds
		if s < 0 || s > 300 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80040, "message": "slowModeSeconds must be 0..300"})
			return
		}
	}
	rm, err := h.repo.UpdateChatSettings(c.Request.Context(), id, uid, req.SlowModeSeconds, req.ChatSubscribersOnly)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastRoomUpdated(c.Request.Context(), rm)
	c.JSON(http.StatusOK, gin.H{"room": rm})
}

type pinDanmakuReq struct {
	Text     string `json:"text"`
	Color    string `json:"color"`
	SenderID string `json:"senderId"` // optional; defaults to caller (owner is pinning their own message)
}

// pinDanmaku snapshots a single message into the room metadata so late
// joiners see it at the top of the chat. Replaces any existing pinned
// danmaku. Empty text via DELETE endpoint clears it.
func (h *Handler) pinDanmaku(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req pinDanmakuReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80041, "message": "text required"})
		return
	}
	if len([]rune(text)) > 200 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80042, "message": "text too long (max 200)"})
		return
	}
	senderID := uid
	if req.SenderID != "" {
		if v, err := strconv.ParseInt(req.SenderID, 10, 64); err == nil && v > 0 {
			senderID = v
		}
	}
	rm, err := h.repo.PinDanmaku(c.Request.Context(), id, uid, text, req.Color, senderID)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastRoomUpdated(c.Request.Context(), rm)
	c.JSON(http.StatusOK, gin.H{"room": rm})
}

// unpinDanmaku clears the pinned-danmaku snapshot on the room.
func (h *Handler) unpinDanmaku(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	rm, err := h.repo.PinDanmaku(c.Request.Context(), id, uid, "", "", uid)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastRoomUpdated(c.Request.Context(), rm)
	c.Status(http.StatusNoContent)
}

// broadcastRoomDeleted fans a one-shot "this room is gone" event to
// every currently-subscribed viewer (per the in-memory WS subscriber
// set). Used by deleteRoom — also fine to be called pre-emptively
// from any other tear-down path (e.g. SRS reconcile if a room's
// stream key looks orphaned).
func (h *Handler) broadcastRoomDeleted(_ context.Context, roomID int64) {
	if h.bus == nil || h.viewers == nil {
		return
	}
	roomKey := strconv.FormatInt(roomID, 10)
	viewers := h.viewers.LiveViewerIDs(roomKey)
	payload := map[string]any{"roomId": roomKey}
	for _, uid := range viewers {
		h.bus.Publish(uid, wsbus.Event{Type: "live.room.deleted", Payload: payload})
	}
}

func (h *Handler) listMine(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	rooms, err := h.repo.ListMine(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

// --- SRS hooks ---
//
// SRS posts JSON like:
//   {"action":"on_publish","client_id":"...","ip":"x.x.x.x",
//    "vhost":"__defaultVhost__","app":"live","stream":"<stream_key>",
//    "param":"?...","server_id":"..."}
//
// We respond with HTTP 200 + body "0" to accept, anything else to reject.
// See https://ossrs.io/lts/en-us/docs/v5/doc/http-callback

type srsHookPayload struct {
	Action   string `json:"action"`
	App      string `json:"app"`
	Stream   string `json:"stream"`
	ClientID string `json:"client_id"`
	IP       string `json:"ip"`
	Param    string `json:"param"`
	File     string `json:"file"` // on_dvr only
}

func (h *Handler) srsHook(c *gin.Context) {
	// Constant-time secret compare — Param() returns user input and
	// `!=` short-circuits, leaking a tiny timing signal. subtle.
	// ConstantTimeCompare needs equal-length slices, so check length
	// first.
	got := c.Param("secret")
	if len(got) != len(h.srsSecret) || subtle.ConstantTimeCompare([]byte(got), []byte(h.srsSecret)) != 1 {
		c.String(http.StatusUnauthorized, "1")
		return
	}
	var p srsHookPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.String(http.StatusBadRequest, "1")
		return
	}

	// Transcoded variants (`<key>_ld` 480p, `<key>_md` ~720p) are SRS
	// pushing back into itself for the bitrate ladder. We must approve
	// or the transcoder loops can't establish — BUT we still verify
	// the base stream key exists in our DB. Without this check, a
	// malicious RTMP publisher can push to `arbitrary_ld` and SRS
	// approves the relay (transcoded variants don't go through DB
	// lookup in the old code), giving anyone free CDN hosting +
	// publishable HLS at /hls/arbitrary_ld.m3u8.
	stream := p.Stream
	if base, ok := strings.CutSuffix(stream, "_ld"); ok {
		stream = base
	} else if base, ok := strings.CutSuffix(stream, "_md"); ok {
		stream = base
	}
	if stream != p.Stream {
		// Transcoded variant — verify the base key is real, then approve.
		if _, err := h.repo.FindByStreamKey(c.Request.Context(), stream); err != nil {
			c.String(http.StatusForbidden, "1")
			return
		}
		c.String(http.StatusOK, "0")
		return
	}

	switch p.Action {
	case "on_publish":
		// Atomically bind the stream key to this SRS client_id. If the
		// slot is already taken by another client we reject — second
		// publisher with a leaked key gets "publisher already active".
		// Same-client reconnects succeed (idempotent).
		claimed, rm, err := h.repo.TryClaimPublisher(c.Request.Context(), p.Stream, p.ClientID)
		if err != nil || rm == nil {
			c.String(http.StatusForbidden, "1")
			return
		}
		if !claimed {
			c.String(http.StatusForbidden, "1")
			return
		}
		_ = h.repo.SetLive(c.Request.Context(), rm.ID)
		// Don't notify followers about a test-mode (host-only) broadcast.
		if h.bus != nil && !rm.IsTest {
			h.notifyFollowersGoLive(c.Request.Context(), rm)
		}

	case "on_unpublish":
		rm, err := h.repo.FindByStreamKey(c.Request.Context(), p.Stream)
		if err != nil {
			// Stream key already rotated / room gone — nothing to do.
			c.String(http.StatusOK, "0")
			return
		}
		// Finalize stats first (uses started_at, viewer_count) BEFORE
		// rotating the key — order matters since both touch the row.
		_ = h.repo.SetEnded(c.Request.Context(), rm.ID)
		// Rotate the stream key so any viewer who learned it from the
		// HLS URL can't push to it on the next broadcast.
		_, _ = h.repo.ReleasePublisher(c.Request.Context(), rm.ID)
		if h.bus != nil && !rm.IsTest {
			h.notifyFollowersOffline(c.Request.Context(), rm)
		}

	case "on_dvr":
		// DVR is disabled in srs.conf (see comment there). Even if
		// someone enables it, the previous code stored the SRS local
		// file path — which is world-readable through nginx /hls/.
		// Drop on the floor; if DVR comes back, route to a private
		// MinIO bucket instead of trusting SRS's public dir.
		// (Approve so SRS doesn't retry endlessly.)
	}
	c.String(http.StatusOK, "0")
}

// viewerList returns the set of user IDs currently subscribed to the room's
// WS danmaku channel — i.e. who is actively watching. We return only IDs
// (string form) and a count; the client can hydrate names from the friend
// list / group member info it already has.
//
// Authed + test-mode-gated so private preview rooms don't leak who's
// watching them. The owner can always see their own room's viewer list.
func (h *Handler) viewerList(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if h.accessRoomForAuthed(c, uid, id) == nil {
		return
	}
	roomKey := strconv.FormatInt(id, 10)
	var ids []int64
	if h.viewers != nil {
		ids = h.viewers.LiveViewerIDs(roomKey)
	}
	strIDs := make([]string, 0, len(ids))
	for _, uid := range ids {
		strIDs = append(strIDs, strconv.FormatInt(uid, 10))
	}
	c.JSON(http.StatusOK, gin.H{"userIds": strIDs, "count": len(strIDs)})
}

// RunScheduledReminderLoop kicks a background goroutine that polls every
// 30 s for streams whose scheduled_at is within the next 10 min and pushes
// a "live.host.scheduled" event to each follower. Set scheduled_notified=true
// after dispatch so a single scheduled time only fires once.
func (h *Handler) RunScheduledReminderLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.scanScheduled(ctx)
			}
		}
	}()
}

func (h *Handler) scanScheduled(ctx context.Context) {
	due, err := h.repo.DueReminders(ctx, 10*time.Minute)
	if err != nil || len(due) == 0 {
		return
	}
	for _, rm := range due {
		ids, _ := h.repo.FollowerIDs(ctx, rm.ID)
		if len(ids) == 0 {
			_ = h.repo.MarkScheduledNotified(ctx, rm.ID)
			continue
		}
		payload := map[string]any{
			"roomId":      fmt.Sprintf("%d", rm.ID),
			"title":       rm.Title,
			"ownerId":     fmt.Sprintf("%d", rm.OwnerID),
			"coverUrl":    rm.CoverURL,
			"scheduledAt": rm.StartedAt, // not yet started; client checks payload
		}
		// StartedAt is nil for not-yet-started rooms — use the real scheduled
		// time from the row instead. Re-fetch the room to be sure.
		if got, err := h.repo.FindByID(ctx, rm.ID); err == nil && got != nil {
			payload["scheduledAt"] = got.StartedAt
		}
		for _, uid := range ids {
			h.bus.Publish(uid, wsbus.Event{Type: "live.host.scheduled", Payload: payload})
		}
		_ = h.repo.MarkScheduledNotified(ctx, rm.ID)
	}
}

// notifyFollowersGoLive fans out a `live.host.golive` event to every user
// who has followed this room. Best effort — failures are logged via the
// bus's regular path; we don't block the SRS hook on it.
func (h *Handler) notifyFollowersGoLive(ctx context.Context, rm *Room) {
	ids, err := h.repo.FollowerIDs(ctx, rm.ID)
	if err != nil || len(ids) == 0 {
		return
	}
	payload := map[string]any{
		"roomId":    fmt.Sprintf("%d", rm.ID),
		"title":     rm.Title,
		"ownerId":   fmt.Sprintf("%d", rm.OwnerID),
		"coverUrl":  rm.CoverURL,
		"startedAt": time.Now().UTC().Format(time.RFC3339),
	}
	for _, uid := range ids {
		h.bus.Publish(uid, wsbus.Event{Type: "live.host.golive", Payload: payload})
	}
}

// notifyFollowersOffline mirrors go-live but for stream end. Followers see
// "host ended their stream" — useful so the UI can dim the room card.
func (h *Handler) notifyFollowersOffline(ctx context.Context, rm *Room) {
	ids, err := h.repo.FollowerIDs(ctx, rm.ID)
	if err != nil || len(ids) == 0 {
		return
	}
	payload := map[string]any{
		"roomId":  fmt.Sprintf("%d", rm.ID),
		"title":   rm.Title,
		"ownerId": fmt.Sprintf("%d", rm.OwnerID),
		"endedAt": time.Now().UTC().Format(time.RFC3339),
	}
	for _, uid := range ids {
		h.bus.Publish(uid, wsbus.Event{Type: "live.host.offline", Payload: payload})
	}
}

// broadcastRoomUpdated lets the host edit title/category/cover/announcement
// live and have current viewers + followers see the new values without
// re-entering the room. Owner is always notified; followers get the heads-up.
func (h *Handler) broadcastRoomUpdated(ctx context.Context, rm *Room) {
	if h.bus == nil {
		return
	}
	followerIDs, _ := h.repo.FollowerIDs(ctx, rm.ID)
	targets := map[int64]struct{}{rm.OwnerID: {}}
	for _, id := range followerIDs {
		targets[id] = struct{}{}
	}
	payload := map[string]any{
		"roomId":   fmt.Sprintf("%d", rm.ID),
		"title":    rm.Title,
		"category": rm.Category,
		"coverUrl": rm.CoverURL,
		"isTest":   rm.IsTest,
		"status":   rm.Status,
	}
	for uid := range targets {
		h.bus.Publish(uid, wsbus.Event{Type: "live.room.updated", Payload: payload})
	}
}

// --- helpers ---

func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// =============== Followers ===============

func (h *Handler) follow(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if err := h.repo.Follow(c.Request.Context(), id, uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) unfollow(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if err := h.repo.Unfollow(c.Request.Context(), id, uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) followStatus(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	following, err := h.repo.IsFollowing(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	count, _ := h.repo.FollowerCount(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"following": following, "count": count})
}

// =============== Persisted danmaku ===============

func (h *Handler) recentDanmaku(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	if h.accessRoomForAuthed(c, uid, id) == nil {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 500 {
		limit = 50
	}
	items, err := h.repo.RecentDanmaku(c.Request.Context(), id, limit)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"danmaku": items})
}

// =============== Bans / kicks ===============

type banReq struct {
	UserID int64  `json:"userId,string"`
	IsKick bool   `json:"isKick"`
	Reason string `json:"reason"`
}

func (h *Handler) banUser(c *gin.Context) {
	ownerID := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	// Only the room owner can ban.
	rm, err := h.repo.FindByID(c.Request.Context(), id)
	if err != nil || rm.OwnerID != ownerID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	var req banReq
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80024, "message": "userId required"})
		return
	}
	if err := h.repo.BanUser(c.Request.Context(), id, req.UserID, ownerID, req.IsKick, req.Reason); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"banned": req.UserID, "isKick": req.IsKick})
}

func (h *Handler) unbanUser(c *gin.Context) {
	ownerID := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	uid, ok := parseID(c.Param("userId"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid user id"})
		return
	}
	rm, err := h.repo.FindByID(c.Request.Context(), id)
	if err != nil || rm.OwnerID != ownerID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err := h.repo.UnbanUser(c.Request.Context(), id, uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

// =============== Recording delete ===============

func (h *Handler) deleteRecording(c *gin.Context) {
	ownerID := c.MustGet("userID").(int64)
	rid, ok := parseID(c.Param("recId"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	fileURL, err := h.repo.DeleteRecording(c.Request.Context(), rid, ownerID)
	if errors.Is(err, ErrNotOwner) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": rid, "fileUrl": fileURL})
}

// =============== Scheduling ===============

type scheduleReq struct {
	ScheduledAt *string `json:"scheduledAt"` // RFC 3339 or null to clear
}

func (h *Handler) setSchedule(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req scheduleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	var t *time.Time
	if req.ScheduledAt != nil && *req.ScheduledAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ScheduledAt)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80025, "message": "scheduledAt must be RFC3339"})
			return
		}
		// Bound the window: must be in the future (not retroactive)
		// and within 60 days (no year-9999 schedules that sit forever).
		// 5-min grace on the lower bound for clock skew between client/server.
		now := time.Now()
		if parsed.Before(now.Add(-5 * time.Minute)) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80026, "message": "scheduledAt 必须是将来的时间"})
			return
		}
		if parsed.After(now.Add(60 * 24 * time.Hour)) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80026, "message": "scheduledAt 最多提前 60 天"})
			return
		}
		t = &parsed
	}
	if err := h.repo.SetScheduled(c.Request.Context(), id, uid, t); err != nil {
		if errors.Is(err, ErrNotOwner) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 80012, "message": "not the owner"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

// listScheduled returns upcoming streams (scheduled_at in the future, not live yet).
func (h *Handler) listScheduled(c *gin.Context) {
	rooms, err := h.repo.ListScheduled(c.Request.Context(), 30)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

func (h *Handler) playbackURLFor(rm *Room) string {
	// HLS m3u8 lives at <hlsURL>/<streamKey>.m3u8 (SRS default URL shape).
	// For idle/ended rooms the URL is still returned so the client can
	// pre-build the player; SRS will 404 until the stream goes live.
	//
	// The URL carries an HMAC-signed token + exp that nginx validates
	// via auth_request for every m3u8 / ts fetch. Without this, anyone
	// who learned the URL once could watch forever — including for
	// test-mode rooms (we have no on_play hook in SRS to gate access
	// there, so the API-issued URL IS the capability).
	if rm == nil {
		return ""
	}
	tok, exp := signPlayToken(rm.StreamKey, h.srsSecret, time.Now())
	return fmt.Sprintf("%s/%s.m3u8?token=%s&exp=%d",
		strings.TrimRight(h.hlsURL, "/"), rm.StreamKey, tok, exp)
}

// playAuth is the internal endpoint nginx hits via auth_request to
// validate every /hls/* request. nginx forwards the original ?token=&exp=
// query string + sets the X-Play-Stream-Key header to the captured
// stream key portion of the URL.
//
// Returns 200 if the signature is valid AND not expired, 401 otherwise.
// Response body is intentionally empty — nginx only looks at the status.
//
// Reachability is restricted to the docker bridge by nginx (same
// allowlist as /api/v1/live/srs-hook/) so this never appears on the
// public internet. The handler is registered on the un-authed routegroup
// because nginx is the only caller and JWT would be meaningless.
func (h *Handler) playAuth(c *gin.Context) {
	streamKey := c.GetHeader("X-Play-Stream-Key")
	if streamKey == "" {
		streamKey = c.Query("stream")
	}
	if streamKey == "" {
		c.String(http.StatusUnauthorized, "")
		return
	}
	token := c.Query("token")
	exp := c.Query("exp")
	if err := verifyPlayToken(streamKey, h.srsSecret, token, exp, time.Now()); err != nil {
		c.String(http.StatusUnauthorized, "")
		return
	}
	c.String(http.StatusOK, "")
}

// playM3U8 fetches the playlist from SRS internally, rewrites each
// segment URL to carry the same signed-token query string, and serves
// the result back to the viewer. Without this rewrite the HLS player
// would request ts files with no query string and nginx would reject
// them — HLS players don't propagate the playlist URL's query to
// segment URLs.
//
// We do NOT extend the token's TTL here — the rewritten URLs carry the
// SAME (token, exp) tuple as the originating request. That keeps a
// single playback session bounded by the token TTL the client received
// in publicDetail; once exp passes, both the playlist and its segments
// stop working and the client must refetch the URL.
func (h *Handler) playM3U8(c *gin.Context) {
	streamKey := c.Query("stream")
	token := c.Query("token")
	exp := c.Query("exp")
	if err := verifyPlayToken(streamKey, h.srsSecret, token, exp, time.Now()); err != nil {
		c.String(http.StatusUnauthorized, "")
		return
	}
	upstream := strings.TrimRight(h.srsInternal, "/") + "/live/" + streamKey + ".m3u8"
	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", upstream, nil)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.String(http.StatusBadGateway, "")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		// Stream not yet live — propagate the 404 so the client
		// shows "offline" rather than a bogus playlist.
		c.String(http.StatusNotFound, "")
		return
	}
	if resp.StatusCode != 200 {
		c.String(http.StatusBadGateway, "")
		return
	}
	body, err := readAll(resp)
	if err != nil {
		c.String(http.StatusBadGateway, "")
		return
	}
	rewritten := appendTokenToSegments(string(body), token, exp)
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, rewritten)
}

// appendTokenToSegments walks the playlist line-by-line and appends
// ?token=&exp= to each non-comment, non-tag entry (i.e. ts segment
// references). Preserves any existing query (SRS doesn't add one, but
// future-proof). Order of operations: if line already has ? we use &.
func appendTokenToSegments(playlist, token, exp string) string {
	q := "token=" + token + "&exp=" + exp
	out := make([]byte, 0, len(playlist)+256)
	for len(playlist) > 0 {
		var line string
		if i := strings.IndexByte(playlist, '\n'); i >= 0 {
			line = playlist[:i+1]
			playlist = playlist[i+1:]
		} else {
			line = playlist
			playlist = ""
		}
		trimmed := strings.TrimRight(line, "\r\n")
		// Skip blank lines and HLS directives (#EXTM3U / #EXTINF / etc).
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line...)
			continue
		}
		// A media segment line. Append the token, preserving any newline.
		sep := "?"
		if strings.Contains(trimmed, "?") {
			sep = "&"
		}
		out = append(out, trimmed...)
		out = append(out, sep...)
		out = append(out, q...)
		// Re-attach the trailing newline (if any).
		if len(line) > len(trimmed) {
			out = append(out, line[len(trimmed):]...)
		}
	}
	return string(out)
}

func readAll(resp *http.Response) ([]byte, error) {
	const maxPlaylistSize = 1 << 20 // 1 MB — a normal m3u8 is < 10 KB
	r := http.MaxBytesReader(nil, resp.Body, maxPlaylistSize)
	return ioReadAll(r)
}

func ioReadAll(r io.Reader) ([]byte, error) {
	b := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return b, nil
			}
			return nil, err
		}
	}
}
