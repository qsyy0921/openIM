package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "index, evaluate, or evaluate-generation")
	databaseURL := flag.String("database-url", env("PLATFORM_DATABASE_URL", ""), "PostgreSQL URL")
	intelligenceURL := flag.String("intelligence-url", env("PLATFORM_INTELLIGENCE_URL", "http://127.0.0.1:18082"), "Intelligence Worker URL")
	model := flag.String("model", env("PLATFORM_RETRIEVAL_EMBEDDING_MODEL", "qwen3-embedding:4b"), "locked embedding model revision")
	dimension := flag.Int("dimension", envInt("PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION", 2560), "embedding dimension")
	denseMinimum := flag.Float64("dense-minimum", envFloat("PLATFORM_RETRIEVAL_DENSE_MIN_SIMILARITY", 0.45), "dense similarity floor")
	maxCandidates := flag.Int("max-candidates", envInt("PLATFORM_RETRIEVAL_MAX_CANDIDATES", 4096), "maximum authorized chunks scanned per query")
	batchSize := flag.Int("batch-size", 32, "embedding index batch size")
	qaPath := flag.String("qa", "datasets/enterprise-knowledge/v1/qa.jsonl", "QA JSONL path")
	tenantID := flag.String("tenant-id", "", "evaluation tenant UUID")
	memberID := flag.String("member-id", "", "evaluation member UUID")
	limit := flag.Int("limit", 8, "retrieval K")
	generationModel := flag.String("generation-model", env("PLATFORM_RAG_EVALUATION_MODEL", ""), "declared generation evaluation model")
	generationSeed := flag.String("generation-seed", "enterprise-rag-generation-v1", "frozen generation sample seed")
	generationAnswerable := flag.Int("generation-answerable", 20, "answerable generation cases")
	generationUnanswerable := flag.Int("generation-unanswerable", 20, "unanswerable generation cases")
	output := flag.String("output", "", "optional JSON report path")
	timeout := flag.Duration("timeout", 120*time.Second, "per dependency request timeout")
	flag.Parse()
	if *mode != "index" && *mode != "evaluate" && *mode != "evaluate-generation" {
		return errors.New("-mode must be index, evaluate, or evaluate-generation")
	}
	if strings.TrimSpace(*databaseURL) == "" {
		return errors.New("-database-url or PLATFORM_DATABASE_URL is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	embedder, err := retrieval.NewHTTPEmbeddingClient(*intelligenceURL, *timeout, *model, *dimension)
	if err != nil {
		return err
	}
	store, err := retrieval.NewStore(pool, embedder, retrieval.Config{
		ModelRevision: *model, Dimension: *dimension, DenseMinSimilarity: *denseMinimum, MaxCandidates: *maxCandidates,
	})
	if err != nil {
		return err
	}
	var result any
	if *mode == "index" {
		result, err = store.IndexMissing(ctx, *batchSize)
	} else if *mode == "evaluate" {
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*memberID) == "" {
			return errors.New("-tenant-id and -member-id are required for evaluation")
		}
		cases, loadErr := retrieval.LoadQACases(*qaPath)
		if loadErr != nil {
			return loadErr
		}
		result, err = retrieval.Evaluate(ctx, store, cases, *tenantID, *memberID, *limit)
	} else {
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*memberID) == "" || strings.TrimSpace(*generationModel) == "" {
			return errors.New("-tenant-id, -member-id, and -generation-model are required for generation evaluation")
		}
		cases, loadErr := retrieval.LoadQACases(*qaPath)
		if loadErr != nil {
			return loadErr
		}
		generator, clientErr := retrieval.NewHTTPGenerationClient(*intelligenceURL, *timeout, *generationModel)
		if clientErr != nil {
			return clientErr
		}
		result, err = retrieval.EvaluateGeneration(ctx, store, generator, cases, retrieval.GenerationEvaluationConfig{
			TenantID: *tenantID, MemberID: *memberID, Model: *generationModel, Seed: *generationSeed,
			AnswerableCases: *generationAnswerable, UnanswerableCases: *generationUnanswerable, RetrievalLimit: *limit,
		})
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, encoded, 0o600); err != nil {
			return err
		}
	}
	fmt.Print(string(encoded))
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(env(key, ""))
	if err == nil {
		return value
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(env(key, ""), 64)
	if err == nil {
		return value
	}
	return fallback
}
