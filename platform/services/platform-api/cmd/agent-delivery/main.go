package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delivery"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agent-delivery stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := delivery.LoadConfig()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(context.Background(), cfg.DependencyTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return err
	}
	openIMClient := openim.NewClient(openim.Config{
		BaseURL: cfg.OpenIMAPIURL, Secret: cfg.OpenIMSecret,
		AdminUser: cfg.OpenIMAdminUserID, Timeout: cfg.DependencyTimeout,
	})
	telegramClient, err := telegram.NewClient(cfg.TelegramAPIURL, cfg.TelegramBotToken, cfg.TelegramTimeout)
	if err != nil {
		return err
	}
	openIMSender, err := delivery.NewOpenIMSender(agent.NewStore(pool), openIMClient, agent.BotUserID)
	if err != nil {
		return err
	}
	telegramSender, err := delivery.NewTelegramSender(telegramClient)
	if err != nil {
		return err
	}
	router, err := delivery.NewRouter(openIMSender, telegramSender)
	if err != nil {
		return err
	}
	worker := delivery.NewWorker(delivery.NewStore(pool), router, cfg.Poll, cfg.Lease, cfg.MaxAttempts)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("agent-delivery started", "channels", []string{"openim", "telegram"})
	return worker.Run(ctx)
}
