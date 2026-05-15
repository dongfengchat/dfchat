package auth

import (
	"errors"
	"net/http"
	"regexp"

	pkgauth "github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
)

var (
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	emailRe    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

type Handler struct {
	svc          *Service
	issuer       *pkgauth.Issuer
	refreshStore *RefreshStore
}

func NewHandler(svc *Service, issuer *pkgauth.Issuer, refresh *RefreshStore) *Handler {
	return &Handler{svc: svc, issuer: issuer, refreshStore: refresh}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("/auth/register", h.register)
	rg.POST("/auth/login", h.login)
	rg.POST("/auth/refresh", h.refresh)
	// Session management — guarded by access-token auth.
	guarded := rg.Group("/auth")
	guarded.Use(middleware.RequireAuth(h.issuer))
	guarded.GET("/sessions", h.listSessions)
	guarded.DELETE("/sessions/:id", h.revokeSession)
	guarded.POST("/logout", h.logout)
	guarded.POST("/change-password", h.changePassword)
	guarded.POST("/sessions/revoke-others", h.revokeOthers)
}

type registerReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

func (h *Handler) register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 10010, "invalid request body")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		fail(c, http.StatusBadRequest, 10011, "username must be 3-32 chars, letters/digits/underscore")
		return
	}
	if !emailRe.MatchString(req.Email) {
		fail(c, http.StatusBadRequest, 10012, "invalid email")
		return
	}
	if len(req.Password) < 8 {
		fail(c, http.StatusBadRequest, 10013, "password must be at least 8 characters")
		return
	}

	u, err := h.svc.Register(c.Request.Context(), RegisterInput(req))
	switch {
	case errors.Is(err, ErrUsernameTaken):
		fail(c, http.StatusConflict, 10014, "username already taken")
		return
	case errors.Is(err, ErrEmailTaken):
		fail(c, http.StatusConflict, 10015, "email already registered")
		return
	case err != nil:
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": u})
}

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 10020, "invalid request body")
		return
	}
	if req.Login == "" || req.Password == "" {
		fail(c, http.StatusBadRequest, 10021, "login and password required")
		return
	}

	res, err := h.svc.Login(c.Request.Context(), req.Login, req.Password, c.ClientIP(), c.Request.UserAgent())
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		fail(c, http.StatusUnauthorized, 10022, "invalid username or password")
		return
	case errors.Is(err, ErrAccountDisabled):
		fail(c, http.StatusForbidden, 10023, "account disabled")
		return
	case err != nil:
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"accessToken":  res.AccessToken,
		"refreshToken": res.RefreshToken,
		"user":         res.User,
	})
}

type refreshReq struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *Handler) refresh(c *gin.Context) {
	var req refreshReq
	if err := c.ShouldBindJSON(&req); err != nil || req.RefreshToken == "" {
		fail(c, http.StatusBadRequest, 10024, "refreshToken required")
		return
	}
	res, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken, c.Request.UserAgent())
	switch {
	case errors.Is(err, ErrRefreshInvalid):
		fail(c, http.StatusUnauthorized, 10025, "refresh token invalid")
		return
	case errors.Is(err, ErrRefreshExpired):
		fail(c, http.StatusUnauthorized, 10026, "refresh token expired")
		return
	case err != nil:
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"accessToken":  res.AccessToken,
		"refreshToken": res.RefreshToken,
		"user":         res.User,
	})
}

// currentRefreshFromHeader pulls the calling client's refresh token out of
// a custom header so we can mark the corresponding session as the current
// device in the listing UI. Not required; if absent we just don't flag any.
func currentRefreshFromHeader(c *gin.Context) string {
	return c.GetHeader("X-Refresh-Token")
}

func (h *Handler) listSessions(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	current := currentRefreshFromHeader(c)
	sessions, err := h.refreshStore.ListSessions(c.Request.Context(), uid, current)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *Handler) revokeSession(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	prefix := c.Param("id")
	ok, err := h.refreshStore.RevokeByPrefix(c.Request.Context(), uid, prefix)
	if err != nil {
		fail(c, http.StatusBadRequest, 10027, err.Error())
		return
	}
	if !ok {
		fail(c, http.StatusNotFound, 10028, "session not found")
		return
	}
	c.Status(http.StatusNoContent)
}

type logoutReq struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *Handler) logout(c *gin.Context) {
	var req logoutReq
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		_ = h.refreshStore.Revoke(c.Request.Context(), req.RefreshToken)
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) revokeOthers(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	keep := c.GetHeader("X-Refresh-Token")
	n, err := h.refreshStore.RevokeOthers(c.Request.Context(), uid, keep)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": n})
}

type changePasswordReq struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *Handler) changePassword(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		fail(c, http.StatusBadRequest, 10050, "currentPassword and newPassword required")
		return
	}
	if err := h.svc.ChangePassword(c.Request.Context(), uid, req.CurrentPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrPasswordMismatch):
			fail(c, http.StatusUnauthorized, 10051, "current password is wrong")
		case errors.Is(err, ErrPasswordWeak):
			fail(c, http.StatusBadRequest, 10052, "new password must be at least 8 chars")
		default:
			fail(c, http.StatusInternalServerError, 50001, "internal error")
		}
		return
	}
	c.Status(http.StatusNoContent)
}

func fail(c *gin.Context, status, code int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "message": msg})
}
