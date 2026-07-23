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
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/admincontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agentcontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/app"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/config"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delegation"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/httpserver"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledge"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	openimclient "github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
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
	identityStore := identity.NewPostgresStore(pool)
	if cfg.KnowledgeParserRevision != knowledge.ParserRevision {
		slog.Error("knowledge parser revision does not match the compiled contract",
			"configured", cfg.KnowledgeParserRevision, "compiled", knowledge.ParserRevision)
		os.Exit(1)
	}
	knowledgeObjects, err := knowledge.NewMinIOStore(startupCtx, knowledge.MinIOConfig{
		Endpoint: cfg.KnowledgeMinIOURL, AccessKey: cfg.KnowledgeMinIOAccessKey,
		SecretKey: cfg.KnowledgeMinIOSecretKey, Bucket: cfg.KnowledgeMinIOBucket,
	})
	if err != nil {
		slog.Error("configure knowledge object storage failed", "error", err)
		os.Exit(1)
	}
	knowledgeStore, err := knowledge.NewStore(pool, knowledge.StoreConfig{
		Bucket: cfg.KnowledgeMinIOBucket, ParserRevision: cfg.KnowledgeParserRevision,
		EmbeddingRevision:  cfg.RetrievalEmbeddingModel,
		EmbeddingDimension: cfg.RetrievalEmbeddingDimension, MaxAttempts: cfg.KnowledgeMaxAttempts,
	})
	if err != nil {
		slog.Error("configure knowledge repository failed", "error", err)
		os.Exit(1)
	}
	knowledgeService := knowledge.NewService(verifier, identityStore, knowledgeStore, knowledgeObjects)
	sessions := identity.NewService(
		verifier,
		identityStore,
		openIM,
		cfg.OpenIMWSURL,
		2*cfg.DependencyTimeout,
	)
	devices := identity.NewDeviceService(verifier, identityStore, openIM)
	actionStore := action.NewStore(pool)
	approvals := action.NewService(verifier, identityStore, actionStore)
	agentStore := agent.NewStore(pool)
	workspace := agent.NewWorkspaceService(verifier, identityStore, agentStore, openIM)
	catalog := agent.NewCatalogService(verifier, identityStore, agentStore)
	control := agentcontrol.NewService(verifier, identityStore, memory.NewStore(pool), proactive.NewStore(pool), toolruntime.NewStore(pool), observe.NewStore(pool), delegation.NewStore(pool))
	control.SetGroupAccess(openIM)
	admin := admincontrol.NewService(verifier, identityStore, runtimecontrol.NewStore(pool), observe.NewStore(pool))
	if err := observe.RegisterOperationalCollector(pool); err != nil {
		slog.Error("register operational metrics failed", "error", err)
		os.Exit(1)
	}
	admin.SetCatalogStore(admincontrol.NewCatalogStore(pool))
	telegramLinks := telegram.NewLinkService(verifier, identityStore, telegram.NewStore(pool))
	if len(cfg.A2AAllowedHosts) > 0 {
		a2aClient, err := remotea2a.NewClient(remotea2a.Config{
			AllowedHosts: cfg.A2AAllowedHosts, AllowedPrivateCIDRs: cfg.A2AAllowedPrivateCIDRs, Timeout: cfg.A2ATimeout,
		}, os.LookupEnv)
		if err != nil {
			slog.Error("configure remote A2A client failed", "error", err)
			os.Exit(1)
		}
		admin.SetRemoteA2AStore(remotea2a.NewStore(pool, a2aClient))
	}
	handler := httpserver.NewHandlerWithAgentControlsTelegramLinksAndKnowledge(
		cfg.Version, sessions, devices, approvals, workspace, catalog, control, admin, telegramLinks, knowledgeService,
	)

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
