package live

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	repo      *Repo
	issuer    *auth.Issuer
	bus       *wsbus.Bus   // fan-out go-live / room-updated to followers + viewers
	viewers   ViewerSource // nil-safe; nil disables /viewers endpoint
	rtmpURL   string       // e.g. rtmp://dfchat.chat/live   (shown to streamer)
	hlsURL    string       // e.g. https://dfchat.chat/hls    (shown to viewer)
	srsSecret string       // shared secret used by SRS hooks (configured in srs.conf)
}

func NewHandler(repo *Repo, issuer *auth.Issuer, bus *wsbus.Bus, rtmpURL, hlsURL, srsSecret string) *Handler {
	return &Handler{repo: repo, issuer: issuer, bus: bus, rtmpURL: rtmpURL, hlsURL: hlsURL, srsSecret: srsSecret}
}

// AttachViewerSource is called from main.go *after* realtime is built — the
// live handler is constructed first because realtime needs liveRepo as
// its LiveBackend, so we wire the back-reference after both exist.
func (h *Handler) AttachViewerSource(v ViewerSource) { h.viewers = v }

func (h *Handler) Register(rg *gin.RouterGroup) {
	// Public — list discovery, room detail (no stream key)
	rg.GET("/live/rooms", h.listLive)
	rg.GET("/live/scheduled", h.listScheduled)
	rg.GET("/live/rooms/:id", h.publicDetail)
	rg.GET("/live/rooms/:id/recordings", h.recordings)
	rg.GET("/live/rooms/:id/danmaku", h.recentDanmaku)
	rg.GET("/live/rooms/:id/viewers", h.viewerList)

	// Owner-only
	g := rg.Group("/live")
	g.Use(middleware.RequireAuth(h.issuer))
	g.POST("/rooms", h.create)
	g.GET("/rooms/:id/owner", h.ownerDetail) // includes stream key + URLs
	g.PATCH("/rooms/:id", h.update)
	g.PATCH("/rooms/:id/visibility", h.setVisibility) // 试播 ↔ 公开
	g.PATCH("/rooms/:id/schedule", h.setSchedule)     // 直播预告
	g.POST("/rooms/:id/rotate-key", h.rotateKey)
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

	// SRS HTTP hooks — protected by shared secret in URL path, not by JWT.
	rg.POST("/live/srs-hook/:secret", h.srsHook)
}

// --- public ---

func (h *Handler) listLive(c *gin.Context) {
	rooms, err := h.repo.ListLive(c.Request.Context(), 30)
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
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
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
	c.Status(http.StatusNoContent)
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
	if c.Param("secret") != h.srsSecret {
		c.String(http.StatusUnauthorized, "1")
		return
	}
	var p srsHookPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.String(http.StatusBadRequest, "1")
		return
	}
	// Transcoded variants (e.g. <streamKey>_ld) are SRS pushing back into
	// itself for the 480p ladder — bypass DB lookup and approve them,
	// otherwise transcode loops can't establish.
	if strings.HasSuffix(p.Stream, "_ld") || strings.HasSuffix(p.Stream, "_md") {
		c.String(http.StatusOK, "0")
		return
	}
	rm, err := h.repo.FindByStreamKey(c.Request.Context(), p.Stream)
	if err != nil {
		// Unknown stream key → reject (SRS will close the publisher).
		c.String(http.StatusForbidden, "1")
		return
	}
	switch p.Action {
	case "on_publish":
		_ = h.repo.SetLive(c.Request.Context(), rm.ID)
		// Don't notify followers about a test-mode (host-only) broadcast.
		if h.bus != nil && !rm.IsTest {
			h.notifyFollowersGoLive(c.Request.Context(), rm)
		}
	case "on_unpublish":
		_ = h.repo.SetEnded(c.Request.Context(), rm.ID)
		if h.bus != nil && !rm.IsTest {
			h.notifyFollowersOffline(c.Request.Context(), rm)
		}
	case "on_dvr":
		// p.File is something like "./objs/nginx/html/live/<key>.mp4"
		// We don't move it to MinIO here — a worker / cron can sweep
		// the dvr dir later. For now just persist the file path so
		// recordings list can show "available, processing".
		if p.File != "" {
			_ = h.repo.RecordRecording(c.Request.Context(), rm.ID, p.File, 0, 0)
		}
	}
	c.String(http.StatusOK, "0")
}

// viewerList returns the set of user IDs currently subscribed to the room's
// WS danmaku channel — i.e. who is actively watching. We return only IDs
// (string form) and a count; the client can hydrate names from the friend
// list / group member info it already has.
func (h *Handler) viewerList(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
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
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
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
	if rm == nil {
		return ""
	}
	// Public detail strips the stream key, so use the same identifier.
	// Compute against the stored key (preserved here at the repo layer).
	return fmt.Sprintf("%s/%s.m3u8", strings.TrimRight(h.hlsURL, "/"), rm.StreamKey)
}
