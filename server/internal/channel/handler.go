package channel

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dongfang/dfchat/server/internal/group"
	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	repo   *Repo
	groups *group.Repo
	issuer *auth.Issuer
}

func NewHandler(repo *Repo, groups *group.Repo, issuer *auth.Issuer) *Handler {
	return &Handler{repo: repo, groups: groups, issuer: issuer}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/groups/:id/channels", middleware.RequireAuth(h.issuer), h.list)
	rg.POST("/groups/:id/channels", middleware.RequireAuth(h.issuer), h.create)
	rg.PATCH("/groups/:id/channels/positions", middleware.RequireAuth(h.issuer), h.reorder)
	rg.PATCH("/channels/:id", middleware.RequireAuth(h.issuer), h.rename)
	rg.DELETE("/channels/:id", middleware.RequireAuth(h.issuer), h.delete)
}

func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) list(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gid, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60010, "message": "invalid id"})
		return
	}
	isMember, err := h.groups.IsMember(c.Request.Context(), gid, uid)
	if err != nil || !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 60011, "message": "not a group member"})
		return
	}
	chs, err := h.repo.ListByGroup(c.Request.Context(), gid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": chs})
}

type createReq struct {
	Name string `json:"name"`
}

func (h *Handler) create(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gid, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60010, "message": "invalid id"})
		return
	}
	if _, err := h.groups.FindByID(c.Request.Context(), gid); err != nil {
		if errors.Is(err, group.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 60012, "message": "group not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Owner or admin (role >= 1) can create channels.
	role, err := h.groups.GetMemberRole(c.Request.Context(), gid, uid)
	if err != nil || role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 60013, "message": "owner or admin only"})
		return
	}
	var req createReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60014, "message": "invalid body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60015, "message": "name 1-64 chars"})
		return
	}
	ch, err := h.repo.Create(c.Request.Context(), gid, name)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"channel": ch})
}

func (h *Handler) delete(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	cid, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60010, "message": "invalid id"})
		return
	}
	ch, err := h.repo.FindByID(c.Request.Context(), cid)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 60016, "message": "channel not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if _, err := h.groups.FindByID(c.Request.Context(), ch.GroupID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	role, err := h.groups.GetMemberRole(c.Request.Context(), ch.GroupID, uid)
	if err != nil || role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 60017, "message": "owner or admin only"})
		return
	}
	switch err := h.repo.Delete(c.Request.Context(), cid); {
	case errors.Is(err, ErrLastChannel):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 60018, "message": "cannot delete last channel"})
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
	default:
		c.Status(http.StatusNoContent)
	}
}

type renameReq struct {
	Name string `json:"name"`
}

// rename changes a channel's display name. Owner or admin only —
// regular members shouldn't be able to relabel rooms.
func (h *Handler) rename(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	cid, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60010, "message": "invalid id"})
		return
	}
	ch, err := h.repo.FindByID(c.Request.Context(), cid)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 60016, "message": "channel not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	role, err := h.groups.GetMemberRole(c.Request.Context(), ch.GroupID, uid)
	if err != nil || role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 60017, "message": "owner or admin only"})
		return
	}
	var req renameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60014, "message": "invalid body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60015, "message": "name 1-64 chars"})
		return
	}
	ch, err = h.repo.Rename(c.Request.Context(), cid, name)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channel": ch})
}

type reorderReq struct {
	// Order is the new position-order of channel IDs (left-most → top).
	// Channels not listed are left in place; channels listed but not
	// in this group are silently dropped.
	Order []string `json:"order"`
}

// reorder rewrites the `position` column on a group's channels per the
// client-supplied id order. Owner or admin only. Accepts a partial list
// — channels not mentioned keep their existing relative order pushed
// to the bottom. This matches the typical drag-to-reorder UX where a
// user only really cares about the top-N being correct.
func (h *Handler) reorder(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gid, ok := parseID(c.Param("id"))
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60010, "message": "invalid id"})
		return
	}
	role, err := h.groups.GetMemberRole(c.Request.Context(), gid, uid)
	if err != nil || role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 60017, "message": "owner or admin only"})
		return
	}
	var req reorderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60014, "message": "invalid body"})
		return
	}
	ids := make([]int64, 0, len(req.Order))
	for _, s := range req.Order {
		id, err := strconv.ParseInt(s, 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 60019, "message": "order list empty"})
		return
	}
	if err := h.repo.Reorder(c.Request.Context(), gid, ids); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}
