package file

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("file not found")

const (
	StatusPending   int16 = 0
	StatusConfirmed int16 = 1
)

type File struct {
	ID         int64     `json:"id,string"`
	UserID     int64     `json:"userId,string"`
	Name       string    `json:"name"`
	Mime       string    `json:"mime,omitempty"`
	Size       int64     `json:"size"`
	StorageKey string    `json:"-"`
	URL        string    `json:"url"`
	Thumbnail  string    `json:"thumbnail,omitempty"`
	Status     int16     `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Pool exposes the DB pool for handler-level cross-table queries
// (e.g. the conversation files list reads from `messages`).
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

type CreatePendingParams struct {
	UserID     int64
	Name       string
	Mime       string
	StorageKey string
	URL        string
}

func (r *Repo) CreatePending(ctx context.Context, p CreatePendingParams) (*File, error) {
	f := &File{}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO files (user_id, name, mime_type, storage_key, url, status)
		 VALUES ($1, $2, $3, $4, $5, 0)
		 RETURNING id, user_id, name, COALESCE(mime_type,''), size_bytes,
		           storage_key, url, COALESCE(thumbnail,''), status, created_at`,
		p.UserID, p.Name, p.Mime, p.StorageKey, p.URL,
	).Scan(&f.ID, &f.UserID, &f.Name, &f.Mime, &f.Size,
		&f.StorageKey, &f.URL, &f.Thumbnail, &f.Status, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (r *Repo) Confirm(ctx context.Context, id, userID int64, size int64, mime string) (*File, error) {
	f := &File{}
	err := r.pool.QueryRow(ctx,
		`UPDATE files SET size_bytes = $3, mime_type = COALESCE(NULLIF($4, ''), mime_type), status = 1
		 WHERE id = $1 AND user_id = $2
		 RETURNING id, user_id, name, COALESCE(mime_type,''), size_bytes,
		           storage_key, url, COALESCE(thumbnail,''), status, created_at`,
		id, userID, size, mime,
	).Scan(&f.ID, &f.UserID, &f.Name, &f.Mime, &f.Size,
		&f.StorageKey, &f.URL, &f.Thumbnail, &f.Status, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*File, error) {
	f := &File{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, name, COALESCE(mime_type,''), size_bytes,
		        storage_key, url, COALESCE(thumbnail,''), status, created_at
		 FROM files WHERE id = $1`, id,
	).Scan(&f.ID, &f.UserID, &f.Name, &f.Mime, &f.Size,
		&f.StorageKey, &f.URL, &f.Thumbnail, &f.Status, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}
