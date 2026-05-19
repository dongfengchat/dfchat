package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	authdomain "github.com/dongfang/dfchat/server/internal/auth"
	"github.com/dongfang/dfchat/server/internal/live"
	"github.com/dongfang/dfchat/server/pkg/audit"
	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool        *pgxpool.Pool
	issuer      *auth.Issuer
	audit       *audit.Logger
	liveRepo    *live.Repo
	liveHandler *live.Handler // for shared teardown (force-end / ban → kick viewers)
	liveHLSURL  string        // public HLS base, used to derive thumbnail URLs
}

func NewHandler(pool *pgxpool.Pool, issuer *auth.Issuer, auditor *audit.Logger, liveRepo *live.Repo, liveHandler *live.Handler, liveHLSURL string) *Handler {
	return &Handler{
		pool:        pool,
		issuer:      issuer,
		audit:       auditor,
		liveRepo:    liveRepo,
		liveHandler: liveHandler,
		liveHLSURL:  liveHLSURL,
	}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/admin")
	g.Use(middleware.RequireAuth(h.issuer))
	g.Use(h.requireAdmin)
	g.GET("/stats", h.stats)
	g.GET("/users", h.listUsers)
	g.PATCH("/users/:id/status", h.patchUserStatus)
	g.GET("/users/:id/logins", h.userLoginHistory)
	g.POST("/users/:id/force-logout", h.forceLogoutUser)
	g.GET("/account-pool", h.accountPoolStats)
	g.GET("/premium-numbers", h.listPremiumNumbers)
	g.POST("/premium-numbers/:no/grant", h.grantPremiumNumber)
	g.POST("/premium-numbers/:no/release", h.releasePremiumNumber)

	// Live moderation — platform admin view + actions on any room.
	g.GET("/live/rooms", h.listLiveRooms)
	g.POST("/live/rooms/:id/force-end", h.forceEndLive)
	g.PATCH("/live/rooms/:id/ban", h.banLiveRoom)
	g.DELETE("/live/rooms/:id", h.adminDeleteLiveRoom)

	// User reports queue + patrol grid (status=1 only, with thumbnail).
	g.GET("/live/reports", h.listLiveReports)
	g.POST("/live/reports/:id/resolve", h.resolveLiveReport)
	g.GET("/live/patrol", h.livePatrol)
	// Evidence archive served from local disk — AI-moderation worker
	// snapshots thumbnails at flag time so admin reviews see the
	// actual offending frame (live thumbs churn every 30 s).
	g.GET("/live/evidence/:day/:name", h.serveEvidence)
	// AI verdict audit log — every tick the worker fires lands here,
	// not just the flagged ones. Lets the admin spot-check what
	// Gemma/Claude/GPT is judging and override mistakes.
	g.GET("/live/verdicts", h.listLiveVerdicts)
	g.POST("/live/verdicts/:id/label", h.labelLiveVerdict)
	g.POST("/live/verdicts/:id/pin", h.pinLiveVerdict)
}

func (h *Handler) requireAdmin(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var isAdmin bool
	err := h.pool.QueryRow(c.Request.Context(), `SELECT is_admin FROM users WHERE id = $1`, uid).Scan(&isAdmin)
	if err != nil || !isAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 70010, "message": "admin only"})
		return
	}
	c.Next()
}

type stats struct {
	TotalUsers     int64 `json:"totalUsers"`
	TotalGroups    int64 `json:"totalGroups"`
	MessagesToday  int64 `json:"messagesToday"`
	TotalMessages  int64 `json:"totalMessages"`
	TotalFiles     int64 `json:"totalFiles"`
}

func (h *Handler) stats(c *gin.Context) {
	var s stats
	ctx := c.Request.Context()
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&s.TotalUsers)
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM groups`).Scan(&s.TotalGroups)
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages WHERE created_at >= date_trunc('day', now())`).Scan(&s.MessagesToday)
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages`).Scan(&s.TotalMessages)
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM files WHERE status = 1`).Scan(&s.TotalFiles)
	c.JSON(http.StatusOK, s)
}

type adminUser struct {
	ID               int64  `json:"id,string"`
	AccountNo        int64  `json:"accountNo,string"`
	Username         string `json:"username"`
	Email            string `json:"email"`
	Nickname         string `json:"nickname"`
	Status           int16  `json:"status"`
	IsAdmin          bool   `json:"isAdmin"`
	EmailVerified    bool   `json:"emailVerified"`
	LastLogin        string `json:"lastLoginAt,omitempty"`
	LastLoginIP      string `json:"lastLoginIp,omitempty"`
	RegisteredFromIP string `json:"registeredFromIp,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

// listUsers returns paginated user rows for the admin console. Search
// hits username / email / nickname / account_no — admins typically know
// one of these from a support ticket. Sort newest-first so spam waves
// surface immediately.
func (h *Handler) listUsers(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	search := c.Query("search")
	ipFilter := c.Query("ip") // optional: filter by registered_from_ip OR last_login_ip

	args := []any{limit, offset}
	q := `SELECT id, account_no, username, email, nickname, status, is_admin, email_verified,
	             COALESCE(last_login_at::text, ''),
	             COALESCE(last_login_ip, ''),
	             COALESCE(registered_from_ip, ''),
	             created_at::text
	      FROM users`
	where := []string{}
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, "(username ILIKE $"+strconv.Itoa(len(args))+
			" OR email ILIKE $"+strconv.Itoa(len(args))+
			" OR nickname ILIKE $"+strconv.Itoa(len(args))+
			" OR account_no::text = trim($"+strconv.Itoa(len(args))+", '%'))")
	}
	if ipFilter != "" {
		args = append(args, ipFilter)
		where = append(where, "(registered_from_ip = $"+strconv.Itoa(len(args))+
			" OR last_login_ip = $"+strconv.Itoa(len(args))+")")
	}
	if len(where) > 0 {
		q += " WHERE " + joinAnd(where)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`

	rows, err := h.pool.Query(c.Request.Context(), q, args...)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()
	out := make([]adminUser, 0)
	for rows.Next() {
		var u adminUser
		if err := rows.Scan(&u.ID, &u.AccountNo, &u.Username, &u.Email, &u.Nickname,
			&u.Status, &u.IsAdmin, &u.EmailVerified,
			&u.LastLogin, &u.LastLoginIP, &u.RegisteredFromIP, &u.CreatedAt); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		out = append(out, u)
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func joinAnd(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for i := 1; i < len(ss); i++ {
		out += " AND " + ss[i]
	}
	return out
}

// userLoginHistory returns the most recent login attempts for a target
// user. Admin uses this to investigate "did account X actually log in
// from country Y on date Z" after a support ticket.
func (h *Handler) userLoginHistory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70020, "message": "invalid id"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out, err := authdomain.LoadLoginsForUser(c.Request.Context(), h.pool, id, limit)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": out})
}

// ===== Premium-number management =====
//
// Locked numbers (is_locked=true in account_no_pool) never come up in
// the random draw — they sit aside so we can grant them to specific
// users later (early adopters, VIPs, support escalations, eventually
// for sale). Admin tools:
//   - list:    GET /admin/premium-numbers?segment=X
//   - grant:   POST /admin/premium-numbers/:no/grant { "userId": "..." }
//             swaps the user's current account_no for the premium
//             number. The user's old account_no goes back into the
//             pool (no longer claimed by anyone).
//   - release: POST /admin/premium-numbers/:no/release
//             un-locks the number so it can flow back into normal
//             random draws.

type premiumNumber struct {
	AccountNo  int64  `json:"accountNo,string"`
	SegmentNo  int    `json:"segmentNo"`
	Claimed    bool   `json:"claimed"`
	ClaimedBy  int64  `json:"claimedBy,string,omitempty"`
	OwnerName  string `json:"ownerName,omitempty"`
}

func (h *Handler) listPremiumNumbers(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{limit}
	q := `SELECT p.account_no, p.segment_no,
	             (p.claimed_user_id IS NOT NULL) AS claimed,
	             COALESCE(p.claimed_user_id, 0)  AS claimed_by,
	             COALESCE(u.username, '')        AS owner_name
	      FROM account_no_pool p
	      LEFT JOIN users u ON u.id = p.claimed_user_id
	      WHERE p.is_locked = TRUE`
	if seg := c.Query("segment"); seg != "" {
		args = append(args, seg)
		q += ` AND p.segment_no = $` + strconv.Itoa(len(args))
	}
	q += ` ORDER BY p.account_no ASC LIMIT $1`

	rows, err := h.pool.Query(c.Request.Context(), q, args...)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()
	out := make([]premiumNumber, 0)
	for rows.Next() {
		var p premiumNumber
		if err := rows.Scan(&p.AccountNo, &p.SegmentNo, &p.Claimed, &p.ClaimedBy, &p.OwnerName); err != nil {
			continue
		}
		out = append(out, p)
	}
	c.JSON(http.StatusOK, gin.H{"numbers": out})
}

type grantPremiumReq struct {
	UserAccountNo string `json:"userAccountNo"` // recipient identified by their CURRENT account_no
}

// grantPremiumNumber assigns the premium number to the user identified
// by UserAccountNo. Two-leg swap in a single tx:
//   - mark the premium row claimed by user.id, clear is_locked
//   - put the user's previous number back as un-claimed (still locked
//     if it WAS a premium number, else regular — we preserve is_locked)
//   - flip users.account_no to the premium one
// All in one transaction so a partial failure can't leave a user with
// no number or two pool rows pointing at them.
func (h *Handler) grantPremiumNumber(c *gin.Context) {
	premiumNo, err := strconv.ParseInt(c.Param("no"), 10, 64)
	if err != nil || premiumNo <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70020, "message": "invalid number"})
		return
	}
	var req grantPremiumReq
	if err := c.ShouldBindJSON(&req); err != nil || req.UserAccountNo == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70021, "message": "userAccountNo required"})
		return
	}
	recipientAccNo, err := strconv.ParseInt(req.UserAccountNo, 10, 64)
	if err != nil || recipientAccNo <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70021, "message": "invalid userAccountNo"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer tx.Rollback(ctx)

	// Find the recipient + their current number.
	var userID int64
	var oldNo int64
	if err := tx.QueryRow(ctx,
		`SELECT id, account_no FROM users WHERE account_no = $1`, recipientAccNo,
	).Scan(&userID, &oldNo); err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 70023, "message": "用户不存在（该账号未注册）"})
		return
	}
	if oldNo == premiumNo {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70024, "message": "该用户已拥有这个号码"})
		return
	}

	// Premium row must be locked + unclaimed.
	var alreadyClaimed bool
	if err := tx.QueryRow(ctx,
		`SELECT (claimed_user_id IS NOT NULL) FROM account_no_pool WHERE account_no = $1 AND is_locked = TRUE`,
		premiumNo,
	).Scan(&alreadyClaimed); err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 70025, "message": "该靓号不在锁定池中"})
		return
	}
	if alreadyClaimed {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"code": 70026, "message": "该靓号已被其他用户占用"})
		return
	}

	// Swap. Order matters: clear the user's account_no first to avoid
	// the UNIQUE constraint firing mid-flight if oldNo and premiumNo
	// were ever the same (defensive — we checked above too).
	// (1) Premium row → claimed by user, unlock so it counts as
	//     a normal claimed row going forward.
	if _, err := tx.Exec(ctx,
		`UPDATE account_no_pool
		 SET claimed_user_id = $1, is_locked = FALSE, reserved_until = NULL
		 WHERE account_no = $2`, userID, premiumNo); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// (2) User's old number → unclaimed, available for someone else.
	if _, err := tx.Exec(ctx,
		`UPDATE account_no_pool SET claimed_user_id = NULL, reserved_until = NULL
		 WHERE account_no = $1`, oldNo); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// (3) Update user record.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET account_no = $1 WHERE id = $2`, premiumNo, userID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	actorID, _ := c.Get("userID")
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: actorID.(int64), Action: "account_no.grant_premium",
			TargetKind: "user", TargetID: userID,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
			Metadata: map[string]any{"premiumNo": premiumNo, "previousNo": oldNo},
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "newAccountNo": premiumNo, "previousAccountNo": oldNo})
}

// releasePremiumNumber un-locks a number so it can flow back into normal
// random draws. Refuses if the number is currently claimed by a user.
func (h *Handler) releasePremiumNumber(c *gin.Context) {
	no, err := strconv.ParseInt(c.Param("no"), 10, 64)
	if err != nil || no <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70020, "message": "invalid number"})
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE account_no_pool
		 SET is_locked = FALSE
		 WHERE account_no = $1 AND is_locked = TRUE AND claimed_user_id IS NULL`, no)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 70025, "message": "该号码不在可释放的锁定池中"})
		return
	}
	actorID, _ := c.Get("userID")
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: actorID.(int64), Action: "account_no.release_premium",
			TargetKind: "account_no", TargetID: no,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ===== Account-number pool stats =====

type segmentStat struct {
	SegmentNo  int   `json:"segmentNo"`
	RangeStart int64 `json:"rangeStart"`
	RangeEnd   int64 `json:"rangeEnd"`
	State      string `json:"state"`
	Total      int64 `json:"total"`
	Claimed    int64 `json:"claimed"`
	Locked     int64 `json:"locked"`     // premium, never randomly drawn
	Reserved   int64 `json:"reserved"`   // currently held by a draw session
	Free       int64 `json:"free"`       // available for drawing right now
	OpenedAt   string `json:"openedAt"`
}

// accountPoolStats returns per-segment counts. Admins use this to see
// when the next segment will need to open, how many premium numbers
// are still locked aside, and whether any segments are unusually drained
// (signal of pool-drain abuse).
func (h *Handler) accountPoolStats(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT s.segment_no, s.range_start, s.range_end, s.state, s.opened_at::text,
		       COUNT(p.account_no) FILTER (WHERE p.account_no IS NOT NULL)                                  AS total,
		       COUNT(p.account_no) FILTER (WHERE p.claimed_user_id IS NOT NULL)                              AS claimed,
		       COUNT(p.account_no) FILTER (WHERE p.is_locked = TRUE AND p.claimed_user_id IS NULL)           AS locked,
		       COUNT(p.account_no) FILTER (WHERE p.claimed_user_id IS NULL AND p.is_locked = FALSE
		                                     AND p.reserved_until IS NOT NULL AND p.reserved_until > now()) AS reserved,
		       COUNT(p.account_no) FILTER (WHERE p.claimed_user_id IS NULL AND p.is_locked = FALSE
		                                     AND (p.reserved_until IS NULL OR p.reserved_until < now()))    AS free
		FROM account_no_segments s
		LEFT JOIN account_no_pool p ON p.segment_no = s.segment_no
		GROUP BY s.segment_no, s.range_start, s.range_end, s.state, s.opened_at
		ORDER BY s.segment_no`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()
	out := make([]segmentStat, 0)
	for rows.Next() {
		var s segmentStat
		if err := rows.Scan(&s.SegmentNo, &s.RangeStart, &s.RangeEnd, &s.State, &s.OpenedAt,
			&s.Total, &s.Claimed, &s.Locked, &s.Reserved, &s.Free); err != nil {
			continue
		}
		out = append(out, s)
	}
	c.JSON(http.StatusOK, gin.H{"segments": out})
}

type statusReq struct {
	Status int16 `json:"status"`
}

func (h *Handler) patchUserStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70020, "message": "invalid id"})
		return
	}
	var req statusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70021, "message": "invalid body"})
		return
	}
	if req.Status < 0 || req.Status > 2 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70022, "message": "status must be 0/1/2"})
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(), `UPDATE users SET status = $1 WHERE id = $2`, req.Status, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 70023, "message": "user not found"})
		return
	}

	// Banning / soft-deleting an account must also kill its live
	// sessions — otherwise existing JWTs keep working until expiry
	// (~2 hours) and the user can pretend nothing happened. Status 0
	// = active stays untouched.
	if req.Status != 0 {
		_, _ = h.pool.Exec(c.Request.Context(),
			`UPDATE refresh_tokens SET revoked_at = now()
			 WHERE user_id = $1 AND revoked_at IS NULL`, id)
	}

	actorID, _ := c.Get("userID")
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID:    actorID.(int64),
			Action:     "user.status_change",
			TargetKind: "user",
			TargetID:   id,
			IP:         c.ClientIP(),
			UserAgent:  c.GetHeader("User-Agent"),
			Metadata:   map[string]any{"newStatus": req.Status},
		})
	}
	c.Status(http.StatusNoContent)
}

// forceLogoutUser revokes every active session for a user without
// changing their status. Useful when an admin spots a hijacked account
// — kick out the attacker, let the legit owner re-login (and change
// password from a clean session).
func (h *Handler) forceLogoutUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70020, "message": "invalid id"})
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	actorID, _ := c.Get("userID")
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID:    actorID.(int64),
			Action:     "user.force_logout",
			TargetKind: "user",
			TargetID:   id,
			IP:         c.ClientIP(),
			UserAgent:  c.GetHeader("User-Agent"),
			Metadata:   map[string]any{"revoked": tag.RowsAffected()},
		})
	}
	c.JSON(http.StatusOK, gin.H{"revoked": tag.RowsAffected()})
}

// ==================== Live moderation ====================

type adminLiveRoom struct {
	ID           int64  `json:"id,string"`
	OwnerID      int64  `json:"ownerId,string"`
	OwnerName    string `json:"ownerName"`
	Title        string `json:"title"`
	Category     string `json:"category,omitempty"`
	Status       int16  `json:"status"`
	IsTest       bool   `json:"isTest"`
	ViewerCount  int    `json:"viewerCount"`
	TotalViews   int64  `json:"totalViews"`
	StartedAt    string `json:"startedAt,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// listLiveRooms returns every live room across the platform — live, idle,
// ended, banned. Admins use this to spot violations and trigger force-end.
func (h *Handler) listLiveRooms(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	statusFilter := c.Query("status") // "live" / "ended" / "banned" / "" (all)

	q := `SELECT r.id, r.owner_id, COALESCE(u.username, '?'), r.title,
	             COALESCE(r.category,''), r.status, r.is_test, r.viewer_count,
	             r.total_views, COALESCE(r.started_at::text,''), r.created_at::text
	      FROM live_rooms r
	      LEFT JOIN users u ON u.id = r.owner_id`
	args := []any{limit}
	switch statusFilter {
	case "live":
		q += ` WHERE r.status = 1`
	case "ended":
		q += ` WHERE r.status = 2`
	case "banned":
		q += ` WHERE r.status = 3`
	}
	q += ` ORDER BY r.status = 1 DESC, r.created_at DESC LIMIT $1`

	rows, err := h.pool.Query(c.Request.Context(), q, args...)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()
	out := make([]adminLiveRoom, 0)
	for rows.Next() {
		var r adminLiveRoom
		if err := rows.Scan(&r.ID, &r.OwnerID, &r.OwnerName, &r.Title, &r.Category,
			&r.Status, &r.IsTest, &r.ViewerCount, &r.TotalViews, &r.StartedAt, &r.CreatedAt); err != nil {
			continue
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, gin.H{"rooms": out})
}

type adminLiveRoomIDParam struct{ id int64 }

func adminParseLiveID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return 0, false
	}
	return id, true
}

// forceEndLive flips the room to ended (status=2) and rotates the stream
// key so any OBS still pushing the old key will be rejected on the next
// auth round-trip. It does NOT kick the existing TCP connection at SRS,
// but rotate-key + status=2 stops the room from reappearing in /live/rooms
// and the new key prevents reconnection.
func (h *Handler) forceEndLive(c *gin.Context) {
	actorID := c.MustGet("userID").(int64)
	id, ok := adminParseLiveID(c)
	if !ok {
		return
	}
	// Load room so EndRoom has the IsTest flag + can fire follower
	// notify with the correct title. FindByID returns the full Room
	// with stream_key — same shape EndRoom expects.
	rm, err := h.liveRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	// Shared teardown — rotates key, broadcasts live.room.deleted to
	// every WS-subscribed viewer (so their player kicks them out),
	// notifies followers offline. Same path the owner's 结束直播
	// button takes.
	h.liveHandler.EndRoom(c.Request.Context(), rm)
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: actorID, Action: "live.force_end",
			TargetKind: "live_room", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type banReq struct {
	Banned bool   `json:"banned"`
	Reason string `json:"reason"`
}

// banLiveRoom sets status=3 (banned) or back to 2 (ended) if Banned=false.
// SRS on_publish hook checks status; status=3 → reject future pushes.
func (h *Handler) banLiveRoom(c *gin.Context) {
	actorID := c.MustGet("userID").(int64)
	id, ok := adminParseLiveID(c)
	if !ok {
		return
	}
	var req banReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 70021, "message": "invalid body"})
		return
	}
	target := int16(3) // banned
	if !req.Banned {
		target = 2 // back to ended (user can recreate room)
	}
	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE live_rooms SET status = $1 WHERE id = $2`, target, id)
	if err != nil || tag.RowsAffected() == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	// Persist the reason so the streamer sees it in their Studio.
	// On unban, clear it.
	reason := strings.TrimSpace(req.Reason)
	if !req.Banned {
		reason = ""
	}
	_ = h.liveRepo.SetBannedReason(c.Request.Context(), id, reason)

	// On ban: tear down the broadcast (kick viewers, rotate key,
	// notify followers offline), push a dedicated `live.room.banned`
	// event to the owner so their Studio shows the ban banner
	// without waiting for a refresh.
	if req.Banned {
		if rm, ferr := h.liveRepo.FindByID(c.Request.Context(), id); ferr == nil && rm != nil {
			h.liveHandler.EndRoom(c.Request.Context(), rm)
			h.liveHandler.NotifyOwnerBanned(c.Request.Context(), rm.OwnerID, id, reason)
		}
	} else {
		// On unban, push a `live.room.unbanned` event so the owner's
		// Studio drops the banner.
		h.liveHandler.NotifyOwnerUnbanned(c.Request.Context(), id)
	}
	if h.audit != nil {
		action := "live.ban"
		if !req.Banned {
			action = "live.unban"
		}
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: actorID, Action: action,
			TargetKind: "live_room", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
			Metadata: map[string]any{"reason": req.Reason},
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": target})
}

// adminDeleteLiveRoom hard-deletes the room + ON DELETE CASCADE wipes
// followers / danmaku / bans / recordings.
func (h *Handler) adminDeleteLiveRoom(c *gin.Context) {
	actorID := c.MustGet("userID").(int64)
	id, ok := adminParseLiveID(c)
	if !ok {
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(), `DELETE FROM live_rooms WHERE id = $1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: actorID, Action: "live.delete",
			TargetKind: "live_room", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
		})
	}
	c.Status(http.StatusNoContent)
}

func genRandomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ============================================================
// Live reports + patrol — Tier-1 content moderation surface.
// listLiveReports returns the queue (default status=0 pending).
// resolveLiveReport closes a report with an optional action.
// livePatrol returns all currently-broadcasting rooms with a
// thumbnail URL pointing at server02's /thumbs/<key>.jpg, so
// the admin can eyeball every concurrent stream from one grid.
// ============================================================

func (h *Handler) listLiveReports(c *gin.Context) {
	status := int16(0)
	if s := c.Query("status"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 0 || v > 2 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "bad status"})
			return
		}
		status = int16(v)
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := h.liveRepo.ListReports(c.Request.Context(), status, limit)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	// Also surface the pending count so the admin UI can render the
	// "N 待审" badge without a follow-up call.
	pending, _ := h.liveRepo.PendingReportCount(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"reports": rows, "pending": pending})
}

type resolveReportReq struct {
	// 1 = handled (we did something), 2 = dismissed (no action).
	Status int16 `json:"status"`
	// What the admin did. Free-form for now; the UI offers buttons
	// for "强制结束 / 封禁主播 / 删除房间 / 无需处置" that map to
	// the audit-style strings below.
	Action string `json:"action"`
}

func (h *Handler) resolveLiveReport(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req resolveReportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	if req.Status != 1 && req.Status != 2 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "status must be 1 or 2"})
		return
	}
	if err := h.liveRepo.ResolveReport(c.Request.Context(), id, uid, req.Status, req.Action); err != nil {
		// Either not found or already resolved — treat both as 404 so
		// double-click on the resolve button doesn't error.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "report not pending"})
		return
	}
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: uid, Action: "live.report.resolve",
			TargetKind: "live_report", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
			Metadata: map[string]any{"status": req.Status, "action": req.Action},
		})
	}
	c.Status(http.StatusNoContent)
}

// livePatrol returns all status=1 rooms (currently broadcasting) with
// a thumbnail URL derived from LIVE_HLS_URL ("https://live.dfchat.chat/hls"
// → "https://live.dfchat.chat/thumbs/<key>.jpg"). Front-end renders a
// grid that auto-refreshes every 30 s so the admin can sweep every
// active stream at once.
type patrolRoom struct {
	ID             int64  `json:"id,string"`
	Title          string `json:"title"`
	OwnerID        int64  `json:"ownerId,string"`
	OwnerNickname  string `json:"ownerNickname"`
	OwnerAccountNo string `json:"ownerAccountNo"`
	ViewerCount    int    `json:"viewerCount"`
	StartedAt      string `json:"startedAt,omitempty"`
	ThumbnailURL   string `json:"thumbnailUrl"`
}

// serveEvidence streams a JPEG out of the moderation worker's
// evidence archive directory. Two-segment path (day + name) so the
// caller can't traverse out of EvidenceDir; we also clean the path
// + reject any segment containing "..".
func (h *Handler) serveEvidence(c *gin.Context) {
	day := c.Param("day")
	name := c.Param("name")
	// Defense in depth — Gin already URL-decodes, but a literal
	// ".." segment after decoding is fatal. Same for slashes.
	if strings.Contains(day, "..") || strings.Contains(day, "/") ||
		strings.Contains(name, "..") || strings.Contains(name, "/") {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	full := live.EvidenceDir + "/" + day + "/" + name
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(full)
}

// ============================================================
// AI verdict audit log
// listLiveVerdicts → GET /admin/live/verdicts?roomId=&flagged=&unlabeled=&limit=
// labelLiveVerdict → POST /admin/live/verdicts/:id/label
//   body: { "label": "agree" | "should_flag" | "false_positive" }
//   side effects:
//     should_flag    → if no linked report, create one in the queue
//     false_positive → if linked report still pending, dismiss it
// pinLiveVerdict   → POST /admin/live/verdicts/:id/pin
//   body: { "pinned": true|false } — survive 7-day cleanup or not
// ============================================================

func (h *Handler) listLiveVerdicts(c *gin.Context) {
	var roomID int64
	if v := c.Query("roomId"); v != "" {
		roomID, _ = strconv.ParseInt(v, 10, 64)
	}
	flagged := c.Query("flagged") == "1"
	unlabeled := c.Query("unlabeled") == "1"
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := h.liveRepo.ListVerdicts(c.Request.Context(), roomID, flagged, unlabeled, limit)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verdicts": rows})
}

type labelVerdictReq struct {
	// "agree" / "should_flag" / "false_positive" — see migration 28.
	// Empty clears the label.
	Label string `json:"label"`
	// Only used when label = "should_flag": admin's optional note
	// that goes into the new report's note field. Defaults to a
	// canned "AI 漏判，管理员补报" if empty.
	Note string `json:"note"`
}

func (h *Handler) labelLiveVerdict(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req labelVerdictReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	switch req.Label {
	case "", "agree", "should_flag", "false_positive":
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "bad label"})
		return
	}

	// Need the full verdict row to know roomId / reportId / category
	// for follow-up actions. Fetch one before labeling.
	verdicts, err := h.liveRepo.ListVerdicts(c.Request.Context(), 0, false, false, 200)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	var v *live.Verdict
	for i := range verdicts {
		if verdicts[i].ID == id {
			v = &verdicts[i]
			break
		}
	}
	if v == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "verdict not found"})
		return
	}

	reportID, err := h.liveRepo.LabelVerdict(c.Request.Context(), id, uid, req.Label)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}

	// Side effects per label type.
	switch req.Label {
	case "should_flag":
		// AI said clean but admin disagrees → create a report so it
		// shows up in the human-review queue. Skip if there's
		// already a report linked to this verdict.
		if reportID == nil || *reportID == 0 {
			note := req.Note
			if note == "" {
				note = fmt.Sprintf("[人工补报 by uid=%d | AI 漏判] AI 给出 %s=%.2f", uid, v.MaxCategory, v.MaxScore)
			}
			rid, ierr := h.liveRepo.InsertReportReturnID(
				c.Request.Context(), v.RoomID, &uid, v.MaxCategory, note, v.ThumbnailURL,
			)
			if ierr == nil && rid > 0 {
				_ = h.liveRepo.LinkVerdictReport(c.Request.Context(), id, rid)
			}
		}
	case "false_positive":
		// AI flagged but admin disagrees → auto-dismiss the linked
		// report (if any) so the queue clears without separate click.
		if reportID != nil && *reportID > 0 {
			_ = h.liveRepo.ResolveReport(c.Request.Context(), *reportID, uid, 2, "ai_false_positive")
		}
	}

	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: uid, Action: "live.verdict.label",
			TargetKind: "live_verdict", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
			Metadata: map[string]any{"label": req.Label},
		})
	}
	c.Status(http.StatusNoContent)
}

type pinVerdictReq struct {
	Pinned bool `json:"pinned"`
}

func (h *Handler) pinLiveVerdict(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80010, "message": "invalid id"})
		return
	}
	var req pinVerdictReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 80022, "message": "invalid body"})
		return
	}
	if err := h.liveRepo.PinVerdict(c.Request.Context(), id, req.Pinned); err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "verdict not found"})
		return
	}
	if h.audit != nil {
		h.audit.Write(c.Request.Context(), audit.Entry{
			ActorID: uid, Action: "live.verdict.pin",
			TargetKind: "live_verdict", TargetID: id,
			IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"),
			Metadata: map[string]any{"pinned": req.Pinned},
		})
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) livePatrol(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT rm.id, rm.title, rm.owner_id, rm.viewer_count,
		       COALESCE(rm.started_at::text,''),
		       rm.stream_key,
		       COALESCE(u.nickname,''), COALESCE(u.account_no::text,'')
		  FROM live_rooms rm
		  LEFT JOIN users u ON u.id = rm.owner_id
		 WHERE rm.status = 1 AND rm.is_test = FALSE
		 ORDER BY rm.started_at DESC NULLS LAST`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()
	base := strings.Replace(strings.TrimRight(h.liveHLSURL, "/"), "/hls", "/thumbs", 1)
	out := make([]patrolRoom, 0)
	for rows.Next() {
		var pr patrolRoom
		var key string
		if err := rows.Scan(&pr.ID, &pr.Title, &pr.OwnerID, &pr.ViewerCount,
			&pr.StartedAt, &key, &pr.OwnerNickname, &pr.OwnerAccountNo); err != nil {
			continue
		}
		pr.ThumbnailURL = base + "/" + key + ".jpg"
		out = append(out, pr)
	}
	c.JSON(http.StatusOK, gin.H{"rooms": out})
}
