package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sharperr/asns/backend/internal/agent"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	socketPath := envOr("ASNS_AGENT_SOCKET", "/run/asns-agent/asns-agent.sock")
	statePath := envOr("ASNS_AGENT_STATE", "/var/lib/asns-agent/state.json")
	shareRoot := envOr("ASNS_SHARE_ROOT", "/srv/asns/shares")
	databasePath := envOr("ASNS_DB_PATH", "/var/lib/asns/asns.db")
	dataRoot := envOr("ASNS_DATA_DIR", filepath.Dir(databasePath))
	userRoot := envOr("ASNS_USER_STORAGE_DIR", filepath.Join(dataRoot, "users"))
	backupRoot := envOr("ASNS_BACKUP_DIR", filepath.Join(dataRoot, "backups"))
	service := agent.NewService(statePath, shareRoot, "/etc/samba/smb.conf", "/etc/samba/asns-shares.conf",
		databasePath, userRoot, dataRoot, backupRoot)
	if len(os.Args) == 2 && os.Args[1] == "--remove-samba-integration" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return service.RemoveSambaIntegration(ctx)
	}
	if len(os.Args) != 1 {
		return errors.New("unsupported ASNS Agent command")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0750); err != nil {
		return fmt.Errorf("create agent socket directory: %w", err)
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace non-socket at ASNS Agent socket path")
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("remove stale ASNS Agent socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect ASNS Agent socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on ASNS Agent socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()
	if err := os.Chmod(socketPath, 0660); err != nil {
		return fmt.Errorf("secure ASNS Agent socket: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Initialize(ctx); err != nil {
		log.Printf("ASNS Agent Samba initialization failed: %v", err)
	}
	server := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("ASNS Agent shutdown failed: %v", err)
		}
	}()
	log.Printf("ASNS Agent listening on restricted Unix socket %s", socketPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve ASNS Agent: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
