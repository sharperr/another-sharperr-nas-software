package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	PasswordHash []byte `json:"-"`
}

type Session struct {
	UserID    int64
	Username  string
	Role      string
	CSRFToken string
	ExpiresAt int64
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin')),
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS sessions_user_id ON sessions(user_id);
		CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			remote_addr TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite schema: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure SQLite database file: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureAdmin(ctx context.Context, username, password string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("check administrator account: %w", err)
	}
	if count > 0 {
		return nil
	}
	if username == "" || password == "" {
		return errors.New("no administrator exists; set ASNS_ADMIN_USERNAME and ASNS_ADMIN_PASSWORD for first startup")
	}
	if len(username) > 64 || len(password) < 12 || len(password) > 72 {
		return errors.New("initial administrator username must be at most 64 characters and password must be 12-72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash administrator password: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, 'admin', ?)`,
		username, hash, time.Now().Unix()); err != nil {
		return fmt.Errorf("create initial administrator: %w", err)
	}
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `SELECT id, username, role, password_hash FROM users WHERE username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Role, &user.PasswordHash)
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, csrfToken string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("remove expired sessions: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, userID, csrfToken, expiresAt.Unix(), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) Session(ctx context.Context, tokenHash string) (Session, error) {
	var session Session
	err := s.db.QueryRowContext(ctx, `
		SELECT sessions.user_id, users.username, users.role, sessions.csrf_token, sessions.expires_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ?
	`, tokenHash, time.Now().Unix()).Scan(&session.UserID, &session.Username, &session.Role, &session.CSRFToken, &session.ExpiresAt)
	return session, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) RecordAudit(ctx context.Context, userID *int64, action, remoteAddr string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs (user_id, action, remote_addr, created_at) VALUES (?, ?, ?, ?)`,
		userID, action, remoteAddr, time.Now().Unix())
	return err
}
