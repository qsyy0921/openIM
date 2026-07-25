package retrieval

import (
	"encoding/json"
	"testing"
)

func TestBuildRankingDiagnosticCaseSeparatesFusionAndRerankerRanks(t *testing.T) {
	item := QACase{
		QAID: "qa-1",
		Evidence: []QAEvidence{
			{ChunkID: "gold-present"},
			{ChunkID: "gold-absent"},
			{ChunkID: "gold-present"},
		},
	}
	reranked := []candidate{
		{evidence: Evidence{ChunkID: "noise"}},
		{
			evidence:    Evidence{ChunkID: "gold-present"},
			lexicalRank: 3,
			denseRank:   7,
		},
	}
	result := buildRankingDiagnosticCase(
		item,
		reranked,
		map[string]int{"noise": 1, "gold-present": 5},
	)
	if len(result.Gold) != 2 {
		t.Fatalf("gold count = %d, want 2", len(result.Gold))
	}
	present := result.Gold[0]
	if !present.Candidate || present.LexicalRank != 3 || present.DenseRank != 7 ||
		present.FusionRank != 5 || present.RerankRank != 2 {
		t.Fatalf("unexpected present diagnostic: %#v", present)
	}
	absent := result.Gold[1]
	if absent.Candidate || absent.LexicalRank != 0 || absent.DenseRank != 0 ||
		absent.FusionRank != 0 || absent.RerankRank != 0 {
		t.Fatalf("unexpected absent diagnostic: %#v", absent)
	}
}

func TestRankingDiagnosticJSONContainsNoQuestionOrContent(t *testing.T) {
	report := RankingDiagnosticReport{
		SchemaVersion:      1,
		ProjectionRevision: "document-title-content-v1",
		Cases:              1,
		Items: []RankingDiagnosticCase{{
			QAID: "qa-1",
			Gold: []RankingDiagnosticGold{{
				ChunkID: "chunk-1", Candidate: true, FusionRank: 2, RerankRank: 6,
			}},
		}},
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]struct{}{
		"question": {}, "content": {}, "title": {}, "excerpt": {}, "answer": {},
	}
	if key, found := findJSONKey(value, forbidden); found {
		t.Fatalf("diagnostic leaked field %q: %s", key, encoded)
	}
}

func findJSONKey(value any, forbidden map[string]struct{}) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, found := forbidden[key]; found {
				return key, true
			}
			if key, found := findJSONKey(child, forbidden); found {
				return key, true
			}
		}
	case []any:
		for _, child := range typed {
			if key, found := findJSONKey(child, forbidden); found {
				return key, true
			}
		}
	}
	return "", false
}
