package channel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("channel not found")
	ErrLastChannel    = errors.New("cannot delete the last channel of a group")
)

type Channel struct {
	ID        int64     `json:"id,string"`
	GroupID   int64     `json:"groupId,string"`
	Type      int16     `json:"type"`
	Name      string    `json:"name"`
	Topic     string    `json:"topic,omitempty"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ConvID returns the canonical conversation id for a channel.
func ConvID(channelID int64) string {
	return fmt.Sprintf("c_%d", channelID)
}

// Create inserts a channel and provisions its conversation rows + members
// (copied from the parent group).
func (r *Repo) Create(ctx context.Context, groupID int64, name string) (*Channel, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var nextPos int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM channels WHERE group_id = $1`,
		groupID).Scan(&nextPos); err != nil {
		return nil, err
	}

	ch := &Channel{}
	if err := tx.QueryRow(ctx,
		`INSERT INTO channels (group_id, name, position) VALUES ($1, $2, $3)
		 RETURNING id, group_id, type, name, COALESCE(topic, ''), position, created_at`,
		groupID, name, nextPos,
	).Scan(&ch.ID, &ch.GroupID, &ch.Type, &ch.Name, &ch.Topic, &ch.Position, &ch.CreatedAt); err != nil {
		return nil, err
	}

	convID := ConvID(ch.ID)
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversations (id, type) VALUES ($1, 2) ON CONFLICT DO NOTHING`, convID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_seq (conversation_id, last_seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`,
		convID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id)
		 SELECT $1, user_id FROM group_members WHERE group_id = $2
		 ON CONFLICT DO NOTHING`, convID, groupID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ch, nil
}

func (r *Repo) ListByGroup(ctx context.Context, groupID int64) ([]*Channel, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, group_id, type, name, COALESCE(topic, ''), position, created_at
		 FROM channels WHERE group_id = $1 ORDER BY position ASC, id ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Channel, 0)
	for rows.Next() {
		ch := &Channel{}
		if err := rows.Scan(&ch.ID, &ch.GroupID, &ch.Type, &ch.Name, &ch.Topic, &ch.Position, &ch.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*Channel, error) {
	ch := &Channel{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, group_id, type, name, COALESCE(topic, ''), position, created_at
		 FROM channels WHERE id = $1`, id,
	).Scan(&ch.ID, &ch.GroupID, &ch.Type, &ch.Name, &ch.Topic, &ch.Position, &ch.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ch, err
}

// Rename updates the display name of an existing channel. Caller must
// gate on owner/admin role.
func (r *Repo) Rename(ctx context.Context, id int64, name string) (*Channel, error) {
	ch := &Channel{}
	err := r.pool.QueryRow(ctx,
		`UPDATE channels SET name = $2 WHERE id = $1
		 RETURNING id, group_id, type, name, COALESCE(topic, ''), position, created_at`,
		id, name,
	).Scan(&ch.ID, &ch.GroupID, &ch.Type, &ch.Name, &ch.Topic, &ch.Position, &ch.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ch, err
}

// Reorder rewrites the position column for the given channel ids in
// the order they appear (0-indexed). Channels in the group but not
// in the list are pushed to the end, preserving their existing
// relative order. Whole thing in a tx so a partial failure can't
// leave us with duplicate / sparse positions.
func (r *Repo) Reorder(ctx context.Context, groupID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Discard any ids that aren't actually members of this group —
	// stops a malicious caller from reshuffling channels in other
	// groups via this endpoint.
	rows, err := tx.Query(ctx,
		`SELECT id FROM channels WHERE group_id = $1`, groupID)
	if err != nil {
		return err
	}
	valid := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		valid[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Apply the requested order first.
	seen := make(map[int64]struct{})
	pos := 0
	for _, id := range ids {
		if _, ok := valid[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET position = $2 WHERE id = $1`, id, pos); err != nil {
			return err
		}
		pos++
	}
	// Push remaining channels (not in the supplied order) to the end,
	// keeping their existing relative order.
	tailRows, err := tx.Query(ctx,
		`SELECT id FROM channels WHERE group_id = $1
		 ORDER BY position ASC, id ASC`, groupID)
	if err != nil {
		return err
	}
	tail := make([]int64, 0)
	for tailRows.Next() {
		var id int64
		if err := tailRows.Scan(&id); err != nil {
			tailRows.Close()
			return err
		}
		if _, claimed := seen[id]; !claimed {
			tail = append(tail, id)
		}
	}
	tailRows.Close()
	if err := tailRows.Err(); err != nil {
		return err
	}
	for _, id := range tail {
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET position = $2 WHERE id = $1`, id, pos); err != nil {
			return err
		}
		pos++
	}
	return tx.Commit(ctx)
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var groupID int64
	if err := tx.QueryRow(ctx, `SELECT group_id FROM channels WHERE id = $1`, id).Scan(&groupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM channels WHERE group_id = $1`, groupID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastChannel
	}
	if _, err := tx.Exec(ctx, `DELETE FROM channels WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GroupOf returns the group_id that owns the given channel. Used by the
// message handler to authorize cross-channel operations (like pinning,
// which requires admin-or-owner role on the parent group).
func (r *Repo) GroupOf(ctx context.Context, channelID int64) (int64, error) {
	var gid int64
	err := r.pool.QueryRow(ctx,
		`SELECT group_id FROM channels WHERE id = $1`, channelID).Scan(&gid)
	return gid, err
}

// MemberIDs returns the user IDs that are conversation_members of this channel.
func (r *Repo) MemberIDs(ctx context.Context, channelID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM conversation_members WHERE conversation_id = $1`,
		ConvID(channelID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
