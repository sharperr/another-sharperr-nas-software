package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenMigratesLegacyUsersAndPreservesSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin')),
			created_at INTEGER NOT NULL
		);
		CREATE TABLE sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX sessions_user_id ON sessions(user_id);
		CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			remote_addr TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		INSERT INTO users VALUES (1, 'admin', X'01', 'admin', 100);
		INSERT INTO sessions VALUES ('token-hash', 1, 'csrf', 9999999999, 100);
		INSERT INTO audit_logs VALUES (1, 1, 'auth.login.succeeded', '127.0.0.1', 100);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	users, err := store.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "admin" || users[0].Role != "admin" || users[0].QuotaBytes != 0 {
		t.Fatalf("unexpected migrated users: %+v", users)
	}
	var sessionCount, auditCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 || auditCount != 1 {
		t.Fatalf("migration lost dependent rows: sessions=%d audit=%d", sessionCount, auditCount)
	}
	if _, err := store.CreateUser(context.Background(), "media", "a-unique-password-longer-than-12", 1024); err != nil {
		t.Fatalf("create user after migration: %v", err)
	}
}

func TestCreateUserAndUpdateQuota(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "media_user", "a-unique-password-longer-than-12", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != "user" || user.QuotaBytes != 4096 {
		t.Fatalf("unexpected user record: %+v", user)
	}
	if _, err := store.CreateUser(ctx, "media_user", "a-unique-password-longer-than-12", 0); !errors.Is(err, ErrUserExists) {
		t.Fatalf("expected duplicate username error, got %v", err)
	}
	if err := store.SetUserQuota(ctx, user.ID, 8192); err != nil {
		t.Fatal(err)
	}
	updated, err := store.UserByUsername(ctx, user.Username)
	if err != nil {
		t.Fatal(err)
	}
	if updated.QuotaBytes != 8192 {
		t.Fatalf("expected updated quota 8192, got %d", updated.QuotaBytes)
	}
}
