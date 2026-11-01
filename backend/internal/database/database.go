package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	QuotaBytes   int64     `json:"quotaBytes"`
	StorageUsed  int64     `json:"storageUsedBytes"`
	CreatedAt    time.Time `json:"createdAt"`
	PasswordHash []byte    `json:"-"`
}

type Session struct {
	UserID    int64
	Username  string
	Role      string
	CSRFToken string
	ExpiresAt int64
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

var ErrUserExists = errors.New("username already exists")

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
			role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
			created_at INTEGER NOT NULL,
			quota_bytes INTEGER NOT NULL DEFAULT 0 CHECK (quota_bytes >= 0)
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
	if err := migrateUsers(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure SQLite database file: %w", err)
	}
	return store, nil
}

func migrateUsers(db *sql.DB) (migrationErr error) {
	var tableSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&tableSQL); err != nil {
		return fmt.Errorf("inspect users schema: %w", err)
	}
	rows, err := db.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return fmt.Errorf("inspect users columns: %w", err)
	}
	hasQuota := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read users columns: %w", err)
		}
		if name == "quota_bytes" {
			hasQuota = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close users schema inspection: %w", err)
	}
	if strings.Contains(tableSQL, "'user'") && hasQuota {
		return nil
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for user migration: %w", err)
	}
	defer func() {
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil && migrationErr == nil {
			migrationErr = fmt.Errorf("enable foreign keys after user migration: %w", err)
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin users schema migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		CREATE TABLE users_new (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
			created_at INTEGER NOT NULL,
			quota_bytes INTEGER NOT NULL DEFAULT 0 CHECK (quota_bytes >= 0)
		);
	`); err != nil {
		return fmt.Errorf("create migrated users table: %w", err)
	}
	quotaExpression := "0"
	if hasQuota {
		quotaExpression = "quota_bytes"
	}
	if _, err := tx.Exec(`INSERT INTO users_new (id, username, password_hash, role, created_at, quota_bytes)
		SELECT id, username, password_hash, role, created_at, ` + quotaExpression + ` FROM users`); err != nil {
		return fmt.Errorf("copy users into migrated table: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE users`); err != nil {
		return fmt.Errorf("replace old users table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE users_new RENAME TO users`); err != nil {
		return fmt.Errorf("finish users schema migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit users schema migration: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureAdmin(ctx context.Context, username, password string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count); err != nil {
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
	if !usernamePattern.MatchString(username) {
		return errors.New("administrator username must contain only letters, numbers, dots, underscores, and hyphens")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash administrator password: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at, quota_bytes) VALUES (?, ?, 'admin', ?, 0)`,
		username, hash, time.Now().Unix()); err != nil {
		return fmt.Errorf("create initial administrator: %w", err)
	}
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT id, username, role, password_hash, quota_bytes, created_at FROM users WHERE username = ?`, username).
		Scan(&user.ID, &user.Username, &user.Role, &user.PasswordHash, &user.QuotaBytes, &createdAt)
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	return user, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, role, quota_bytes, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		var createdAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &user.QuotaBytes, &createdAt); err != nil {
			return nil, fmt.Errorf("read user: %w", err)
		}
		user.CreatedAt = time.Unix(createdAt, 0).UTC()
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *Store) CreateUser(ctx context.Context, username, password string, quotaBytes int64) (User, error) {
	if !usernamePattern.MatchString(username) {
		return User{}, errors.New("username must be 1-64 letters, numbers, dots, underscores, or hyphens")
	}
	if len(password) < 12 || len(password) > 72 {
		return User{}, errors.New("password must be 12-72 bytes")
	}
	if quotaBytes < 0 {
		return User{}, errors.New("storage quota cannot be negative")
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE username = ?`, username).Scan(&exists)
	if err == nil {
		return User{}, ErrUserExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("check username: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash user password: %w", err)
	}
	createdAt := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at, quota_bytes) VALUES (?, ?, 'user', ?, ?)`,
		username, hash, createdAt.Unix(), quotaBytes)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("get created user ID: %w", err)
	}
	return User{ID: id, Username: username, Role: "user", QuotaBytes: quotaBytes, CreatedAt: createdAt}, nil
}

func (s *Store) SetUserQuota(ctx context.Context, userID, quotaBytes int64) error {
	if quotaBytes < 0 {
		return errors.New("storage quota cannot be negative")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET quota_bytes = ? WHERE id = ? AND role = 'user'`, quotaBytes, userID)
	if err != nil {
		return fmt.Errorf("update user storage quota: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated user: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND role = 'user'`, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted user: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
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
