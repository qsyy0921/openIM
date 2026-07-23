package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledge"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
)

type embeddingAdapter struct {
	client *retrieval.HTTPEmbeddingClient
}

func (a embeddingAdapter) Embed(ctx context.Context, texts []string) (knowledge.EmbeddingBatch, error) {
	batch, err := a.client.Embed(ctx, texts)
	if err != nil {
		return knowledge.EmbeddingBatch{}, err
	}
	return knowledge.EmbeddingBatch{Model: batch.Model, Dimension: batch.Dimension, Vectors: batch.Vectors}, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("knowledge ingestion stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL, err := required("PLATFORM_DATABASE_URL")
	if err != nil {
		return err
	}
	dependencyTimeout, err := durationEnv("PLATFORM_DEPENDENCY_TIMEOUT", 0)
	if err != nil {
		return err
	}
	retrievalIntelligenceURL, err := required("PLATFORM_RETRIEVAL_INTELLIGENCE_URL")
	if err != nil {
		return err
	}
	embeddingRevision, err := required("PLATFORM_RETRIEVAL_EMBEDDING_MODEL")
	if err != nil {
		return err
	}
	dimension, err := integerEnv("PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION", 2560, 2560)
	if err != nil {
		return err
	}
	parserRevision, err := required("PLATFORM_KNOWLEDGE_PARSER_REVISION")
	if err != nil {
		return err
	}
	if parserRevision != knowledge.ParserRevision {
		return fmt.Errorf("PLATFORM_KNOWLEDGE_PARSER_REVISION must equal %s", knowledge.ParserRevision)
	}
	maxAttempts, err := integerEnv("PLATFORM_KNOWLEDGE_INGESTION_MAX_ATTEMPTS", 1, 8)
	if err != nil {
		return err
	}
	batchSize, err := integerEnv("PLATFORM_KNOWLEDGE_INGESTION_BATCH", 1, 128)
	if err != nil {
		return err
	}
	lease, err := durationEnv("PLATFORM_KNOWLEDGE_INGESTION_LEASE", 0)
	if err != nil {
		return err
	}
	poll, err := durationEnv("PLATFORM_KNOWLEDGE_INGESTION_POLL_INTERVAL", 0)
	if err != nil {
		return err
	}
	owner, err := required("PLATFORM_KNOWLEDGE_INGESTION_OWNER")
	if err != nil {
		return err
	}
	tempDirectory, err := required("PLATFORM_KNOWLEDGE_INGESTION_TEMP_DIR")
	if err != nil {
		return err
	}
	minioURL, err := required("PLATFORM_KNOWLEDGE_MINIO_URL")
	if err != nil {
		return err
	}
	minioAccess, err := required("PLATFORM_KNOWLEDGE_MINIO_ACCESS_KEY")
	if err != nil {
		return err
	}
	minioSecret, err := required("PLATFORM_KNOWLEDGE_MINIO_SECRET_KEY")
	if err != nil {
		return err
	}
	minioBucket, err := required("PLATFORM_KNOWLEDGE_MINIO_BUCKET")
	if err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), dependencyTimeout)
	defer cancelStartup()
	pool, err := pgxpool.New(startupCtx, databaseURL)
	if err != nil {
		return fmt.Errorf("configure knowledge PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		return fmt.Errorf("connect knowledge PostgreSQL: %w", err)
	}
	objects, err := knowledge.NewMinIOStore(startupCtx, knowledge.MinIOConfig{
		Endpoint: minioURL, AccessKey: minioAccess, SecretKey: minioSecret, Bucket: minioBucket,
	})
	if err != nil {
		return err
	}
	store, err := knowledge.NewStore(pool, knowledge.StoreConfig{
		Bucket: minioBucket, ParserRevision: parserRevision, EmbeddingRevision: embeddingRevision,
		EmbeddingDimension: dimension, MaxAttempts: maxAttempts,
	})
	if err != nil {
		return err
	}
	embeddingClient, err := retrieval.NewHTTPEmbeddingClient(
		retrievalIntelligenceURL,
		dependencyTimeout,
		embeddingRevision,
		dimension,
	)
	if err != nil {
		return err
	}
	worker, err := knowledge.NewWorker(store, objects, knowledge.NewParser(), embeddingAdapter{client: embeddingClient}, knowledge.WorkerConfig{
		Owner: owner, LeaseDuration: lease, PollInterval: poll, BatchSize: batchSize, TempDirectory: tempDirectory,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("knowledge ingestion started", "owner", owner, "parser_revision", parserRevision, "embedding_revision", embeddingRevision)
	return worker.Run(ctx)
}

func required(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" && fallback > 0 {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func integerEnv(key string, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

var _ knowledge.EmbeddingProvider = embeddingAdapter{}
