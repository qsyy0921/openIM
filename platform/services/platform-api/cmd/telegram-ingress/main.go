package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		slog.Error("telegram-ingress stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := telegram.LoadConfig()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return err
	}
	client, err := telegram.NewClient(cfg.APIBaseURL, cfg.BotToken, cfg.HTTPTimeout)
	if err != nil {
		return err
	}
	poller, err := telegram.NewPoller(client, telegram.NewStore(pool), ingress.NewStore(pool), cfg.PollTimeout, cfg.RetryDelay, cfg.CatalogAlias)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return poller.Run(ctx)
}
