package retrieval

import (
	"strings"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
)

func TestBuildProductionEvaluationReportAppliesLockedGates(t *testing.T) {
	config := validProductionEvaluationConfig()
	retrievalReport := validRetrievalEvaluationReport()
	generationReport := validGenerationEvaluationReport()
	report, err := BuildProductionEvaluationReport(config, retrievalReport, generationReport)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.EvaluationRunID == "" || report.TenantID != config.TenantID {
		t.Fatalf("production report = %#v", report)
	}
	repeated, err := BuildProductionEvaluationReport(config, retrievalReport, generationReport)
	if err != nil || repeated.EvaluationRunID != report.EvaluationRunID {
		t.Fatalf("evaluation run identity is not deterministic: %#v, %v", repeated, err)
	}
	if report.Thresholds.MinimumRecallAt10Baseline != frozenBaselineRecallAt8 {
		t.Fatalf("frozen baseline threshold = %f", report.Thresholds.MinimumRecallAt10Baseline)
	}
	if report.Thresholds.MaximumStaleVersionLeakage != 0 ||
		report.Thresholds.MinimumCandidateContractSuccess != 1 ||
		report.Thresholds.MinimumCitationChecksumIntegrity != 1 ||
		report.Thresholds.MinimumFaithfulness != 0.95 {
		t.Fatalf("production thresholds omit an enforced gate: %#v", report.Thresholds)
	}

	retrievalReport.ACLLeakageRate = 0.001
	report, err = BuildProductionEvaluationReport(config, retrievalReport, generationReport)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("ACL leakage passed the production gate")
	}

	retrievalReport = validRetrievalEvaluationReport()
	generationReport.CitationChecksumIntegrity = 0.99
	generationReport.ProductionGatePassed = false
	report, err = BuildProductionEvaluationReport(config, retrievalReport, generationReport)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("citation checksum drift passed the production gate")
	}
}

func TestBuildProductionEvaluationReportRejectsUnlockedMetadata(t *testing.T) {
	config := validProductionEvaluationConfig()
	config.GenerationModel = "another-model"
	if _, err := BuildProductionEvaluationReport(config, validRetrievalEvaluationReport(), validGenerationEvaluationReport()); err == nil {
		t.Fatal("unlocked generation model was accepted")
	}
	config = validProductionEvaluationConfig()
	config.DatasetDigest = "sha256:invalid"
	if _, err := BuildProductionEvaluationReport(config, validRetrievalEvaluationReport(), validGenerationEvaluationReport()); err == nil {
		t.Fatal("invalid dataset digest was accepted")
	}
	config = validProductionEvaluationConfig()
	config.ProjectionRevision = knowledgeprojection.HistoricalRevision
	if _, err := BuildProductionEvaluationReport(config, validRetrievalEvaluationReport(), validGenerationEvaluationReport()); err == nil {
		t.Fatal("historical projection revision was accepted")
	}
}

func validProductionEvaluationConfig() ProductionEvaluationConfig {
	return ProductionEvaluationConfig{
		TenantID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", DatasetRevision: "enterprise-knowledge/v1",
		DatasetDigest: "sha256:" + strings.Repeat("a", 64), ApplicationCommit: strings.Repeat("b", 40),
		EmbeddingRevision:  "qwen3-embedding:4b",
		ProjectionRevision: knowledgeprojection.Revision,
		RerankerRevision:   LockedRerankerRevision,
		GenerationModel:    LockedGenerationModel,
	}
}

func validRetrievalEvaluationReport() EvaluationReport {
	return EvaluationReport{
		SchemaVersion: 5, ProjectionRevision: knowledgeprojection.Revision,
		Cases: 1120, AnswerableCases: 1040, UnanswerableCases: 80,
		ACLDeniedCases: 1120, RecallAt5: 0.90, RecallAt10: 0.95, MRR: 0.75,
		ACLLeakageRate: 0, ProvenanceIntegrity: 1, ChecksumIntegrity: 1,
	}
}

func validGenerationEvaluationReport() GenerationEvaluationReport {
	return GenerationEvaluationReport{
		SchemaVersion: 1, Model: LockedGenerationModel, Cases: 120,
		AnswerableCases: 60, UnanswerableCases: 60, CandidateContractSuccess: 1,
		AbstentionAccuracy: 0.98, CitationPrecision: 0.99, CitationChecksumIntegrity: 1,
		Faithfulness: 0.98, ProductionGateEvaluated: true, ProductionGatePassed: true,
	}
}
