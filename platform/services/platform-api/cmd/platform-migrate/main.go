package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/migrations"
)

func main() {
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		slog.Error("PLATFORM_DATABASE_URL is required")
		os.Exit(1)
	}
	timeoutValue := strings.TrimSpace(os.Getenv("PLATFORM_DEPENDENCY_TIMEOUT"))
	timeout, err := time.ParseDuration(timeoutValue)
	if err != nil || timeout <= 0 {
		slog.Error("PLATFORM_DEPENDENCY_TIMEOUT must be a positive duration")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		slog.Error("configure PostgreSQL failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := migrations.Apply(ctx, pool); err != nil {
		slog.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations applied")
}
