package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// logLoginAttempt records a row in login_logs. Best-effort: a failed
// insert is logged but never blocks the auth response.
//
// userID may be 0 for failed attempts where we don't know who the user
// was claiming to be (the row matched nobody, so we can't FK it).
// loginInput is the raw string the user typed at the login form —
// useful for forensics on failed attempts (could be a username, email,
// or numeric account_no).
func (h *Handler) logLoginAttempt(c *gin.Context, userID int64, loginInput string, success bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var uidArg any = nil
	if userID > 0 {
		uidArg = userID
	}
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO login_logs (user_id, login_input, success, ip, user_agent)
		 VALUES ($1, $2, $3, $4, $5)`,
		uidArg, loginInput, success, c.ClientIP(), c.Request.UserAgent())
}

// LoginLogEntry is the JSON shape returned to clients + admins.
type LoginLogEntry struct {
	ID        int64  `json:"id,string"`
	Success   bool   `json:"success"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	CreatedAt string `json:"createdAt"`
}

// recentLogins (authed) returns the calling user's last 20 login
// attempts. Same pattern as Google's account.google.com/security-events:
// helps users spot "did someone log into my account from somewhere
// I don't recognise".
func (h *Handler) recentLogins(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	out, err := LoadLoginsForUser(c.Request.Context(), h.pool, uid, 20)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": out})
}

// LoadLoginsForUser fetches the last `limit` login attempts for a user.
// Exported so the admin handler can call it for arbitrary user ids
// (e.g. "show me everyone who logged into account 100123").
func LoadLoginsForUser(ctx context.Context, pool *pgxpool.Pool, userID int64, limit int) ([]LoginLogEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, success, COALESCE(ip,''), COALESCE(user_agent,''), created_at::text
		FROM login_logs WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LoginLogEntry, 0, limit)
	for rows.Next() {
		var e LoginLogEntry
		if err := rows.Scan(&e.ID, &e.Success, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
