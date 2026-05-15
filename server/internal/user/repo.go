package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("user not found")

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

type CreateParams struct {
	Username     string
	Email        string
	PasswordHash string
	Nickname     string
}

func (r *Repo) Create(ctx context.Context, p CreateParams) (*User, error) {
	const q = `
		INSERT INTO users (username, email, password_hash, nickname)
		VALUES ($1, $2, $3, $4)
		RETURNING id, username, email, nickname, COALESCE(avatar_url, ''), COALESCE(bio, ''),
		          status, email_verified, is_admin, created_at`
	row := r.pool.QueryRow(ctx, q, p.Username, p.Email, p.PasswordHash, p.Nickname)
	u := &User{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Nickname, &u.AvatarURL, &u.Bio,
		&u.Status, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt); err != nil {
		return nil, err
	}
	return u, nil
}

type Credentials struct {
	ID           int64
	PasswordHash string
	Status       int16
}

func (r *Repo) FindCredentialsByLogin(ctx context.Context, login string) (*Credentials, error) {
	const q = `SELECT id, password_hash, status FROM users
		WHERE (username = $1 OR email = $1) LIMIT 1`
	c := &Credentials{}
	err := r.pool.QueryRow(ctx, q, login).Scan(&c.ID, &c.PasswordHash, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*User, error) {
	const q = `SELECT id, username, email, nickname, COALESCE(avatar_url, ''), COALESCE(bio, ''),
		status, email_verified, is_admin, created_at FROM users WHERE id = $1`
	u := &User{}
	err := r.pool.QueryRow(ctx, q, id).Scan(&u.ID, &u.Username, &u.Email, &u.Nickname,
		&u.AvatarURL, &u.Bio, &u.Status, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *Repo) FindCredentialsByID(ctx context.Context, id int64) (*Credentials, error) {
	c := &Credentials{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, password_hash, status FROM users WHERE id = $1`, id,
	).Scan(&c.ID, &c.PasswordHash, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (r *Repo) UpdatePasswordHash(ctx context.Context, id int64, hash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}

// SoftDelete marks a user as deleted (status=2), scrubs identifying fields,
// and rewrites the username/email so they can be reused. We keep the row so
// foreign-key history (messages, friendships) stays valid.
func (r *Repo) SoftDelete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users
		   SET status        = 2,
		       username      = 'deleted_' || id::text,
		       email         = 'deleted_' || id::text || '@deleted.invalid',
		       nickname      = '已注销用户',
		       password_hash = '',
		       avatar_url    = NULL,
		       bio           = NULL,
		       updated_at    = now()
		 WHERE id = $1`, id)
	return err
}

func (r *Repo) UpdateLastLogin(ctx context.Context, id int64, ip string) error {
	const q = `UPDATE users SET last_login_at = $1, last_login_ip = $2 WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, time.Now(), ip, id)
	return err
}

// UpdateProfileParams allows partial updates: nil pointers leave the column
// untouched.
type UpdateProfileParams struct {
	Nickname  *string
	Bio       *string
	AvatarURL *string
}

func (r *Repo) UpdateProfile(ctx context.Context, id int64, p UpdateProfileParams) (*User, error) {
	// Build dynamic SET clause to avoid a giant CASE statement.
	sets := []string{}
	args := []any{}
	idx := 1
	if p.Nickname != nil {
		sets = append(sets, "nickname = $"+itoa(idx))
		args = append(args, *p.Nickname)
		idx++
	}
	if p.Bio != nil {
		sets = append(sets, "bio = $"+itoa(idx))
		args = append(args, *p.Bio)
		idx++
	}
	if p.AvatarURL != nil {
		sets = append(sets, "avatar_url = $"+itoa(idx))
		args = append(args, *p.AvatarURL)
		idx++
	}
	if len(sets) == 0 {
		return r.FindByID(ctx, id)
	}
	args = append(args, id)
	q := "UPDATE users SET " + joinComma(sets) + ", updated_at = now() WHERE id = $" + itoa(idx) + `
		RETURNING id, username, email, nickname, COALESCE(avatar_url, ''), COALESCE(bio, ''),
		          status, email_verified, is_admin, created_at`
	u := &User{}
	err := r.pool.QueryRow(ctx, q, args...).Scan(&u.ID, &u.Username, &u.Email, &u.Nickname,
		&u.AvatarURL, &u.Bio, &u.Status, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func itoa(i int) string {
	// strconv.Itoa without the import noise.
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	b := [16]byte{}
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}

func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	n := 0
	for _, s := range ss {
		n += len(s) + 2
	}
	b := make([]byte, 0, n)
	for i, s := range ss {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, s...)
	}
	return string(b)
}
