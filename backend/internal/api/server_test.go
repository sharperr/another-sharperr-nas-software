package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

	identity, cookie := testLogin(t, handler, "admin", "a-unique-password-longer-than-12")
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookie)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF rejection, got %d", logoutResponse.Code)
	}

	logout = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookie)
	logout.Header.Set("X-CSRF-Token", identity.CSRF)
	logoutResponse = httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("expected successful logout, got %d", logoutResponse.Code)
	}
	if logoutResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected secure HTTP headers")
	}
}

func TestSystemSettingsRequireAdministratorAndAgent(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin(context.Background(), "admin", "a-unique-password-longer-than-12"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(context.Background(), "media", "another-unique-password-longer-than-12", 0); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store, auth.NewService(store), monitoring.New(t.TempDir(), t.TempDir()), false, "")
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("system settings should require authentication; got %d", unauthenticated.Code)
	}
	_, userCookie := testLogin(t, handler, "media", "another-unique-password-longer-than-12")
	userRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	userRequest.AddCookie(userCookie)
	userForbidden := httptest.NewRecorder()
	handler.ServeHTTP(userForbidden, userRequest)
	if userForbidden.Code != http.StatusForbidden {
		t.Fatalf("system settings should be administrator-only; got %d", userForbidden.Code)
	}
	identity, cookie := testLogin(t, handler, "admin", "a-unique-password-longer-than-12")
	request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", strings.NewReader(`{"hostname":"nas"}`))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", identity.CSRF)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("system settings should report missing Agent, got %d: %s", response.Code, response.Body.String())
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

func TestAdminUserManagementAndEnforcedStorageQuota(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(filepath.Join(t.TempDir(), "asns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin(ctx, "admin", "a-unique-password-longer-than-12"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store, auth.NewService(store), monitoring.New(t.TempDir(), t.TempDir()), false, "", filepath.Join(t.TempDir(), "users"))

	adminIdentity, adminCookie := testLogin(t, handler, "admin", "a-unique-password-longer-than-12")
	create := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(
		`{"username":"media","password":"another-unique-password-longer-than-12","quotaBytes":4}`,
	))
	create.AddCookie(adminCookie)
	create.Header.Set("X-CSRF-Token", adminIdentity.CSRF)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create user failed: %d %s", createResponse.Code, createResponse.Body.String())
	}
	var createdUser struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdUser); err != nil {
		t.Fatal(err)
	}

	userIdentity, userCookie := testLogin(t, handler, "media", "another-unique-password-longer-than-12")
	forbidden := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	request.AddCookie(userCookie)
	handler.ServeHTTP(forbidden, request)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("standard user must not list accounts, got %d", forbidden.Code)
	}

	upload := func(name, contents string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("directory", ""); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("X-CSRF-Token", userIdentity.CSRF)
		request.AddCookie(userCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := upload("file.txt", "four"); response.Code != http.StatusNoContent {
		t.Fatalf("upload within quota failed: %d %s", response.Code, response.Body.String())
	}
	if response := upload("extra.txt", "x"); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over quota was not rejected: %d %s", response.Code, response.Body.String())
	}
	updateQuota := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/users/%d/quota", createdUser.ID), strings.NewReader(`{"quotaBytes":8}`))
	updateQuota.AddCookie(adminCookie)
	updateQuota.Header.Set("X-CSRF-Token", adminIdentity.CSRF)
	updateQuotaResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateQuotaResponse, updateQuota)
	if updateQuotaResponse.Code != http.StatusOK {
		t.Fatalf("updating user quota failed: %d %s", updateQuotaResponse.Code, updateQuotaResponse.Body.String())
	}
	if response := upload("extra.txt", "more"); response.Code != http.StatusNoContent {
		t.Fatalf("upload after quota increase failed: %d %s", response.Code, response.Body.String())
	}

	adminUsers := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	request.AddCookie(adminCookie)
	handler.ServeHTTP(adminUsers, request)
	if adminUsers.Code != http.StatusOK {
		t.Fatalf("admin user listing failed: %d %s", adminUsers.Code, adminUsers.Body.String())
	}
	var response struct {
		Users []struct {
			Username         string `json:"username"`
			QuotaBytes       int64  `json:"quotaBytes"`
			StorageUsedBytes int64  `json:"storageUsedBytes"`
		} `json:"users"`
	}
	if err := json.Unmarshal(adminUsers.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	foundUser := false
	for _, user := range response.Users {
		if user.Username == "media" {
			foundUser = true
			if user.QuotaBytes != 8 || user.StorageUsedBytes != 8 {
				t.Fatalf("unexpected user quota/usage: %+v", user)
			}
		}
	}
	if !foundUser {
		t.Fatal("created user missing from administrator listing")
	}
}

func testLogin(t *testing.T, handler http.Handler, username, password string) (auth.Identity, *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)))
	request.RemoteAddr = "127.0.0.1:2345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login %q failed: %d %s", username, response.Code, response.Body.String())
	}
	var identity auth.Identity
	if err := json.Unmarshal(response.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login %q returned %d cookies", username, len(cookies))
	}
	return identity, cookies[0]
}
