package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "index, evaluate, evaluate-generation, or finalize")
	databaseURL := flag.String("database-url", env("PLATFORM_DATABASE_URL", ""), "PostgreSQL URL")
	intelligenceURL := flag.String("intelligence-url", env("PLATFORM_RETRIEVAL_INTELLIGENCE_URL", "http://127.0.0.1:18083"), "Retrieval Worker URL")
	generationURL := flag.String("generation-url", env("PLATFORM_GENERATION_INTELLIGENCE_URL", ""), "Generation Worker URL; defaults to the configured Intelligence Worker URL")
	model := flag.String("model", env("PLATFORM_RETRIEVAL_EMBEDDING_MODEL", "qwen3-embedding:4b"), "locked embedding model revision")
	dimension := flag.Int("dimension", envInt("PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION", 2560), "embedding dimension")
	projectionRevision := flag.String(
		"projection-revision",
		env("PLATFORM_RETRIEVAL_PROJECTION_REVISION", ""),
		"locked retrieval projection revision",
	)
	denseMinimum := flag.Float64("dense-minimum", envFloat("PLATFORM_RETRIEVAL_DENSE_MIN_SIMILARITY", 0.45), "dense similarity floor")
	maxCandidates := flag.Int("max-candidates", envInt("PLATFORM_RETRIEVAL_MAX_CANDIDATES", 32), "maximum candidates per lexical and dense branch")
	rerankerModel := flag.String("reranker-model", env("PLATFORM_RETRIEVAL_RERANKER_MODEL", retrieval.LockedRerankerModel), "locked reranker model")
	rerankerRevision := flag.String("reranker-revision", env("PLATFORM_RETRIEVAL_RERANKER_REVISION", retrieval.LockedRerankerRevision), "locked reranker revision")
	hnswEFSearch := flag.Int("hnsw-ef-search", envInt("PLATFORM_RETRIEVAL_HNSW_EF_SEARCH", 100), "bounded HNSW ef_search")
	batchSize := flag.Int("batch-size", 32, "embedding reindex batch size")
	embeddingWorkers := flag.Int("embedding-workers", 1, "bounded concurrent embedding requests for index generation")
	qaPath := flag.String("qa", "datasets/enterprise-knowledge/v1/qa.jsonl", "QA JSONL path")
	tenantID := flag.String("tenant-id", "", "evaluation tenant UUID")
	memberID := flag.String("member-id", "", "evaluation member UUID")
	deniedMemberID := flag.String("denied-member-id", "", "active evaluation member UUID with zero document grants")
	limit := flag.Int("limit", 8, "retrieval K")
	generationModel := flag.String("generation-model", env("PLATFORM_RAG_EVALUATION_MODEL", retrieval.LockedGenerationModel), "locked generation evaluation model")
	generationSeed := flag.String("generation-seed", "enterprise-rag-generation-v1", "frozen generation sample seed")
	generationAnswerable := flag.Int("generation-answerable", 60, "answerable generation cases")
	generationUnanswerable := flag.Int("generation-unanswerable", 60, "unanswerable generation cases")
	retrievalReportPath := flag.String("retrieval-report", "", "schema-v5 retrieval report for finalize")
	generationReportPath := flag.String("generation-report", "", "generation report for finalize")
	datasetRevision := flag.String("dataset-revision", "enterprise-knowledge/v1", "immutable dataset revision")
	applicationCommit := flag.String("application-commit", "", "evaluated application Git commit")
	output := flag.String("output", "", "optional JSON report path")
	timeout := flag.Duration("timeout", 120*time.Second, "per dependency request timeout")
	flag.Parse()
	resolvedGenerationURL, err := resolveGenerationURL(*generationURL, *intelligenceURL)
	if err != nil {
		return err
	}
	if *mode != "index" && *mode != "evaluate" && *mode != "evaluate-generation" && *mode != "finalize" {
		return errors.New("-mode must be index, evaluate, evaluate-generation, or finalize")
	}
	if strings.TrimSpace(*projectionRevision) != knowledgeprojection.Revision {
		return errors.New("-projection-revision violates the compiled retrieval projection contract")
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
	if *mode == "finalize" {
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*retrievalReportPath) == "" ||
			strings.TrimSpace(*generationReportPath) == "" || strings.TrimSpace(*applicationCommit) == "" {
			return errors.New("-tenant-id, -retrieval-report, -generation-report, and -application-commit are required for finalize")
		}
		var retrievalReport retrieval.EvaluationReport
		if err := decodeReport(*retrievalReportPath, &retrievalReport); err != nil {
			return fmt.Errorf("load retrieval evaluation report: %w", err)
		}
		var generationReport retrieval.GenerationEvaluationReport
		if err := decodeReport(*generationReportPath, &generationReport); err != nil {
			return fmt.Errorf("load generation evaluation report: %w", err)
		}
		datasetDigest, err := fileSHA256(*qaPath)
		if err != nil {
			return fmt.Errorf("digest evaluation dataset: %w", err)
		}
		report, err := retrieval.BuildProductionEvaluationReport(retrieval.ProductionEvaluationConfig{
			TenantID: strings.TrimSpace(*tenantID), DatasetRevision: strings.TrimSpace(*datasetRevision),
			DatasetDigest: datasetDigest, ApplicationCommit: strings.TrimSpace(*applicationCommit),
			EmbeddingRevision:  strings.TrimSpace(*model),
			ProjectionRevision: strings.TrimSpace(*projectionRevision),
			RerankerRevision:   strings.TrimSpace(*rerankerRevision),
			GenerationModel:    strings.TrimSpace(*generationModel),
		}, retrievalReport, generationReport)
		if err != nil {
			return err
		}
		report, err = retrieval.RecordProductionEvaluation(ctx, pool, report)
		if err != nil {
			return err
		}
		return emitResult(report, *output)
	}
	embedder, err := retrieval.NewHTTPEmbeddingClient(*intelligenceURL, *timeout, *model, *dimension)
	if err != nil {
		return err
	}
	reranker, err := retrieval.NewHTTPReranker(*intelligenceURL, *timeout, *rerankerModel, *rerankerRevision)
	if err != nil {
		return err
	}
	store, err := retrieval.NewStore(pool, embedder, reranker, retrieval.Config{
		ModelRevision: *model, ProjectionRevision: *projectionRevision,
		Dimension: *dimension, DenseMinSimilarity: *denseMinimum, MaxCandidates: *maxCandidates,
		RerankerModel: *rerankerModel, RerankerRevision: *rerankerRevision, HNSWEFSearch: *hnswEFSearch,
	})
	if err != nil {
		return err
	}
	var result any
	if *mode == "index" {
		if strings.TrimSpace(*tenantID) == "" {
			return errors.New("-tenant-id is required for index generation")
		}
		result, err = store.BuildIndexGeneration(
			ctx,
			strings.TrimSpace(*tenantID),
			*batchSize,
			*embeddingWorkers,
		)
	} else if *mode == "evaluate" {
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*memberID) == "" ||
			strings.TrimSpace(*deniedMemberID) == "" {
			return errors.New("-tenant-id, -member-id, and -denied-member-id are required for evaluation")
		}
		cases, loadErr := retrieval.LoadQACases(*qaPath)
		if loadErr != nil {
			return loadErr
		}
		result, err = retrieval.Evaluate(ctx, store, cases, retrieval.EvaluationConfig{
			TenantID: strings.TrimSpace(*tenantID), MemberID: strings.TrimSpace(*memberID),
			DeniedMemberID: strings.TrimSpace(*deniedMemberID),
		})
	} else {
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*memberID) == "" || strings.TrimSpace(*generationModel) == "" {
			return errors.New("-tenant-id, -member-id, and -generation-model are required for generation evaluation")
		}
		cases, loadErr := retrieval.LoadQACases(*qaPath)
		if loadErr != nil {
			return loadErr
		}
		generator, clientErr := retrieval.NewHTTPGenerationClient(resolvedGenerationURL, *timeout, *generationModel)
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
	return emitResult(result, *output)
}

func resolveGenerationURL(configured, intelligence string) (string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(intelligence); value != "" {
		return value, nil
	}
	return "", errors.New("generation worker URL is required")
}

func emitResult(result any, output string) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output, encoded, 0o600); err != nil {
			return err
		}
	}
	fmt.Print(string(encoded))
	return nil
}

func decodeReport(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("evaluation report contains trailing JSON values")
		}
		return err
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 128<<20)); err != nil {
		return "", err
	}
	var trailing [1]byte
	if count, err := file.Read(trailing[:]); err != io.EOF || count != 0 {
		if err != nil {
			return "", err
		}
		return "", errors.New("evaluation dataset exceeds 128 MiB")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
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
