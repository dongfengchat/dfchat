// Package audit writes privileged actions to the audit_logs table.
// It's intentionally tiny — admin handlers (and anything else that
// performs a reviewable mutation) call audit.Write directly.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Logger struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Logger {
	return &Logger{pool: pool, log: log}
}

// Write logs an action. Best-effort: on DB error we log but do not block
// the calling handler (audit failure should never break a real request).
func (l *Logger) Write(ctx context.Context, e Entry) {
	var meta []byte
	if e.Metadata != nil {
		meta, _ = json.Marshal(e.Metadata)
	}
	_, err := l.pool.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_kind, target_id, ip, user_agent, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ActorID, e.Action, nullableStr(e.TargetKind), nullableInt(e.TargetID),
		nullableStr(e.IP), nullableStr(e.UserAgent), meta,
	)
	if err != nil {
		l.log.Warn("audit insert failed", "err", err, "action", e.Action)
	}
}

type Entry struct {
	ActorID    int64
	Action     string
	TargetKind string
	TargetID   int64
	IP         string
	UserAgent  string
	Metadata   map[string]any
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
