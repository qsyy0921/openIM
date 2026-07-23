package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("telegram-admin failed", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("telegram-admin", flag.ContinueOnError)
	userID := flags.Int64("user-id", 0, "Telegram numeric user ID")
	chatID := flags.Int64("chat-id", 0, "Telegram numeric chat ID")
	tenantID := flags.String("tenant-id", "", "enterprise tenant UUID")
	memberID := flags.String("member-id", "", "enterprise member UUID")
	sessionType := flags.Int("session-type", 0, "1 for private, 2 for group")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("PLATFORM_DATABASE_URL is required")
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(os.Getenv("PLATFORM_DEPENDENCY_TIMEOUT")))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("PLATFORM_DEPENDENCY_TIMEOUT must be a positive duration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if err := telegram.NewStore(pool).Bind(ctx, *userID, *chatID, strings.TrimSpace(*tenantID), strings.TrimSpace(*memberID), int32(*sessionType)); err != nil {
		return err
	}
	slog.Info("Telegram enterprise binding stored", "telegram_user_id", *userID, "telegram_chat_id", *chatID, "tenant_id", *tenantID, "member_id", *memberID)
	return nil
}
