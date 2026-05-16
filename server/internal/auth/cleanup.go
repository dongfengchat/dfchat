package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unverifiedRetention is how long an account is allowed to sit
// unverified before the sweeper purges it. 14 days is enough for a real
// user who registered then went on vacation to come back and click the
// link; longer than that the row is almost certainly noise (typo email,
// spammer, abandoned signup).
const unverifiedRetention = 14 * 24 * time.Hour

// expiredTokenRetention is how long stale (expired or used-up)
// email_verify and password_reset rows are kept around before we GC
// them. Doesn't matter functionally — the handlers already reject
// expired ones — but keeping the tables small helps lookups and makes
// `psql` more readable.
const expiredTokenRetention = 7 * 24 * time.Hour

// RunCleanupLoop sweeps the database every hour:
//   - Hard-deletes users that registered > unverifiedRetention ago and
//     never verified their email. ON DELETE CASCADE drops their tokens
//     and any other FK-linked rows.
//   - GCs stale email_verify_tokens and password_reset_tokens whose
//     expires_at is more than expiredTokenRetention in the past.
//
// Idempotent and safe to run on every replica (single-monolith for now,
// but doesn't matter). Cancel via ctx.
func RunCleanupLoop(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	// One sweep on startup so we don't wait an hour after a deploy.
	sweep(ctx, pool, log)

	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep(ctx, pool, log)
		}
	}
}

func sweep(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	// Use a short bounded timeout so a hung sweep can't pile up.
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cutoff := time.Now().Add(-unverifiedRetention)
	tag, err := pool.Exec(sctx, `
		DELETE FROM users
		WHERE email_verified = false
		  AND status = 0
		  AND created_at < $1`, cutoff)
	if err != nil {
		log.Warn("auth cleanup: drop unverified users failed", "err", err.Error())
	} else if n := tag.RowsAffected(); n > 0 {
		log.Info("auth cleanup: dropped unverified users", "count", n,
			"older_than", unverifiedRetention.String())
	}

	tokCutoff := time.Now().Add(-expiredTokenRetention)
	tag, err = pool.Exec(sctx, `
		DELETE FROM email_verify_tokens WHERE expires_at < $1`, tokCutoff)
	if err != nil {
		log.Warn("auth cleanup: gc verify tokens failed", "err", err.Error())
	} else if n := tag.RowsAffected(); n > 0 {
		log.Info("auth cleanup: gc verify tokens", "count", n)
	}

	tag, err = pool.Exec(sctx, `
		DELETE FROM password_reset_tokens WHERE expires_at < $1`, tokCutoff)
	if err != nil {
		log.Warn("auth cleanup: gc reset tokens failed", "err", err.Error())
	} else if n := tag.RowsAffected(); n > 0 {
		log.Info("auth cleanup: gc reset tokens", "count", n)
	}
}
