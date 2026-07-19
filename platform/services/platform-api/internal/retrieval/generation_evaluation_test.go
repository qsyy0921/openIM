package retrieval

import (
	"context"
	"errors"
	"testing"
)

type generationSearchStub struct{ byQuestion map[string][]Evidence }

func (s generationSearchStub) Search(_ context.Context, query Query) ([]Evidence, error) {
	return s.byQuestion[query.Text], nil
}

type generationProviderStub struct {
	byCase map[string]GenerationCandidate
	err    error
}

func (s generationProviderStub) Generate(_ context.Context, request GenerationRequest) (GenerationCandidate, error) {
	if s.err != nil {
		return GenerationCandidate{}, s.err
	}
	return s.byCase[request.CaseID], nil
}

func TestEvaluateGenerationSeparatesGroundedAnswerAndAbstention(t *testing.T) {
	cases := []QACase{
		{QAID: "answerable", Question: "审批时限？", Answerable: true, RequiredFacts: []string{"48小时"}, Evidence: []QAEvidence{{ChunkID: "chunk-a"}}},
		{QAID: "unanswerable", Question: "未记录承诺？", Answerable: false},
	}
	search := generationSearchStub{byQuestion: map[string][]Evidence{
		"审批时限？":  {{CitationID: "C1", ChunkID: "chunk-a", Content: "审批时限为48小时"}},
		"未记录承诺？": {{CitationID: "C1", ChunkID: "chunk-b", Content: "会议纪要没有相关内容"}},
	}}
	provider := generationProviderStub{byCase: map[string]GenerationCandidate{
		"answerable":   {Text: "审批时限为 48 小时 [C1]", Model: "model", ProviderResponseID: "a", CitationIDs: []string{"C1"}, GroundingStatus: GenerationGrounded},
		"unanswerable": {Text: "现有证据不足以回答。", Model: "model", ProviderResponseID: "b", GroundingStatus: GenerationInsufficientEvidence},
	}}
	report, err := EvaluateGeneration(context.Background(), search, provider, cases, GenerationEvaluationConfig{
		TenantID: "tenant", MemberID: "member", Model: "model", Seed: "seed",
		AnswerableCases: 1, UnanswerableCases: 1, RetrievalLimit: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases != 2 || report.GroundingDecisionAccuracy != 1 || report.AbstentionAccuracy != 1 || report.RequiredFactCoverage != 1 || report.CitationPrecision != 1 || report.CitationRecall != 1 || report.CitationSyntaxIntegrity != 1 || report.EndToEndSuccessRate != 1 {
		t.Fatalf("unexpected generation report: %#v", report)
	}
}

func TestEvaluateGenerationPreservesProviderFailure(t *testing.T) {
	cases := []QACase{
		{QAID: "a", Question: "a", Answerable: true, Evidence: []QAEvidence{{ChunkID: "chunk-a"}}},
		{QAID: "u", Question: "u", Answerable: false},
	}
	search := generationSearchStub{byQuestion: map[string][]Evidence{
		"a": {{CitationID: "C1", ChunkID: "chunk-a", Content: "a"}},
		"u": {{CitationID: "C1", ChunkID: "chunk-u", Content: "u"}},
	}}
	report, err := EvaluateGeneration(context.Background(), search, generationProviderStub{err: errors.New("provider down")}, cases, GenerationEvaluationConfig{
		TenantID: "tenant", MemberID: "member", Model: "model", Seed: "seed",
		AnswerableCases: 1, UnanswerableCases: 1, RetrievalLimit: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProviderFailures != 2 || report.EndToEndSuccessRate != 0 || len(report.Failures) != 2 {
		t.Fatalf("provider failure was hidden: %#v", report)
	}
}

func TestGenerationCaseSelectionIsStable(t *testing.T) {
	cases := []QACase{
		{QAID: "a1", Answerable: true}, {QAID: "a2", Answerable: true},
		{QAID: "u1", Answerable: false}, {QAID: "u2", Answerable: false},
	}
	first, firstDigest, err := selectGenerationCases(cases, 1, 1, "frozen")
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := selectGenerationCases(cases, 1, 1, "frozen")
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || first[0].QAID != second[0].QAID || first[1].QAID != second[1].QAID {
		t.Fatal("generation sample is not deterministic")
	}
}
