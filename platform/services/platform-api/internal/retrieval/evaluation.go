package retrieval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type QAEvidence struct {
	ChunkID string `json:"chunk_id"`
}

type QACase struct {
	QAID          string       `json:"qa_id"`
	Question      string       `json:"question"`
	Answer        string       `json:"answer"`
	Answerable    bool         `json:"answerable"`
	RequiredFacts []string     `json:"required_facts"`
	Evidence      []QAEvidence `json:"evidence"`
}

type EvaluationReport struct {
	SchemaVersion         int                 `json:"schema_version"`
	Cases                 int                 `json:"cases"`
	AnswerableCases       int                 `json:"answerable_cases"`
	UnanswerableCases     int                 `json:"unanswerable_cases"`
	RecallAtK             float64             `json:"recall_at_k"`
	MRR                   float64             `json:"mrr"`
	RetrievalPrecisionAtK float64             `json:"retrieval_precision_at_k"`
	UnanswerableEmptyRate float64             `json:"unanswerable_retrieval_empty_rate"`
	GenerationEvaluated   bool                `json:"generation_abstention_evaluated"`
	ProvenanceIntegrity   float64             `json:"provenance_integrity"`
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

func Evaluate(ctx context.Context, store *Store, cases []QACase, tenantID, memberID string, limit int) (EvaluationReport, error) {
	if store == nil || tenantID == "" || memberID == "" || len(cases) == 0 {
		return EvaluationReport{}, errors.New("RAG evaluation dependencies are invalid")
	}
	report := EvaluationReport{SchemaVersion: 3, Cases: len(cases), Failures: make([]EvaluationFailure, 0)}
	vectors := make([][]float32, len(cases))
	for start := 0; start < len(cases); start += 128 {
		end := start + 128
		if end > len(cases) {
			end = len(cases)
		}
		texts := make([]string, end-start)
		for index := start; index < end; index++ {
			texts[index-start] = cases[index].Question
		}
		batch, err := store.embedder.Embed(ctx, texts)
		if err != nil {
			return report, fmt.Errorf("embed QA evaluation batch: %w", err)
		}
		if err := store.validateEmbeddingBatch(batch, len(texts)); err != nil {
			return report, err
		}
		for index := range batch.Vectors {
			vector, err := normalized(batch.Vectors[index])
			if err != nil {
				return report, fmt.Errorf("normalize QA evaluation embedding: %w", err)
			}
			vectors[start+index] = vector
		}
	}
	var recalls, reciprocalRanks, correctCitations, citations, abstentions, integrity float64
	for caseIndex, item := range cases {
		query := Query{TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer", Text: item.Question, Limit: limit}
		terms := lexicalTerms(item.Question)
		if len(terms) == 0 {
			return report, fmt.Errorf("QA case %s has no searchable terms", item.QAID)
		}
		results, err := store.searchWithVector(ctx, query, terms, vectors[caseIndex])
		if err != nil {
			return report, fmt.Errorf("evaluate QA case %s: %w", item.QAID, err)
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
		firstRank := 0
		for index, evidence := range results {
			citations++
			if evidence.CitationID == fmt.Sprintf("C%d", index+1) && evidence.DocumentID != "" && evidence.VersionID != "" && evidence.Checksum != "" {
				integrity++
			}
			if _, ok := expected[evidence.ChunkID]; ok {
				correctCitations++
				if firstRank == 0 {
					firstRank = index + 1
				}
			}
		}
		if firstRank > 0 {
			recalls++
			reciprocalRanks += 1 / float64(firstRank)
		} else if len(report.Failures) < 50 {
			report.Failures = append(report.Failures, EvaluationFailure{QAID: item.QAID, Reason: "expected evidence not retrieved"})
		}
	}
	if report.AnswerableCases > 0 {
		report.RecallAtK = recalls / float64(report.AnswerableCases)
		report.MRR = reciprocalRanks / float64(report.AnswerableCases)
	}
	if citations > 0 {
		report.RetrievalPrecisionAtK = correctCitations / citations
		report.ProvenanceIntegrity = integrity / citations
	}
	if report.UnanswerableCases > 0 {
		report.UnanswerableEmptyRate = abstentions / float64(report.UnanswerableCases)
	}
	return report, nil
}
