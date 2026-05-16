package friend

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	repo   *Repo
	issuer *auth.Issuer
	bus    *wsbus.Bus
}

func NewHandler(repo *Repo, issuer *auth.Issuer, bus *wsbus.Bus) *Handler {
	return &Handler{repo: repo, issuer: issuer, bus: bus}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/friends")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("", h.list)
	// Friend-request send paths are the only realistic spam vector here
	// (harassment via repeated requests after cancels). 0.5 r/s, burst 3
	// = 3 immediate then 1 every 2s — plenty for legitimate use, brutal
	// for bots. Combined with the per-target 7d cooldown after a reject
	// (see SendRequest), this should make harassment impractical.
	requestSend := g.Group("")
	requestSend.Use(middleware.RateLimitPerUser(0.5, 3))
	requestSend.POST("", h.add) // legacy alias
	requestSend.POST("/requests", h.sendRequest)
	g.DELETE("/:id", h.remove)
	g.GET("/requests", h.listRequests)
	g.POST("/requests/:id/accept", h.acceptRequest)
	g.DELETE("/requests/:id", h.rejectOrCancel)
	g.GET("/blocked", h.listBlocked)
	g.POST("/blocked/:id", h.block)
	g.DELETE("/blocked/:id", h.unblock)
}

func (h *Handler) list(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	friends, err := h.repo.List(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Augment with presence from the in-memory WS bus.
	for _, f := range friends {
		f.IsOnline = h.bus.HasSubscribers(f.ID)
	}
	c.JSON(http.StatusOK, gin.H{"friends": friends})
}

type addReq struct {
	Username string `json:"username"`
}

func (h *Handler) add(c *gin.Context) {
	// Kept as alias of sendRequest for older clients.
	h.sendRequest(c)
}

func (h *Handler) sendRequest(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req addReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10040, "message": "username required"})
		return
	}
	targetID, err := h.repo.FindIDByUsername(c.Request.Context(), req.Username)
	if errors.Is(err, ErrUserNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10041, "message": "user not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if targetID == uid {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10042, "message": "cannot add yourself"})
		return
	}
	// Either side blocked? Treat as a 404 to avoid leaking who blocked whom.
	blocked, _ := h.repo.IsBlockedEither(c.Request.Context(), uid, targetID)
	if blocked {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10041, "message": "user not found"})
		return
	}
	if err := h.repo.SendRequest(c.Request.Context(), uid, targetID); err != nil {
		switch {
		case errors.Is(err, ErrAlreadyFriends):
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 10043, "message": "already friends"})
		case errors.Is(err, ErrAlreadyRequested):
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 10044, "message": "request already pending"})
		default:
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		}
		return
	}
	// Tell the target a request landed so the sidebar badge increments live.
	h.bus.Publish(targetID, wsbus.Event{
		Type: "friend.request",
		Payload: gin.H{"fromUserId": strconv.FormatInt(uid, 10)},
	})
	c.JSON(http.StatusCreated, gin.H{"targetUserId": strconv.FormatInt(targetID, 10), "status": "pending"})
}

func (h *Handler) listRequests(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	inc, err := h.repo.ListIncoming(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	out, err := h.repo.ListOutgoing(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"incoming": inc, "outgoing": out})
}

func (h *Handler) acceptRequest(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	fromID, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || fromID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10045, "message": "invalid id"})
		return
	}
	if err := h.repo.AcceptRequest(c.Request.Context(), uid, fromID); err != nil {
		if errors.Is(err, ErrRequestNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10046, "message": "request not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Notify both sides so their friends lists refresh live.
	for _, u := range [2]int64{uid, fromID} {
		h.bus.Publish(u, wsbus.Event{
			Type: "friend.accepted",
			Payload: gin.H{"with": map[string]string{
				"me": strconv.FormatInt(u, 10), "peer": strconv.FormatInt(otherID(u, uid, fromID), 10),
			}},
		})
	}
	c.Status(http.StatusNoContent)
}

func otherID(target, a, b int64) int64 {
	if target == a {
		return b
	}
	return a
}

func (h *Handler) block(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	otherID, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || otherID <= 0 || otherID == uid {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10060, "message": "invalid id"})
		return
	}
	if err := h.repo.Block(c.Request.Context(), uid, otherID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) unblock(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	otherID, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || otherID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10060, "message": "invalid id"})
		return
	}
	if err := h.repo.Unblock(c.Request.Context(), uid, otherID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) listBlocked(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	blocked, err := h.repo.ListBlocked(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"blocked": blocked})
}

// rejectOrCancel deletes a pending edge regardless of direction: if the
// caller is the recipient → reject; if they're the sender → cancel.
func (h *Handler) rejectOrCancel(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	otherID, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || otherID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10045, "message": "invalid id"})
		return
	}
	// Try reject (other → me) first; if nothing matched, try cancel (me → other).
	if err := h.repo.RejectRequest(c.Request.Context(), uid, otherID); err == nil {
		c.Status(http.StatusNoContent)
		return
	}
	if err := h.repo.CancelOutgoing(c.Request.Context(), uid, otherID); err != nil {
		if errors.Is(err, ErrRequestNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10046, "message": "request not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) remove(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10043, "message": "invalid id"})
		return
	}
	if err := h.repo.Remove(c.Request.Context(), uid, targetID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}
