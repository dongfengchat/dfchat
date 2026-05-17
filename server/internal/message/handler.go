package message

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// Per-user rate limit for write paths. 5 r/s sustained, burst 15 —
	// generous for normal chat (humans type ~1-2 msgs/sec under stress)
	// but cuts off scripted floods. Reads stay on the global 30 r/s.
	write := g.Group("")
	write.Use(middleware.RateLimitPerUser(5, 15))
	write.POST("", h.send)
	write.PATCH("/:id", h.edit)
	write.DELETE("/:id", h.delete) // permanent server-side delete; mirrors PATCH
	write.POST("/:id/recall", h.recall)
	write.POST("/:id/reactions", h.addReaction)
	write.DELETE("/:id/reactions/:emoji", h.removeReaction)
	write.POST("/:id/pin", h.pin)
	write.DELETE("/:id/pin", h.unpin)
	g.GET("", h.list)

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
	if err := validateContent(req.Type, req.Content); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20024, "message": err.Error()})
		return
	}
	if len(req.Mentions) > maxMentionsPerMsg {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20025, "message": "too many mentions"})
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

	// Filter mentions: keep only ids that actually belong to this
	// conversation. Stops a sender from notifying random users (or
	// probing user-id existence via mention fan-out) and quietly drops
	// kicked / former members so they don't get phantom pings.
	//
	// MentionEveryone (sentinel id 0) is preserved if the sender has
	// permission to @everyone — owner/admin in groups, anyone in a
	// private DM. The client renders this as a "@全体成员" pill.
	mentions := parseMentions(req.Mentions)
	if len(mentions) > 0 {
		validMembers, err := h.repo.MembersOf(c.Request.Context(), convID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		// Decide whether to keep @everyone in the filter result.
		allowEveryone := false
		if strings.HasPrefix(convID, "p_") {
			// 1:1 — both parties already get every message, @everyone
			// is meaningless but also harmless. Pass-through.
			allowEveryone = true
		} else if strings.HasPrefix(convID, "g_") {
			gid, err := strconv.ParseInt(strings.TrimPrefix(convID, "g_"), 10, 64)
			if err == nil {
				if role, rerr := h.groups.GetMemberRole(c.Request.Context(), gid, uid); rerr == nil && role >= 1 {
					allowEveryone = true
				}
			}
		} else if strings.HasPrefix(convID, "c_") {
			cid, err := strconv.ParseInt(strings.TrimPrefix(convID, "c_"), 10, 64)
			if err == nil {
				if gid, gerr := h.channels.GroupOf(c.Request.Context(), cid); gerr == nil {
					if role, rerr := h.groups.GetMemberRole(c.Request.Context(), gid, uid); rerr == nil && role >= 1 {
						allowEveryone = true
					}
				}
			}
		}
		mentions = filterMentions(mentions, validMembers, allowEveryone)
	}

	var replyTo *int64
	// Quote snapshot — embedded into the reply's own content so the
	// quoted preview survives even after the original message has been
	// hard-deleted or aged out of the 30-day retention window. Without
	// this the client would render "[原消息已不存在]" or worse, leak
	// the existence of an id it can no longer fetch. The snapshot is
	// minimal: sender, a short text preview, and the original type so
	// the client can render "[图片]" / "[文件]" appropriately.
	content := req.Content
	if req.ReplyTo != "" {
		id, perr := strconv.ParseInt(req.ReplyTo, 10, 64)
		if perr != nil || id <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20026, "message": "invalid replyTo"})
			return
		}
		quoted, err := h.repo.GetByID(c.Request.Context(), id)
		if err != nil || quoted.ConversationID != convID {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20027, "message": "replyTo not in this conversation"})
			return
		}
		replyTo = &id

		// Splice a `_replyToSnapshot` field into the JSON content. We
		// stash it under a leading-underscore key to keep it out of
		// any naive type-specific schema (`text` content normally
		// only has `{text}`); the client looks for it explicitly.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(content, &raw); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20024, "message": "content must be valid JSON"})
			return
		}
		snap := map[string]any{
			"senderId": strconv.FormatInt(quoted.SenderID, 10),
			"type":     quoted.Type,
			"preview":  previewOf(quoted),
		}
		if quoted.IsRecalled {
			snap["preview"] = "[消息已撤回]"
		}
		if b, err := json.Marshal(snap); err == nil {
			raw["_replyToSnapshot"] = b
			if patched, err := json.Marshal(raw); err == nil {
				content = patched
			}
		}
	}

	m, err := h.repo.Insert(c.Request.Context(), InsertParams{
		ConversationID: convID,
		SenderID:       uid,
		Type:           req.Type,
		Content:        content,
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

// previewOf builds a short, type-appropriate string describing a
// message, used by the reply snapshot so quote previews survive even
// after the original message is gone. Capped to 120 chars so we don't
// bloat a thread of nested quotes (anti-quote-amplification).
func previewOf(m *Message) string {
	switch m.Type {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Content, &body)
		s := body.Text
		runes := []rune(s)
		if len(runes) > 120 {
			s = string(runes[:120]) + "…"
		}
		return s
	case "image":
		return "[图片]"
	case "file":
		var body struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(m.Content, &body)
		if body.Name != "" {
			return "[文件] " + body.Name
		}
		return "[文件]"
	case "audio":
		return "[语音]"
	case "video":
		return "[视频]"
	case "sticker":
		return "[贴纸]"
	case "call":
		return "[通话]"
	case "livestream":
		return "[直播]"
	}
	return "[消息]"
}

func parseMentions(raw []string) []int64 {
	out := make([]int64, 0, len(raw))
	for _, s := range raw {
		// "0" survives as the MentionEveryone sentinel — the
		// permission check + dedup happens later in filterMentions.
		// Negative ids are rejected outright.
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id >= 0 {
			out = append(out, id)
		}
	}
	return out
}

// filterMentions keeps only ids that are members of the conversation
// (deduplicated). Order from the caller is preserved for ids that
// survive, which keeps the @-list reading natural in the UI.
//
// MentionEveryone (sentinel id 0) is preserved iff allowEveryone is
// true. Caller is expected to have computed that based on the sender's
// role in the conversation. If a sender lacks the privilege but tries
// anyway, the sentinel is silently dropped — same UX as the
// per-user filter for non-members.
func filterMentions(mentions, members []int64, allowEveryone bool) []int64 {
	if len(mentions) == 0 {
		return nil
	}
	memberSet := make(map[int64]struct{}, len(members))
	for _, m := range members {
		memberSet[m] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(mentions))
	out := mentions[:0]
	for _, m := range mentions {
		if m == MentionEveryone {
			if !allowEveryone {
				continue
			}
		} else if _, ok := memberSet[m]; !ok {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
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
	// Redact body BEFORE we fan out / respond. RedactRecalled mutates
	// in place; the returned message + the wire event both end up with
	// content={} so the recalled text never leaks to recipients (or
	// back to the sender, who already knew it anyway — keeping the
	// shape consistent across all paths simplifies the client UI).
	RedactRecalled([]*Message{m})
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

// delete permanently removes a message from the server. Unlike recall
// (which keeps the row + flag for sequencing continuity), delete drops
// the row entirely — reactions / pins / read_receipts cascade away,
// and conversations.last_message_id gets repaired by the retention
// sweeper if it pointed at this row.
//
// Authority window mirrors RetentionWindow: within 30 days the author
// can manually delete; beyond that the server doesn't have the row
// anyway (the retention sweeper already removed it). The handler
// rejects requests outside the window with a clear error so the UI
// can stop showing a "delete" menu item rather than just 404-ing.
//
// Fan-out goes as `chat.delete` with { messageId, conversationId,
// deletedBy }. Clients with a local archive should KEEP their copy
// (this is the whole point of the "30-day server authority horizon"
// design) — the event is informational so the in-flight server-side
// view updates.
func (h *Handler) delete(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20090, "message": "invalid id"})
		return
	}
	orig, err := h.repo.GetByID(c.Request.Context(), id)
	if errors.Is(err, ErrMessageNotFound) {
		// Could be already past retention — the server simply doesn't
		// have it. From the user's POV that's fine: it's gone.
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if orig.SenderID != uid {
		// Group admins can use moderation tools (not yet built);
		// regular users can only delete their own.
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20091, "message": "not your message"})
		return
	}
	if time.Since(orig.CreatedAt) > RetentionWindow {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20092, "message": "超过保留期，服务端已自动清理"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Fan-out so live clients update the in-memory chat view. Clients
	// with persistent local archives should treat this as "remove
	// from server-backed view" — their local copy stays.
	members, _ := h.membersOf(c, orig.ConversationID)
	payload := gin.H{
		"messageId":      strconv.FormatInt(id, 10),
		"conversationId": orig.ConversationID,
		"deletedBy":      strconv.FormatInt(uid, 10),
	}
	for _, member := range members {
		h.bus.Publish(member, wsbus.Event{Type: "chat.delete", Payload: payload})
	}
	c.Status(http.StatusNoContent)
}

type editReq struct {
	Content json.RawMessage `json:"content"`
}

// edit lets the original author rewrite a text message's body within a
// 5-minute window. Only `text` type is editable — image/file/etc. have
// no meaningful "rewrite" semantics (the content there is a URL into
// storage; if you want to change the file you re-upload + re-send).
// Recalled messages are not editable.
//
// The edit fan-out goes out as chat.edit with the new content embedded;
// the client replaces in place and renders a "(已编辑)" badge.
func (h *Handler) edit(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20070, "message": "invalid id"})
		return
	}
	var req editReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Content) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20071, "message": "content required"})
		return
	}
	// We don't expose Type on the edit body — the type stays whatever the
	// original was. validateContent still needs a type to gate, so reload
	// the original to find out (and to enforce text-only editing).
	orig, err := h.repo.GetByID(c.Request.Context(), id)
	if errors.Is(err, ErrMessageNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 20072, "message": "message not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if orig.SenderID != uid {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20073, "message": "not your message"})
		return
	}
	if orig.IsRecalled {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20074, "message": "cannot edit a recalled message"})
		return
	}
	if orig.Type != "text" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20075, "message": "only text messages are editable"})
		return
	}
	if time.Since(orig.CreatedAt) > EditWindowSeconds*time.Second {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20076, "message": "edit window expired (5 min)"})
		return
	}
	if err := validateContent(orig.Type, req.Content); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 20077, "message": err.Error()})
		return
	}

	m, err := h.repo.Edit(c.Request.Context(), id, req.Content)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Fan-out to every member so existing clients re-render in place.
	members, _ := h.membersOf(c, m.ConversationID)
	for _, member := range members {
		h.bus.Publish(member, wsbus.Event{Type: "chat.edit", Payload: m})
	}
	c.JSON(http.StatusOK, gin.H{"message": m})
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
	// Recalled messages keep their row + flag for sequencing, but their
	// content body is replaced with `{}` so the original payload doesn't
	// leak via this catch-up endpoint.
	RedactRecalled(msgs)
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
	// Mirror addReaction's gate: a user can't poke at reactions on a
	// message in a conversation they don't belong to. Without this, the
	// endpoint becomes a probe for "does message X exist with reaction
	// Y from me" (204 vs 500) — a small but unnecessary information leak.
	ok, err := h.repo.IsMember(c.Request.Context(), convID, uid)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20043, "message": "not a member"})
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

// maxPinsPerConv caps how many messages can sit on the pin shelf for a
// single conversation. Beyond this the pin button on the client fails
// politely with 20055; admins should unpin something stale first. The
// goal is to keep the pin tray usable as a UI element, not to let one
// chatty group accumulate thousands of bookmarks.
const maxPinsPerConv = 50

// canManagePin reports whether the caller is allowed to pin / unpin in
// the given conversation. Rules per conv type:
//   - private DMs (p_*): either member may pin. Pinned messages are
//     symmetric anyway, both parties see them.
//   - groups (g_*) / channels (c_*): owner or admin role required.
//     Members shouldn't be able to spam-pin or rage-unpin admin pins.
//
// Returns (allowed, err). err is non-nil only on DB failure.
func (h *Handler) canManagePin(c *gin.Context, convID string, uid int64) (bool, error) {
	ctx := c.Request.Context()
	switch {
	case strings.HasPrefix(convID, "p_"):
		// Private DM: either side may pin. IsMember already gates entry.
		return true, nil
	case strings.HasPrefix(convID, "g_"):
		gid, err := strconv.ParseInt(strings.TrimPrefix(convID, "g_"), 10, 64)
		if err != nil {
			return false, err
		}
		role, err := h.groups.GetMemberRole(ctx, gid, uid)
		if err != nil {
			return false, nil
		}
		return role >= 1, nil
	case strings.HasPrefix(convID, "c_"):
		cid, err := strconv.ParseInt(strings.TrimPrefix(convID, "c_"), 10, 64)
		if err != nil {
			return false, err
		}
		gid, err := h.channels.GroupOf(ctx, cid)
		if err != nil {
			return false, nil
		}
		role, err := h.groups.GetMemberRole(ctx, gid, uid)
		if err != nil {
			return false, nil
		}
		return role >= 1, nil
	}
	return false, nil
}

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
	allowed, err := h.canManagePin(c, convID, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if !allowed {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20056, "message": "需要管理员或群主权限"})
		return
	}
	// Enforce per-conv pin cap so a single conv can't hoard the table.
	if n, err := h.repo.CountPins(c.Request.Context(), convID); err == nil && n >= maxPinsPerConv {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 20055, "message": "已达置顶上限，请先取消其它再试"})
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
	allowed, err := h.canManagePin(c, convID, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if !allowed {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 20056, "message": "需要管理员或群主权限"})
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
	// Mirror /messages — recalled pinned snapshots lose their body too.
	RedactRecalled(msgs)
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
