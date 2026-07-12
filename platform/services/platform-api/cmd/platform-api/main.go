package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/app"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/config"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/httpserver"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	openimclient "github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), cfg.DependencyTimeout)
	defer cancelStartup()

	pool, err := pgxpool.New(startupCtx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("configure PostgreSQL failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		slog.Error("connect PostgreSQL failed", "error", err)
		os.Exit(1)
	}

	verifier, err := identity.NewOIDCVerifier(startupCtx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		slog.Error("configure OIDC verifier failed", "error", err)
		os.Exit(1)
	}
	openIM := openimclient.NewClient(openimclient.Config{
		BaseURL:   cfg.OpenIMAPIURL,
		Secret:    cfg.OpenIMSecret,
		AdminUser: cfg.OpenIMAdminUserID,
		Timeout:   cfg.DependencyTimeout,
	})
	sessions := identity.NewService(
		verifier,
		identity.NewPostgresStore(pool),
		openIM,
		cfg.OpenIMWSURL,
		2*cfg.DependencyTimeout,
	)
	actionStore := action.NewStore(pool)
	approvals := action.NewService(verifier, identity.NewPostgresStore(pool), actionStore)
	agentStore := agent.NewStore(pool)
	workspace := agent.NewWorkspaceService(verifier, identity.NewPostgresStore(pool), agentStore, openIM)
	handler := httpserver.NewHandler(cfg.Version, sessions, approvals, workspace)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		slog.Error("listen failed", "address", cfg.HTTPAddr, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("platform-api started", "address", cfg.HTTPAddr, "version", cfg.Version)
	if err := app.Run(ctx, cfg, listener, handler); err != nil {
		slog.Error("platform-api stopped with error", "error", err)
		os.Exit(1)
	}
	slog.Info("platform-api stopped")
}
