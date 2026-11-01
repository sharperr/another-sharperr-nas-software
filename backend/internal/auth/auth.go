package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sharperr/asns/backend/internal/database"
)

const SessionDuration = 12 * time.Hour

var ErrInvalidCredentials = errors.New("invalid username or password")

type Store interface {
	UserByUsername(context.Context, string) (database.User, error)
	CreateSession(context.Context, string, int64, string, time.Time) error
	Session(context.Context, string) (database.Session, error)
	DeleteSession(context.Context, string) error
}

type Service struct {
	store Store
}

type Identity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	CSRF     string `json:"csrfToken"`
}

type LoginResult struct {
	Token    string
	Identity Identity
	Expires  time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	user, err := s.store.UserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("look up administrator: %w", err)
	}
	if bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := randomToken(32)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	csrf, err := randomToken(32)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate CSRF token: %w", err)
	}
	expires := time.Now().Add(SessionDuration)
	if err := s.store.CreateSession(ctx, hashToken(token), user.ID, csrf, expires); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Token:    token,
		Identity: Identity{ID: user.ID, Username: user.Username, Role: user.Role, CSRF: csrf},
		Expires:  expires,
	}, nil
}

func (s *Service) Resolve(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrInvalidCredentials
	}
	session, err := s.store.Session(ctx, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrInvalidCredentials
	}
	if err != nil {
		return Identity{}, fmt.Errorf("resolve session: %w", err)
	}
	return Identity{
		ID: session.UserID, Username: session.Username, Role: session.Role, CSRF: session.CSRFToken,
	}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashToken(token))
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
