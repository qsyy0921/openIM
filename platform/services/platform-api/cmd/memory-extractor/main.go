package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
)

func main() {
	if err := run(); err != nil {
		slog.Error("memory-extractor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	intelligenceURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PLATFORM_INTELLIGENCE_URL")), "/")
	if databaseURL == "" || intelligenceURL == "" {
		return errors.New("PLATFORM_DATABASE_URL and PLATFORM_INTELLIGENCE_URL are required")
	}
	dependency, err := requiredDuration("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return err
	}
	poll, err := requiredDuration("PLATFORM_MEMORY_EXTRACTION_POLL_INTERVAL")
	if err != nil {
		return err
	}
	lease, err := requiredDuration("PLATFORM_MEMORY_EXTRACTION_LEASE")
	if err != nil {
		return err
	}
	maxAttempts, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PLATFORM_MEMORY_EXTRACTION_MAX_ATTEMPTS")))
	if err != nil || maxAttempts < 1 || maxAttempts > 10 {
		return errors.New("PLATFORM_MEMORY_EXTRACTION_MAX_ATTEMPTS must be between 1 and 10")
	}
	startup, cancel := context.WithTimeout(context.Background(), dependency)
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
	worker := memory.NewExtractionWorker(
		memory.NewExtractionStore(pool),
		memory.NewExtractionClient(intelligenceURL, dependency),
		memory.NewStore(pool),
		poll, lease, maxAttempts,
	)
	slog.Info("memory-extractor started")
	return worker.Run(ctx)
}

func requiredDuration(key string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	parsed, err := time.ParseDuration(value)
	if value == "" || err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive duration")
	}
	return parsed, nil
}
