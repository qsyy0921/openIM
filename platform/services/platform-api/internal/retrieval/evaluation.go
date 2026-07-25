package retrieval

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
)

const (
	evaluationEmbeddingBatchSize = 4
	evaluationEmbeddingWorkers   = 2
)

type QAEvidence struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
}

type QACase struct {
	QAID               string       `json:"qa_id"`
	Question           string       `json:"question"`
	Answer             string       `json:"answer"`
	Answerable         bool         `json:"answerable"`
	RequiredFacts      []string     `json:"required_facts"`
	Evidence           []QAEvidence `json:"evidence"`
	DomainCode         string       `json:"domain_code"`
	Type               string       `json:"type"`
	NegativeVersionIDs []string     `json:"negative_version_ids"`
}

type EvaluationReport struct {
	SchemaVersion         int                 `json:"schema_version"`
	ProjectionRevision    string              `json:"projection_revision"`
	Cases                 int                 `json:"cases"`
	AnswerableCases       int                 `json:"answerable_cases"`
	UnanswerableCases     int                 `json:"unanswerable_cases"`
	ACLDeniedCases        int                 `json:"acl_denied_cases"`
	RecallAt5             float64             `json:"recall_at_5"`
	RecallAt10            float64             `json:"recall_at_10"`
	RecallAtK             float64             `json:"recall_at_k"`
	MRR                   float64             `json:"mrr"`
	NDCGAt10              float64             `json:"ndcg_at_10"`
	PrecisionAt5          float64             `json:"precision_at_5"`
	PrecisionAt10         float64             `json:"precision_at_10"`
	RetrievalPrecisionAtK float64             `json:"retrieval_precision_at_k"`
	UnanswerableEmptyRate float64             `json:"unanswerable_retrieval_empty_rate"`
	ACLLeakageRate        float64             `json:"acl_leakage_rate"`
	StaleVersionLeakage   float64             `json:"stale_version_leakage_rate"`
	GenerationEvaluated   bool                `json:"generation_abstention_evaluated"`
	ProvenanceIntegrity   float64             `json:"provenance_integrity"`
	ChecksumIntegrity     float64             `json:"checksum_integrity"`
	Failures              []EvaluationFailure `json:"failures"`
}

type EvaluationFailure struct {
	QAID   string `json:"qa_id"`
	Reason string `json:"reason"`
}

func LoadQACases(path string) ([]QACase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := make([]QACase, 0)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var item QACase
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode QA case: %w", err)
		}
		if item.QAID == "" || item.Question == "" {
			return nil, errors.New("QA case identity and question are required")
		}
		if _, exists := seen[item.QAID]; exists {
			return nil, errors.New("QA case IDs are not unique")
		}
		seen[item.QAID] = struct{}{}
		if item.Answerable && len(item.Evidence) == 0 {
			return nil, fmt.Errorf("answerable QA case %s has no evidence", item.QAID)
		}
		result = append(result, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("QA dataset is empty")
	}
	return result, nil
}

type EvaluationConfig struct {
	TenantID       string
	MemberID       string
	DeniedMemberID string
}

type evaluationEmbeddingResult struct {
	start   int
	vectors [][]float32
	err     error
}

func (s *Store) embedEvaluationQuestions(ctx context.Context, cases []QACase) ([][]float32, error) {
	if len(cases) == 0 {
		return nil, errors.New("QA evaluation embedding input is empty")
	}
	vectors := make([][]float32, len(cases))
	groupSize := evaluationEmbeddingBatchSize * evaluationEmbeddingWorkers
	for groupStart := 0; groupStart < len(cases); groupStart += groupSize {
		groupEnd := min(groupStart+groupSize, len(cases))
		batchCount := (groupEnd - groupStart + evaluationEmbeddingBatchSize - 1) /
			evaluationEmbeddingBatchSize
		results := make([]evaluationEmbeddingResult, batchCount)
		var wait sync.WaitGroup
		for batchIndex, start := 0, groupStart; start < groupEnd; batchIndex, start =
			batchIndex+1, start+evaluationEmbeddingBatchSize {
			end := min(start+evaluationEmbeddingBatchSize, groupEnd)
			texts := make([]string, end-start)
			for index := start; index < end; index++ {
				texts[index-start] = cases[index].Question
			}
			results[batchIndex].start = start
			wait.Add(1)
			go func(index int, batchTexts []string) {
				defer wait.Done()
				batch, err := s.embedder.Embed(ctx, batchTexts)
				if err == nil {
					err = s.validateEmbeddingBatch(batch, len(batchTexts))
				}
				if err != nil {
					results[index].err = err
					return
				}
				results[index].vectors = make([][]float32, len(batch.Vectors))
				for vectorIndex := range batch.Vectors {
					vector, normalizeErr := normalized(batch.Vectors[vectorIndex])
					if normalizeErr != nil {
						results[index].err = fmt.Errorf(
							"normalize QA evaluation embedding: %w",
							normalizeErr,
						)
						return
					}
					results[index].vectors[vectorIndex] = vector
				}
			}(batchIndex, texts)
		}
		wait.Wait()
		for _, result := range results {
			if result.err != nil {
				return nil, fmt.Errorf(
					"embed QA evaluation batch at case %d: %w",
					result.start,
					result.err,
				)
			}
			copy(vectors[result.start:], result.vectors)
		}
	}
	return vectors, nil
}

func Evaluate(ctx context.Context, store *Store, cases []QACase, config EvaluationConfig) (EvaluationReport, error) {
	if store == nil || config.TenantID == "" || config.MemberID == "" ||
		config.DeniedMemberID == "" || config.MemberID == config.DeniedMemberID || len(cases) == 0 {
		return EvaluationReport{}, errors.New("RAG evaluation dependencies are invalid")
	}
	if err := store.validateEvaluationMembers(ctx, config); err != nil {
		return EvaluationReport{}, err
	}
	report := EvaluationReport{
		SchemaVersion: 5, ProjectionRevision: store.config.ProjectionRevision,
		Cases: len(cases), ACLDeniedCases: len(cases),
		Failures: make([]EvaluationFailure, 0),
	}
	vectors, err := store.embedEvaluationQuestions(ctx, cases)
	if err != nil {
		return report, err
	}
	var recall5, recall10, reciprocalRanks, ndcg10, precision5, precision10 float64
	var abstentions, integrity, checksumIntegrity, rankedItems, aclLeaks, staleLeaks float64
	var staleCases int
	for caseIndex, item := range cases {
		query := Query{
			TenantID: config.TenantID, MemberID: config.MemberID,
			Purpose: "agent_answer", Text: item.Question, Limit: maxEvidenceItems,
		}
		terms := lexicalTerms(item.Question)
		if len(terms) == 0 {
			return report, fmt.Errorf("QA case %s has no searchable terms", item.QAID)
		}
		candidates, err := store.rerankedCandidatesWithVector(ctx, query, terms, vectors[caseIndex])
		if err != nil {
			return report, fmt.Errorf("evaluate QA case %s: %w", item.QAID, err)
		}
		results := selectEvaluationEvidence(candidates, 10)
		deniedCandidates, err := store.authorizedCandidates(ctx, Query{
			TenantID: config.TenantID, MemberID: config.DeniedMemberID,
			Purpose: "agent_answer", Text: item.Question, Limit: maxEvidenceItems,
		}, terms, vectors[caseIndex])
		if err != nil {
			return report, fmt.Errorf("evaluate denied QA case %s: %w", item.QAID, err)
		}
		if len(deniedCandidates) > 0 {
			aclLeaks++
			appendEvaluationFailure(&report, item.QAID, "unauthorized member retrieved evidence")
		}
		for index, evidence := range results {
			rankedItems++
			if evidence.CitationID == fmt.Sprintf("C%d", index+1) &&
				evidence.DocumentID != "" && evidence.VersionID != "" &&
				evidence.ChunkID != "" && evidence.Checksum != "" &&
				evidence.IndexRevision == store.config.ModelRevision &&
				evidence.ProjectionRevision == store.config.ProjectionRevision {
				integrity++
			}
			checksum := sha256.Sum256([]byte(evidence.Content))
			if evidence.Checksum == fmt.Sprintf("sha256:%x", checksum[:]) {
				checksumIntegrity++
			} else {
				appendEvaluationFailure(&report, item.QAID, "retrieved evidence checksum mismatch")
			}
		}
		if len(item.NegativeVersionIDs) > 0 {
			staleCases++
			negative := make(map[string]struct{}, len(item.NegativeVersionIDs))
			for _, versionID := range item.NegativeVersionIDs {
				negative[versionID] = struct{}{}
			}
			leaked := false
			for _, evidence := range results {
				if _, exists := negative[evidence.VersionID]; exists {
					leaked = true
					break
				}
			}
			if leaked {
				staleLeaks++
				appendEvaluationFailure(&report, item.QAID, "superseded version retrieved")
			}
		}
		if !item.Answerable {
			report.UnanswerableCases++
			if len(results) == 0 {
				abstentions++
			}
			continue
		}
		report.AnswerableCases++
		expected := make(map[string]struct{}, len(item.Evidence))
		for _, evidence := range item.Evidence {
			expected[evidence.ChunkID] = struct{}{}
		}
		firstRank, relevant5, relevant10 := rankedRelevance(results, expected)
		if firstRank > 0 && firstRank <= 5 {
			recall5++
		}
		if firstRank > 0 {
			recall10++
			reciprocalRanks += 1 / float64(firstRank)
		} else {
			appendEvaluationFailure(&report, item.QAID, "expected evidence not retrieved")
		}
		precision5 += float64(relevant5) / 5
		precision10 += float64(relevant10) / 10
		ndcg10 += normalizedDiscountedGain(results, expected)
	}
	if report.AnswerableCases > 0 {
		report.RecallAt5 = recall5 / float64(report.AnswerableCases)
		report.RecallAt10 = recall10 / float64(report.AnswerableCases)
		report.RecallAtK = report.RecallAt10
		report.MRR = reciprocalRanks / float64(report.AnswerableCases)
		report.NDCGAt10 = ndcg10 / float64(report.AnswerableCases)
		report.PrecisionAt5 = precision5 / float64(report.AnswerableCases)
		report.PrecisionAt10 = precision10 / float64(report.AnswerableCases)
		report.RetrievalPrecisionAtK = report.PrecisionAt10
	}
	if rankedItems > 0 {
		report.ProvenanceIntegrity = integrity / rankedItems
		report.ChecksumIntegrity = checksumIntegrity / rankedItems
	}
	if report.UnanswerableCases > 0 {
		report.UnanswerableEmptyRate = abstentions / float64(report.UnanswerableCases)
	}
	report.ACLLeakageRate = aclLeaks / float64(report.ACLDeniedCases)
	if staleCases > 0 {
		report.StaleVersionLeakage = staleLeaks / float64(staleCases)
	}
	return report, nil
}

func (s *Store) validateEvaluationMembers(ctx context.Context, config EvaluationConfig) error {
	var authorizedActive, deniedActive, authorizedGrants, deniedGrants int
	if err := s.pool.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE id = $2::uuid AND status = 'active')::integer,
  count(*) FILTER (WHERE id = $3::uuid AND status = 'active')::integer,
  (SELECT count(*)::integer FROM authz.document_grants
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid AND permission = 'read'),
  (SELECT count(*)::integer FROM authz.document_grants
    WHERE tenant_id = $1::uuid AND member_id = $3::uuid AND permission = 'read')
FROM identity.members
WHERE tenant_id = $1::uuid AND id IN ($2::uuid, $3::uuid)`,
		config.TenantID, config.MemberID, config.DeniedMemberID,
	).Scan(&authorizedActive, &deniedActive, &authorizedGrants, &deniedGrants); err != nil {
		return fmt.Errorf("validate evaluation members: %w", err)
	}
	if authorizedActive != 1 || deniedActive != 1 || authorizedGrants < 1 || deniedGrants != 0 {
		return errors.New("evaluation requires one active granted member and one active zero-grant member")
	}
	return nil
}

func rankedRelevance(results []Evidence, expected map[string]struct{}) (firstRank, relevant5, relevant10 int) {
	for index, evidence := range results {
		if _, ok := expected[evidence.ChunkID]; !ok {
			continue
		}
		rank := index + 1
		if firstRank == 0 {
			firstRank = rank
		}
		if rank <= 5 {
			relevant5++
		}
		if rank <= 10 {
			relevant10++
		}
	}
	return firstRank, relevant5, relevant10
}

func normalizedDiscountedGain(results []Evidence, expected map[string]struct{}) float64 {
	if len(expected) == 0 {
		return 0
	}
	var dcg float64
	for index, evidence := range results {
		if _, ok := expected[evidence.ChunkID]; ok {
			dcg += 1 / math.Log2(float64(index+2))
		}
	}
	idealCount := min(len(expected), 10)
	var ideal float64
	for index := 0; index < idealCount; index++ {
		ideal += 1 / math.Log2(float64(index+2))
	}
	if ideal == 0 {
		return 0
	}
	return dcg / ideal
}

func appendEvaluationFailure(report *EvaluationReport, qaID, reason string) {
	if len(report.Failures) < 200 {
		report.Failures = append(report.Failures, EvaluationFailure{QAID: qaID, Reason: reason})
	}
}
