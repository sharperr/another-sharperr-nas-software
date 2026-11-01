package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sharperr/asns/backend/internal/api"
	"github.com/sharperr/asns/backend/internal/auth"
	"github.com/sharperr/asns/backend/internal/backups"
	"github.com/sharperr/asns/backend/internal/database"
	"github.com/sharperr/asns/backend/internal/monitoring"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dbPath := envOr("ASNS_DB_PATH", filepath.Join("data", "asns.db"))
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return err
	}

	store, err := database.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.EnsureAdmin(context.Background(), os.Getenv("ASNS_ADMIN_USERNAME"), os.Getenv("ASNS_ADMIN_PASSWORD")); err != nil {
		return err
	}

	monitor := monitoring.New("/proc", "/sys")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	monitor.Start(ctx, time.Second, func(snapshot monitoring.Snapshot) error {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return store.RecordMetric(writeCtx, database.MetricSample{
			CollectedAt: snapshot.CollectedAt, CPUPercent: snapshot.CPUPercent,
			MemoryTotalBytes: snapshot.Memory.TotalBytes, MemoryUsedBytes: snapshot.Memory.UsedBytes,
			ReceiveBytesPerSecond: snapshot.Network.ReceiveBytesPerSecond,
			SendBytesPerSecond:    snapshot.Network.SendBytesPerSecond, UptimeSeconds: snapshot.UptimeSeconds,
		})
	})
	userStorageRoot := envOr("ASNS_USER_STORAGE_DIR", filepath.Join(filepath.Dir(dbPath), "users"))
	backupRoot := envOr("ASNS_BACKUP_DIR", filepath.Join(filepath.Dir(dbPath), "backups"))
	backups.NewManager(store, dbPath, userStorageRoot, backupRoot).StartScheduler(ctx)

	secureCookie, err := strconv.ParseBool(envOr("ASNS_COOKIE_SECURE", "false"))
	if err != nil {
		return errors.New("ASNS_COOKIE_SECURE must be true or false")
	}

	handler := api.NewServer(store, auth.NewService(store), monitor, secureCookie, os.Getenv("ASNS_WEB_DIR"),
		userStorageRoot, backupRoot, envOr("ASNS_AGENT_SOCKET", "/run/asns-agent/asns-agent.sock"))
	server := &http.Server{
		Addr:              envOr("ASNS_ADDR", "127.0.0.1:8080"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown failed: %v", err)
		}
	}()

	log.Printf("ASNS API listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
