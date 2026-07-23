package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
)

func main() {
	if err := run(); err != nil {
		slog.Error("memory-projector stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	dependencyTimeout, err := requiredDuration("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return err
	}
	poll, err := requiredDuration("PLATFORM_MEMORY_PROJECTOR_POLL_INTERVAL")
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(context.Background(), dependencyTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("memory-projector started")
	return memory.NewWorker(memory.NewStore(pool), poll).Run(ctx)
}

func requiredDuration(key string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, errors.New(key + " is required")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive duration")
	}
	return parsed, nil
}
