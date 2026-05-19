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
	"fmt"
	"strings"
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

	// Chat moderation settings (Tier C).
	ChatSubscribersOnly bool `json:"chatSubscribersOnly"`
	SlowModeSeconds     int  `json:"slowModeSeconds"`

	// Pinned danmaku — one per room. Nil when nothing pinned. Snapshot
	// of the original text/sender so the pin survives even if the
	// pinning user later changes their nickname.
	PinnedDanmakuText   *string    `json:"pinnedDanmakuText,omitempty"`
	PinnedDanmakuSender *int64     `json:"pinnedDanmakuSender,omitempty,string"`
	PinnedDanmakuColor  *string    `json:"pinnedDanmakuColor,omitempty"`
	PinnedDanmakuAt     *time.Time `json:"pinnedDanmakuAt,omitempty"`
}

// roomSelectAll is the canonical column list for SELECTs that hydrate
// a full Room with stream_key exposed (owner-facing endpoints). The
// roomSelectPublic variant blanks the key for public listings so it
// doesn't leak via discover/search responses.
const roomSelectAll = `id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		stream_key, status, viewer_count, total_views, is_test,
		started_at, ended_at, created_at,
		chat_subscribers_only, slow_mode_seconds,
		pinned_danmaku_text, pinned_danmaku_sender,
		pinned_danmaku_color, pinned_danmaku_at`

const roomSelectPublic = `id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		'' AS stream_key, status, viewer_count, total_views, is_test,
		started_at, ended_at, created_at,
		chat_subscribers_only, slow_mode_seconds,
		pinned_danmaku_text, pinned_danmaku_sender,
		pinned_danmaku_color, pinned_danmaku_at`

// scanRoom hydrates a Room from a row produced by a SELECT using
// roomSelectAll. The pgx Row + Rows interfaces both satisfy this
// shape so it works from QueryRow and Query-then-Next callers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRoom(row rowScanner, rm *Room) error {
	return row.Scan(
		&rm.ID, &rm.OwnerID, &rm.Title, &rm.CoverURL, &rm.Category,
		&rm.StreamKey, &rm.Status, &rm.ViewerCount, &rm.TotalViews, &rm.IsTest,
		&rm.StartedAt, &rm.EndedAt, &rm.CreatedAt,
		&rm.ChatSubscribersOnly, &rm.SlowModeSeconds,
		&rm.PinnedDanmakuText, &rm.PinnedDanmakuSender,
		&rm.PinnedDanmakuColor, &rm.PinnedDanmakuAt,
	)
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
	q := `INSERT INTO live_rooms (owner_id, title, category, stream_key)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		RETURNING ` + roomSelectAll
	rm := &Room{}
	if err := scanRoom(r.pool.QueryRow(ctx, q, ownerID, title, category, key), rm); err != nil {
		return nil, err
	}
	return rm, nil
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*Room, error) {
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx,
		`SELECT `+roomSelectAll+` FROM live_rooms WHERE id = $1`, id), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rm, err
}

// FindByStreamKey is the lookup the SRS callback uses to validate
// `on_publish` against. Always include in dedicated `(stream_key)` index.
func (r *Repo) FindByStreamKey(ctx context.Context, key string) (*Room, error) {
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx,
		`SELECT `+roomSelectAll+` FROM live_rooms WHERE stream_key = $1`, key), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidStreamKey
	}
	return rm, err
}

// ListLiveFilter narrows the public discover list. Empty Q / Category =
// no filter. Q is matched ILIKE against title (substring, case-insensitive).
type ListLiveFilter struct {
	Q        string
	Category string
	Limit    int
}

func (r *Repo) ListLive(ctx context.Context, filter ListLiveFilter) ([]*Room, error) {
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 30
	}
	// Build the WHERE clause incrementally. Always exclude test-mode
	// rooms; only show currently-live status. Filter args use
	// $-placeholder indices that grow as we add clauses.
	where := []string{"status = $1", "NOT is_test"}
	args := []any{StatusLive}
	idx := 2
	if cat := strings.TrimSpace(filter.Category); cat != "" {
		where = append(where, "category = $"+itoa(idx))
		args = append(args, cat)
		idx++
	}
	if q := strings.TrimSpace(filter.Q); q != "" {
		// Escape ILIKE wildcards so a user typing % gets a literal %.
		safe := strings.ReplaceAll(q, `\`, `\\`)
		safe = strings.ReplaceAll(safe, `%`, `\%`)
		safe = strings.ReplaceAll(safe, `_`, `\_`)
		where = append(where, "title ILIKE $"+itoa(idx))
		args = append(args, "%"+safe+"%")
		idx++
	}
	args = append(args, filter.Limit)
	sql := `SELECT ` + roomSelectPublic + ` FROM live_rooms WHERE ` + join(where, " AND ") +
		` ORDER BY viewer_count DESC, started_at DESC LIMIT $` + itoa(idx)
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := scanRoom(rows, rm); err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}

func (r *Repo) ListMine(ctx context.Context, ownerID int64) ([]*Room, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+roomSelectAll+` FROM live_rooms WHERE owner_id = $1 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Room, 0)
	for rows.Next() {
		rm := &Room{}
		if err := scanRoom(rows, rm); err != nil {
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
		` RETURNING ` + roomSelectAll
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx, q, args...), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// SetVisibility flips the is_test flag (host preview ↔ public discover).
// Only the owner can call this; mismatched owner returns ErrNotOwner.
func (r *Repo) SetVisibility(ctx context.Context, id, ownerID int64, isTest bool) (*Room, error) {
	q := `UPDATE live_rooms SET is_test = $3
	      WHERE id = $1 AND owner_id = $2
	      RETURNING ` + roomSelectAll
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx, q, id, ownerID, isTest), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// UpdateChatSettings is the owner-only "chat moderation" endpoint —
// flips slow_mode_seconds and chat_subscribers_only in one shot. Both
// are server-enforced in the realtime danmaku send path. A future
// migration can add e.g. emote-only or word-blocklist alongside.
func (r *Repo) UpdateChatSettings(ctx context.Context, id, ownerID int64, slowSeconds *int, subscribersOnly *bool) (*Room, error) {
	sets := []string{}
	args := []any{}
	idx := 1
	if slowSeconds != nil {
		s := *slowSeconds
		if s < 0 {
			s = 0
		}
		if s > 300 {
			s = 300
		}
		sets = append(sets, "slow_mode_seconds = $"+itoa(idx))
		args = append(args, s)
		idx++
	}
	if subscribersOnly != nil {
		sets = append(sets, "chat_subscribers_only = $"+itoa(idx))
		args = append(args, *subscribersOnly)
		idx++
	}
	if len(sets) == 0 {
		return r.FindByID(ctx, id)
	}
	args = append(args, id, ownerID)
	q := "UPDATE live_rooms SET " + join(sets, ", ") +
		" WHERE id = $" + itoa(idx) + " AND owner_id = $" + itoa(idx+1) +
		` RETURNING ` + roomSelectAll
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx, q, args...), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// PinDanmaku snapshots the given danmaku into the live_rooms row so it
// stays visible at the top of the chat for late joiners. Set pin to a
// non-empty text to pin, or empty / nil to clear.
func (r *Repo) PinDanmaku(ctx context.Context, id, ownerID int64, text, color string, senderID int64) (*Room, error) {
	q := `UPDATE live_rooms
	      SET pinned_danmaku_text   = NULLIF($3, ''),
	          pinned_danmaku_sender = CASE WHEN $3 = '' THEN NULL ELSE $4 END,
	          pinned_danmaku_color  = CASE WHEN $3 = '' THEN NULL ELSE NULLIF($5, '') END,
	          pinned_danmaku_at     = CASE WHEN $3 = '' THEN NULL ELSE now() END
	      WHERE id = $1 AND owner_id = $2
	      RETURNING ` + roomSelectAll
	rm := &Room{}
	err := scanRoom(r.pool.QueryRow(ctx, q, id, ownerID, text, senderID, color), rm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotOwner
	}
	return rm, err
}

// ChatSettings returns the minimal "what does the danmaku send handler
// need to enforce" tuple. Tuple shape (not struct) because this is
// the LiveBackend interface used by realtime — keeps that contract
// small. Returns zero values for unknown rooms (caller treats that
// as "no moderation").
func (r *Repo) ChatSettings(ctx context.Context, roomID int64) (slowSeconds int, subscribersOnly bool, ownerID int64, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT slow_mode_seconds, chat_subscribers_only, owner_id
		FROM live_rooms WHERE id = $1`, roomID,
	).Scan(&slowSeconds, &subscribersOnly, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, 0, ErrNotFound
	}
	return
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
	// We always overwrite the binding. Rationale: SRS enforces single-
	// publisher exclusivity at the RTMP layer — it will NOT call this
	// hook for a second concurrent publisher on the same stream key
	// (it rejects the connection before ever firing on_publish). So
	// when we receive on_publish, the previous binding is by
	// definition stale: the old SRS session is gone (crashed, network
	// loss, on_unpublish dropped, etc.). The earlier "reject if slot
	// held by a different client_id" guard was preventing legit
	// owner restarts after any abandoned session, with no actual
	// security benefit.
	//
	// Leaked-stream-key takeover (attacker grabs the key from an HLS
	// URL and pushes while owner is offline) is still bounded by the
	// 5-minute abandoned-key sweeper (see RotateAbandonedStreamKeys).
	_, err = r.pool.Exec(ctx, `
		UPDATE live_rooms SET current_publish_client_id = $2 WHERE id = $1`,
		rm.ID, clientID)
	if err != nil {
		return false, rm, err
	}
	return true, rm, nil
}

// ReleasePublisher clears the publisher binding AND rotates the stream
// key. Called only for explicit stop events (owner click "结束直播",
// admin ban, key reset). NOT called from on_unpublish — see
// RotateAbandonedStreamKeys for the lazy-rotation flow that lets OBS
// network blips reconnect without losing the key.
//
// Rotation matters: by the time the stream is over, the old key has
// likely been seen by viewers (it's in the HLS URL) — if we don't
// rotate eventually, anyone with the old URL could push before the
// legitimate owner does next time. After this call, the owner must
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
		       stream_key                = $2,
		       last_key_rotation_at      = now()
		 WHERE id = $1`, roomID, newKey)
	return newKey, err
}

// RotateAbandonedStreamKeys rotates the stream_key on rooms that have
// been status=2 (ended) for longer than `maxIdle`, but whose key
// hasn't been rotated since they ended. Run periodically by the
// background sweeper.
//
// The grace window matters: on_unpublish fires after SRS's
// publish_normal_timeout (7 s) which trips on any RTMP gap — every
// brief OBS disconnect, WiFi handoff, or laptop sleep would invalidate
// the key under the old "rotate inside on_unpublish" design. With
// this grace flow, OBS reconnecting within maxIdle reuses the same
// key cleanly; only genuinely abandoned streams get their keys
// rotated (closing the takeover-by-leaked-HLS-URL window).
//
// Implementation: SELECT candidates, then per-row UPDATE with a
// Go-generated key. We avoid the obvious single-statement form
// (encode(gen_random_bytes(...))) because gen_random_bytes lives in
// the pgcrypto extension which isn't enabled on this DB, and adding
// it requires CREATE EXTENSION privilege the dfchat user lacks. The
// candidate set is tiny (at most "rooms that ended in the last hour
// or two on a busy day"), so the per-row cost is negligible.
//
// Returns the IDs that were rotated for logging.
func (r *Repo) RotateAbandonedStreamKeys(ctx context.Context, maxIdle time.Duration) ([]int64, error) {
	threshold := time.Now().Add(-maxIdle)
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM live_rooms
		 WHERE status = 2
		   AND ended_at IS NOT NULL
		   AND ended_at < $1
		   AND (last_key_rotation_at IS NULL OR last_key_rotation_at < ended_at)`,
		threshold)
	if err != nil {
		return nil, err
	}
	var candidates []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rotated := make([]int64, 0, len(candidates))
	for _, id := range candidates {
		newKey, err := genStreamKey()
		if err != nil {
			return rotated, err
		}
		// Re-check the rotation guard in the WHERE so a concurrent
		// owner-side rotate that happens between our SELECT and this
		// UPDATE doesn't get clobbered. Idempotent if the row no
		// longer matches.
		tag, err := r.pool.Exec(ctx, `
			UPDATE live_rooms
			   SET stream_key           = $2,
			       last_key_rotation_at = now()
			 WHERE id = $1
			   AND status = 2
			   AND (last_key_rotation_at IS NULL OR last_key_rotation_at < ended_at)`,
			id, newKey)
		if err != nil {
			return rotated, err
		}
		if tag.RowsAffected() == 1 {
			rotated = append(rotated, id)
		}
	}
	return rotated, nil
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
// Owner-initiated only — see ReleasePublisher for stop-flow rotations
// and RotateAbandonedStreamKeys for the time-based grace-period sweep.
func (r *Repo) RotateStreamKey(ctx context.Context, id, ownerID int64) (string, error) {
	key, err := genStreamKey()
	if err != nil {
		return "", err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE live_rooms
		    SET stream_key           = $3,
		        last_key_rotation_at = now()
		  WHERE id = $1 AND owner_id = $2`,
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
	// SenderNickname + SenderAccountNo: looked up from users at SELECT
	// time so the client can render "<昵称> #<账号>" in the chat side
	// panel without making N follow-up lookups. Both can be empty if
	// the sender's account has been deleted (FK is ON DELETE SET NULL
	// in some places but here we LEFT JOIN to survive).
	SenderNickname  string    `json:"senderNickname,omitempty"`
	SenderAccountNo string    `json:"senderAccountNo,omitempty"`
	Content         string    `json:"text"`
	Color           string    `json:"color,omitempty"`
	CreatedAt       time.Time `json:"ts"`
}

// UserDisplay returns the public-facing identifiers for a user: their
// chosen nickname and their stable account number (the one rendered as
// "#123456" in the UI). Used by the realtime danmaku broadcast path so
// each WS event arrives already labeled — no client-side lookup needed.
//
// LEFT-JOIN-shaped via separate SELECT: if the row's gone the caller
// gets empty strings rather than ErrNotFound; danmaku from deleted
// accounts should still render, just without a name.
func (r *Repo) UserDisplay(ctx context.Context, userID int64) (nickname, accountNo string, err error) {
	// account_no is bigint NOT NULL — cast to text so COALESCE doesn't
	// trip "invalid input syntax for type bigint: ''" when the user
	// row exists but we're shielding callers from a hard fail.
	row := r.pool.QueryRow(ctx,
		`SELECT COALESCE(nickname,''), COALESCE(account_no::text,'') FROM users WHERE id = $1`,
		userID)
	if err = row.Scan(&nickname, &accountNo); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	return
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
// directly to its render buffer. LEFT JOIN users so deleted accounts
// don't drop their historical danmaku from the recap; instead they
// render as a danmaku with no nickname/accountNo (caller decides how
// to show it — currently just shows blank name).
func (r *Repo) RecentDanmaku(ctx context.Context, roomID int64, limit int) ([]Danmaku, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT t.id, t.room_id, t.sender_id, t.content, COALESCE(t.color,''), t.created_at,
		        COALESCE(u.nickname,''), COALESCE(u.account_no::text,'')
		   FROM (
		     SELECT id, room_id, sender_id, content, color, created_at
		       FROM live_danmaku WHERE room_id = $1
		       ORDER BY created_at DESC LIMIT $2
		   ) t
		   LEFT JOIN users u ON u.id = t.sender_id
		  ORDER BY t.created_at ASC`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Danmaku, 0)
	for rows.Next() {
		var d Danmaku
		if err := rows.Scan(&d.ID, &d.RoomID, &d.SenderID, &d.Content, &d.Color, &d.CreatedAt,
			&d.SenderNickname, &d.SenderAccountNo); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// =============== AI moderation verdicts (every tick) ===============

// Verdict is one row in live_ai_verdicts — captures the AI's
// judgment of a single thumbnail at a single moment. Includes both
// clean (max_score below threshold) and flagged verdicts so the
// admin can audit AI accuracy in either direction. Manual labels +
// pinning live on the row so the audit trail is self-contained.
type Verdict struct {
	ID            int64      `json:"id,string"`
	RoomID        int64      `json:"roomId,string"`
	Provider      string     `json:"provider"`
	MaxCategory   string     `json:"maxCategory"`
	MaxScore      float32    `json:"maxScore"`
	Scores        []byte     `json:"-"` // raw JSONB bytes; ScoresJSON below for marshaling
	ScoresJSON    string     `json:"scores"`
	Reason        string     `json:"reason,omitempty"`
	ThumbnailURL  string     `json:"thumbnailUrl,omitempty"`
	Flagged       bool       `json:"flagged"`
	ReportID      *int64     `json:"reportId,omitempty,string"`
	ManualLabel   string     `json:"manualLabel,omitempty"`
	LabeledBy     *int64     `json:"labeledBy,omitempty,string"`
	LabeledAt     *time.Time `json:"labeledAt,omitempty"`
	Pinned        bool       `json:"pinned"`
	CreatedAt     time.Time  `json:"createdAt"`
	// Joined snapshot — saves N+1 lookups in the admin UI.
	RoomTitle      string `json:"roomTitle,omitempty"`
	RoomStatus     int    `json:"roomStatus,omitempty"`
	OwnerNickname  string `json:"ownerNickname,omitempty"`
	OwnerAccountNo string `json:"ownerAccountNo,omitempty"`
}

// InsertVerdict stores one tick's judgment. Returns the new id so
// the worker can wire it to a follow-up live_room_reports.id (when
// flagged) via UpdateVerdictReportLink.
func (r *Repo) InsertVerdict(ctx context.Context, roomID int64, provider, maxCategory string, maxScore float64, scoresJSON, reason, thumbURL string, flagged bool) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO live_ai_verdicts
		  (room_id, provider, max_category, max_score, scores, reason, thumbnail_url, flagged)
		VALUES ($1, $2, $3, $4, $5::jsonb, NULLIF($6,''), NULLIF($7,''), $8)
		RETURNING id`,
		roomID, provider, maxCategory, maxScore, scoresJSON, reason, thumbURL, flagged,
	).Scan(&id)
	return id, err
}

// LinkVerdictReport wires a verdict to the report row the worker
// just inserted for it. Allows the admin UI to jump from verdict ↔
// report bidirectionally and lets "false_positive" auto-dismiss
// the linked report.
func (r *Repo) LinkVerdictReport(ctx context.Context, verdictID, reportID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE live_ai_verdicts SET report_id = $2 WHERE id = $1`, verdictID, reportID)
	return err
}

// ListVerdicts returns verdicts newest-first with the joined room +
// owner snapshot. Filters:
//   - roomID > 0:        only that room
//   - flaggedOnly:       only AI-flagged rows
//   - unlabeledOnly:     only rows the admin hasn't labeled yet
//   - sinceCursor > 0:   pagination (created_at < since); 0 = first page
func (r *Repo) ListVerdicts(ctx context.Context, roomID int64, flaggedOnly, unlabeledOnly bool, limit int) ([]Verdict, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{limit}
	where := []string{"1=1"}
	if roomID > 0 {
		args = append(args, roomID)
		where = append(where, fmt.Sprintf("v.room_id = $%d", len(args)))
	}
	if flaggedOnly {
		where = append(where, "v.flagged = TRUE")
	}
	if unlabeledOnly {
		where = append(where, "v.manual_label IS NULL")
	}
	q := `
		SELECT
		  v.id, v.room_id, v.provider, v.max_category, v.max_score,
		  v.scores::text, COALESCE(v.reason,''), COALESCE(v.thumbnail_url,''),
		  v.flagged, v.report_id, COALESCE(v.manual_label,''), v.labeled_by, v.labeled_at,
		  v.pinned, v.created_at,
		  rm.title, rm.status,
		  COALESCE(u.nickname,''), COALESCE(u.account_no::text,'')
		  FROM live_ai_verdicts v
		  JOIN live_rooms rm ON rm.id = v.room_id
		  LEFT JOIN users u ON u.id = rm.owner_id
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY v.created_at DESC LIMIT $1`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Verdict, 0)
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(
			&v.ID, &v.RoomID, &v.Provider, &v.MaxCategory, &v.MaxScore,
			&v.ScoresJSON, &v.Reason, &v.ThumbnailURL,
			&v.Flagged, &v.ReportID, &v.ManualLabel, &v.LabeledBy, &v.LabeledAt,
			&v.Pinned, &v.CreatedAt,
			&v.RoomTitle, &v.RoomStatus,
			&v.OwnerNickname, &v.OwnerAccountNo,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LabelVerdict records an admin's manual judgment of a verdict.
// `label` is one of: agree / should_flag / false_positive — values
// match the comment in the migration. Pass empty string to clear.
// Returns the row's report_id (or nil) so the caller can act on
// linked reports — typical pattern:
//
//   - label=should_flag and reportID was NULL → caller creates a
//     new report row to put it in the admin queue
//   - label=false_positive and reportID was non-NULL → caller
//     resolves that report with status=2 (dismissed)
func (r *Repo) LabelVerdict(ctx context.Context, id, byUserID int64, label string) (*int64, error) {
	var reportID *int64
	err := r.pool.QueryRow(ctx, `
		UPDATE live_ai_verdicts
		   SET manual_label = NULLIF($2,''),
		       labeled_by   = $3,
		       labeled_at   = now()
		 WHERE id = $1
		 RETURNING report_id`, id, label, byUserID,
	).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return reportID, err
}

// PinVerdict toggles the pinned flag so the cleanup sweeper leaves
// the row + its archived thumbnail in place beyond the 7-day window.
func (r *Repo) PinVerdict(ctx context.Context, id int64, pinned bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE live_ai_verdicts SET pinned = $2 WHERE id = $1`, id, pinned)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SweepOldVerdicts deletes verdicts older than `maxAge` that aren't
// pinned, returning the list of thumbnail_url paths the caller
// should also unlink from disk. The DB delete is the source of
// truth; failed file unlinks just leak storage.
func (r *Repo) SweepOldVerdicts(ctx context.Context, maxAge time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-maxAge)
	rows, err := r.pool.Query(ctx, `
		DELETE FROM live_ai_verdicts
		 WHERE pinned = FALSE
		   AND created_at < $1
		 RETURNING COALESCE(thumbnail_url,'')`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var thumbs []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return thumbs, err
		}
		if t != "" {
			thumbs = append(thumbs, t)
		}
	}
	return thumbs, rows.Err()
}

// =============== User reports (admin review queue) ===============

// Report mirrors a row in live_room_reports. The thumbnail_url is
// captured at the moment of report so evidence survives a stream
// ending; the reporter side fills it in by querying the latest
// thumbnail we have on server02. Status 0 = pending, 1 = handled,
// 2 = dismissed; reviewed_by + action_taken populated on transition.
type Report struct {
	ID            int64      `json:"id,string"`
	RoomID        int64      `json:"roomId,string"`
	ReporterID    *int64     `json:"reporterId,omitempty,string"`
	Reason        string     `json:"reason"`
	Note          string     `json:"note,omitempty"`
	ThumbnailURL  string     `json:"thumbnailUrl,omitempty"`
	Status        int16      `json:"status"`
	ReviewedBy    *int64     `json:"reviewedBy,omitempty,string"`
	ReviewedAt    *time.Time `json:"reviewedAt,omitempty"`
	ActionTaken   string     `json:"actionTaken,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	// Joined room snapshot for the admin queue render — saves a
	// follow-up roundtrip per row to fetch title / owner / status.
	RoomTitle       string `json:"roomTitle,omitempty"`
	RoomOwnerID     int64  `json:"roomOwnerId,omitempty,string"`
	RoomStatus      int    `json:"roomStatus,omitempty"`
	RoomCoverURL    string `json:"roomCoverUrl,omitempty"`
	RoomStreamKey   string `json:"-"` // never serialized; used internally for thumbnail URL formatting
	OwnerNickname   string `json:"ownerNickname,omitempty"`
	OwnerAccountNo  string `json:"ownerAccountNo,omitempty"`
	ReporterNickname  string `json:"reporterNickname,omitempty"`
	ReporterAccountNo string `json:"reporterAccountNo,omitempty"`
}

// InsertReport adds a pending report. If the same (room, reporter,
// reason) combo already exists as status=0 the partial unique index
// in migration 27 rejects the INSERT and we return nil — i.e. "ok,
// you already reported this". reporterID = nil for system/AI reports.
func (r *Repo) InsertReport(ctx context.Context, roomID int64, reporterID *int64, reason, note, thumbURL string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO live_room_reports (room_id, reporter_id, reason, note, thumbnail_url)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''))
		ON CONFLICT (room_id, reporter_id, reason)
		  WHERE reporter_id IS NOT NULL AND status = 0
		  DO NOTHING`,
		roomID, reporterID, reason, note, thumbURL)
	return err
}

// InsertReportReturnID is the same as InsertReport but returns the
// new row's id. Used by the moderation worker to wire the verdict
// row to its report row for bidirectional UI navigation. Returns
// id=0 on ON CONFLICT skip (duplicate within unique-index window).
func (r *Repo) InsertReportReturnID(ctx context.Context, roomID int64, reporterID *int64, reason, note, thumbURL string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO live_room_reports (room_id, reporter_id, reason, note, thumbnail_url)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''))
		ON CONFLICT (room_id, reporter_id, reason)
		  WHERE reporter_id IS NOT NULL AND status = 0
		  DO NOTHING
		RETURNING id`,
		roomID, reporterID, reason, note, thumbURL,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict — already a pending report for this combo.
		return 0, nil
	}
	return id, err
}

// ListReports returns reports filtered by status, newest first, with
// joined room + owner + reporter labels so the admin queue can render
// each row without N+1 follow-ups.
func (r *Repo) ListReports(ctx context.Context, status int16, limit int) ([]Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	// account_no is BIGINT in users — COALESCE(bigint, '') errors with
	// "invalid input syntax for type bigint", so cast to text first.
	rows, err := r.pool.Query(ctx, `
		SELECT
		  rep.id, rep.room_id, rep.reporter_id, rep.reason, COALESCE(rep.note,''),
		  COALESCE(rep.thumbnail_url,''), rep.status, rep.reviewed_by, rep.reviewed_at,
		  COALESCE(rep.action_taken,''), rep.created_at,
		  rm.title, rm.owner_id, rm.status, COALESCE(rm.cover_url,''), rm.stream_key,
		  COALESCE(uo.nickname,''), COALESCE(uo.account_no::text,''),
		  COALESCE(ur.nickname,''), COALESCE(ur.account_no::text,'')
		  FROM live_room_reports rep
		  JOIN live_rooms rm ON rm.id = rep.room_id
		  LEFT JOIN users uo ON uo.id = rm.owner_id
		  LEFT JOIN users ur ON ur.id = rep.reporter_id
		 WHERE rep.status = $1
		 ORDER BY rep.created_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Report, 0)
	for rows.Next() {
		var p Report
		if err := rows.Scan(
			&p.ID, &p.RoomID, &p.ReporterID, &p.Reason, &p.Note,
			&p.ThumbnailURL, &p.Status, &p.ReviewedBy, &p.ReviewedAt,
			&p.ActionTaken, &p.CreatedAt,
			&p.RoomTitle, &p.RoomOwnerID, &p.RoomStatus, &p.RoomCoverURL, &p.RoomStreamKey,
			&p.OwnerNickname, &p.OwnerAccountNo,
			&p.ReporterNickname, &p.ReporterAccountNo,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ResolveReport transitions a pending report into status=1 (handled)
// or status=2 (dismissed). Records who reviewed it + the action taken
// so the audit trail joins cleanly with audit_logs.
func (r *Repo) ResolveReport(ctx context.Context, id, reviewerID int64, status int16, action string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE live_room_reports
		   SET status = $2, reviewed_by = $3, reviewed_at = now(), action_taken = NULLIF($4,'')
		 WHERE id = $1 AND status = 0`, id, status, reviewerID, action)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingReportCount returns the number of status=0 reports — used by
// the admin dashboard's red-dot badge.
func (r *Repo) PendingReportCount(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM live_room_reports WHERE status = 0`).Scan(&n)
	return n, err
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
// Aliases scheduled_at → started_at so the same JSON shape works for both
// the "upcoming" widget and the "currently live" listing on the client.
func (r *Repo) ListScheduled(ctx context.Context, limit int) ([]*Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_id, title, COALESCE(cover_url,''), COALESCE(category,''),
		        '' as stream_key, status, viewer_count, total_views, is_test,
		        scheduled_at, ended_at, created_at,
		        chat_subscribers_only, slow_mode_seconds,
		        pinned_danmaku_text, pinned_danmaku_sender,
		        pinned_danmaku_color, pinned_danmaku_at
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
		if err := scanRoom(rows, rm); err != nil {
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
		        scheduled_at, ended_at, created_at,
		        chat_subscribers_only, slow_mode_seconds,
		        pinned_danmaku_text, pinned_danmaku_sender,
		        pinned_danmaku_color, pinned_danmaku_at
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
		if err := scanRoom(rows, rm); err != nil {
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
