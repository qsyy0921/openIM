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
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
)

func main() {
	if err := run(); err != nil {
		slog.Error("proactive-runtime stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL, err := required("PLATFORM_DATABASE_URL")
	if err != nil {
		return err
	}
	intelligenceURL, err := required("PLATFORM_INTELLIGENCE_URL")
	if err != nil {
		return err
	}
	arxivURL, err := required("PLATFORM_ARXIV_BASE_URL")
	if err != nil {
		return err
	}
	userAgent, err := required("PLATFORM_ARXIV_USER_AGENT")
	if err != nil {
		return err
	}
	dependency, err := requiredDuration("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return err
	}
	poll, err := requiredDuration("PLATFORM_PROACTIVE_POLL_INTERVAL")
	if err != nil {
		return err
	}
	lease, err := requiredDuration("PLATFORM_PROACTIVE_LEASE")
	if err != nil {
		return err
	}
	maxAttempts, err := requiredBoundedInt("PLATFORM_PROACTIVE_MAX_ATTEMPTS", 1, 10)
	if err != nil {
		return err
	}
	maxResults, err := requiredBoundedInt("PLATFORM_ARXIV_MAX_RESULTS", 1, 100)
	if err != nil {
		return err
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
	arxiv, err := proactive.NewArxivClient(arxivURL, userAgent, maxResults, dependency)
	if err != nil {
		return err
	}
	store := proactive.NewStore(pool)
	collector := proactive.NewCollector(store, arxiv, poll, lease)
	dispatcher := proactive.NewDispatcher(
		store, proactive.NewRankerClient(intelligenceURL, dependency),
		memoryBridge{store: memory.NewStore(pool)}, agent.NewStore(pool),
		poll, lease, maxAttempts,
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 2)
	go func() { errs <- collector.Run(ctx) }()
	go func() { errs <- dispatcher.Run(ctx) }()
	slog.Info("proactive-runtime started")
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

type memoryBridge struct{ store *memory.Store }

func (b memoryBridge) Search(ctx context.Context, tenantID, memberID, query string, limit int) ([]proactive.RankMemory, error) {
	facts, err := b.store.SearchPersonal(ctx, tenantID, memberID, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]proactive.RankMemory, len(facts))
	for index, fact := range facts {
		result[index] = proactive.RankMemory{ID: fact.ID, Category: fact.Category, Checksum: fact.Checksum, Content: fact.Content}
	}
	return result, nil
}

func required(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", errors.New(key + " is required")
	}
	return value, nil
}

func requiredDuration(key string) (time.Duration, error) {
	value, err := required(key)
	if err != nil {
		return 0, err
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive duration")
	}
	return parsed, nil
}

func requiredBoundedInt(key string, minimum, maximum int) (int, error) {
	value, err := required(key)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(key + " is outside the allowed range")
	}
	return parsed, nil
}
