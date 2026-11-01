package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sharperr/asns/backend/internal/agent"
	"github.com/sharperr/asns/backend/internal/auth"
	"github.com/sharperr/asns/backend/internal/backups"
	"github.com/sharperr/asns/backend/internal/database"
	"github.com/sharperr/asns/backend/internal/files"
	"github.com/sharperr/asns/backend/internal/monitoring"
)

const sessionCookie = "asns_session"

type auditStore interface {
	RecordAudit(context.Context, *int64, string, string) error
}

type Handler struct {
	auth          *auth.Service
	store         *database.Store
	audit         auditStore
	monitor       *monitoring.System
	storage       *files.Manager
	backups       *backups.Manager
	shareAgent    *agent.Client
	secureCookie  bool
	webDir        string
	loginMu       sync.Mutex
	loginAttempts map[string]loginWindow
}

type loginWindow struct {
	start time.Time
	count int
}

func NewServer(store *database.Store, authService *auth.Service, monitor *monitoring.System, secureCookie bool, webDir string, storageRoots ...string) http.Handler {
	storageRoot := "/var/lib/asns/users"
	if len(storageRoots) > 0 && storageRoots[0] != "" {
		storageRoot = storageRoots[0]
	}
	backupRoot := filepath.Join(filepath.Dir(storageRoot), "backups")
	if len(storageRoots) > 1 && storageRoots[1] != "" {
		backupRoot = storageRoots[1]
	}
	var shareAgent *agent.Client
	if len(storageRoots) > 2 && storageRoots[2] != "" {
		shareAgent = agent.NewClient(storageRoots[2])
	}
	h := &Handler{
		auth: authService, store: store, audit: store, monitor: monitor, secureCookie: secureCookie,
		webDir: webDir, storage: files.NewManager(storageRoot),
		backups:       backups.NewManager(store, store.DatabasePath(), storageRoot, backupRoot),
		shareAgent:    shareAgent,
		loginAttempts: make(map[string]loginWindow),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.Handle("POST /api/v1/auth/logout", h.requireAuth(http.HandlerFunc(h.logout)))
	mux.Handle("GET /api/v1/auth/me", h.requireAuth(http.HandlerFunc(h.me)))
	mux.Handle("GET /api/v1/system", h.requireAuth(http.HandlerFunc(h.system)))
	mux.Handle("GET /api/v1/disks", h.requireAuth(http.HandlerFunc(h.disks)))
	mux.Handle("GET /api/v1/disks/smart", h.requireAuth(http.HandlerFunc(h.diskHealth)))
	mux.Handle("GET /api/v1/raid", h.requireAuth(http.HandlerFunc(h.raidStatus)))
	mux.Handle("GET /api/v1/system/settings", h.requireAdmin(http.HandlerFunc(h.systemSettings)))
	mux.Handle("PUT /api/v1/system/settings", h.requireAdmin(http.HandlerFunc(h.updateSystemSettings)))
	mux.Handle("GET /api/v1/services", h.requireAdmin(http.HandlerFunc(h.servicesStatus)))
	mux.Handle("POST /api/v1/services/{name}/restart", h.requireAdmin(http.HandlerFunc(h.restartService)))
	mux.Handle("GET /api/v1/monitoring/history", h.requireAuth(http.HandlerFunc(h.monitoringHistory)))
	mux.Handle("GET /api/v1/backups", h.requireAdmin(http.HandlerFunc(h.listBackups)))
	mux.Handle("DELETE /api/v1/backups/{name}", h.requireAdmin(http.HandlerFunc(h.deleteBackup)))
	mux.Handle("PUT /api/v1/backups/settings", h.requireAdmin(http.HandlerFunc(h.setBackupSettings)))
	mux.Handle("POST /api/v1/backups/run", h.requireAdmin(http.HandlerFunc(h.runBackup)))
	mux.Handle("POST /api/v1/backups/restore/stage", h.requireAdmin(http.HandlerFunc(h.stageBackupRestore)))
	mux.Handle("POST /api/v1/backups/restore/apply", h.requireAdmin(http.HandlerFunc(h.applyBackupRestore)))
	mux.Handle("GET /api/v1/backups/restore/{id}", h.requireAdmin(http.HandlerFunc(h.backupRestoreStatus)))
	mux.Handle("GET /api/v1/shares", h.requireAdmin(http.HandlerFunc(h.listShares)))
	mux.Handle("POST /api/v1/shares", h.requireAdmin(http.HandlerFunc(h.createShare)))
	mux.Handle("PUT /api/v1/shares/{id}", h.requireAdmin(http.HandlerFunc(h.updateShare)))
	mux.Handle("DELETE /api/v1/shares/{id}", h.requireAdmin(http.HandlerFunc(h.deleteShare)))
	mux.Handle("POST /api/v1/smb-users", h.requireAdmin(http.HandlerFunc(h.createSMBUser)))
	mux.Handle("PUT /api/v1/smb-users/{username}/password", h.requireAdmin(http.HandlerFunc(h.updateSMBPassword)))
	mux.Handle("DELETE /api/v1/smb-users/{username}", h.requireAdmin(http.HandlerFunc(h.deleteSMBUser)))
	mux.Handle("GET /api/v1/users", h.requireAdmin(http.HandlerFunc(h.listUsers)))
	mux.Handle("POST /api/v1/users", h.requireAdmin(http.HandlerFunc(h.createUser)))
	mux.Handle("PUT /api/v1/users/{id}/quota", h.requireAdmin(http.HandlerFunc(h.updateUserQuota)))
	mux.Handle("GET /api/v1/files", h.requireAuth(http.HandlerFunc(h.listFiles)))
	mux.Handle("GET /api/v1/sync/manifest", h.requireAuth(http.HandlerFunc(h.syncManifest)))
	mux.Handle("PUT /api/v1/sync/file", h.requireAuth(http.HandlerFunc(h.replaceSyncFile)))
	mux.Handle("POST /api/v1/files/upload", h.requireAuth(http.HandlerFunc(h.uploadFile)))
	mux.Handle("POST /api/v1/files/directory", h.requireAuth(http.HandlerFunc(h.createDirectory)))
	mux.Handle("GET /api/v1/files/download", h.requireAuth(http.HandlerFunc(h.downloadFile)))
	mux.Handle("DELETE /api/v1/files", h.requireAuth(http.HandlerFunc(h.removeFile)))
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
		next.ServeHTTP(secureHeaderWriter{ResponseWriter: w}, r)
	})
}

type secureHeaderWriter struct {
	http.ResponseWriter
}

func (w secureHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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

func (h *Handler) diskHealth(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	disks, err := h.shareAgent.DiskHealth(r.Context())
	if err != nil {
		log.Printf("ASNS SMART discovery unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SMART health is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

func (h *Handler) raidStatus(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	arrays, err := h.shareAgent.RAIDArrays(r.Context())
	if err != nil {
		log.Printf("ASNS RAID status unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "RAID status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"arrays": arrays})
}

func (h *Handler) systemSettings(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	settings, err := h.shareAgent.SystemSettings(r.Context())
	if err != nil {
		log.Printf("ASNS system settings unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "system settings are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Hostname *string `json:"hostname"`
		Timezone *string `json:"timezone"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := h.shareAgent.UpdateSystemSettings(r.Context(), request.Hostname, request.Timezone)
	if err != nil {
		log.Printf("ASNS system settings update failed: %v", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) servicesStatus(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	services, err := h.shareAgent.Services(r.Context())
	if err != nil {
		log.Printf("ASNS service status unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "service status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

func (h *Handler) restartService(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	name := r.PathValue("name")
	if name != "asns.service" && name != "smbd.service" {
		writeError(w, http.StatusBadRequest, "service is not managed by ASNS")
		return
	}
	if err := h.shareAgent.RestartService(r.Context(), name); err != nil {
		log.Printf("ASNS service restart failed for %s: %v", name, err)
		writeError(w, http.StatusServiceUnavailable, "service could not be restarted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) monitoringHistory(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if value := r.URL.Query().Get("hours"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 168 {
			writeError(w, http.StatusBadRequest, "hours must be between 1 and 168")
			return
		}
		hours = parsed
	}
	samples, err := h.store.MetricHistory(r.Context(), time.Now().Add(-time.Duration(hours)*time.Hour), 100000)
	if err != nil {
		log.Printf("ASNS monitoring history unavailable: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to load monitoring history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hours": hours, "samples": samples})
}

func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.BackupSettings(r.Context())
	if err != nil {
		log.Printf("ASNS backup settings unavailable: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to load backup settings")
		return
	}
	archives, err := h.backups.List(settings.Destination)
	if err != nil {
		log.Printf("ASNS backup archive listing failed: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to list backup archives")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "archives": archives})
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.BackupSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load backup settings")
		return
	}
	if err := h.backups.Delete(settings.Destination, r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "backups.deleted")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setBackupSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request database.BackupSettings
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.backups.EnsureDestination(request.Destination); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SetBackupSettings(r.Context(), request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "backups.settings_updated")
	settings, err := h.store.BackupSettings(r.Context())
	if err != nil {
		log.Printf("ASNS backup settings readback failed: %v", err)
		writeError(w, http.StatusInternalServerError, "backup settings were saved but could not be reloaded")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) runBackup(w http.ResponseWriter, r *http.Request) {
	identity, _ := identityFromContext(r.Context())
	claimed, err := h.store.ClaimManualBackup(r.Context(), time.Now())
	if err != nil {
		log.Printf("ASNS backup request status update failed: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to start backup")
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "a backup is already running")
		return
	}
	h.recordAudit(r, &identity.ID, "backups.started")
	go h.executeBackup()
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) executeBackup() {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	settings, err := h.store.BackupSettings(ctx)
	if err == nil {
		_, err = h.backups.Create(ctx, settings.Destination)
	}
	status := "succeeded"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
		log.Printf("ASNS backup failed: %v", err)
	}
	if saveErr := h.store.CompleteBackup(context.Background(), status, message); saveErr != nil {
		log.Printf("ASNS backup status persistence failed: %v", saveErr)
	}
}

func (h *Handler) stageBackupRestore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stage, err := h.backups.StageRestore(request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "backups.restore_staged")
	writeJSON(w, http.StatusOK, stage)
}

func (h *Handler) applyBackupRestore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		ID           string `json:"id"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Confirmation != "RESTORE" {
		writeError(w, http.StatusBadRequest, "type RESTORE to confirm applying this backup")
		return
	}
	if err := h.backups.ApplyStage(request.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured; restore cannot safely restart the service")
		return
	}
	job, err := h.shareAgent.ApplyRestore(r.Context(), request.ID)
	if err != nil {
		log.Printf("ASNS restore request failed: %v", err)
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent could not start the restore")
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "backups.restore_started")
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) backupRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	job, err := h.shareAgent.RestoreStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "restore job is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) listShares(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	shares, err := h.shareAgent.Shares(r.Context())
	if err != nil {
		log.Printf("ASNS SMB share listing failed: %v", err)
		writeError(w, http.StatusServiceUnavailable, "Samba management is unavailable")
		return
	}
	users, err := h.shareAgent.SMBUsers(r.Context())
	if err != nil {
		log.Printf("ASNS SMB account listing failed: %v", err)
		writeError(w, http.StatusServiceUnavailable, "Samba account management is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares, "users": users})
}

func (h *Handler) createShare(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request struct {
		Name        string             `json:"name"`
		Permissions []agent.Permission `json:"permissions"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	share, err := h.shareAgent.CreateShare(r.Context(), request.Name, request.Permissions)
	if err != nil {
		log.Printf("ASNS SMB share creation failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.share_created")
	writeJSON(w, http.StatusCreated, share)
}

func (h *Handler) updateShare(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request struct {
		Name        string             `json:"name"`
		Permissions []agent.Permission `json:"permissions"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	share, err := h.shareAgent.UpdateShare(r.Context(), r.PathValue("id"), request.Name, request.Permissions)
	if err != nil {
		log.Printf("ASNS SMB share update failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.share_updated")
	writeJSON(w, http.StatusOK, share)
}

func (h *Handler) deleteShare(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	if err := h.shareAgent.DeleteShare(r.Context(), r.PathValue("id")); err != nil {
		log.Printf("ASNS SMB share removal failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.share_removed")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createSMBUser(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := h.shareAgent.CreateSMBUser(r.Context(), request.Username, request.Password)
	if err != nil {
		log.Printf("ASNS SMB account creation failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.smb_user_created")
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) updateSMBPassword(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.shareAgent.UpdateSMBPassword(r.Context(), r.PathValue("username"), request.Password); err != nil {
		log.Printf("ASNS SMB password update failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.smb_password_updated")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteSMBUser(w http.ResponseWriter, r *http.Request) {
	if h.shareAgent == nil {
		writeError(w, http.StatusServiceUnavailable, "ASNS Agent is not configured")
		return
	}
	if err := h.shareAgent.DeleteSMBUser(r.Context(), r.PathValue("username")); err != nil {
		log.Printf("ASNS SMB account removal failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "sharing.smb_user_removed")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return h.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok || identity.Role != "admin" {
			writeError(w, http.StatusForbidden, "administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("ASNS user listing failed: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to list users")
		return
	}
	result := make([]database.User, 0, len(users))
	for _, user := range users {
		used, err := h.storage.Usage(user.ID)
		if err != nil {
			log.Printf("ASNS user storage usage failed: %v", err)
			writeError(w, http.StatusInternalServerError, "unable to read user storage usage")
			return
		}
		user.StorageUsed = used
		result = append(result, user)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": result})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		QuotaBytes int64  `json:"quotaBytes"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := h.store.CreateUser(r.Context(), request.Username, request.Password, request.QuotaBytes)
	if errors.Is(err, database.ErrUserExists) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, _, err := h.storage.List(user.ID, ""); err != nil {
		if deleteErr := h.store.DeleteUser(r.Context(), user.ID); deleteErr != nil {
			log.Printf("ASNS rollback failed after storage initialization: %v", deleteErr)
		}
		log.Printf("ASNS user storage initialization failed: %v", err)
		writeError(w, http.StatusInternalServerError, "unable to initialize user storage")
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "users.created")
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) updateUserQuota(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var request struct {
		QuotaBytes int64 `json:"quotaBytes"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	err = h.storage.WithLock(func() error {
		return h.store.SetUserQuota(r.Context(), userID, request.QuotaBytes)
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	h.recordAudit(r, &identity.ID, "users.quota_updated")
	writeJSON(w, http.StatusOK, map[string]int64{"userId": userID, "quotaBytes": request.QuotaBytes})
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) {
	identity, _ := identityFromContext(r.Context())
	directory := r.URL.Query().Get("directory")
	entries, used, err := h.storage.List(identity.ID, directory)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	user, err := h.store.UserByUsername(r.Context(), identity.Username)
	if err != nil {
		log.Printf("ASNS storage quota lookup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "storage quota is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "directory": directory, "usedBytes": used, "quotaBytes": user.QuotaBytes,
	})
}

func (h *Handler) syncManifest(w http.ResponseWriter, r *http.Request) {
	identity, _ := identityFromContext(r.Context())
	entries, err := h.storage.Manifest(identity.ID, r.URL.Query().Get("directory"))
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) replaceSyncFile(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, http.StatusInternalServerError, "unable to prepare sync upload")
		return
	}
	if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, http.StatusInternalServerError, "unable to prepare sync upload")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxUploadBytes)
	if r.ContentLength < 0 || r.ContentLength > files.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "sync file size is missing or exceeds the 1 GiB limit")
		return
	}
	identity, _ := identityFromContext(r.Context())
	getQuota := func() (int64, error) {
		user, err := h.store.UserByUsername(r.Context(), identity.Username)
		if err != nil {
			return 0, fmt.Errorf("look up upload quota: %w", err)
		}
		return user.QuotaBytes, nil
	}
	var replaceErr error
	if r.URL.Query().Has("expectedSha256") {
		replaceErr = h.storage.ReplaceIfHash(identity.ID, r.URL.Query().Get("path"),
			r.URL.Query().Get("expectedSha256"), r.ContentLength, getQuota, r.Body)
	} else {
		replaceErr = h.storage.Replace(identity.ID, r.URL.Query().Get("path"), r.ContentLength, getQuota, r.Body)
	}
	if replaceErr != nil {
		writeStorageError(w, replaceErr)
		return
	}
	h.recordAudit(r, &identity.ID, "files.sync_replaced")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload request")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "a file is required")
		return
	}
	defer file.Close()
	name := header.Filename
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	relative := path.Join(r.FormValue("directory"), name)
	identity, _ := identityFromContext(r.Context())
	if header.Size > files.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds the 1 GiB per-upload limit")
		return
	}
	getQuota := func() (int64, error) {
		user, err := h.store.UserByUsername(r.Context(), identity.Username)
		if err != nil {
			return 0, fmt.Errorf("look up upload quota: %w", err)
		}
		return user.QuotaBytes, nil
	}
	if err := h.storage.Upload(identity.ID, relative, header.Size, getQuota, file); err != nil {
		writeStorageError(w, err)
		return
	}
	h.recordAudit(r, &identity.ID, "files.uploaded")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createDirectory(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity, _ := identityFromContext(r.Context())
	if err := h.storage.CreateDirectory(identity.ID, request.Path); err != nil {
		writeStorageError(w, err)
		return
	}
	h.recordAudit(r, &identity.ID, "files.directory_created")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) downloadFile(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, http.StatusInternalServerError, "unable to prepare file download")
		return
	}
	identity, _ := identityFromContext(r.Context())
	file, info, err := h.storage.Open(identity.ID, r.URL.Query().Get("path"))
	if err != nil {
		writeStorageError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h *Handler) removeFile(w http.ResponseWriter, r *http.Request) {
	identity, _ := identityFromContext(r.Context())
	var removeErr error
	if r.URL.Query().Has("expectedSha256") {
		removeErr = h.storage.RemoveIfHash(identity.ID, r.URL.Query().Get("path"), r.URL.Query().Get("expectedSha256"))
	} else {
		removeErr = h.storage.Remove(identity.ID, r.URL.Query().Get("path"))
	}
	if removeErr != nil {
		writeStorageError(w, removeErr)
		return
	}
	h.recordAudit(r, &identity.ID, "files.deleted")
	w.WriteHeader(http.StatusNoContent)
}

func writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrQuotaExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, files.ErrFileChanged):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, files.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "storage entry not found")
	case errors.Is(err, os.ErrExist):
		writeError(w, http.StatusConflict, "storage entry already exists")
	default:
		log.Printf("ASNS file operation failed: %v", err)
		writeError(w, http.StatusInternalServerError, "file operation failed")
	}
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid JSON request")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
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
