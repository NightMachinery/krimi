package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/NightMachinery/krimi/server/internal/krimi"
)

func main() {
	addr := envOrDefault("KRIMI_ADDR", "127.0.0.1:18082")
	dataDir := envOrDefault("KRIMI_DATA_DIR", filepath.Join(".self_host", "data"))
	dbPath := envOrDefault("KRIMI_DB_PATH", filepath.Join(dataDir, "krimi.sqlite"))
	roomTTL := durationEnvOrDefault("KRIMI_ROOM_TTL", 7*24*time.Hour)
	cleanupInterval := durationEnvOrDefault("KRIMI_CLEANUP_INTERVAL", time.Hour)

	store, err := krimi.NewStore(dbPath, roomTTL)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close store: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go krimi.RunCleanupLoop(ctx, store, cleanupInterval)

	server := &http.Server{
		Addr:              addr,
		Handler:           krimi.NewServer(store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("Krimi server listening on %s with db %s", addr, dbPath)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnvOrDefault(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if hours, err := strconv.Atoi(value); err == nil {
		return time.Duration(hours) * time.Hour
	}
	log.Printf("invalid duration %s=%q; using %s", name, value, fallback)
	return fallback
}
