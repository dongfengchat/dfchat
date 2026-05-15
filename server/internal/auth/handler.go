package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	pkgauth "github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/mailer"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	emailRe    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

type Handler struct {
	svc           *Service
	issuer        *pkgauth.Issuer
	refreshStore  *RefreshStore
	mailer        *mailer.Mailer
	pool          *pgxpool.Pool
	publicBaseURL string
}

func NewHandler(svc *Service, issuer *pkgauth.Issuer, refresh *RefreshStore, m *mailer.Mailer, pool *pgxpool.Pool, publicBaseURL string) *Handler {
	return &Handler{svc: svc, issuer: issuer, refreshStore: refresh, mailer: m, pool: pool, publicBaseURL: publicBaseURL}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("/auth/register", h.register)
	rg.POST("/auth/login", h.login)
	rg.POST("/auth/refresh", h.refresh)
	// Forgot/reset password are unauthenticated — user has no token yet.
	rg.POST("/auth/forgot-password", h.forgotPassword)
	rg.POST("/auth/reset-password", h.resetPassword)
	// Email verification — the GET is public (link clicked from inbox);
	// the POST to resend is authenticated.
	rg.GET("/auth/verify-email", h.verifyEmail)
	// Session management — guarded by access-token auth.
	guarded := rg.Group("/auth")
	guarded.Use(middleware.RequireAuth(h.issuer))
	guarded.GET("/sessions", h.listSessions)
	guarded.DELETE("/sessions/:id", h.revokeSession)
	guarded.POST("/logout", h.logout)
	guarded.POST("/change-password", h.changePassword)
	guarded.POST("/sessions/revoke-others", h.revokeOthers)
	guarded.POST("/send-verification", h.sendVerification)
}

// ============= Email verification + Password reset ==============

func newToken() (string, error) {
	b := make([]byte, 24) // 48 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sendVerification (authed) generates a token, stores it, mails the user a
// click link. Rate limited by the global middleware; an additional 60s
// cooldown is enforced via the latest token's created_at.
func (h *Handler) sendVerification(c *gin.Context) {
	uid := c.MustGet("userID").(int64)

	// Look up the user's email + verified status.
	var email string
	var verified bool
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT email, email_verified FROM users WHERE id = $1`, uid).Scan(&email, &verified)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 10010, "message": "user not found"})
		return
	}
	if verified {
		c.JSON(http.StatusOK, gin.H{"alreadyVerified": true})
		return
	}

	// 60s cooldown to prevent spam.
	var lastIssued time.Time
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT created_at FROM email_verify_tokens WHERE user_id = $1
		 ORDER BY created_at DESC LIMIT 1`, uid).Scan(&lastIssued)
	if !lastIssued.IsZero() && time.Since(lastIssued) < 60*time.Second {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": 10031, "message": "请求过于频繁，请 1 分钟后再试"})
		return
	}

	tok, err := newToken()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO email_verify_tokens (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		tok, uid, expiresAt); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	// Link points straight at the API — it renders a tiny "验证成功" page
	// for any browser the user happens to be in (no static-site routing needed).
	apiBase := strings.Replace(strings.TrimRight(h.publicBaseURL, "/"), "://", "://app.", 1)
	link := fmt.Sprintf("%s/api/v1/auth/verify-email?token=%s", apiBase, tok)
	body := fmt.Sprintf("你好，\n\n点击下方链接验证你的邮箱（24 小时内有效）：\n\n%s\n\n如果不是你本人操作，请忽略此邮件。\n\n— DFCHAT", link)
	_ = h.mailer.Send(email, "验证你的 DFCHAT 邮箱", body)

	resp := gin.H{"ok": true}
	if !h.mailer.Enabled() {
		// Dev convenience: surface the link in the API response so the user
		// doesn't need to grep the API logs.
		resp["devLink"] = link
	}
	c.JSON(http.StatusOK, resp)
}

// verifyEmail (public) processes the link from the mail. Returns plain
// text so it renders cleanly when opened in a browser tab.
func (h *Handler) verifyEmail(c *gin.Context) {
	tok := c.Query("token")
	if tok == "" {
		c.String(http.StatusBadRequest, "缺少 token 参数")
		return
	}
	var uid int64
	var expiresAt time.Time
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT user_id, expires_at FROM email_verify_tokens WHERE token = $1`, tok).Scan(&uid, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.String(http.StatusBadRequest, "验证链接无效或已使用")
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "内部错误")
		return
	}
	if time.Now().After(expiresAt) {
		c.String(http.StatusBadRequest, "验证链接已过期，请重新请求")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "内部错误")
		return
	}
	defer tx.Rollback(c.Request.Context())
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE users SET email_verified = true WHERE id = $1`, uid); err != nil {
		c.String(http.StatusInternalServerError, "更新失败")
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM email_verify_tokens WHERE user_id = $1`, uid); err != nil {
		c.String(http.StatusInternalServerError, "更新失败")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.String(http.StatusInternalServerError, "更新失败")
		return
	}

	c.String(http.StatusOK, "✅ 邮箱验证成功！可以回到 DFCHAT 客户端继续使用。")
}

type forgotPasswordReq struct {
	Email string `json:"email"`
}

// forgotPassword always returns 200 regardless of whether the email exists
// — prevents "email enumeration" leaks. If the email matches a user we
// generate a reset token and mail the link; otherwise we silently no-op.
func (h *Handler) forgotPassword(c *gin.Context) {
	var req forgotPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" {
		c.JSON(http.StatusOK, gin.H{"ok": true}) // intentional silent OK
		return
	}
	email := strings.TrimSpace(req.Email)

	var uid int64
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	tok, err := newToken()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	expiresAt := time.Now().Add(60 * time.Minute) // shorter than verify (security)
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO password_reset_tokens (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		tok, uid, expiresAt); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	// Static HTML page on the marketing site handles the form + POST to API.
	link := fmt.Sprintf("%s/reset-password.html?token=%s", strings.TrimRight(h.publicBaseURL, "/"), tok)
	body := fmt.Sprintf("你好，\n\n收到了重置 DFCHAT 密码的请求。点击下方链接设置新密码（1 小时内有效）：\n\n%s\n\n如果不是你本人操作，请忽略此邮件；你的密码不会被改变。\n\n— DFCHAT", link)
	_ = h.mailer.Send(email, "重置 DFCHAT 密码", body)

	resp := gin.H{"ok": true}
	if !h.mailer.Enabled() {
		resp["devLink"] = link
	}
	c.JSON(http.StatusOK, resp)
}

type resetPasswordReq struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

func (h *Handler) resetPassword(c *gin.Context) {
	var req resetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" || len(req.NewPassword) < 8 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10040, "message": "token + 至少 8 位新密码"})
		return
	}

	var uid int64
	var expiresAt time.Time
	var used bool
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT user_id, expires_at, used FROM password_reset_tokens WHERE token = $1`, req.Token).Scan(&uid, &expiresAt, &used)
	if errors.Is(err, pgx.ErrNoRows) || used {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10041, "message": "重置链接无效或已使用"})
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if time.Now().After(expiresAt) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10042, "message": "重置链接已过期，请重新申请"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE users SET password_hash = $1 WHERE id = $2`, hash, uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE password_reset_tokens SET used = true WHERE token = $1`, req.Token); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Invalidate all refresh tokens so any lurking attacker session dies.
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM refresh_tokens WHERE user_id = $1`, uid); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
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
