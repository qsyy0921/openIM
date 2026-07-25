package retrieval

import (
	"context"
	"errors"
	"fmt"
)

type RankingDiagnosticReport struct {
	SchemaVersion      int                     `json:"schema_version"`
	ProjectionRevision string                  `json:"projection_revision"`
	Cases              int                     `json:"cases"`
	Items              []RankingDiagnosticCase `json:"items"`
}

type RankingDiagnosticCase struct {
	QAID string                  `json:"qa_id"`
	Gold []RankingDiagnosticGold `json:"gold"`
}

type RankingDiagnosticGold struct {
	ChunkID     string `json:"chunk_id"`
	Candidate   bool   `json:"candidate"`
	LexicalRank int    `json:"lexical_rank"`
	DenseRank   int    `json:"dense_rank"`
	FusionRank  int    `json:"fusion_rank"`
	RerankRank  int    `json:"rerank_rank"`
}

func DiagnoseRanking(
	ctx context.Context,
	store *Store,
	cases []QACase,
	tenantID string,
	memberID string,
) (RankingDiagnosticReport, error) {
	if store == nil || tenantID == "" || memberID == "" || len(cases) == 0 {
		return RankingDiagnosticReport{}, errors.New("ranking diagnostic dependencies are invalid")
	}
	vectors, err := store.embedEvaluationQuestions(ctx, cases)
	if err != nil {
		return RankingDiagnosticReport{}, err
	}
	report := RankingDiagnosticReport{
		SchemaVersion:      1,
		ProjectionRevision: store.config.ProjectionRevision,
		Cases:              len(cases),
		Items:              make([]RankingDiagnosticCase, 0, len(cases)),
	}
	for index, item := range cases {
		terms := lexicalTerms(item.Question)
		if len(terms) == 0 {
			return RankingDiagnosticReport{}, fmt.Errorf(
				"QA case %s has no searchable terms",
				item.QAID,
			)
		}
		query := Query{
			TenantID: tenantID,
			MemberID: memberID,
			Purpose:  "retrieval_diagnostic",
			Text:     item.Question,
			Limit:    maxEvidenceItems,
		}
		candidates, err := store.authorizedCandidates(ctx, query, terms, vectors[index])
		if err != nil {
			return RankingDiagnosticReport{}, fmt.Errorf(
				"diagnose QA case %s candidates: %w",
				item.QAID,
				err,
			)
		}
		fusionRanks := make(map[string]int, len(candidates))
		for rank, candidate := range candidates {
			fusionRanks[candidate.evidence.ChunkID] = rank + 1
		}
		if len(candidates) > 0 {
			if err := store.rerankCandidates(ctx, item.Question, candidates); err != nil {
				return RankingDiagnosticReport{}, fmt.Errorf(
					"diagnose QA case %s reranker: %w",
					item.QAID,
					err,
				)
			}
		}
		report.Items = append(report.Items, buildRankingDiagnosticCase(item, candidates, fusionRanks))
	}
	return report, nil
}

func buildRankingDiagnosticCase(
	item QACase,
	reranked []candidate,
	fusionRanks map[string]int,
) RankingDiagnosticCase {
	byID := make(map[string]candidate, len(reranked))
	rerankRanks := make(map[string]int, len(reranked))
	for rank, candidate := range reranked {
		chunkID := candidate.evidence.ChunkID
		byID[chunkID] = candidate
		rerankRanks[chunkID] = rank + 1
	}
	gold := make([]RankingDiagnosticGold, 0, len(item.Evidence))
	seen := make(map[string]struct{}, len(item.Evidence))
	for _, expected := range item.Evidence {
		if _, exists := seen[expected.ChunkID]; exists {
			continue
		}
		seen[expected.ChunkID] = struct{}{}
		entry := RankingDiagnosticGold{ChunkID: expected.ChunkID}
		if candidate, exists := byID[expected.ChunkID]; exists {
			entry.Candidate = true
			entry.LexicalRank = candidate.lexicalRank
			entry.DenseRank = candidate.denseRank
			entry.FusionRank = fusionRanks[expected.ChunkID]
			entry.RerankRank = rerankRanks[expected.ChunkID]
		}
		gold = append(gold, entry)
	}
	return RankingDiagnosticCase{QAID: item.QAID, Gold: gold}
}
