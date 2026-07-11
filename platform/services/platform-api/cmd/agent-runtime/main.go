package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agent-runtime stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := agent.LoadConfig()
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
	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, cfg.SaramaConfig())
	if err != nil {
		return err
	}
	defer group.Close()
	store := agent.NewStore(pool)
	consumer := agent.NewConsumer(group, cfg.EventTopic, store)
	candidates := agent.NewCandidateClient(cfg.IntelligenceURL, cfg.DependencyTimeout)
	retriever := retrieval.NewStore(pool)
	actions := action.NewStore(pool)
	sender := openim.NewClient(openim.Config{BaseURL: cfg.OpenIMAPIURL, Secret: cfg.OpenIMSecret, AdminUser: cfg.OpenIMAdminUserID, Timeout: cfg.DependencyTimeout})
	worker := agent.NewWorker(store, candidates, retrievalBridge{store: retriever}, actionBridge{store: actions}, sender, cfg.Poll, cfg.Lease, cfg.MaxAttempts)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 2)
	go func() { errs <- consumer.Run(ctx) }()
	go func() { errs <- worker.Run(ctx) }()
	slog.Info("agent-runtime started", "event_topic", cfg.EventTopic, "consumer_group", cfg.ConsumerGroup)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errs:
		stop()
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

type actionBridge struct{ store *action.Store }

func (b actionBridge) EnsureIntent(ctx context.Context, request agent.IntentRequest) (agent.Intent, error) {
	result, err := b.store.EnsureIntent(ctx, action.IntentRequest{RunID: request.RunID, TenantID: request.TenantID, MemberID: request.MemberID, ActionType: request.ActionType, Title: request.Title})
	return agent.Intent{ID: result.ID, Digest: result.Digest}, err
}

type retrievalBridge struct{ store *retrieval.Store }

func (b retrievalBridge) Search(ctx context.Context, query agent.RetrievalQuery) ([]agent.Evidence, error) {
	items, err := b.store.Search(ctx, retrieval.Query{
		TenantID: query.TenantID, MemberID: query.MemberID, Purpose: query.Purpose, Text: query.Text, Limit: query.Limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]agent.Evidence, len(items))
	for i, item := range items {
		result[i] = agent.Evidence{CitationID: item.CitationID, DocumentID: item.DocumentID, VersionID: item.VersionID,
			ChunkID: item.ChunkID, Title: item.Title, SourceURI: item.SourceURI, Checksum: item.Checksum, Content: item.Content}
	}
	return result, nil
}
