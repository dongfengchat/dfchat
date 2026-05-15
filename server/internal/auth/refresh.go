package auth

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
	ErrRefreshInvalid = errors.New("refresh token invalid")
	ErrRefreshExpired = errors.New("refresh token expired")
)

type RefreshStore struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

func NewRefreshStore(pool *pgxpool.Pool, ttl time.Duration) *RefreshStore {
	return &RefreshStore{pool: pool, ttl: ttl}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Issue creates a new refresh token row for userID.
func (s *RefreshStore) Issue(ctx context.Context, userID int64, device string) (string, time.Time, error) {
	tok, err := generateToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(s.ttl)
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (token, user_id, device, expires_at) VALUES ($1, $2, $3, $4)`,
		tok, userID, device, exp); err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// Swap rotates the refresh token: revokes the old, issues a new one. Returns
// (userID, newToken, expiresAt) on success.
func (s *RefreshStore) Swap(ctx context.Context, token, device string) (int64, string, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	defer tx.Rollback(ctx)

	var (
		userID    int64
		expiresAt time.Time
		revokedAt *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT user_id, expires_at, revoked_at FROM refresh_tokens WHERE token = $1 FOR UPDATE`,
		token).Scan(&userID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", time.Time{}, ErrRefreshInvalid
	}
	if err != nil {
		return 0, "", time.Time{}, err
	}
	if revokedAt != nil {
		return 0, "", time.Time{}, ErrRefreshInvalid
	}
	if time.Now().After(expiresAt) {
		return 0, "", time.Time{}, ErrRefreshExpired
	}

	now := time.Now()
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $1, last_used_at = $1 WHERE token = $2`,
		now, token); err != nil {
		return 0, "", time.Time{}, err
	}

	newTok, err := generateToken()
	if err != nil {
		return 0, "", time.Time{}, err
	}
	exp := now.Add(s.ttl)
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (token, user_id, device, expires_at) VALUES ($1, $2, $3, $4)`,
		newTok, userID, device, exp); err != nil {
		return 0, "", time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", time.Time{}, err
	}
	return userID, newTok, exp, nil
}

// Revoke marks a single token revoked (used on explicit logout).
func (s *RefreshStore) Revoke(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE token = $1 AND revoked_at IS NULL`,
		token)
	return err
}

type Session struct {
	// We expose a short prefix as the "session id" so the actual token never
	// leaves the server. Sufficient for "this is the device, revoke it".
	TokenPrefix string    `json:"id"`
	Device      string    `json:"device"`
	CreatedAt   time.Time `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	IsCurrent   bool      `json:"isCurrent"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// ListSessions returns active refresh tokens for the user, mostly so the
// devices page can render "Mac · last active 3 min ago" rows.
func (s *RefreshStore) ListSessions(ctx context.Context, userID int64, currentToken string) ([]*Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT token, COALESCE(device, ''), created_at, last_used_at, expires_at
		 FROM refresh_tokens
		 WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Session, 0)
	for rows.Next() {
		var (
			tok        string
			device     string
			createdAt  time.Time
			lastUsed   *time.Time
			expiresAt  time.Time
		)
		if err := rows.Scan(&tok, &device, &createdAt, &lastUsed, &expiresAt); err != nil {
			return nil, err
		}
		prefix := tok
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		out = append(out, &Session{
			TokenPrefix: prefix,
			Device:      device,
			CreatedAt:   createdAt,
			LastUsedAt:  lastUsed,
			IsCurrent:   tok == currentToken,
			ExpiresAt:   expiresAt,
		})
	}
	return out, rows.Err()
}

// RevokeAllForUser kills every session for the user. Used by delete-account.
func (s *RefreshStore) RevokeAllForUser(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID)
	return err
}

// RevokeOthers revokes every session for the user except the one matching
// keepToken. Used by "下线其它设备" in settings.
func (s *RefreshStore) RevokeOthers(ctx context.Context, userID int64, keepToken string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL AND token <> $2`,
		userID, keepToken)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RevokeByPrefix is the device-page primitive: revoke any user's session by
// the short id we surfaced. We require the userID so an attacker who guesses
// a prefix can't revoke someone else's session.
func (s *RefreshStore) RevokeByPrefix(ctx context.Context, userID int64, prefix string) (bool, error) {
	if len(prefix) < 8 {
		return false, errors.New("prefix too short")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens
		 SET revoked_at = now()
		 WHERE user_id = $1 AND token LIKE $2 || '%' AND revoked_at IS NULL`,
		userID, prefix)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
