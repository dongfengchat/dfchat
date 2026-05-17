package group

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	repo   *Repo
	issuer *auth.Issuer
}

func NewHandler(repo *Repo, issuer *auth.Issuer) *Handler {
	return &Handler{repo: repo, issuer: issuer}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/groups")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("", h.listMine)
	// Per-user strict limit on the write paths most prone to abuse:
	//   - POST   /groups          — mass group creation (spam)
	//   - POST   /groups/join     — invite-code enumeration / probing
	// 1 r/s sustained, burst 3 (matches the rest of the strict tier).
	strict := g.Group("")
	strict.Use(middleware.RateLimitPerUser(1, 3))
	strict.POST("", h.create)
	strict.POST("/join", h.join)
	g.GET("/:id", h.detail)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete) // owner-only manual delete
	g.GET("/:id/members", h.members)
	g.DELETE("/:id/leave", h.leave)
	g.PATCH("/:id/members/:userId/role", h.setMemberRole)
	g.DELETE("/:id/members/:userId", h.kickMember)
	g.GET("/:id/notify", h.getNotifyMode)
	g.PATCH("/:id/notify", h.setNotifyMode)
	// Owner-only privileged actions.
	g.POST("/:id/transfer", h.transferOwner)
	g.POST("/:id/invite/rotate", h.rotateInvite)
}

type updateGroupReq struct {
	Name         *string `json:"name"`
	IconURL      *string `json:"iconUrl"`
	Description  *string `json:"description"`
	Announcement *string `json:"announcement"`
	IsPublic     *bool   `json:"isPublic"`
}

// update edits group metadata. Owner can change anything;
// admins can change announcement+description but not name/icon/isPublic.
func (h *Handler) update(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req updateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30010, "message": "invalid body"})
		return
	}
	role, err := h.repo.GetMemberRole(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30025, "message": "not a member"})
		return
	}
	// role: 0 member, 1 admin, 2 owner. Members can't edit at all.
	if role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30050, "message": "members cannot edit group"})
		return
	}
	// Admins can update announcement + description; only owner can flip
	// the name / icon / public-discoverable flag.
	if role < 2 {
		req.Name = nil
		req.IconURL = nil
		req.IsPublic = nil
	}
	// Cheap length guards so the SQL doesn't have to error out from
	// VARCHAR truncation. Mirrors create's check on name.
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if len(n) < 1 || len(n) > 64 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30011, "message": "name must be 1-64 chars"})
			return
		}
		req.Name = &n
	}
	if req.Description != nil && len(*req.Description) > 500 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30015, "message": "description too long (max 500)"})
		return
	}
	if req.Announcement != nil && len(*req.Announcement) > 2000 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30016, "message": "announcement too long (max 2000)"})
		return
	}
	g, err := h.repo.Update(c.Request.Context(), id, UpdateInput{
		Name:         req.Name,
		IconURL:      req.IconURL,
		Description:  req.Description,
		Announcement: req.Announcement,
		IsPublic:     req.IsPublic,
	})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g})
}

// delete drops the group entirely. Owner-only — admins can kick members
// and edit text fields but can't dissolve the group. ON DELETE CASCADE
// on group_members / channels / conversation_members handles cleanup;
// the group's conversations stay (so historical messages aren't orphaned
// from a moderation/audit perspective) but become unreachable since no
// one is left in conversation_members.
func (h *Handler) delete(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	role, err := h.repo.GetMemberRole(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30025, "message": "not a member"})
		return
	}
	if role < 2 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30060, "message": "owner only"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

type transferReq struct {
	UserID string `json:"userId"`
}

// transferOwner hands ownership to another existing member. The new
// owner must already be in group_members (you can't gift the group to a
// stranger). Owner-only; the old owner is demoted to admin (role=1) so
// they keep enough power to leave gracefully.
func (h *Handler) transferOwner(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req transferReq
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30010, "message": "userId required"})
		return
	}
	newOwner, err := strconv.ParseInt(req.UserID, 10, 64)
	if err != nil || newOwner <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30010, "message": "invalid userId"})
		return
	}
	if newOwner == uid {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30061, "message": "cannot transfer to yourself"})
		return
	}
	role, err := h.repo.GetMemberRole(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30025, "message": "not a member"})
		return
	}
	if role < 2 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30060, "message": "owner only"})
		return
	}
	if err := h.repo.TransferOwnership(c.Request.Context(), id, uid, newOwner); err != nil {
		if errors.Is(err, ErrNotMember) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30062, "message": "target user is not a member"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

// rotateInvite mints a fresh invite_code for the group, invalidating
// the old one. Owner or admin can do it (admins can already kick, so
// adding "rotate invite to undo a leak" doesn't grant more power than
// they had). Mirrors the automatic rotation that happens on Kick.
func (h *Handler) rotateInvite(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	role, err := h.repo.GetMemberRole(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30025, "message": "not a member"})
		return
	}
	if role < 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30063, "message": "需要管理员或群主权限"})
		return
	}
	newCode, err := h.repo.RotateInviteCode(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"inviteCode": newCode})
}

type notifyModeReq struct {
	Mode int16 `json:"mode"`
}

func (h *Handler) setNotifyMode(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req notifyModeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30010, "message": "invalid body"})
		return
	}
	if err := h.repo.SetNotifyMode(c.Request.Context(), id, uid, req.Mode); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30051, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": req.Mode})
}

func (h *Handler) getNotifyMode(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	mode, err := h.repo.GetNotifyMode(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": mode})
}

type createReq struct {
	Name string `json:"name"`
}

func (h *Handler) create(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req createReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30010, "message": "invalid body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 1 || len(name) > 64 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30011, "message": "name must be 1-64 chars"})
		return
	}
	g, err := h.repo.Create(c.Request.Context(), uid, name)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"group": g})
}

func (h *Handler) listMine(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gs, err := h.repo.ListMine(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": gs})
}

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30012, "message": "invalid id"})
		return 0, false
	}
	return id, true
}

func (h *Handler) detail(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	g, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30013, "message": "group not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	isMember, err := h.repo.IsMember(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30014, "message": "not a member"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g})
}

func (h *Handler) members(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	isMember, err := h.repo.IsMember(c.Request.Context(), id, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30014, "message": "not a member"})
		return
	}
	// Optional search filter — case-insensitive substring match on
	// username OR nickname. Empty / missing q returns the full list.
	// Trimmed + capped so a giant query string doesn't waste a scan.
	q := strings.TrimSpace(c.Query("q"))
	if len(q) > 64 {
		q = q[:64]
	}
	ms, err := h.repo.MembersFiltered(c.Request.Context(), id, q)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": ms})
}

type joinReq struct {
	InviteCode string `json:"inviteCode"`
}

func (h *Handler) join(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req joinReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.InviteCode) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30020, "message": "inviteCode required"})
		return
	}
	g, err := h.repo.JoinByInvite(c.Request.Context(), strings.TrimSpace(req.InviteCode), uid)
	switch {
	case errors.Is(err, ErrInviteCodeInvalid):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30021, "message": "invalid invite code"})
		return
	case errors.Is(err, ErrGroupFull):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 30022, "message": "group is full"})
		return
	case errors.Is(err, ErrAlreadyMember):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 30023, "message": "already a member"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g})
}

type roleReq struct {
	Role int16 `json:"role"`
}

func (h *Handler) setMemberRole(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gid, ok := parseID(c)
	if !ok {
		return
	}
	targetID, perr := strconv.ParseInt(c.Param("userId"), 10, 64)
	if perr != nil || targetID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30030, "message": "invalid userId"})
		return
	}
	g, err := h.repo.FindByID(c.Request.Context(), gid)
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30013, "message": "group not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Only the owner can change roles.
	if g.OwnerID != uid {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30031, "message": "only owner can change roles"})
		return
	}
	if targetID == g.OwnerID {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30032, "message": "cannot change owner role"})
		return
	}
	var req roleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30033, "message": "invalid body"})
		return
	}
	if err := h.repo.SetMemberRole(c.Request.Context(), gid, targetID, req.Role); err != nil {
		if errors.Is(err, ErrNotMember) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30034, "message": "not a member"})
			return
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30035, "message": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) kickMember(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	gid, ok := parseID(c)
	if !ok {
		return
	}
	targetID, perr := strconv.ParseInt(c.Param("userId"), 10, 64)
	if perr != nil || targetID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30040, "message": "invalid userId"})
		return
	}
	if _, err := h.repo.FindByID(c.Request.Context(), gid); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30013, "message": "group not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if targetID == uid {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 30041, "message": "use /leave to remove yourself"})
		return
	}
	// Caller's role.
	callerRole, err := h.repo.GetMemberRole(c.Request.Context(), gid, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30042, "message": "not a group member"})
		return
	}
	// Target's role.
	targetRole, err := h.repo.GetMemberRole(c.Request.Context(), gid, targetID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30034, "message": "target not a member"})
		return
	}
	// Owner can kick anyone (except themselves, handled above).
	// Admin can kick plain members only.
	// Member cannot kick.
	if callerRole == 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30043, "message": "members cannot kick"})
		return
	}
	if callerRole == 1 && targetRole >= 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30044, "message": "admins cannot kick admins or owner"})
		return
	}
	if err := h.repo.Kick(c.Request.Context(), gid, targetID); err != nil {
		if errors.Is(err, ErrIsOwner) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30045, "message": "cannot kick owner"})
			return
		}
		if errors.Is(err, ErrNotMember) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30034, "message": "not a member"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) leave(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, ok := parseID(c)
	if !ok {
		return
	}
	err := h.repo.Leave(c.Request.Context(), id, uid)
	switch {
	case errors.Is(err, ErrIsOwner):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30024, "message": "owner cannot leave"})
		return
	case errors.Is(err, ErrNotMember):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 30025, "message": "not a member"})
		return
	case errors.Is(err, ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 30013, "message": "group not found"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}
