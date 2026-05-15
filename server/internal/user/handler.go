package user

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// SessionRevoker is implemented by the auth refresh store. We declare it here
// so the user package doesn't have to import internal/auth and create an
// import cycle.
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID int64) error
}

type Handler struct {
	repo     *Repo
	issuer   *auth.Issuer
	revoker  SessionRevoker
}

func NewHandler(repo *Repo, issuer *auth.Issuer, revoker SessionRevoker) *Handler {
	return &Handler{repo: repo, issuer: issuer, revoker: revoker}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	users := rg.Group("/users")
	users.Use(middleware.RequireAuth(h.issuer))
	users.GET("/me", h.me)
	users.PATCH("/me", h.updateMe)
	users.DELETE("/me", h.deleteMe)
}

func (h *Handler) me(c *gin.Context) {
	uid, _ := c.Get("userID")
	u, err := h.repo.FindByID(c.Request.Context(), uid.(int64))
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10030, "message": "user not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

type deleteMeReq struct {
	Password string `json:"password"`
}

func (h *Handler) deleteMe(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req deleteMeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10070, "message": "password confirmation required"})
		return
	}
	// Verify current password before destroying the account — a stolen session
	// alone shouldn't be enough to nuke the user.
	creds, err := h.repo.FindCredentialsByID(c.Request.Context(), uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash), []byte(req.Password)); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 10071, "message": "password mismatch"})
		return
	}
	if err := h.repo.SoftDelete(c.Request.Context(), uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if h.revoker != nil {
		_ = h.revoker.RevokeAllForUser(c.Request.Context(), uid)
	}
	c.Status(http.StatusNoContent)
}

type updateMeReq struct {
	Nickname  *string `json:"nickname"`
	Bio       *string `json:"bio"`
	AvatarURL *string `json:"avatarUrl"`
}

func (h *Handler) updateMe(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req updateMeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10031, "message": "invalid body"})
		return
	}
	// Trim + validate sizes. Anything else (avatar host check, profanity)
	// is layered above this — the repo only enforces NOT NULL.
	if req.Nickname != nil {
		v := strings.TrimSpace(*req.Nickname)
		if v == "" || len(v) > 64 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10032, "message": "nickname must be 1-64 chars"})
			return
		}
		req.Nickname = &v
	}
	if req.Bio != nil {
		v := strings.TrimSpace(*req.Bio)
		if len(v) > 255 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10033, "message": "bio too long (max 255)"})
			return
		}
		req.Bio = &v
	}
	if req.AvatarURL != nil {
		v := strings.TrimSpace(*req.AvatarURL)
		if len(v) > 1024 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10034, "message": "avatar url too long"})
			return
		}
		req.AvatarURL = &v
	}
	u, err := h.repo.UpdateProfile(c.Request.Context(), uid, UpdateProfileParams{
		Nickname: req.Nickname, Bio: req.Bio, AvatarURL: req.AvatarURL,
	})
	if errors.Is(err, ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10030, "message": "user not found"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}
