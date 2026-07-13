package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
)

type runtimeStoreStub struct {
	run              *Run
	version          CatalogVersion
	loadErr          error
	savedCandidate   *Candidate
	savedEvidence    []Evidence
	failed           bool
	failure          string
	retryMaxAttempts int
}

func (s *runtimeStoreStub) Claim(context.Context, time.Duration, int) (*Run, error) {
	return s.run, nil
}
func (s *runtimeStoreStub) LoadCatalogVersion(context.Context, Run) (CatalogVersion, error) {
	return s.version, s.loadErr
}
func (s *runtimeStoreStub) SaveCandidate(_ context.Context, _ Run, candidate Candidate, evidence []Evidence) error {
	s.savedCandidate, s.savedEvidence = &candidate, evidence
	return nil
}
func (*runtimeStoreStub) EnsureBotIdentity(context.Context, string, string) error { return nil }
func (*runtimeStoreStub) CompleteReply(context.Context, Run, string, bool) error  { return nil }
func (s *runtimeStoreStub) FailOrRetry(_ context.Context, _ Run, failure string, maxAttempts int, _ time.Duration) error {
	s.failure, s.retryMaxAttempts = failure, maxAttempts
	return nil
}
func (s *runtimeStoreStub) Fail(_ context.Context, _ Run, failure string) error {
	s.failed, s.failure = true, failure
	return nil
}

type retrieverStub struct {
	query  RetrievalQuery
	items  []Evidence
	called bool
}

func (s *retrieverStub) Search(_ context.Context, query RetrievalQuery) ([]Evidence, error) {
	s.called, s.query = true, query
	return s.items, nil
}

type candidateGeneratorStub struct {
	version CatalogVersion
	result  Candidate
	called  bool
}

func (s *candidateGeneratorStub) Generate(_ context.Context, _ Run, version CatalogVersion, _ []Evidence) (Candidate, error) {
	s.called, s.version = true, version
	return s.result, nil
}

type intentManagerStub struct{}

func (intentManagerStub) EnsureIntent(context.Context, IntentRequest) (Intent, error) {
	return Intent{}, errors.New("unexpected intent call")
}

type messageSenderStub struct{}

func (messageSenderStub) EnsureAgentBot(context.Context, string, string) error { return nil }
func (messageSenderStub) SendText(context.Context, string, openim.TextTarget, string, string) (openim.SendResult, error) {
	return openim.SendResult{}, errors.New("unexpected send call")
}

func TestWorkerUsesPinnedCatalogPolicyForCandidatePhase(t *testing.T) {
	run := &Run{ID: "run-1", TenantID: "tenant-1", MemberID: "member-1", AgentID: "agent-1", AgentVersionID: "version-2", Prompt: "question", LeaseToken: "lease", Attempts: 1}
	version := CatalogVersion{
		AgentID: "agent-1", VersionID: "version-2", VersionNumber: 2,
		Spec: AgentSpec{
			RuntimeKind: KnowledgeTicketRuntime, Instructions: "bounded", ModelRoute: DeepSeekV4ProRoute,
			Retrieval: RetrievalSpec{Purpose: "agent_answer", Limit: 3}, MaxModelAttempts: 2,
		},
	}
	store := &runtimeStoreStub{run: run, version: version}
	retriever := &retrieverStub{items: []Evidence{{CitationID: "C1", DocumentID: "doc-1"}}}
	candidates := &candidateGeneratorStub{result: Candidate{Text: "answer [C1]", Model: "model", ProviderResponseID: "response", CitationIDs: []string{"C1"}}}
	worker := NewWorker(store, candidates, retriever, intentManagerStub{}, messageSenderStub{}, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !retriever.called || retriever.query.Limit != 3 || retriever.query.Purpose != "agent_answer" {
		t.Fatalf("retrieval query = %#v", retriever.query)
	}
	if !candidates.called || candidates.version.VersionID != "version-2" {
		t.Fatalf("candidate version = %#v", candidates.version)
	}
	if store.savedCandidate == nil || len(store.savedEvidence) != 1 || store.savedEvidence[0].CitationID != "C1" {
		t.Fatalf("saved candidate=%#v evidence=%#v", store.savedCandidate, store.savedEvidence)
	}
}

func TestWorkerFailsInvalidPinnedCatalogBeforeDependencies(t *testing.T) {
	store := &runtimeStoreStub{
		run:     &Run{ID: "run-1", AgentID: "agent-1", AgentVersionID: "missing", LeaseToken: "lease"},
		loadErr: fmtCatalogError("missing version"),
	}
	retriever := &retrieverStub{}
	candidates := &candidateGeneratorStub{}
	worker := NewWorker(store, candidates, retriever, intentManagerStub{}, messageSenderStub{}, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !store.failed || retriever.called || candidates.called {
		t.Fatalf("failed=%v retriever=%v candidate=%v", store.failed, retriever.called, candidates.called)
	}
}

func fmtCatalogError(detail string) error {
	return errors.Join(ErrInvalidCatalog, errors.New(detail))
}
