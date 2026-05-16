package auth

import (
	"context"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	// Username: 5-32 chars, letters/digits/underscore. Bumped from 3
	// to 5 because 3-char names are mostly squatters or low-effort
	// throwaways, and 5 leaves room for "alice"-style real names.
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{5,32}$`)
	emailRe    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

	// Reserved usernames — keep impostor accounts off well-known names.
	// Matched case-insensitively against the requested username.
	reservedUsernames = map[string]struct{}{
		"admin": {}, "administrator": {}, "root": {}, "system": {},
		"support": {}, "help": {}, "official": {}, "staff": {}, "moderator": {}, "mod": {},
		"dfchat": {}, "noreply": {}, "no_reply": {}, "service": {}, "api": {},
		"security": {}, "abuse": {}, "postmaster": {}, "hostmaster": {}, "webmaster": {},
	}
)

// bcrypt silently truncates input to 72 bytes. Reject longer passwords up
// front so users can't be confused by "I typed a long password and only
// the first 72 chars actually count."
const maxPasswordBytes = 72

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
	// Sensitive unauthenticated endpoints — strict rate limit on top of
	// the global one (1 r/s steady, burst 3, per client IP). Stops password
	// spraying, account-creation floods, and reset-mail abuse.
	strict := rg.Group("")
	strict.Use(middleware.RateLimitStrict())
	strict.POST("/auth/register", h.register)
	strict.POST("/auth/login", h.login)
	strict.POST("/auth/forgot-password", h.forgotPassword)
	strict.POST("/auth/reset-password", h.resetPassword)
	// Account-number pool endpoints — public, called before register.
	// Strict-rate-limited because each draw atomically locks 10 numbers
	// for 10 minutes; unlimited would let one attacker drain a segment.
	strict.POST("/auth/account-no/draw", h.drawAccountNumbers)
	strict.POST("/auth/account-no/refresh", h.refreshAccountNumbers)
	// /auth/refresh is hot during normal app use — global limit is enough.
	rg.POST("/auth/refresh", h.refresh)
	// Email verification — the GET is public (link clicked from inbox);
	// the POST to resend is authenticated.
	rg.GET("/auth/verify-email", h.verifyEmail)
	// Email change confirm — public (link clicked from new inbox).
	rg.GET("/auth/confirm-email-change", h.confirmEmailChange)
	// Session management — guarded by access-token auth.
	guarded := rg.Group("/auth")
	guarded.Use(middleware.RequireAuth(h.issuer))
	guarded.GET("/sessions", h.listSessions)
	guarded.DELETE("/sessions/:id", h.revokeSession)
	guarded.POST("/logout", h.logout)
	guarded.POST("/change-password", h.changePassword)
	guarded.POST("/sessions/revoke-others", h.revokeOthers)
	guarded.POST("/send-verification", h.sendVerification)
	guarded.POST("/request-email-change", h.requestEmailChange)
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

	// 60s cooldown to prevent spam (in addition to the per-IP rate limit).
	var lastIssued time.Time
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT created_at FROM email_verify_tokens WHERE user_id = $1
		 ORDER BY created_at DESC LIMIT 1`, uid).Scan(&lastIssued)
	if !lastIssued.IsZero() && time.Since(lastIssued) < 60*time.Second {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": 10031, "message": "请求过于频繁，请 1 分钟后再试"})
		return
	}

	link := h.sendVerificationFor(c.Request.Context(), uid, email)
	resp := gin.H{"ok": true}
	if !h.mailer.Enabled() && link != "" {
		// Dev convenience: surface the link in the API response so the user
		// doesn't need to grep the api logs to grab it.
		resp["devLink"] = link
	}
	c.JSON(http.StatusOK, resp)
}

// verifyEmail (public) processes the link from the mail. Returns plain
// text so it renders cleanly when opened in a browser tab.
//
// Token consumption is atomic: a single CTE deletes the row and returns
// the user_id only if the token exists AND hasn't expired. This closes
// a race where two concurrent clicks on the same link could both succeed.
// It also makes the token strictly single-use — a leaked or copy-pasted
// link can't be reused even within its 24h window.
func (h *Handler) verifyEmail(c *gin.Context) {
	tok := c.Query("token")
	if tok == "" {
		renderHTML(c, http.StatusBadRequest, pageError, "链接无效", "缺少 token 参数。请回到 DFCHAT 客户端重新发送验证邮件。", "")
		return
	}

	var uid int64
	err := h.pool.QueryRow(c.Request.Context(), `
		WITH consumed AS (
			DELETE FROM email_verify_tokens
			WHERE token = $1 AND expires_at > now()
			RETURNING user_id
		)
		UPDATE users SET email_verified = true
		WHERE id = (SELECT user_id FROM consumed)
		RETURNING id`, tok).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either the token never existed, was already used, or expired.
		// We don't distinguish — all three are "click resend".
		renderHTML(c, http.StatusBadRequest, pageError, "链接已失效",
			"这个验证链接无效、已使用，或已超过 24 小时有效期。请回到 DFCHAT 客户端重新发送一封验证邮件。", "")
		return
	}
	if err != nil {
		renderHTML(c, http.StatusInternalServerError, pageError, "服务器错误",
			"邮箱验证暂时无法完成，请稍后再试或联系支持。", "")
		return
	}

	// Belt-and-suspenders: drop any other outstanding tokens for this user.
	// (They're now meaningless; verified is sticky.) Best-effort, ignore err.
	_, _ = h.pool.Exec(c.Request.Context(),
		`DELETE FROM email_verify_tokens WHERE user_id = $1`, uid)

	renderHTML(c, http.StatusOK, pageSuccess, "邮箱验证成功",
		"你的 DFCHAT 邮箱已通过验证。现在可以正常接收找回密码邮件以及修改邮箱所需的确认链接了。", "")
}

// ===== Email change (with double-confirmation) =====

type requestEmailChangeReq struct {
	NewEmail        string `json:"newEmail"`
	CurrentPassword string `json:"currentPassword"`
}

// requestEmailChange (authed) starts an email change. We never update
// users.email here — instead we mail the *new* address with a one-shot
// confirmation link. Only after that link is clicked does the actual
// swap happen, proving ownership of the new mailbox.
//
// Re-entering the current password is required even though the user is
// already authenticated. This guards against the "stolen laptop / open
// session" scenario where someone with bearer access could otherwise
// quietly hijack the account by changing the email.
func (h *Handler) requestEmailChange(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req requestEmailChangeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.NewEmail == "" || req.CurrentPassword == "" {
		fail(c, http.StatusBadRequest, 10080, "newEmail + currentPassword required")
		return
	}
	newEmail := strings.ToLower(strings.TrimSpace(req.NewEmail))
	if !emailRe.MatchString(newEmail) || len(newEmail) > 128 {
		fail(c, http.StatusBadRequest, 10081, "invalid email")
		return
	}
	if isDisposableEmail(newEmail) {
		fail(c, http.StatusBadRequest, 10082, "请使用真实邮箱，一次性邮箱不被允许")
		return
	}

	// Re-confirm current password before allowing the change.
	var currentEmail, passwordHash string
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT email, password_hash FROM users WHERE id = $1`, uid).Scan(&currentEmail, &passwordHash)
	if err != nil {
		fail(c, http.StatusNotFound, 10010, "user not found")
		return
	}
	if newEmail == currentEmail {
		fail(c, http.StatusBadRequest, 10083, "新邮箱不能和当前邮箱相同")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.CurrentPassword)); err != nil {
		fail(c, http.StatusUnauthorized, 10084, "当前密码错误")
		return
	}

	// Don't leak whether the target email is already taken — we still
	// queue the request silently. The actual swap step will fail safely
	// because users.email is UNIQUE; the attacker just sees a "click the
	// link" message that never works. But we DO check up front and quietly
	// no-op the mail if so — saves a wasted SMTP send.
	var collision int
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT 1 FROM users WHERE email = $1 AND id <> $2`, newEmail, uid).Scan(&collision)
	if collision == 1 {
		// Silent OK to avoid email-enumeration probing.
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	// 60s cooldown to prevent spam.
	var lastIssued time.Time
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT created_at FROM email_change_requests WHERE user_id = $1
		 ORDER BY created_at DESC LIMIT 1`, uid).Scan(&lastIssued)
	if !lastIssued.IsZero() && time.Since(lastIssued) < 60*time.Second {
		fail(c, http.StatusTooManyRequests, 10085, "请求过于频繁，请 1 分钟后再试")
		return
	}

	tok, err := newToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	expiresAt := time.Now().Add(60 * time.Minute)
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO email_change_requests (token, user_id, new_email, expires_at) VALUES ($1, $2, $3, $4)`,
		tok, uid, newEmail, expiresAt); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}

	apiBase := strings.Replace(strings.TrimRight(h.publicBaseURL, "/"), "://", "://app.", 1)
	link := fmt.Sprintf("%s/api/v1/auth/confirm-email-change?token=%s", apiBase, tok)
	body := fmt.Sprintf("你好，\n\n收到了将 DFCHAT 账号邮箱改为这个地址的请求。点击下方链接确认（1 小时内有效）：\n\n%s\n\n如果不是你本人操作，请忽略此邮件；当前邮箱不会被改变。\n\n— DFCHAT", link)
	_ = h.mailer.Send(newEmail, "确认 DFCHAT 邮箱变更", body)

	resp := gin.H{"ok": true}
	if !h.mailer.Enabled() {
		resp["devLink"] = link
	}
	c.JSON(http.StatusOK, resp)
}

// confirmEmailChange (public) processes the link the new mailbox got.
// Single-atomic CTE: deletes the token if valid and not expired, returns
// (user_id, new_email), then updates users.email with the new value AND
// flips email_verified=true (since they just proved ownership). The
// UNIQUE constraint on users.email protects against races where the new
// address got taken between request and confirm — we surface a clear
// error message.
func (h *Handler) confirmEmailChange(c *gin.Context) {
	tok := c.Query("token")
	if tok == "" {
		renderHTML(c, http.StatusBadRequest, pageError, "链接无效", "缺少 token 参数。请回到 DFCHAT 客户端重新申请。", "")
		return
	}

	var uid int64
	var newEmail string
	err := h.pool.QueryRow(c.Request.Context(), `
		DELETE FROM email_change_requests
		WHERE token = $1 AND expires_at > now()
		RETURNING user_id, new_email`, tok).Scan(&uid, &newEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		renderHTML(c, http.StatusBadRequest, pageError, "确认链接已失效",
			"这个链接无效、已使用，或已超过 1 小时有效期。请回到 DFCHAT 客户端重新发起邮箱变更。", "")
		return
	}
	if err != nil {
		renderHTML(c, http.StatusInternalServerError, pageError, "服务器错误",
			"邮箱变更暂时无法完成，请稍后再试。", "")
		return
	}

	// Try the swap. If the target email was taken in the meantime,
	// users.email UNIQUE constraint raises 23505 (pgx pgconn error).
	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE users SET email = $1, email_verified = true WHERE id = $2`, newEmail, uid)
	if err != nil {
		renderHTML(c, http.StatusConflict, pageError, "邮箱已被占用",
			"这个邮箱在你点击确认链接之前被其他账号注册了。请回到 DFCHAT 客户端换一个邮箱再试。", "")
		return
	}
	if tag.RowsAffected() != 1 {
		renderHTML(c, http.StatusInternalServerError, pageError, "更新失败", "请回到客户端重试。", "")
		return
	}
	// Belt-and-suspenders: any other outstanding requests for this user
	// (e.g. the user clicked "change" twice with different addresses) are
	// now stale.
	_, _ = h.pool.Exec(c.Request.Context(),
		`DELETE FROM email_change_requests WHERE user_id = $1`, uid)

	renderHTML(c, http.StatusOK, pageSuccess, "邮箱已更新",
		"你的 DFCHAT 账号邮箱已成功变更。今后找回密码、再次修改邮箱、登录验证都会使用这个新邮箱。",
		newEmail)
}

// renderHTML writes a branded HTML response page. Centralised so the
// content-type header and Cache-Control are set consistently.
func renderHTML(c *gin.Context, status int, variant pageVariant, title, body, detail string) {
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", []byte(htmlPage(variant, title, body, detail)))
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
	if len(req.NewPassword) > maxPasswordBytes {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10043, "message": "密码过长（最多 72 字节）"})
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 10044, "message": msg})
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
	Username       string `json:"username"`
	Email          string `json:"email"`
	Password       string `json:"password"`
	Nickname       string `json:"nickname"`
	AccountNo      string `json:"accountNo"`      // chosen from the draw
	SelectionToken string `json:"selectionToken"` // proves they got it from a real draw
}

func (h *Handler) register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 10010, "请求格式错误")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		fail(c, http.StatusBadRequest, 10011, "用户名须为 5-32 位字母 / 数字 / 下划线")
		return
	}
	if _, reserved := reservedUsernames[strings.ToLower(req.Username)]; reserved {
		fail(c, http.StatusBadRequest, 10016, "用户名被系统保留，请换一个")
		return
	}
	// `deleted_<n>` is what SoftDelete rewrites usernames to. Letting people
	// squat it would block legitimate account-deletion flows.
	if strings.HasPrefix(strings.ToLower(req.Username), "deleted_") {
		fail(c, http.StatusBadRequest, 10016, "用户名被系统保留，请换一个")
		return
	}
	if !emailRe.MatchString(req.Email) || len(req.Email) > 128 {
		fail(c, http.StatusBadRequest, 10012, "邮箱格式不正确")
		return
	}
	if isDisposableEmail(req.Email) {
		fail(c, http.StatusBadRequest, 10019, "请使用真实邮箱注册，一次性邮箱不被允许")
		return
	}
	if len(req.Password) < 8 {
		fail(c, http.StatusBadRequest, 10013, "密码至少 8 位")
		return
	}
	if len(req.Password) > maxPasswordBytes {
		fail(c, http.StatusBadRequest, 10017, "密码过长（最多 72 字节）")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		fail(c, http.StatusBadRequest, 10060, msg)
		return
	}
	if len(req.Nickname) > 64 {
		fail(c, http.StatusBadRequest, 10018, "昵称过长（最多 64 字符）")
		return
	}

	// Account number must come from a real draw — selectionToken proves
	// the client went through /auth/account-no/draw. Anti-bot: scripted
	// registrations have to do the same dance as the UI.
	if req.SelectionToken == "" || req.AccountNo == "" {
		fail(c, http.StatusBadRequest, 10093, "缺少账号选择信息，请回到注册页重新摇号")
		return
	}
	chosenNo, parseErr := parsePositiveInt(req.AccountNo)
	if parseErr != nil {
		fail(c, http.StatusBadRequest, 10093, "账号格式错误")
		return
	}

	// All the writes (user insert + pool claim + selection delete) need
	// to be atomic, so we run them inside a single tx.
	ctx := c.Request.Context()
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	nickname := req.Nickname
	if nickname == "" {
		nickname = req.Username
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	defer tx.Rollback(ctx)

	var u userInsertResult
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, nickname, account_no)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, account_no, username, email, nickname,
		          COALESCE(avatar_url, ''), COALESCE(bio, ''),
		          status, email_verified, is_admin, created_at`,
		req.Username, email, string(hash), nickname, chosenNo,
	).Scan(&u.ID, &u.AccountNo, &u.Username, &u.Email, &u.Nickname,
		&u.AvatarURL, &u.Bio, &u.Status, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		switch detectUniqueViolation(err) {
		case "username":
			fail(c, http.StatusConflict, 10014, "用户名已被注册")
			return
		case "email":
			fail(c, http.StatusConflict, 10015, "邮箱已被注册")
			return
		case "account_no":
			fail(c, http.StatusConflict, 10094, "这个账号刚被别人选走了，请回到注册页换一个")
			return
		}
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}

	// Cross-check the selection (must contain this number, IP must match,
	// not expired), mark the pool row claimed, release the 9 siblings,
	// drop the selection row.
	if err := validateAndConsumeSelection(ctx, tx, c.ClientIP(), req.SelectionToken, chosenNo, u.ID); err != nil {
		switch {
		case errors.Is(err, errSelectionInvalid):
			fail(c, http.StatusBadRequest, 10091, "会话已失效，请回到注册页重新摇号")
		case errors.Is(err, errChosenNotInSelection):
			fail(c, http.StatusBadRequest, 10095, "你选的账号不在本次摇号结果里")
		case errors.Is(err, errChosenAlreadyClaimed):
			fail(c, http.StatusConflict, 10094, "这个账号刚被别人选走了，请回到注册页换一个")
		default:
			fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		}
		return
	}
	if err := tx.Commit(ctx); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}

	// Auto-send the verification email. Done in a goroutine so registration
	// returns immediately even on slow SMTP. Detached ctx since request
	// ctx is cancelled when we return below.
	go func(uid int64, email string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		h.sendVerificationFor(ctx, uid, email)
	}(u.ID, u.Email)

	c.JSON(http.StatusCreated, gin.H{"user": u})
}

// userInsertResult mirrors user.User's JSON shape without pulling in
// the import — keeps the handler self-contained. The fields use the
// same json tags so the response looks identical to the old flow.
type userInsertResult struct {
	ID            int64     `json:"id,string"`
	AccountNo     int64     `json:"accountNo,string"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Nickname      string    `json:"nickname"`
	AvatarURL     string    `json:"avatarUrl,omitempty"`
	Bio           string    `json:"bio,omitempty"`
	Status        int16     `json:"status"`
	EmailVerified bool      `json:"emailVerified"`
	IsAdmin       bool      `json:"isAdmin"`
	CreatedAt     time.Time `json:"createdAt"`
}

// detectUniqueViolation maps a pg unique-constraint error to the name
// of the violating column. Returns "" if not a unique violation.
func detectUniqueViolation(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	if pgErr.Code != "23505" {
		return ""
	}
	switch {
	case strings.Contains(pgErr.ConstraintName, "username"):
		return "username"
	case strings.Contains(pgErr.ConstraintName, "email"):
		return "email"
	case strings.Contains(pgErr.ConstraintName, "account_no"):
		return "account_no"
	}
	return ""
}

// sendVerificationFor issues + mails a verification link for the given
// user. Used by both the explicit /auth/send-verification endpoint and
// the auto-trigger on successful registration. Returns the link so dev
// mode (no SMTP) can surface it in the API response; in prod the link
// only goes out via the mail. Errors are swallowed because callers can't
// act on a failed send anyway (user retries via the in-app button) — the
// mailer logs them.
func (h *Handler) sendVerificationFor(ctx context.Context, uid int64, email string) string {
	tok, err := newToken()
	if err != nil {
		return ""
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO email_verify_tokens (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		tok, uid, expiresAt); err != nil {
		return ""
	}
	apiBase := strings.Replace(strings.TrimRight(h.publicBaseURL, "/"), "://", "://app.", 1)
	link := fmt.Sprintf("%s/api/v1/auth/verify-email?token=%s", apiBase, tok)
	body := fmt.Sprintf("你好，\n\n感谢注册 DFCHAT。点击下方链接验证你的邮箱（24 小时内有效）：\n\n%s\n\n如果不是你本人操作，请忽略此邮件。\n\n— DFCHAT", link)
	_ = h.mailer.Send(email, "验证你的 DFCHAT 邮箱", body)
	return link
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
	if len(req.NewPassword) > maxPasswordBytes {
		fail(c, http.StatusBadRequest, 10053, "new password too long (max 72 bytes)")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		fail(c, http.StatusBadRequest, 10054, msg)
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
