package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Tunables — kept as const so they're easy to find and change later.
const (
	drawSize           = 10               // numbers per draw
	maxRefreshes       = 3                // additional refreshes after the initial draw
	reservationTTL     = 10 * time.Minute // how long held numbers stay off the random pool
	selectionTTL       = 10 * time.Minute // selection token lifetime
	freeCountThreshold = 200              // when fewer than this many free numbers remain
	//                                    // in the lowest-no open segment, open the next.
)

// drawAccountNumbers (public) returns 10 randomly-sampled free account
// numbers from the lowest-no segment that still has stock, plus a
// selection token the client carries through refresh and register.
//
// Atomicity: all 10 are reserved (reserved_until = now+10min) in a
// single UPDATE so two concurrent draws can never return the same
// number.
func (h *Handler) drawAccountNumbers(c *gin.Context) {
	clientIP := c.ClientIP()
	// Per-IP 24h cap on draws. Reservations live 10 minutes, so a single
	// IP doing 30 draws (= 300 reservations) is the absolute max — well
	// under 5% of one segment's 10k stock. Stops slow pool-drainers.
	if _, over := hitIPDailyCap(clientIP, "draw", ipDailyDrawLimit); over {
		fail(c, http.StatusTooManyRequests, 10099,
			"今日摇号次数已达上限，请明天再试或更换网络")
		return
	}
	if err := h.ensureSegmentCapacity(c.Request.Context()); err != nil {
		// Non-fatal — fall through and try anyway. If there's truly
		// nothing left we'll catch it below.
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	defer tx.Rollback(c.Request.Context())

	numbers, err := reserveRandomFromPool(c.Request.Context(), tx, drawSize, nil)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "号码池暂时取不到，请稍后再试")
		return
	}
	if len(numbers) == 0 {
		fail(c, http.StatusServiceUnavailable, 50002, "当前段位号码已用完，请稍后再试")
		return
	}

	tok, err := newToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	expiresAt := time.Now().Add(selectionTTL)
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO account_no_selections (token, client_ip, refreshes_used, reserved_nos, expires_at)
		VALUES ($1, $2, 0, $3, $4)`,
		tok, clientIP, numbers, expiresAt); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"numbers":        toStringSlice(numbers),
		"selectionToken": tok,
		"refreshesLeft":  maxRefreshes,
	})
}

type refreshDrawReq struct {
	SelectionToken string `json:"selectionToken"`
}

// refreshAccountNumbers swaps the user's current 10 reservations for a
// fresh batch. The previous batch is released so siblings can re-enter
// the pool. Refreshes are capped (server-enforced).
func (h *Handler) refreshAccountNumbers(c *gin.Context) {
	clientIP := c.ClientIP()
	var req refreshDrawReq
	if err := c.ShouldBindJSON(&req); err != nil || req.SelectionToken == "" {
		fail(c, http.StatusBadRequest, 10090, "selectionToken required")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var (
		oldNumbers     []int64
		refreshesUsed  int
		expiresAt      time.Time
		sessionIP      string
	)
	err = tx.QueryRow(c.Request.Context(), `
		SELECT reserved_nos, refreshes_used, expires_at, client_ip
		FROM account_no_selections WHERE token = $1
		FOR UPDATE`, req.SelectionToken).Scan(&oldNumbers, &refreshesUsed, &expiresAt, &sessionIP)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(c, http.StatusBadRequest, 10091, "会话已失效，请重新获取号码")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	if time.Now().After(expiresAt) {
		fail(c, http.StatusBadRequest, 10091, "会话已过期，请重新获取号码")
		return
	}
	if sessionIP != clientIP {
		// Sessions are pinned to the originating IP so a stolen token
		// can't be used from elsewhere. Mismatch -> treat as invalid.
		fail(c, http.StatusBadRequest, 10091, "会话已失效，请重新获取号码")
		return
	}
	if refreshesUsed >= maxRefreshes {
		fail(c, http.StatusTooManyRequests, 10092, "刷新次数已用完，请从当前 10 个中选择")
		return
	}

	// Release the old reservations so they can flow back into the pool.
	// We use account_no = ANY($1) for an indexed range hit.
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE account_no_pool SET reserved_until = NULL WHERE account_no = ANY($1)`,
		oldNumbers); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}

	// Reserve a fresh batch, EXCLUDING the just-released ones so the
	// user doesn't see the exact same numbers (would defeat the point).
	newNumbers, err := reserveRandomFromPool(c.Request.Context(), tx, drawSize, oldNumbers)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	if len(newNumbers) == 0 {
		fail(c, http.StatusServiceUnavailable, 50002, "当前段位号码已用完，请稍后再试")
		return
	}

	newExpiry := time.Now().Add(selectionTTL)
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE account_no_selections
		SET reserved_nos = $1, refreshes_used = refreshes_used + 1, expires_at = $2
		WHERE token = $3`,
		newNumbers, newExpiry, req.SelectionToken); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "服务器内部错误")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"numbers":        toStringSlice(newNumbers),
		"selectionToken": req.SelectionToken,
		"refreshesLeft":  maxRefreshes - (refreshesUsed + 1),
	})
}

// reserveRandomFromPool atomically picks N random free numbers from the
// lowest-no open segment that still has stock, sets reserved_until on
// each row, and returns the picked numbers. exclude is a list of nos
// to skip (used on refresh so the user doesn't get the same set back).
//
// Runs inside the caller's tx so the SELECT-then-UPDATE is consistent;
// concurrent draws lining up on the same candidate will conflict on
// row locks and one will retry.
func reserveRandomFromPool(ctx context.Context, tx pgx.Tx, n int, exclude []int64) ([]int64, error) {
	// One query: select N random free rows from the lowest open segment,
	// then update them to reserved_until=now+ttl, returning their nos.
	// FOR UPDATE SKIP LOCKED so concurrent draws don't deadlock.
	const q = `
		WITH candidates AS (
			SELECT account_no FROM account_no_pool
			WHERE is_locked = FALSE
			  AND claimed_user_id IS NULL
			  AND (reserved_until IS NULL OR reserved_until < now())
			  AND ($2::bigint[] IS NULL OR NOT account_no = ANY($2))
			  AND segment_no = (
			    SELECT MIN(p.segment_no) FROM account_no_pool p
			    JOIN account_no_segments s ON s.segment_no = p.segment_no
			    WHERE s.state = 'open'
			      AND p.is_locked = FALSE
			      AND p.claimed_user_id IS NULL
			      AND (p.reserved_until IS NULL OR p.reserved_until < now())
			  )
			ORDER BY random()
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE account_no_pool p
		SET reserved_until = now() + ($3 * interval '1 second')
		FROM candidates c
		WHERE p.account_no = c.account_no
		RETURNING p.account_no
	`
	rows, err := tx.Query(ctx, q, n, exclude, int(reservationTTL.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0, n)
	for rows.Next() {
		var no int64
		if err := rows.Scan(&no); err != nil {
			return nil, err
		}
		out = append(out, no)
	}
	return out, rows.Err()
}

// ensureSegmentCapacity opens a new segment when the lowest open one is
// nearly exhausted. Lazy: triggered on each draw, no cron needed.
// Returns nil on success or "nothing to do".
func (h *Handler) ensureSegmentCapacity(ctx context.Context) error {
	var freeCount int64
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM account_no_pool p
		JOIN account_no_segments s ON s.segment_no = p.segment_no
		WHERE s.state = 'open'
		  AND p.is_locked = FALSE
		  AND p.claimed_user_id IS NULL
		  AND (p.reserved_until IS NULL OR p.reserved_until < now())
	`).Scan(&freeCount); err != nil {
		return err
	}
	if freeCount >= freeCountThreshold {
		return nil
	}

	var maxSeg int
	if err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(segment_no), 0) FROM account_no_segments`).Scan(&maxSeg); err != nil {
		return err
	}
	newSeg := maxSeg + 1
	rangeStart := int64(100000 + (newSeg-1)*10000)
	rangeEnd := rangeStart + 9999
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO account_no_segments (segment_no, range_start, range_end)
		VALUES ($1, $2, $3) ON CONFLICT (segment_no) DO NOTHING`,
		newSeg, rangeStart, rangeEnd); err != nil {
		return err
	}
	// populateSegment is idempotent.
	return populateSegment(ctx, h.pool, int64(newSeg), rangeStart, rangeEnd, slog.Default())
}

// validateAndConsumeSelection is called from register. It verifies the
// (selectionToken, accountNo) pair, claims the chosen number to the
// new user, releases the 9 siblings, and deletes the selection. All
// inside the caller's tx so a register-failure rolls everything back.
func validateAndConsumeSelection(ctx context.Context, tx pgx.Tx, clientIP, selectionToken string, chosen int64, newUserID int64) error {
	var (
		reservedNos []int64
		ip          string
		expiresAt   time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT reserved_nos, client_ip, expires_at
		FROM account_no_selections WHERE token = $1
		FOR UPDATE`, selectionToken).Scan(&reservedNos, &ip, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSelectionInvalid
	}
	if err != nil {
		return err
	}
	if time.Now().After(expiresAt) {
		return errSelectionInvalid
	}
	if ip != clientIP {
		return errSelectionInvalid
	}
	found := false
	for _, n := range reservedNos {
		if n == chosen {
			found = true
			break
		}
	}
	if !found {
		return errChosenNotInSelection
	}

	// Mark chosen as claimed (must still be unclaimed — race safety).
	tag, err := tx.Exec(ctx, `
		UPDATE account_no_pool
		SET claimed_user_id = $1, reserved_until = NULL
		WHERE account_no = $2 AND claimed_user_id IS NULL`, newUserID, chosen)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errChosenAlreadyClaimed
	}

	// Release the 9 siblings so they can be drawn by someone else.
	siblings := make([]int64, 0, len(reservedNos)-1)
	for _, n := range reservedNos {
		if n != chosen {
			siblings = append(siblings, n)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE account_no_pool SET reserved_until = NULL WHERE account_no = ANY($1)`,
		siblings); err != nil {
		return err
	}

	// Selection is consumed.
	if _, err := tx.Exec(ctx,
		`DELETE FROM account_no_selections WHERE token = $1`, selectionToken); err != nil {
		return err
	}
	return nil
}

var (
	errSelectionInvalid     = errors.New("selection invalid or expired")
	errChosenNotInSelection = errors.New("chosen number not part of this selection")
	errChosenAlreadyClaimed = errors.New("chosen number already claimed")
)

func toStringSlice(ns []int64) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = formatInt(n)
	}
	return out
}

// parsePositiveInt accepts a digits-only string and returns the int64
// value. Refuses leading +/- and whitespace, anything strconv.Atoi
// permits. Used to validate user-supplied accountNo on register.
func parsePositiveInt(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int64(r-'0')
		if n > 1<<53 {
			return 0, errors.New("overflow")
		}
	}
	return n, nil
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
