package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sharperr/asns/backend/internal/auth"
	"github.com/sharperr/asns/backend/internal/database"
	"github.com/sharperr/asns/backend/internal/monitoring"
)

const sessionCookie = "asns_session"

type auditStore interface {
	RecordAudit(context.Context, *int64, string, string) error
}

type Handler struct {
	auth          *auth.Service
	audit         auditStore
	monitor       *monitoring.System
	secureCookie  bool
	webDir        string
	loginMu       sync.Mutex
	loginAttempts map[string]loginWindow
}

type loginWindow struct {
	start time.Time
	count int
}

func NewServer(store *database.Store, authService *auth.Service, monitor *monitoring.System, secureCookie bool, webDir string) http.Handler {
	h := &Handler{
		auth: authService, audit: store, monitor: monitor, secureCookie: secureCookie,
		webDir: webDir, loginAttempts: make(map[string]loginWindow),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.Handle("POST /api/v1/auth/logout", h.requireAuth(http.HandlerFunc(h.logout)))
	mux.Handle("GET /api/v1/auth/me", h.requireAuth(http.HandlerFunc(h.me)))
	mux.Handle("GET /api/v1/system", h.requireAuth(http.HandlerFunc(h.system)))
	mux.Handle("GET /api/v1/disks", h.requireAuth(http.HandlerFunc(h.disks)))
	mux.Handle("/api/", http.NotFoundHandler())
	mux.HandleFunc("/", h.serveUI)
	return secureHeaders(mux)
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": "0.1.0"})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.allowLogin(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Username == "" || request.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return
	}
	result, err := h.auth.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			h.recordAudit(r, nil, "auth.login.failed")
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		log.Printf("ASNS login failed: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to create session")
		return
	}
	h.recordAudit(r, &result.Identity.ID, "auth.login.succeeded")
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: result.Token, Path: "/", Expires: result.Expires,
		MaxAge: int(time.Until(result.Expires).Seconds()), HttpOnly: true, Secure: h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, result.Identity)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookie)
	if cookie != nil {
		identity, _ := identityFromContext(r.Context())
		if err := h.auth.Logout(r.Context(), cookie.Value); err != nil {
			log.Printf("ASNS logout failed: %v", err)
			writeError(w, http.StatusInternalServerError, "unable to end session")
			return
		}
		h.recordAudit(r, &identity.ID, "auth.logout")
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	identity, _ := identityFromContext(r.Context())
	writeJSON(w, http.StatusOK, identity)
}

func (h *Handler) system(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := h.monitor.Snapshot()
	if err != nil {
		log.Printf("ASNS system monitoring unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "system monitoring data is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) disks(w http.ResponseWriter, _ *http.Request) {
	disks, err := h.monitor.Disks()
	if err != nil {
		log.Printf("ASNS disk discovery unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "disk discovery is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

type identityContextKey struct{}

func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		identity, err := h.auth.Resolve(r.Context(), cookie.Value)
		if err != nil {
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				log.Printf("ASNS session lookup failed: %v", err)
				writeError(w, http.StatusInternalServerError, "unable to validate session")
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
				Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
			})
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(identity.CSRF)) != 1 {
				writeError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func identityFromContext(ctx context.Context) (auth.Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(auth.Identity)
	return identity, ok
}

func (h *Handler) allowLogin(ip string) bool {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	now := time.Now()
	for key, window := range h.loginAttempts {
		if now.Sub(window.start) >= time.Minute {
			delete(h.loginAttempts, key)
		}
	}
	if _, exists := h.loginAttempts[ip]; !exists && len(h.loginAttempts) >= 4096 {
		return false
	}
	window := h.loginAttempts[ip]
	if now.Sub(window.start) >= time.Minute {
		window = loginWindow{start: now}
	}
	window.count++
	h.loginAttempts[ip] = window
	return window.count <= 10
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) recordAudit(r *http.Request, userID *int64, action string) {
	if err := h.audit.RecordAudit(r.Context(), userID, action, clientIP(r)); err != nil {
		log.Printf("ASNS audit log write failed: %v", err)
	}
}

func (h *Handler) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.webDir == "" {
		http.Error(w, "ASNS API is running; set ASNS_WEB_DIR to serve the frontend build", http.StatusNotFound)
		return
	}
	indexPath := filepath.Join(h.webDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		http.Error(w, "frontend build is not available", http.StatusNotFound)
		return
	}
	staticFiles := http.FileServer(http.Dir(h.webDir))
	staticPath, err := url.PathUnescape(r.URL.EscapedPath())
	if err == nil {
		staticPath = path.Clean("/" + strings.TrimPrefix(staticPath, "/"))
		file, openErr := http.Dir(h.webDir).Open(staticPath)
		if openErr == nil {
			info, statErr := file.Stat()
			closeErr := file.Close()
			if statErr == nil && closeErr == nil && !info.IsDir() {
				staticFiles.ServeHTTP(w, r)
				return
			}
			if closeErr != nil {
				log.Printf("ASNS static file close failed: %v", closeErr)
				http.Error(w, "unable to serve frontend", http.StatusInternalServerError)
				return
			}
			if statErr != nil {
				log.Printf("ASNS static file stat failed: %v", statErr)
				http.Error(w, "unable to serve frontend", http.StatusInternalServerError)
				return
			}
		} else if !errors.Is(openErr, os.ErrNotExist) {
			log.Printf("ASNS static file open failed: %v", openErr)
			http.Error(w, "unable to serve frontend", http.StatusInternalServerError)
			return
		}
	}
	http.ServeFile(w, r, indexPath)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("ASNS response encoding failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
