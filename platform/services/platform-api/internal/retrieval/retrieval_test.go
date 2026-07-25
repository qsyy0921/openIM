package retrieval

import (
	"reflect"
	"testing"
)

func TestLexicalTermsAreBoundedAndNormalized(t *testing.T) {
	got := lexicalTerms("  OpenIM, openim 权限 检索！x ")
	want := []string{"openim", "权限", "检索"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lexicalTerms() = %#v", got)
	}
}

func TestApplyRerankerScoresFusesRetrievalAndRerankerRanks(t *testing.T) {
	items := []candidate{
		{evidence: Evidence{ChunkID: "fusion-first"}, fusionScore: 0.04},
		{evidence: Evidence{ChunkID: "fusion-second"}, fusionScore: 0.03},
		{evidence: Evidence{ChunkID: "fusion-third"}, fusionScore: 0.02},
		{evidence: Evidence{ChunkID: "fusion-fourth"}, fusionScore: 0.01},
	}
	response := RerankResponse{
		Model:    LockedRerankerModel,
		Revision: LockedRerankerRevision,
		Scores: []RerankScore{
			{CandidateID: "fusion-first", Score: 0.1},
			{CandidateID: "fusion-second", Score: 0.2},
			{CandidateID: "fusion-third", Score: 0.3},
			{CandidateID: "fusion-fourth", Score: 0.4},
		},
	}
	config := Config{
		RerankerModel:    LockedRerankerModel,
		RerankerRevision: LockedRerankerRevision,
	}

	if err := applyRerankerScores(items, response, config); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(items))
	for index := range items {
		got[index] = items[index].evidence.ChunkID
	}
	want := []string{
		"fusion-first",
		"fusion-fourth",
		"fusion-second",
		"fusion-third",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("final rank order = %#v, want %#v", got, want)
	}
	if items[0].rerankScore != 0.1 || items[1].rerankScore != 0.4 {
		t.Fatalf("raw reranker scores were not preserved: %#v", items)
	}
}

func TestApplyRerankerScoresStillFailsClosed(t *testing.T) {
	items := []candidate{{evidence: Evidence{ChunkID: "chunk-1"}}}
	response := RerankResponse{
		Model:    LockedRerankerModel,
		Revision: LockedRerankerRevision,
		Scores:   []RerankScore{{CandidateID: "other", Score: 1}},
	}
	config := Config{
		RerankerModel:    LockedRerankerModel,
		RerankerRevision: LockedRerankerRevision,
	}
	if err := applyRerankerScores(items, response, config); err == nil {
		t.Fatal("expected missing authorized candidate to fail closed")
	}
}
