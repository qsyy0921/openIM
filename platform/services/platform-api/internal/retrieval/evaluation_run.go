package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
)

const (
	productionEvaluationSchemaVersion = 2
	frozenBaselineRecallAt8           = 0.928846
)

type ProductionEvaluationConfig struct {
	TenantID           string
	DatasetRevision    string
	DatasetDigest      string
	ApplicationCommit  string
	EmbeddingRevision  string
	ProjectionRevision string
	RerankerRevision   string
	GenerationModel    string
}

type ProductionEvaluationThresholds struct {
	MinimumRetrievalCases            int     `json:"minimum_retrieval_cases"`
	MinimumGenerationCases           int     `json:"minimum_generation_cases"`
	MinimumRecallAt5                 float64 `json:"minimum_recall_at_5"`
	MinimumRecallAt10Baseline        float64 `json:"minimum_recall_at_10_baseline_comparison"`
	MinimumMRR                       float64 `json:"minimum_mrr"`
	MaximumACLLeakageRate            float64 `json:"maximum_acl_leakage_rate"`
	MaximumStaleVersionLeakage       float64 `json:"maximum_stale_version_leakage_rate"`
	MinimumProvenanceIntegrity       float64 `json:"minimum_provenance_integrity"`
	MinimumChecksumIntegrity         float64 `json:"minimum_checksum_integrity"`
	MinimumCandidateContractSuccess  float64 `json:"minimum_candidate_contract_success_rate"`
	MinimumAbstentionAccuracy        float64 `json:"minimum_abstention_accuracy"`
	MinimumCitationPrecision         float64 `json:"minimum_citation_precision"`
	MinimumCitationChecksumIntegrity float64 `json:"minimum_citation_checksum_integrity"`
	MinimumFaithfulness              float64 `json:"minimum_faithfulness"`
}

type ProductionEvaluationReport struct {
	SchemaVersion      int                            `json:"schema_version"`
	EvaluationRunID    string                         `json:"evaluation_run_id"`
	TenantID           string                         `json:"tenant_id"`
	DatasetRevision    string                         `json:"dataset_revision"`
	DatasetDigest      string                         `json:"dataset_digest"`
	ApplicationCommit  string                         `json:"application_commit"`
	EmbeddingRevision  string                         `json:"embedding_revision"`
	ProjectionRevision string                         `json:"projection_revision"`
	RerankerRevision   string                         `json:"reranker_revision"`
	GenerationModel    string                         `json:"generation_model"`
	Thresholds         ProductionEvaluationThresholds `json:"thresholds"`
	Retrieval          EvaluationReport               `json:"retrieval"`
	Generation         GenerationEvaluationReport     `json:"generation"`
	Passed             bool                           `json:"passed"`
	FailureCaseIDs     []string                       `json:"failure_case_ids"`
	RecordedAt         time.Time                      `json:"recorded_at"`
}

func BuildProductionEvaluationReport(config ProductionEvaluationConfig, retrievalReport EvaluationReport, generationReport GenerationEvaluationReport) (ProductionEvaluationReport, error) {
	config = normalizeProductionEvaluationConfig(config)
	if err := validateProductionEvaluationConfig(config); err != nil {
		return ProductionEvaluationReport{}, err
	}
	if retrievalReport.SchemaVersion != 5 ||
		retrievalReport.ProjectionRevision != config.ProjectionRevision ||
		retrievalReport.Cases < 1 ||
		retrievalReport.AnswerableCases+retrievalReport.UnanswerableCases != retrievalReport.Cases ||
		retrievalReport.ACLDeniedCases != retrievalReport.Cases {
		return ProductionEvaluationReport{}, errors.New("retrieval evaluation report violates the schema-v5 contract")
	}
	if generationReport.SchemaVersion != 1 || generationReport.Cases < 1 ||
		generationReport.AnswerableCases+generationReport.UnanswerableCases != generationReport.Cases ||
		generationReport.Model != config.GenerationModel {
		return ProductionEvaluationReport{}, errors.New("generation evaluation report violates the locked contract")
	}
	thresholds := productionEvaluationThresholds()
	generationGateEvaluated := generationReport.Cases >= thresholds.MinimumGenerationCases &&
		generationReport.Model == LockedGenerationModel
	generationGatePassed := generationGateEvaluated &&
		generationReport.CandidateContractSuccess >= thresholds.MinimumCandidateContractSuccess &&
		generationReport.AbstentionAccuracy >= thresholds.MinimumAbstentionAccuracy &&
		generationReport.CitationPrecision >= thresholds.MinimumCitationPrecision &&
		generationReport.CitationChecksumIntegrity >= thresholds.MinimumCitationChecksumIntegrity &&
		generationReport.Faithfulness >= thresholds.MinimumFaithfulness
	if generationReport.ProductionGateEvaluated != generationGateEvaluated ||
		generationReport.ProductionGatePassed != generationGatePassed {
		return ProductionEvaluationReport{}, errors.New("generation evaluation gate fields do not match the measured metrics")
	}
	runID, err := productionEvaluationRunID(config, retrievalReport, generationReport)
	if err != nil {
		return ProductionEvaluationReport{}, err
	}
	report := ProductionEvaluationReport{
		SchemaVersion: productionEvaluationSchemaVersion, EvaluationRunID: runID,
		TenantID: config.TenantID, DatasetRevision: config.DatasetRevision, DatasetDigest: config.DatasetDigest,
		ApplicationCommit: config.ApplicationCommit, EmbeddingRevision: config.EmbeddingRevision,
		ProjectionRevision: config.ProjectionRevision,
		RerankerRevision:   config.RerankerRevision, GenerationModel: config.GenerationModel,
		Thresholds: thresholds, Retrieval: retrievalReport, Generation: generationReport,
		FailureCaseIDs: evaluationFailureIDs(retrievalReport, generationReport),
		RecordedAt:     time.Now().UTC(),
	}
	report.Passed =
		retrievalReport.Cases >= thresholds.MinimumRetrievalCases &&
			generationReport.Cases >= thresholds.MinimumGenerationCases &&
			retrievalReport.RecallAt5 >= thresholds.MinimumRecallAt5 &&
			retrievalReport.RecallAt10 >= thresholds.MinimumRecallAt10Baseline &&
			retrievalReport.MRR >= thresholds.MinimumMRR &&
			retrievalReport.ACLLeakageRate <= thresholds.MaximumACLLeakageRate &&
			retrievalReport.StaleVersionLeakage <= thresholds.MaximumStaleVersionLeakage &&
			retrievalReport.ProvenanceIntegrity >= thresholds.MinimumProvenanceIntegrity &&
			retrievalReport.ChecksumIntegrity >= thresholds.MinimumChecksumIntegrity &&
			generationGatePassed
	return report, nil
}

func RecordProductionEvaluation(ctx context.Context, pool *pgxpool.Pool, report ProductionEvaluationReport) (ProductionEvaluationReport, error) {
	if pool == nil || report.EvaluationRunID == "" || report.RecordedAt.IsZero() {
		return ProductionEvaluationReport{}, errors.New("production evaluation persistence contract is invalid")
	}
	expected, err := BuildProductionEvaluationReport(ProductionEvaluationConfig{
		TenantID: report.TenantID, DatasetRevision: report.DatasetRevision,
		DatasetDigest: report.DatasetDigest, ApplicationCommit: report.ApplicationCommit,
		EmbeddingRevision:  report.EmbeddingRevision,
		ProjectionRevision: report.ProjectionRevision,
		RerankerRevision:   report.RerankerRevision,
		GenerationModel:    report.GenerationModel,
	}, report.Retrieval, report.Generation)
	if err != nil {
		return ProductionEvaluationReport{}, err
	}
	if expected.EvaluationRunID != report.EvaluationRunID || expected.Passed != report.Passed ||
		!reflect.DeepEqual(expected.Thresholds, report.Thresholds) ||
		!reflect.DeepEqual(expected.FailureCaseIDs, report.FailureCaseIDs) {
		return ProductionEvaluationReport{}, errors.New("production evaluation report does not match its measured evidence")
	}
	thresholds, err := json.Marshal(report.Thresholds)
	if err != nil {
		return ProductionEvaluationReport{}, fmt.Errorf("encode production evaluation thresholds: %w", err)
	}
	metrics, err := json.Marshal(report)
	if err != nil {
		return ProductionEvaluationReport{}, fmt.Errorf("encode production evaluation metrics: %w", err)
	}
	state := "failed"
	if report.Passed {
		state = "passed"
	}
	const query = `
INSERT INTO knowledge.evaluation_runs (
    id, tenant_id, dataset_revision, dataset_digest, application_commit,
    embedding_revision, projection_revision, reranker_revision, generation_model, state,
    thresholds, metrics, failure_case_ids, started_at, completed_at
) VALUES (
    $1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10,
    $11::jsonb, $12::jsonb, $13::text[], $14, $14
) ON CONFLICT (id) DO NOTHING`
	tag, err := pool.Exec(ctx, query,
		report.EvaluationRunID, report.TenantID, report.DatasetRevision,
		report.DatasetDigest, report.ApplicationCommit, report.EmbeddingRevision,
		report.ProjectionRevision, report.RerankerRevision, report.GenerationModel,
		state, thresholds, metrics, report.FailureCaseIDs, report.RecordedAt,
	)
	if err != nil {
		return ProductionEvaluationReport{}, fmt.Errorf("record production evaluation: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return report, nil
	}
	var storedMetrics []byte
	if err := pool.QueryRow(ctx,
		"SELECT metrics FROM knowledge.evaluation_runs WHERE id = $1::uuid",
		report.EvaluationRunID,
	).Scan(&storedMetrics); err != nil {
		return ProductionEvaluationReport{}, fmt.Errorf("read existing production evaluation: %w", err)
	}
	var stored ProductionEvaluationReport
	if err := json.Unmarshal(storedMetrics, &stored); err != nil {
		return ProductionEvaluationReport{}, fmt.Errorf("decode existing production evaluation: %w", err)
	}
	if !productionEvaluationReportsEquivalent(stored, report) {
		return ProductionEvaluationReport{}, errors.New("evaluation run ID conflicts with different measured evidence")
	}
	return stored, nil
}

func productionEvaluationThresholds() ProductionEvaluationThresholds {
	return ProductionEvaluationThresholds{
		MinimumRetrievalCases: 1120, MinimumGenerationCases: 120,
		MinimumRecallAt5: 0.85, MinimumRecallAt10Baseline: frozenBaselineRecallAt8,
		MinimumMRR: 0.70, MaximumACLLeakageRate: 0,
		MaximumStaleVersionLeakage: 0, MinimumProvenanceIntegrity: 1,
		MinimumChecksumIntegrity: 1, MinimumCandidateContractSuccess: 1,
		MinimumAbstentionAccuracy: 0.95, MinimumCitationPrecision: 0.95,
		MinimumCitationChecksumIntegrity: 1, MinimumFaithfulness: 0.95,
	}
}

func normalizeProductionEvaluationConfig(config ProductionEvaluationConfig) ProductionEvaluationConfig {
	config.TenantID = strings.TrimSpace(config.TenantID)
	config.DatasetRevision = strings.TrimSpace(config.DatasetRevision)
	config.DatasetDigest = strings.TrimSpace(config.DatasetDigest)
	config.ApplicationCommit = strings.TrimSpace(config.ApplicationCommit)
	config.EmbeddingRevision = strings.TrimSpace(config.EmbeddingRevision)
	config.ProjectionRevision = strings.TrimSpace(config.ProjectionRevision)
	config.RerankerRevision = strings.TrimSpace(config.RerankerRevision)
	config.GenerationModel = strings.TrimSpace(config.GenerationModel)
	return config
}

func validateProductionEvaluationConfig(config ProductionEvaluationConfig) error {
	if !uuidPattern.MatchString(config.TenantID) ||
		config.DatasetRevision == "" || len(config.DatasetRevision) > 256 ||
		!digestPattern.MatchString(config.DatasetDigest) ||
		!commitPattern.MatchString(config.ApplicationCommit) ||
		config.EmbeddingRevision == "" || len(config.EmbeddingRevision) > 256 ||
		config.ProjectionRevision != knowledgeprojection.Revision ||
		config.RerankerRevision != LockedRerankerRevision ||
		config.GenerationModel != LockedGenerationModel {
		return errors.New("production evaluation metadata violates the locked contract")
	}
	return nil
}

func evaluationFailureIDs(retrievalReport EvaluationReport, generationReport GenerationEvaluationReport) []string {
	seen := make(map[string]struct{}, len(retrievalReport.Failures)+len(generationReport.Failures))
	for _, failure := range retrievalReport.Failures {
		if failure.QAID != "" {
			seen[failure.QAID] = struct{}{}
		}
	}
	for _, failure := range generationReport.Failures {
		if failure.QAID != "" {
			seen[failure.QAID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func productionEvaluationRunID(config ProductionEvaluationConfig, retrievalReport EvaluationReport, generationReport GenerationEvaluationReport) (string, error) {
	payload, err := json.Marshal(struct {
		Config     ProductionEvaluationConfig
		Retrieval  EvaluationReport
		Generation GenerationEvaluationReport
	}{Config: config, Retrieval: retrievalReport, Generation: generationReport})
	if err != nil {
		return "", fmt.Errorf("encode production evaluation identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	return uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String(), nil
}

func productionEvaluationReportsEquivalent(left, right ProductionEvaluationReport) bool {
	left.RecordedAt = time.Time{}
	right.RecordedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)
