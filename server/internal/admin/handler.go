package admin

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"

	authdomain "github.com/dongfang/dfchat/server/internal/auth"
	"github.com/dongfang/dfchat/server/pkg/audit"
	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool   *pgxpool.Pool
	issuer *auth.Issuer
	audit  *audit.Logger
}

func NewHandler(pool *pgxpool.Pool, issuer *auth.Issuer, auditor *audit.Logger) *Handler {
	return &Handler{pool: pool, issuer: issuer, audit: auditor}
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
	// Rotate stream_key to a fresh random value.
	newKey, err := genRandomHex(16)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE live_rooms SET status = 2, ended_at = NOW(), stream_key = $2 WHERE id = $1`,
		id, newKey)
	if err != nil || tag.RowsAffected() == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 80011, "message": "room not found"})
		return
	}
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
