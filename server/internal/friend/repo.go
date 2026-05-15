package friend

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAlreadyFriends = errors.New("already friends")
var ErrAlreadyRequested = errors.New("request already pending")
var ErrRequestNotFound = errors.New("friend request not found")
var ErrUserNotFound = errors.New("user not found")

type Friend struct {
	ID        int64     `json:"id,string"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatarUrl,omitempty"`
	Remark    string    `json:"remark,omitempty"`
	IsOnline  bool      `json:"isOnline"`
	CreatedAt time.Time `json:"createdAt"`
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// FindIDByUsername returns the user id for a given username, or ErrUserNotFound.
func (r *Repo) FindIDByUsername(ctx context.Context, username string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM users WHERE username = $1`, username).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	return id, err
}

// SendRequest creates a one-sided pending row (requester → target). If a
// reciprocal pending row already exists, both go to accepted (auto-accept on
// mutual request). If the pair is already friends, returns ErrAlreadyFriends.
func (r *Repo) SendRequest(ctx context.Context, requester, target int64) error {
	if requester == target {
		return errors.New("cannot add yourself")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Already an accepted edge in either direction → already friends.
	var existsAccepted bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM friendships
		 WHERE ((user_id=$1 AND friend_id=$2) OR (user_id=$2 AND friend_id=$1))
		   AND status = 1)`, requester, target).Scan(&existsAccepted); err != nil {
		return err
	}
	if existsAccepted {
		return ErrAlreadyFriends
	}

	// Already requested by me → no-op.
	var existsPendingMine bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM friendships
		 WHERE user_id=$1 AND friend_id=$2 AND status=0)`, requester, target).Scan(&existsPendingMine); err != nil {
		return err
	}
	if existsPendingMine {
		return ErrAlreadyRequested
	}

	// Reciprocal pending (target previously requested me) → auto-accept.
	var existsReciprocal bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM friendships
		 WHERE user_id=$1 AND friend_id=$2 AND status=0)`, target, requester).Scan(&existsReciprocal); err != nil {
		return err
	}
	if existsReciprocal {
		if _, err := tx.Exec(ctx,
			`UPDATE friendships SET status=1 WHERE user_id=$1 AND friend_id=$2`, target, requester); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO friendships (user_id, friend_id, status) VALUES ($1, $2, 1)
			 ON CONFLICT (user_id, friend_id) DO UPDATE SET status=1`, requester, target); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	// Plain new request.
	if _, err := tx.Exec(ctx,
		`INSERT INTO friendships (user_id, friend_id, status) VALUES ($1, $2, 0)
		 ON CONFLICT (user_id, friend_id) DO NOTHING`, requester, target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcceptRequest converts a pending (from→me) into accepted on both sides.
func (r *Repo) AcceptRequest(ctx context.Context, me, from int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE friendships SET status=1 WHERE user_id=$1 AND friend_id=$2 AND status=0`,
		from, me)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO friendships (user_id, friend_id, status) VALUES ($1, $2, 1)
		 ON CONFLICT (user_id, friend_id) DO UPDATE SET status=1`, me, from); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RejectRequest drops a pending (from→me) row.
func (r *Repo) RejectRequest(ctx context.Context, me, from int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM friendships WHERE user_id=$1 AND friend_id=$2 AND status=0`,
		from, me)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// CancelOutgoing drops a pending (me→to) request the user no longer wants.
func (r *Repo) CancelOutgoing(ctx context.Context, me, to int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM friendships WHERE user_id=$1 AND friend_id=$2 AND status=0`,
		me, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	return nil
}

type Request struct {
	UserID    int64     `json:"userId,string"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatarUrl,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListIncoming returns pending requests sent TO me (other users want to friend me).
func (r *Repo) ListIncoming(ctx context.Context, me int64) ([]*Request, error) {
	return r.listRequests(ctx, `
		SELECT u.id, u.username, u.nickname, COALESCE(u.avatar_url, ''), f.created_at
		FROM friendships f JOIN users u ON u.id = f.user_id
		WHERE f.friend_id = $1 AND f.status = 0
		ORDER BY f.created_at DESC`, me)
}

// ListOutgoing returns pending requests sent BY me (awaiting target acceptance).
func (r *Repo) ListOutgoing(ctx context.Context, me int64) ([]*Request, error) {
	return r.listRequests(ctx, `
		SELECT u.id, u.username, u.nickname, COALESCE(u.avatar_url, ''), f.created_at
		FROM friendships f JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = $1 AND f.status = 0
		ORDER BY f.created_at DESC`, me)
}

func (r *Repo) listRequests(ctx context.Context, q string, args ...any) ([]*Request, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Request, 0)
	for rows.Next() {
		req := &Request{}
		if err := rows.Scan(&req.UserID, &req.Username, &req.Nickname, &req.AvatarURL, &req.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func (r *Repo) Remove(ctx context.Context, userA, userB int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM friendships WHERE user_id=$1 AND friend_id=$2`, userA, userB); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM friendships WHERE user_id=$1 AND friend_id=$2`, userB, userA); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) List(ctx context.Context, userID int64) ([]*Friend, error) {
	const q = `
		SELECT u.id, u.username, u.nickname, COALESCE(u.avatar_url, ''),
		       COALESCE(f.remark, ''), f.created_at
		FROM friendships f
		JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = $1 AND f.status = 1
		ORDER BY f.created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	friends := make([]*Friend, 0)
	for rows.Next() {
		f := &Friend{}
		if err := rows.Scan(&f.ID, &f.Username, &f.Nickname, &f.AvatarURL, &f.Remark, &f.CreatedAt); err != nil {
			return nil, err
		}
		friends = append(friends, f)
	}
	return friends, rows.Err()
}

// Block sets the (me → other) row to status=2 (blocked). Removes the
// reciprocal accepted row so the blocked side no longer sees us in their
// friends list. Idempotent.
func (r *Repo) Block(ctx context.Context, me, other int64) error {
	if me == other {
		return errors.New("cannot block yourself")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO friendships (user_id, friend_id, status) VALUES ($1, $2, 2)
		 ON CONFLICT (user_id, friend_id) DO UPDATE SET status = 2`,
		me, other); err != nil {
		return err
	}
	// Remove the other side's row so we disappear from their friends list.
	// (They can still see we blocked them implicitly by failing message sends.)
	if _, err := tx.Exec(ctx,
		`DELETE FROM friendships WHERE user_id = $1 AND friend_id = $2`,
		other, me); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) Unblock(ctx context.Context, me, other int64) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM friendships WHERE user_id = $1 AND friend_id = $2 AND status = 2`,
		me, other)
	return err
}

// IsBlockedEither returns true if either side has blocked the other. Used to
// gate sends and friend requests.
func (r *Repo) IsBlockedEither(ctx context.Context, a, b int64) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM friendships
		   WHERE ((user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1))
		     AND status = 2)`,
		a, b).Scan(&ok)
	return ok, err
}

// ListBlocked returns user IDs the caller has blocked.
func (r *Repo) ListBlocked(ctx context.Context, me int64) ([]*Friend, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.username, u.nickname, COALESCE(u.avatar_url,''), '' AS remark, f.created_at
		FROM friendships f JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = $1 AND f.status = 2
		ORDER BY f.created_at DESC`, me)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Friend, 0)
	for rows.Next() {
		f := &Friend{}
		if err := rows.Scan(&f.ID, &f.Username, &f.Nickname, &f.AvatarURL, &f.Remark, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AreFriends reports whether userA has userB as an accepted friend.
func (r *Repo) AreFriends(ctx context.Context, userA, userB int64) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM friendships WHERE user_id=$1 AND friend_id=$2 AND status=1`,
		userA, userB).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
