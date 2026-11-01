package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sharperr/asns/backend/internal/database"
)

func TestLoginResolveAndLogout(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin(context.Background(), "admin", "a-unique-password-longer-than-12"); err != nil {
		t.Fatal(err)
	}

	service := NewService(store)
	if _, err := service.Login(context.Background(), "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
	result, err := service.Login(context.Background(), "admin", "a-unique-password-longer-than-12")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := service.Resolve(context.Background(), result.Token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Username != "admin" || identity.Role != "admin" || identity.CSRF == "" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if err := service.Logout(context.Background(), result.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(context.Background(), result.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected revoked session, got %v", err)
	}
}
