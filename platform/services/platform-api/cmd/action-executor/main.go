package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfg, err := action.LoadExecutorConfig()
	if err != nil {
		slog.Error("invalid action executor configuration", "error", err)
		os.Exit(1)
	}
	startup, cancel := context.WithTimeout(context.Background(), cfg.DependencyTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		slog.Error("configure PostgreSQL failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		slog.Error("connect PostgreSQL failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("action-executor started")
	if err := action.NewExecutor(pool, cfg.Poll, cfg.Lease, cfg.MaxAttempts).Run(ctx); err != nil {
		slog.Error("action-executor stopped", "error", err)
		os.Exit(1)
	}
}
