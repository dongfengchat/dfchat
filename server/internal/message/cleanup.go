package message

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RetentionWindow is how long the server stores a message before sweeping
// it. After this window:
//   - The row is hard-deleted (CASCADE drops reactions / pins / read
//     receipts that depend on it).
//   - The author / admins lose ability to recall / edit / delete it via
//     the API — the server simply doesn't have the row anymore.
//   - Clients that synced before the sweep keep a permanent local copy.
//   - Clients that come online after the sweep will not see messages
//     from before this window when they download history.
//
// 30 days is a deliberate product choice — long enough that real users
// won't lose anything to vacation/illness, short enough that a server
// breach exposes at most one month of conversation.
//
// Pinned messages are EXEMPT — they're explicit user signal to keep
// visible, and a typical group rules / announcement pin should outlive
// the rolling window.
const RetentionWindow = 30 * 24 * time.Hour

// retentionSweepInterval is how often the sweeper runs. Hourly is plenty
// — the window is 30 days, so a missed hour doesn't matter, and 24
// passes per day keeps each pass small.
const retentionSweepInterval = 1 * time.Hour

// RunRetentionLoop is the background goroutine that enforces
// RetentionWindow. Start it once from main.go alongside the auth
// cleanup loop. Idempotent and safe to call multiple times — duplicate
// loops just double the work, they don't corrupt anything.
//
// Cancel via ctx.
func RunRetentionLoop(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	// One sweep on startup so we don't sit on a backlog after a deploy
	// (especially the first deploy where 30+ days of messages exist).
	sweepRetention(ctx, pool, log)

	t := time.NewTicker(retentionSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweepRetention(ctx, pool, log)
		}
	}
}

func sweepRetention(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cutoff := time.Now().Add(-RetentionWindow)

	// Delete in a single statement so it's transactionally consistent:
	// either every eligible message goes, or none do. The CASCADE on
	// message_reactions / message_pins / conversation_members handles
	// the dependent rows; conversations + conversation_seq are NOT
	// touched (the seq counter persists so new messages don't reuse
	// stale seq numbers; conversations.last_message_id is repaired
	// below if it pointed at a deleted message).
	//
	// We explicitly skip:
	//   - pinned messages (they're explicit "keep forever" signal)
	//   - is_recalled=TRUE rows that are < 30d (already content-empty,
	//     but keep the row so seq remains continuous and recall
	//     events that came in late don't 404)
	tag, err := pool.Exec(sctx, `
		DELETE FROM messages
		WHERE created_at < $1
		  AND id NOT IN (SELECT message_id FROM message_pins)`, cutoff)
	if err != nil {
		log.Warn("message retention: sweep failed", "err", err.Error())
		return
	}
	deleted := tag.RowsAffected()
	if deleted == 0 {
		// Quiet on idle systems — log only when work happened.
		return
	}

	// Repair conversations.last_message_id for any conversation whose
	// pointer got orphaned by the sweep. Without this, the sidebar's
	// "last message" preview would be blank until a new message lands.
	if _, err := pool.Exec(sctx, `
		UPDATE conversations c
		SET last_message_id = (
			SELECT id FROM messages
			WHERE conversation_id = c.id
			ORDER BY seq DESC LIMIT 1
		)
		WHERE c.last_message_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM messages WHERE id = c.last_message_id)`); err != nil {
		log.Warn("message retention: repair last_message_id failed", "err", err.Error())
	}

	log.Info("message retention: swept",
		"deleted", deleted,
		"older_than", RetentionWindow.String(),
		"cutoff", cutoff.Format(time.RFC3339))
}
