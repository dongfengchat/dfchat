package message

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/dongfang/dfchat/server/internal/channel"
	"github.com/dongfang/dfchat/server/internal/friend"
	"github.com/dongfang/dfchat/server/internal/group"
	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	repo     *Repo
	friends  *friend.Repo
	groups   *group.Repo
	channels *channel.Repo
	issuer   *auth.Issuer
	bus      *wsbus.Bus
}

func NewHandler(repo *Repo, friends *friend.Repo, groups *group.Repo, channels *channel.Repo, issuer *auth.Issuer, bus *wsbus.Bus) *Handler {
	return &Handler{repo: repo, friends: friends, groups: groups, channels: channels, issuer: issuer, bus: bus}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/messages")
	g.Use(middleware.RequireAuth(h.issuer))
	g.POST("", h.send)
	g.GET("", h.list)
	g.POST("/:id/recall", h.recall)
	g.POST("/:id/reactions", h.addReaction)
	g.DELETE("/:id/reactions/:emoji", h.removeReaction)
	g.POST("/:id/pin", h.pin)
	g.DELETE("/:id/pin", h.unpin)

	// Conversation-scoped endpoints (pins list + read receipt + prefs).
	convs := rg.Group("/conversations")
	convs.Use(middleware.RequireAuth(h.issuer))
	convs.GET("/:id/pins", h.listPins)
	convs.POST("/:id/read", h.markRead)
	convs.PATCH("/:id/preferences", h.updatePrefs)
}

type sendReq struct {
	To        string          `json:"to"`        // target user id for private
	GroupID   string          `json:"groupId"`   // target group id (legacy flat-group)
	ChannelID string          `json:"channelId"` // target channel id (preferred for groups)
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	Mentions  []string        `json:"mentions"`
	ReplyTo   string          `json:"replyTo"` // id of the message being replied to
}

func (h *Handler) send(c *gin.Context) {
	uid := c.MustGet("userID").(int64)

	var req sendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20010, "message": "invalid body"})
		return
	}
	if req.Type == "" {
		req.Type = "text"
	}
	if len(req.Content) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20011, "message": "content required"})
		return
	}

	var (
		convID string
		fanout []int64
	)
	switch {
	case req.ChannelID != "":
		cid, err := strconv.ParseInt(req.ChannelID, 10, 64)
		if err != nil || cid <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20018, "message": "invalid channelId"})
			return
		}
		convID = channel.ConvID(cid)
		ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
		if err != nil || !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20019, "message": "not a channel member"})
			return
		}
		ids, err := h.channels.MemberIDs(c.Request.Context(), cid)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		fanout = ids

	case req.GroupID != "":
		gid, err := strconv.ParseInt(req.GroupID, 10, 64)
		if err != nil || gid <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20015, "message": "invalid groupId"})
			return
		}
		isMember, err := h.groups.IsMember(c.Request.Context(), gid, uid)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		if !isMember {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20016, "message": "not a group member"})
			return
		}
		convID = group.GroupConvID(gid)
		if err := h.repo.EnsureGroupConversation(c.Request.Context(), convID); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		ids, err := h.groups.MemberIDs(c.Request.Context(), gid)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		fanout = ids

	case req.To != "":
		targetID, err := strconv.ParseInt(req.To, 10, 64)
		if err != nil || targetID <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20012, "message": "invalid target id"})
			return
		}
		if targetID == uid {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20013, "message": "cannot send to yourself"})
			return
		}
		blocked, err := h.friends.IsBlockedEither(c.Request.Context(), uid, targetID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		if blocked {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20023, "message": "blocked"})
			return
		}
		areFriends, err := h.friends.AreFriends(c.Request.Context(), uid, targetID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		if !areFriends {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20014, "message": "not friends"})
			return
		}
		convID, err = h.repo.EnsurePrivateConversation(c.Request.Context(), uid, targetID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		fanout = []int64{uid, targetID}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20017, "message": "one of 'to' / 'channelId' / 'groupId' required"})
		return
	}

	mentions := parseMentions(req.Mentions)

	var replyTo *int64
	if req.ReplyTo != "" {
		if id, err := strconv.ParseInt(req.ReplyTo, 10, 64); err == nil && id > 0 {
			replyTo = &id
		}
	}

	m, err := h.repo.Insert(c.Request.Context(), InsertParams{
		ConversationID: convID,
		SenderID:       uid,
		Type:           req.Type,
		Content:        req.Content,
		Mentions:       mentions,
		ReplyTo:        replyTo,
	})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	for _, member := range fanout {
		h.bus.Publish(member, wsbus.Event{Type: "chat.recv", Payload: m})
	}

	c.JSON(http.StatusCreated, gin.H{"message": m})
}

func parseMentions(raw []string) []int64 {
	out := make([]int64, 0, len(raw))
	for _, s := range raw {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func (h *Handler) recall(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20030, "message": "invalid id"})
		return
	}
	m, err := h.repo.Recall(c.Request.Context(), id, uid)
	switch {
	case errors.Is(err, ErrMessageNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20031, "message": "message not found"})
		return
	case errors.Is(err, ErrNotOwner):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20032, "message": "not your message"})
		return
	case errors.Is(err, ErrRecallWindow):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20033, "message": "recall window expired (2 min)"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Fan-out recall event to every member of the conversation.
	members, err := h.membersOf(c, m.ConversationID)
	if err == nil {
		for _, member := range members {
			h.bus.Publish(member, wsbus.Event{Type: "chat.recall", Payload: m})
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": m})
}

// membersOf returns the user ids that should receive events for convID.
// conversation_members is the single source of truth.
func (h *Handler) membersOf(c *gin.Context, convID string) ([]int64, error) {
	return h.repo.MembersOf(c.Request.Context(), convID)
}

func (h *Handler) list(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	convID := c.Query("conversationId")
	if convID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20020, "message": "conversationId required"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20021, "message": "not a member"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	beforeSeq, _ := strconv.ParseInt(c.Query("beforeSeq"), 10, 64)
	afterSeq, _ := strconv.ParseInt(c.Query("afterSeq"), 10, 64)
	aroundSeq, _ := strconv.ParseInt(c.Query("aroundSeq"), 10, 64)

	var msgs []*Message
	switch {
	case aroundSeq > 0:
		// Window centered on a target seq, used for search jump-to.
		msgs, err = h.repo.ListAround(c.Request.Context(), convID, aroundSeq, limit)
	case afterSeq > 0:
		msgs, err = h.repo.ListAfter(c.Request.Context(), convID, afterSeq, limit)
	default:
		msgs, err = h.repo.ListRecent(c.Request.Context(), convID, limit, beforeSeq)
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if _, err := h.repo.AttachReactions(c.Request.Context(), msgs); err != nil {
		// Non-fatal: log via the response anyway.
	}
	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

// --- reactions ---------------------------------------------------------

type reactReq struct {
	Emoji string `json:"emoji"`
}

func (h *Handler) addReaction(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	mid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || mid <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20040, "message": "invalid id"})
		return
	}
	var req reactReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Emoji == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20041, "message": "emoji required"})
		return
	}
	// emoji length is capped to 16 bytes by the schema.
	convID, err := h.repo.ConvOfMessage(c.Request.Context(), mid)
	if errors.Is(err, ErrMessageNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20042, "message": "message not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20043, "message": "not a member"})
		return
	}
	if _, err := h.repo.AddReaction(c.Request.Context(), mid, uid, req.Emoji); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastReactionUpdate(c, convID, mid)
	c.Status(http.StatusNoContent)
}

func (h *Handler) removeReaction(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	mid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || mid <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20040, "message": "invalid id"})
		return
	}
	emoji := c.Param("emoji")
	if emoji == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20041, "message": "emoji required"})
		return
	}
	convID, err := h.repo.ConvOfMessage(c.Request.Context(), mid)
	if errors.Is(err, ErrMessageNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20042, "message": "message not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if _, err := h.repo.RemoveReaction(c.Request.Context(), mid, uid, emoji); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	h.broadcastReactionUpdate(c, convID, mid)
	c.Status(http.StatusNoContent)
}

func (h *Handler) broadcastReactionUpdate(c *gin.Context, convID string, msgID int64) {
	reactions, err := h.repo.SummariseReactions(c.Request.Context(), msgID)
	if err != nil {
		return
	}
	members, err := h.repo.MembersOf(c.Request.Context(), convID)
	if err != nil {
		return
	}
	payload := gin.H{"messageId": strconv.FormatInt(msgID, 10), "conversationId": convID, "reactions": reactions}
	for _, uid := range members {
		h.bus.Publish(uid, wsbus.Event{Type: "chat.reaction", Payload: payload})
	}
}

// --- pins --------------------------------------------------------------

func (h *Handler) pin(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	mid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || mid <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20050, "message": "invalid id"})
		return
	}
	convID, err := h.repo.ConvOfMessage(c.Request.Context(), mid)
	if errors.Is(err, ErrMessageNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20051, "message": "message not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20052, "message": "not a member"})
		return
	}
	switch err := h.repo.Pin(c.Request.Context(), convID, mid, uid); {
	case errors.Is(err, ErrAlreadyPinned):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 20053, "message": "already pinned"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	members, _ := h.repo.MembersOf(c.Request.Context(), convID)
	payload := gin.H{"messageId": strconv.FormatInt(mid, 10), "conversationId": convID, "pinnedBy": strconv.FormatInt(uid, 10)}
	for _, m := range members {
		h.bus.Publish(m, wsbus.Event{Type: "chat.pin", Payload: payload})
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) unpin(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	mid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || mid <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20050, "message": "invalid id"})
		return
	}
	convID, err := h.repo.ConvOfMessage(c.Request.Context(), mid)
	if errors.Is(err, ErrMessageNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20051, "message": "message not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20052, "message": "not a member"})
		return
	}
	if err := h.repo.Unpin(c.Request.Context(), convID, mid); err != nil {
		if errors.Is(err, ErrNotPinned) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20054, "message": "not pinned"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	members, _ := h.repo.MembersOf(c.Request.Context(), convID)
	payload := gin.H{"messageId": strconv.FormatInt(mid, 10), "conversationId": convID}
	for _, m := range members {
		h.bus.Publish(m, wsbus.Event{Type: "chat.unpin", Payload: payload})
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) listPins(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	convID := c.Param("id")
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20021, "message": "not a member"})
		return
	}
	pins, err := h.repo.ListPins(c.Request.Context(), convID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Attach reactions to pinned message snapshots too.
	msgs := make([]*Message, 0, len(pins))
	for _, p := range pins {
		if p.Message != nil {
			msgs = append(msgs, p.Message)
		}
	}
	_, _ = h.repo.AttachReactions(c.Request.Context(), msgs)
	c.JSON(http.StatusOK, gin.H{"pins": pins})
}

// --- read receipts -----------------------------------------------------

type readReq struct {
	Seq int64 `json:"seq"`
}

type prefsReq struct {
	Muted *bool `json:"muted"`
}

func (h *Handler) updatePrefs(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	convID := c.Param("id")
	var req prefsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20070, "message": "invalid body"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20021, "message": "not a member"})
		return
	}
	if req.Muted != nil {
		if err := h.repo.SetMuted(c.Request.Context(), convID, uid, *req.Muted); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) markRead(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	convID := c.Param("id")
	var req readReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Seq <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20060, "message": "seq required"})
		return
	}
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20021, "message": "not a member"})
		return
	}
	newSeq, err := h.repo.MarkRead(c.Request.Context(), convID, uid, req.Seq)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Broadcast to other members so they can update "已读" indicators.
	members, _ := h.repo.MembersOf(c.Request.Context(), convID)
	payload := gin.H{"conversationId": convID, "userId": strconv.FormatInt(uid, 10), "seq": newSeq}
	for _, m := range members {
		if m == uid {
			continue
		}
		h.bus.Publish(m, wsbus.Event{Type: "chat.read", Payload: payload})
	}
	c.JSON(http.StatusOK, gin.H{"seq": newSeq})
}
