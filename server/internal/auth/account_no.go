package auth

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// isLockedPattern reports whether the account number is too premium to
// be assigned by random draw. These get is_locked=true in the pool and
// stay in reserve for admin grants / premium sale.
//
// We deliberately keep this set small in the early stages — only the
// truly extraordinary patterns. Numbers with merely "nice" patterns
// (4 consecutive same digits, trailing 000) stay in the random pool so
// early users have a real chance of drawing something they'll love.
// We can tighten later by running a sweep that locks unsold ones.
//
// Locked patterns:
//   1. All-same digit:     111111, 222222, …            (1 per 10k)
//   2. Strict ascending:   123456, 234567, 345678, 456789
//   3. Strict descending:  987654, 876543, …            (none in 1xxxxx)
//   4. Full palindrome:    100001, 101101, 122221, …    (~100 per 1M)
//   5. 5+ consecutive same digits anywhere               (~20 per 10k)
//
// Combined this is ~30-50 numbers per 10k-segment — under 0.5%.
func isLockedPattern(n int64) bool {
	s := strconv.FormatInt(n, 10)
	if len(s) < 6 {
		return false
	}
	return allSameDigit(s) ||
		isStrictAscending(s) ||
		isStrictDescending(s) ||
		isPalindrome(s) ||
		hasConsecutiveSame(s, 5)
}

func allSameDigit(s string) bool {
	if s == "" {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// Strict ascending: every digit exactly 1 greater than the previous.
// Catches 123456, 234567, 345678, 456789.
func isStrictAscending(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1]+1 {
			return false
		}
	}
	return true
}

// Strict descending: every digit exactly 1 less than the previous.
// Catches 987654, 876543, …
func isStrictDescending(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] == 0 || s[i] != s[i-1]-1 {
			return false
		}
	}
	return true
}

// Palindrome: reads the same forward and backward.
// Catches 123321, 100001, 122221, 109901, …
func isPalindrome(s string) bool {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		if s[i] != s[j] {
			return false
		}
	}
	return true
}

// hasConsecutiveSame reports whether s contains a run of `minRun` or
// more identical digits anywhere.
func hasConsecutiveSame(s string, minRun int) bool {
	if len(s) < minRun {
		return false
	}
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= minRun {
				return true
			}
		} else {
			run = 1
		}
	}
	return false
}

// EnsureSegmentPools initialises (idempotently) the account_no_pool
// rows for every segment in 'open' state. Safe to call repeatedly on
// startup — only inserts what's missing.
//
// For a fresh segment of 10k numbers this inserts ~9990 rows (skipping
// any that are already in users.account_no from before this feature
// landed), so it takes < 1 second.
func EnsureSegmentPools(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	rows, err := pool.Query(ctx, `
		SELECT segment_no, range_start, range_end
		FROM account_no_segments
		WHERE state = 'open'
		ORDER BY segment_no`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type seg struct {
		no, start, end int64
	}
	var segments []seg
	for rows.Next() {
		var s seg
		if err := rows.Scan(&s.no, &s.start, &s.end); err != nil {
			return err
		}
		segments = append(segments, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range segments {
		if err := populateSegment(ctx, pool, s.no, s.start, s.end, log); err != nil {
			log.Warn("account-no: populate segment failed", "segment", s.no, "err", err.Error())
		}
	}
	return nil
}

// populateSegment fills account_no_pool for one segment. For every
// integer in [start, end]:
//   - skip if already present in account_no_pool (idempotent rerun)
//   - skip if already claimed in users.account_no (existing accounts)
//   - flag is_locked according to the premium-pattern rules
//
// Uses a single COPY-style INSERT batched to ~500 rows per tx so we
// don't load a 10k VALUES list into one statement.
func populateSegment(ctx context.Context, pool *pgxpool.Pool, segmentNo, start, end int64, log *slog.Logger) error {
	// What's already in users.account_no for this segment? Skip those —
	// they're already "claimed", we don't want to confuse the pool.
	usedRows, err := pool.Query(ctx,
		`SELECT account_no FROM users WHERE account_no BETWEEN $1 AND $2`, start, end)
	if err != nil {
		return err
	}
	used := make(map[int64]bool)
	for usedRows.Next() {
		var n int64
		if err := usedRows.Scan(&n); err != nil {
			usedRows.Close()
			return err
		}
		used[n] = true
	}
	usedRows.Close()

	// What's already in the pool? Skip those — rerun safety.
	poolRows, err := pool.Query(ctx,
		`SELECT account_no FROM account_no_pool WHERE segment_no = $1`, segmentNo)
	if err != nil {
		return err
	}
	inPool := make(map[int64]bool)
	for poolRows.Next() {
		var n int64
		if err := poolRows.Scan(&n); err != nil {
			poolRows.Close()
			return err
		}
		inPool[n] = true
	}
	poolRows.Close()

	const batchSize = 500
	type row struct {
		no     int64
		locked bool
	}
	batch := make([]row, 0, batchSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		// Multi-row INSERT VALUES ($1,$2,$3),($4,$5,$6)…
		args := make([]any, 0, len(batch)*3)
		valuesSQL := make([]byte, 0, len(batch)*30)
		for i, r := range batch {
			if i > 0 {
				valuesSQL = append(valuesSQL, ',')
			}
			base := i * 3
			valuesSQL = append(valuesSQL, '(')
			valuesSQL = append(valuesSQL, '$')
			valuesSQL = strconv.AppendInt(valuesSQL, int64(base+1), 10)
			valuesSQL = append(valuesSQL, ',', '$')
			valuesSQL = strconv.AppendInt(valuesSQL, int64(base+2), 10)
			valuesSQL = append(valuesSQL, ',', '$')
			valuesSQL = strconv.AppendInt(valuesSQL, int64(base+3), 10)
			valuesSQL = append(valuesSQL, ')')
			args = append(args, r.no, segmentNo, r.locked)
		}
		sql := `INSERT INTO account_no_pool (account_no, segment_no, is_locked) VALUES ` +
			string(valuesSQL) + ` ON CONFLICT (account_no) DO NOTHING`
		_, err := pool.Exec(ctx, sql, args...)
		batch = batch[:0]
		return err
	}

	inserted := 0
	locked := 0
	for n := start; n <= end; n++ {
		if used[n] || inPool[n] {
			continue
		}
		l := isLockedPattern(n)
		if l {
			locked++
		}
		batch = append(batch, row{no: n, locked: l})
		inserted++
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if inserted > 0 {
		log.Info("account-no: segment populated",
			"segment", segmentNo,
			"inserted", inserted,
			"locked_premium", locked,
			"already_used", len(used))
	}
	return nil
}
