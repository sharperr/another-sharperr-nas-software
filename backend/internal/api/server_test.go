package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sharperr/asns/backend/internal/auth"
	"github.com/sharperr/asns/backend/internal/database"
	"github.com/sharperr/asns/backend/internal/monitoring"
)

func TestAuthenticationAndCSRF(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer store.Close()
	if err := store.EnsureAdmin(context.Background(), "admin", "a-unique-password-longer-than-12"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store, auth.NewService(store), monitoring.New(t.TempDir(), t.TempDir()), false, "")

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", unauthenticated.Code)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"a-unique-password-longer-than-12"}`))
	login.RemoteAddr = "127.0.0.1:1234"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("expected successful login, got %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
	var identity auth.Identity
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie is missing required protections: %+v", cookies)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookies[0])
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF rejection, got %d", logoutResponse.Code)
	}

	logout = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookies[0])
	logout.Header.Set("X-CSRF-Token", identity.CSRF)
	logoutResponse = httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("expected successful logout, got %d", logoutResponse.Code)
	}
	if loginResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected secure HTTP headers")
	}
}

func TestFrontendFallbackAndAPI404(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	webDir := t.TempDir()
	index := []byte("<!doctype html><title>ASNS</title>")
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), index, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(webDir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "app.js"), []byte("console.log('asns')"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store, auth.NewService(store), monitoring.New(t.TempDir(), t.TempDir()), false, webDir)

	for _, path := range []string{"/", "/storage"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.String() != string(index) {
			t.Fatalf("SPA fallback failed for %s: %d %q", path, response.Code, response.Body.String())
		}
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log('asns')" {
		t.Fatalf("static asset serving failed: %d %q", asset.Code, asset.Body.String())
	}
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown API path should return 404, got %d", notFound.Code)
	}
}
