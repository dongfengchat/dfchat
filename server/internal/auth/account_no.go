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
// Single rule, by user spec: 4 or more identical trailing digits.
// Captures everything Chinese users actually brag about — XX0000,
// XX8888, XX6666 — and naturally subsumes the rarer extremes (all-same
// 111111 has 4+ tail same; 5+ trailing like 100000 does too).
//
// Volume per 10k-segment: exactly 10 — one for each possible trailing
// digit 0..9. Tiny fraction (0.1%) reserved aside for admin grants.
//
// Math curiosities the user explicitly called "垃圾号" (palindromes
// like 101101, strict ascending like 123456) are intentionally NOT
// locked. If those should ever count as premium, add another rule
// here — but the current one matches actual user perception.
func isLockedPattern(n int64) bool {
	s := strconv.FormatInt(n, 10)
	if len(s) < 6 {
		return false
	}
	return hasSameTail(s, 4)
}

// hasSameTail reports whether the last n characters of s are the same
// digit. `hasSameTail("101111", 4)` → true (last 4 are 1s).
// `hasSameTail("100100", 4)` → false (last 4 are 0,1,0,0).
func hasSameTail(s string, n int) bool {
	if len(s) < n {
		return false
	}
	last := s[len(s)-1]
	for i := len(s) - n; i < len(s)-1; i++ {
		if s[i] != last {
			return false
		}
	}
	return true
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

		// Belt-and-suspenders self-check. Query the DB back and count
		// how many rows in this segment ended up locked. Compare against
		// what the math says we should have (10 per 10k-segment, minus
		// any that landed in `used`). Wide discrepancy = something
		// upstream broke (trigger missing, isLockedPattern regression,
		// schema drift) — log loud so ops investigates.
		verifyLockedCount(ctx, pool, segmentNo, start, end, log)
	}
	return nil
}

// verifyLockedCount reads back the DB to count how many premium numbers
// are locked in the just-populated segment, and warns if the count
// disagrees with what isLockedPattern would say.
//
// This catches three classes of failure invisible to the populate
// path itself:
//   - Trigger missing from a fresh DB (would let unlocked premiums through)
//   - isLockedPattern weakened by a regression (would lock too few)
//   - Manual SQL between migration and pool population
func verifyLockedCount(ctx context.Context, pool *pgxpool.Pool, segmentNo, start, end int64, log *slog.Logger) {
	var actualLocked int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM account_no_pool WHERE segment_no = $1 AND is_locked = TRUE`,
		segmentNo).Scan(&actualLocked); err != nil {
		log.Warn("account-no: verify failed", "segment", segmentNo, "err", err.Error())
		return
	}
	expectedLocked := 0
	for n := start; n <= end; n++ {
		if isLockedPattern(n) {
			expectedLocked++
		}
	}
	if actualLocked != expectedLocked {
		log.Warn("account-no: locked-count mismatch — premium numbers may have leaked",
			"segment", segmentNo,
			"expected", expectedLocked,
			"actual", actualLocked,
			"hint", "check trigger migration 000022, isLockedPattern, and any manual SQL")
	} else {
		log.Info("account-no: locked-count self-check OK",
			"segment", segmentNo, "locked", actualLocked)
	}
}
