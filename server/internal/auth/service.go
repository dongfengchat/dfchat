package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/dongfang/dfchat/server/internal/user"
	pkgauth "github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrEmailTaken         = errors.New("email already taken")
	ErrAccountDisabled    = errors.New("account disabled")
)

type Service struct {
	users   *user.Repo
	issuer  *pkgauth.Issuer
	refresh *RefreshStore
}

func NewService(users *user.Repo, issuer *pkgauth.Issuer, refresh *RefreshStore) *Service {
	return &Service{users: users, issuer: issuer, refresh: refresh}
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
	Nickname string
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (*user.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return nil, err
	}
	nickname := in.Nickname
	if nickname == "" {
		nickname = in.Username
	}
	u, err := s.users.Create(ctx, user.CreateParams{
		Username:     in.Username,
		Email:        strings.ToLower(in.Email),
		PasswordHash: string(hash),
		Nickname:     nickname,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "username") {
				return nil, ErrUsernameTaken
			}
			if strings.Contains(pgErr.ConstraintName, "email") {
				return nil, ErrEmailTaken
			}
		}
		return nil, err
	}
	return u, nil
}

type LoginResult struct {
	User         *user.User
	AccessToken  string
	RefreshToken string
}

func (s *Service) Login(ctx context.Context, login, password, ip, device string) (*LoginResult, error) {
	creds, err := s.users.FindCredentialsByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if creds.Status != 0 {
		return nil, ErrAccountDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	u, err := s.users.FindByID(ctx, creds.ID)
	if err != nil {
		return nil, err
	}
	token, _, err := s.issuer.IssueAccess(u.ID)
	if err != nil {
		return nil, err
	}
	refresh, _, err := s.refresh.Issue(ctx, u.ID, device)
	if err != nil {
		return nil, err
	}
	_ = s.users.UpdateLastLogin(ctx, u.ID, ip)
	return &LoginResult{User: u, AccessToken: token, RefreshToken: refresh}, nil
}

var ErrPasswordMismatch = errors.New("password mismatch")
var ErrPasswordWeak = errors.New("password too weak")

// ChangePassword verifies the current password then sets a new bcrypt hash.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	if len(next) < 8 {
		return ErrPasswordWeak
	}
	creds, err := s.users.FindCredentialsByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash), []byte(current)); err != nil {
		return ErrPasswordMismatch
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), 12)
	if err != nil {
		return err
	}
	return s.users.UpdatePasswordHash(ctx, userID, string(hash))
}

// Refresh swaps a refresh token for a new (access, refresh) pair.
func (s *Service) Refresh(ctx context.Context, refreshToken, device string) (*LoginResult, error) {
	userID, newRefresh, _, err := s.refresh.Swap(ctx, refreshToken, device)
	if err != nil {
		return nil, err
	}
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	access, _, err := s.issuer.IssueAccess(userID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{User: u, AccessToken: access, RefreshToken: newRefresh}, nil
}
