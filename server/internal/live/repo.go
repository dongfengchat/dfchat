// Package live owns the live-stream domain: rooms + stream keys + recordings.
//
// The actual media plane is SRS (ossrs/srs container). The Go API only
// holds metadata: who owns which room, what's the current stream key,
// is it live, etc. SRS pings the API via HTTP hooks (on_publish /
// on_unpublish / on_dvr) so we know when a stream actually starts and
// stops; that's how we keep status accurate.

package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("live room not found")
	ErrNotOwner      = errors.New("not the room owner")
	ErrInvalidStreamKey = errors.New("invalid stream key")
)

// Room status codes — kept in sync with the migration enum comments.
const (
	StatusIdle   int16 = 0
	StatusLive   int16 = 1
	StatusEnded  int16 = 2
	StatusBanned int16 = 3
)

type Room struct {
	ID          int64      `json:"id,string"`
	OwnerID     int64      `json:"ownerId,string"`
	Title       string     `json:"title"`
	CoverURL    string     `json:"coverUrl,omitempty"`
	Category    string     `json:"category,omitempty"`
	StreamKey   string     `json:"streamKey,omitempty"` // omitted from public listings; included only for owner
	Status      int16      `json:"status"`
	ViewerCount int        `json:"viewerCount"`
	TotalViews  int64      `json:"totalViews"`
	IsTest      bool       `json:"isTest"`              // true = host-only preview, hidden from discover
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// genStreamKey returns 32 hex chars (~128 bits of entropy). Long enough that
// brute-forcing a key on a public ingest is computationally infeasible.
func genStreamKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (r *Repo) Create(ctx context.Context, ownerID int64, title, category string) (*Room, error) {
	key, err := genStreamKey()
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO live_rooms (owner_id, title, category, stream_key)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		RETURNING id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		          stream_key, status, viewer_count, total_views, is_test,
		          started_at, ended_at, created_at`
	rm := &Room{}
	err = r.pool.QueryRow(ctx, q, ownerID, title, category, key).Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
	)
	return rm, err
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*Room, error) {
	rm := &Room{}
	const q = `SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		stream_key, status, viewer_count, total_views, is_test, started_at, ended_at, created_at
		FROM live_rooms WHERE id = $1`
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rm, err
}

// FindByStreamKey is the lookup the SRS callback uses to validate
// `on_publish` against. Always include in dedicated `(stream_key)` index.
func (r *Repo) FindByStreamKey(ctx context.Context, key string) (*Room, error) {
	rm := &Room{}
	const q = `SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		stream_key, status, viewer_count, total_views, is_test, started_at, ended_at, created_at
		FROM live_rooms WHERE stream_key = $1`
	err := r.pool.QueryRow(ctx, q, key).Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidStreamKey
	}
	return rm, err
}

func (r *Repo) ListLive(ctx context.Context, limit int) ([]*Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		        '' AS stream_key, status, viewer_count, total_views, is_test,
		        started_at, ended_at, created_at
		 FROM live_rooms
		 WHERE status = $1 AND NOT is_test
		 ORDER BY viewer_count DESC, started_at DESC
		 LIMIT $2`, StatusLive, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := rows.Scan(&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
			&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
			&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}

func (r *Repo) ListMine(ctx context.Context, ownerID int64) ([]*Room, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		        stream_key, status, viewer_count, total_views, is_test,
		        started_at, ended_at, created_at
		 FROM live_rooms
		 WHERE owner_id = $1
		 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := rows.Scan(&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
			&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
			&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}

func (r *Repo) UpdateMeta(ctx context.Context, id, ownerID int64, title, category, coverURL *string) (*Room, error) {
	sets := []string{}
	args := []any{}
	idx := 1
	if title != nil {
		sets = append(sets, "title = $"+itoa(idx))
		args = append(args, *title)
		idx++
	}
	if category != nil {
		sets = append(sets, "category = NULLIF($"+itoa(idx)+", '')")
		args = append(args, *category)
		idx++
	}
	if coverURL != nil {
		sets = append(sets, "cover_url = NULLIF($"+itoa(idx)+", '')")
		args = append(args, *coverURL)
		idx++
	}
	if len(sets) == 0 {
		return r.FindByID(ctx, id)
	}
	args = append(args, id, ownerID)
	q := "UPDATE live_rooms SET " + join(sets, ", ") +
		" WHERE id = $" + itoa(idx) + " AND owner_id = $" + itoa(idx+1) +
		` RETURNING id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		           stream_key, status, viewer_count, total_views, is_test, started_at, ended_at, created_at`
	rm := &Room{}
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// SetVisibility flips the is_test flag (host preview ↔ public discover).
// Only the owner can call this; mismatched owner returns ErrNotOwner.
func (r *Repo) SetVisibility(ctx context.Context, id, ownerID int64, isTest bool) (*Room, error) {
	const q = `UPDATE live_rooms SET is_test = $3
	           WHERE id = $1 AND owner_id = $2
	           RETURNING id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
	                     stream_key, status, viewer_count, total_views, is_test,
	                     started_at, ended_at, created_at`
	rm := &Room{}
	err := r.pool.QueryRow(ctx, q, id, ownerID, isTest).Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// SetLive flips a room into the live state (SRS on_publish hook). Also
// resets per-broadcast counters (viewer_count, peak_viewers,
// total_danmaku) so a re-broadcast on the same room doesn't carry
// stats from the previous session. Guarded with status != 1 so a
// duplicate on_publish doesn't reset started_at mid-stream and lie
// about uptime.
func (r *Repo) SetLive(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE live_rooms
		    SET status        = 1,
		        started_at    = now(),
		        ended_at      = NULL,
		        viewer_count  = 0,
		        peak_viewers  = 0,
		        total_danmaku = 0
		  WHERE id = $1
		    AND status != 1`, id)
	return err
}

// SetEnded marks a room as no longer broadcasting (SRS on_unpublish
// hook). Finalizes peak_viewers (high-water-mark of viewer_count),
// total_danmaku (counter is already accumulated by realtime), and
// duration_seconds. After this the row is queryable for a recap UI
// without scanning danmaku rows.
//
// Idempotent: the WHERE status=1 guards against double on_unpublish
// from SRS's retry path (no-op rather than rewinding ended_at).
func (r *Repo) SetEnded(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE live_rooms
		   SET status            = 2,
		       ended_at          = now(),
		       duration_seconds  = COALESCE(EXTRACT(EPOCH FROM (now() - started_at))::INT, 0),
		       peak_viewers      = GREATEST(peak_viewers, viewer_count),
		       viewer_count      = 0,
		       current_publish_client_id = NULL
		 WHERE id = $1 AND status = 1`, id)
	return err
}

// TryClaimPublisher atomically binds an SRS client_id to a stream key.
// Used by the on_publish hook to lock the broadcast to the first
// publisher that arrived — any subsequent publisher with the same
// stream key but a different client_id is rejected (key-takeover
// attack). Returns (true, room) if claimed, (false, room) if the slot
// is already held by another client.
//
// The same client_id reconnecting (e.g. an SRS reload that re-issues
// the hook with the same publisher) succeeds — that's a no-op rather
// than a takeover.
func (r *Repo) TryClaimPublisher(ctx context.Context, streamKey, clientID string) (claimed bool, rm *Room, err error) {
	rm, err = r.FindByStreamKey(ctx, streamKey)
	if err != nil {
		return false, nil, err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE live_rooms
		   SET current_publish_client_id = $2
		 WHERE id = $1
		   AND (current_publish_client_id IS NULL OR current_publish_client_id = $2)`,
		rm.ID, clientID)
	if err != nil {
		return false, rm, err
	}
	return tag.RowsAffected() == 1, rm, nil
}

// ReleasePublisher clears the publisher binding AND rotates the stream
// key. Called from the on_unpublish hook. Rotation matters: by the
// time the stream is over, the old key has likely been seen by
// viewers (it's in the HLS URL) — if we don't rotate, the next time
// the owner starts streaming, anyone with the old URL could push
// before the legitimate owner does. After this call, the owner must
// fetch the fresh key via /live/rooms/:id/owner.
//
// Returns the new key for logging convenience.
func (r *Repo) ReleasePublisher(ctx context.Context, roomID int64) (string, error) {
	newKey, err := genStreamKey()
	if err != nil {
		return "", err
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE live_rooms
		   SET current_publish_client_id = NULL,
		       stream_key                = $2
		 WHERE id = $1`, roomID, newKey)
	return newKey, err
}

// BumpDanmaku is the +1 counter for total_danmaku, used by the
// realtime handler after a successful insert. Best-effort: failure
// is logged not surfaced.
func (r *Repo) BumpDanmaku(ctx context.Context, roomID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE live_rooms SET total_danmaku = total_danmaku + 1 WHERE id = $1`, roomID)
	return err
}

// ActiveStreamKeys returns the stream_key of every room currently marked
// status=1 (live). Used by the SRS reconcile loop to diff DB state vs
// what SRS actually has and mark dead rooms ended.
func (r *Repo) ActiveStreamKeys(ctx context.Context) (map[string]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, stream_key FROM live_rooms WHERE status = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		out[key] = id
	}
	return out, rows.Err()
}

// ForceEnd is a no-guard SetEnded used by the SRS reconcile loop. It
// flips status=2 and finalizes stats just like SetEnded, but doesn't
// require status=1 (the row might already be a zombie). Public so
// the cleanup goroutine can call it.
func (r *Repo) ForceEnd(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE live_rooms
		   SET status            = 2,
		       ended_at          = COALESCE(ended_at, now()),
		       duration_seconds  = COALESCE(EXTRACT(EPOCH FROM (now() - started_at))::INT, duration_seconds),
		       peak_viewers      = GREATEST(peak_viewers, viewer_count),
		       viewer_count      = 0,
		       current_publish_client_id = NULL
		 WHERE id = $1`, id)
	return err
}

func (r *Repo) Delete(ctx context.Context, id, ownerID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM live_rooms WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOwner
	}
	return nil
}

// RotateStreamKey forces a fresh stream key (e.g. if the old one leaked).
func (r *Repo) RotateStreamKey(ctx context.Context, id, ownerID int64) (string, error) {
	key, err := genStreamKey()
	if err != nil {
		return "", err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE live_rooms SET stream_key = $3 WHERE id = $1 AND owner_id = $2`,
		id, ownerID, key)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotOwner
	}
	return key, nil
}

// RecordRecording inserts a row pointing at a freshly-uploaded mp4.
func (r *Repo) RecordRecording(ctx context.Context, roomID int64, fileURL string, duration int, sizeBytes int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO live_recordings (room_id, file_url, duration, size_bytes)
		 VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0))`,
		roomID, fileURL, duration, sizeBytes)
	return err
}

// Recordings returns the most recent recordings for a room.
func (r *Repo) Recordings(ctx context.Context, roomID int64) ([]map[string]any, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, file_url, COALESCE(duration, 0), COALESCE(size_bytes, 0), created_at
		 FROM live_recordings WHERE room_id = $1
		 ORDER BY created_at DESC LIMIT 50`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id        int64
			fileURL   string
			duration  int
			sizeBytes int64
			createdAt time.Time
		)
		if err := rows.Scan(&id, &fileURL, &duration, &sizeBytes, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":        id,
			"fileUrl":   fileURL,
			"duration":  duration,
			"sizeBytes": sizeBytes,
			"createdAt": createdAt,
		})
	}
	return out, rows.Err()
}

// Helpers (avoid importing strconv/strings just for two trivial uses).
// =============== Followers ===============

func (r *Repo) Follow(ctx context.Context, roomID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO live_room_followers (room_id, user_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, roomID, userID)
	return err
}

func (r *Repo) Unfollow(ctx context.Context, roomID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM live_room_followers WHERE room_id = $1 AND user_id = $2`,
		roomID, userID)
	return err
}

func (r *Repo) IsFollowing(ctx context.Context, roomID, userID int64) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM live_room_followers WHERE room_id = $1 AND user_id = $2`,
		roomID, userID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repo) FollowerCount(ctx context.Context, roomID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM live_room_followers WHERE room_id = $1`,
		roomID).Scan(&n)
	return n, err
}

// FollowerIDs returns user ids subscribed to this host's go-live notifications.
// Used by realtime to fan out the "host went live" event.
func (r *Repo) FollowerIDs(ctx context.Context, roomID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM live_room_followers WHERE room_id = $1`, roomID)
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

// =============== Persisted danmaku ===============

type Danmaku struct {
	ID        int64     `json:"id,string"`
	RoomID    int64     `json:"roomId,string"`
	SenderID  int64     `json:"senderId,string"`
	Content   string    `json:"text"`
	Color     string    `json:"color,omitempty"`
	CreatedAt time.Time `json:"ts"`
}

func (r *Repo) InsertDanmaku(ctx context.Context, roomID, senderID int64, content, color string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO live_danmaku (room_id, sender_id, content, color)
		 VALUES ($1, $2, $3, NULLIF($4, ''))`,
		roomID, senderID, content, color)
	return err
}

// RecentDanmaku returns up to `limit` newest danmaku for a room, in
// chronological (oldest-first) order so the client can append them
// directly to its render buffer.
func (r *Repo) RecentDanmaku(ctx context.Context, roomID int64, limit int) ([]Danmaku, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, room_id, sender_id, content, COALESCE(color,''), created_at
		 FROM (
		   SELECT id, room_id, sender_id, content, color, created_at
		   FROM live_danmaku WHERE room_id = $1
		   ORDER BY created_at DESC LIMIT $2
		 ) t ORDER BY created_at ASC`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Danmaku, 0)
	for rows.Next() {
		var d Danmaku
		if err := rows.Scan(&d.ID, &d.RoomID, &d.SenderID, &d.Content, &d.Color, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// =============== Bans / kicks ===============

func (r *Repo) BanUser(ctx context.Context, roomID, userID, bannedBy int64, isKick bool, reason string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO live_room_bans (room_id, user_id, banned_by, is_kick, reason)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		 ON CONFLICT (room_id, user_id) DO UPDATE
		   SET is_kick = EXCLUDED.is_kick,
		       reason  = EXCLUDED.reason,
		       banned_by = EXCLUDED.banned_by`,
		roomID, userID, bannedBy, isKick, reason)
	return err
}

func (r *Repo) UnbanUser(ctx context.Context, roomID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM live_room_bans WHERE room_id = $1 AND user_id = $2`,
		roomID, userID)
	return err
}

// IsBanned returns (banned, isKick). isKick=true means the viewer should
// also be disconnected; isKick=false means muted-only.
func (r *Repo) IsBanned(ctx context.Context, roomID, userID int64) (banned, isKick bool, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT is_kick FROM live_room_bans WHERE room_id = $1 AND user_id = $2`,
		roomID, userID).Scan(&isKick)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, isKick, nil
}

// =============== Recordings ===============

// DeleteRecording removes a recording row. Caller verifies owner first.
func (r *Repo) DeleteRecording(ctx context.Context, recID, ownerID int64) (fileURL string, err error) {
	const q = `
		DELETE FROM live_recordings r
		USING live_rooms rm
		WHERE r.id = $1 AND r.room_id = rm.id AND rm.owner_id = $2
		RETURNING r.file_url`
	err = r.pool.QueryRow(ctx, q, recID, ownerID).Scan(&fileURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotOwner
	}
	return fileURL, err
}

// =============== Scheduling / viewer count ===============

// ListScheduled returns upcoming public rooms (scheduled_at > NOW, not live).
func (r *Repo) ListScheduled(ctx context.Context, limit int) ([]*Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		        '' as stream_key, status, viewer_count, total_views, is_test,
		        scheduled_at, ended_at, created_at
		 FROM live_rooms
		 WHERE NOT is_test AND scheduled_at IS NOT NULL
		   AND scheduled_at > NOW() AND status != 1
		 ORDER BY scheduled_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := rows.Scan(&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
			&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
			&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}


func (r *Repo) SetScheduled(ctx context.Context, id, ownerID int64, scheduledAt *time.Time) error {
	// Reset notified flag so the reminder fires for the new time.
	tag, err := r.pool.Exec(ctx,
		`UPDATE live_rooms SET scheduled_at = $3, scheduled_notified = FALSE
		 WHERE id = $1 AND owner_id = $2`, id, ownerID, scheduledAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOwner
	}
	return nil
}

// DueReminders returns rooms whose scheduled_at is within the next `window`
// duration and which haven't been notified yet. Caller is expected to
// fan-out to followers, then call MarkScheduledNotified.
func (r *Repo) DueReminders(ctx context.Context, window time.Duration) ([]*Room, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		        '' as stream_key, status, viewer_count, total_views, is_test,
		        scheduled_at, ended_at, created_at
		 FROM live_rooms
		 WHERE NOT scheduled_notified AND NOT is_test
		   AND scheduled_at IS NOT NULL
		   AND scheduled_at > NOW()
		   AND scheduled_at <= NOW() + ($1 * INTERVAL '1 second')`,
		int(window.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := rows.Scan(&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
			&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
			&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}

func (r *Repo) MarkScheduledNotified(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE live_rooms SET scheduled_notified = TRUE WHERE id = $1`, id)
	return err
}

// SetViewerCount is called by realtime when subscribers change. Total
// views is bumped on first subscribe per user. peak_viewers is also
// nudged upward so the live-ended recap retains the high-water mark.
func (r *Repo) SetViewerCount(ctx context.Context, roomID int64, count int, bumpTotal bool) error {
	if bumpTotal {
		_, err := r.pool.Exec(ctx, `
			UPDATE live_rooms
			   SET viewer_count = $2,
			       peak_viewers = GREATEST(peak_viewers, $2),
			       total_views  = total_views + 1
			 WHERE id = $1`, roomID, count)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE live_rooms
		   SET viewer_count = $2,
		       peak_viewers = GREATEST(peak_viewers, $2)
		 WHERE id = $1`, roomID, count)
	return err
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	b := [16]byte{}
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

func join(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += sep + s
	}
	return out
}
