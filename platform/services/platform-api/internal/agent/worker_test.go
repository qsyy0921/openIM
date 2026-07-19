package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
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
	savedRoute       *RouteResult
	waitingApproval  string
}

func (s *runtimeStoreStub) Claim(context.Context, time.Duration, int) (*Run, error) {
	return s.run, nil
}
func (s *runtimeStoreStub) LoadCatalogVersion(context.Context, Run) (CatalogVersion, error) {
	return s.version, s.loadErr
}
func (*runtimeStoreStub) LoadCapabilitySnapshot(context.Context, Run) (capability.Snapshot, error) {
	return capability.Snapshot{}, nil
}
func (s *runtimeStoreStub) SaveRoute(_ context.Context, _ Run, route RouteResult) error {
	s.savedRoute = &route
	return nil
}
func (*runtimeStoreStub) SaveToolPlan(context.Context, Run, ToolPlan) error { return nil }
func (*runtimeStoreStub) SaveToolResult(context.Context, Run, any) error    { return nil }
func (s *runtimeStoreStub) WaitForToolApproval(_ context.Context, _ Run, approvalID string) error {
	s.waitingApproval = approvalID
	return nil
}
func (s *runtimeStoreStub) SaveCandidate(_ context.Context, _ Run, candidate Candidate, evidence []Evidence) error {
	s.savedCandidate, s.savedEvidence = &candidate, evidence
	return nil
}
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

func (s *retrieverStub) SearchKnowledge(_ context.Context, _ Run, _ capability.Snapshot, query RetrievalQuery) ([]Evidence, error) {
	s.called, s.query = true, query
	return s.items, nil
}
func (*retrieverStub) ExecuteTool(context.Context, Run, capability.Snapshot, ToolCallRequest) (ToolExecution, error) {
	return ToolExecution{}, errors.New("unexpected tool execution")
}

type candidateGeneratorStub struct {
	version CatalogVersion
	result  Candidate
	called  bool
}

func (s *candidateGeneratorStub) Generate(_ context.Context, _ Run, version CatalogVersion, _ []Evidence, _ []MemoryFact, _ []ToolResultContext) (Candidate, error) {
	s.called, s.version = true, version
	return s.result, nil
}

type toolPlannerStub struct{}

func (toolPlannerStub) Plan(context.Context, Run, CatalogVersion, capability.Descriptor) (ToolPlan, error) {
	return ToolPlan{}, errors.New("unexpected tool planning")
}

type memoryContextStub struct {
	items         []MemoryFact
	group         []MemoryFact
	searched      bool
	groupSearched bool
	exposures     []MemoryFact
	reason        string
}

func (s *memoryContextStub) SearchPersonal(context.Context, Run, string, int) ([]MemoryFact, error) {
	s.searched = true
	return s.items, nil
}

func (s *memoryContextStub) SearchGroup(context.Context, Run, string, int) ([]MemoryFact, error) {
	s.groupSearched = true
	return s.group, nil
}

func (s *memoryContextStub) RecordExposures(_ context.Context, _ Run, facts []MemoryFact, reason string) error {
	s.exposures, s.reason = facts, reason
	return nil
}

func TestWorkerLoadsPersonalAndAuthorizedGroupMemory(t *testing.T) {
	memoryContext := &memoryContextStub{
		items: []MemoryFact{{ID: "personal-1", Content: "personal"}},
		group: []MemoryFact{{ID: "group-1", Content: "group"}},
	}
	worker := &Worker{memory: memoryContext}
	facts, err := worker.loadMemory(context.Background(), Run{SessionType: 2, Prompt: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if !memoryContext.searched || !memoryContext.groupSearched || len(facts) != 2 || facts[1].ID != "group-1" {
		t.Fatalf("group memory load = %#v, personal=%v group=%v", facts, memoryContext.searched, memoryContext.groupSearched)
	}
}

func TestWorkerDoesNotReadGroupMemoryForSingleChat(t *testing.T) {
	memoryContext := &memoryContextStub{items: []MemoryFact{{ID: "personal-1"}}}
	worker := &Worker{memory: memoryContext}
	if _, err := worker.loadMemory(context.Background(), Run{SessionType: 1, Prompt: "question"}); err != nil {
		t.Fatal(err)
	}
	if memoryContext.groupSearched {
		t.Fatal("single chat read group memory")
	}
}

type intentManagerStub struct{}

func (intentManagerStub) EnsureIntent(context.Context, IntentRequest) (Intent, error) {
	return Intent{}, errors.New("unexpected intent call")
}

type intentRouterStub struct {
	result RouteResult
	called bool
}

func (s *intentRouterStub) Route(context.Context, Run, capability.Snapshot) (RouteResult, error) {
	s.called = true
	return s.result, nil
}

type deliveryManagerStub struct {
	request *DeliveryRequest
}

func (s *deliveryManagerStub) Prepare(_ context.Context, request DeliveryRequest) error {
	s.request = &request
	return nil
}

func TestWorkerUsesPinnedCatalogPolicyForCandidatePhase(t *testing.T) {
	run := validWorkerRun()
	run.AgentVersionID = "version-2"
	run.Prompt = "question"
	version := CatalogVersion{
		AgentID: "agent-1", VersionID: "version-2", VersionNumber: 2,
		Spec: AgentSpec{
			RuntimeKind: KnowledgeTicketRuntime, Instructions: "bounded", ModelRoute: DeepSeekV4ProRoute,
			Retrieval: RetrievalSpec{Purpose: "agent_answer", Limit: 3}, MaxModelAttempts: 2,
		},
	}
	store := &runtimeStoreStub{run: run, version: version}
	retriever := &retrieverStub{items: []Evidence{{CitationID: "C1", DocumentID: "doc-1"}}}
	candidates := &candidateGeneratorStub{result: Candidate{Text: "answer [C1]", Model: "model", ProviderResponseID: "response", CitationIDs: []string{"C1"}, GroundingStatus: GroundingGrounded}}
	memoryContext := &memoryContextStub{items: []MemoryFact{{ID: "memory-1", Category: "preference", Content: "concise"}}}
	worker := NewWorker(store, candidates, toolPlannerStub{}, retriever, memoryContext, intentManagerStub{}, &intentRouterStub{}, &deliveryManagerStub{}, time.Second, time.Second, 5)
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
	if !memoryContext.searched || len(memoryContext.exposures) != 1 || memoryContext.reason != "knowledge_response" {
		t.Fatalf("memory context=%#v", memoryContext)
	}
}

func TestWorkerFailsInvalidPinnedCatalogBeforeDependencies(t *testing.T) {
	store := &runtimeStoreStub{
		run:     validWorkerRun(),
		loadErr: fmtCatalogError("missing version"),
	}
	store.run.AgentVersionID = "missing"
	retriever := &retrieverStub{}
	candidates := &candidateGeneratorStub{}
	worker := NewWorker(store, candidates, toolPlannerStub{}, retriever, &memoryContextStub{}, intentManagerStub{}, &intentRouterStub{}, &deliveryManagerStub{}, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !store.failed || retriever.called || candidates.called {
		t.Fatalf("failed=%v retriever=%v candidate=%v", store.failed, retriever.called, candidates.called)
	}
}

func TestWorkerPreparesChannelDeliveryAfterCandidatePersistence(t *testing.T) {
	run := validWorkerRun()
	run.SourceChannel = "telegram"
	run.ConversationID = "tg_-10001"
	run.SessionType = 2
	run.CandidateText = "answer"
	run.Attempts = 2
	deliveries := &deliveryManagerStub{}
	worker := NewWorker(&runtimeStoreStub{run: run, version: CatalogVersion{}}, &candidateGeneratorStub{},
		toolPlannerStub{}, &retrieverStub{}, &memoryContextStub{}, intentManagerStub{}, &intentRouterStub{}, deliveries, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deliveries.request == nil || deliveries.request.Channel != "telegram" || deliveries.request.TargetID != "-10001" {
		t.Fatalf("delivery request = %#v", deliveries.request)
	}
}

func validWorkerRun() *Run {
	return &Run{
		ID: "run-1", TraceID: "trace-1", TenantID: "tenant-1", MemberID: "member-1",
		AgentID: "agent-1", AgentVersionID: "version-1", AgentSpecChecksum: "checksum", CapabilitySnapshotID: "capability-v1:abc",
		SourceChannel: "openim", ConversationID: "si_a_b", ExecutionPlane: "passive",
		SenderID: "user-1", SessionType: 1, LeaseToken: "lease", Attempts: 1,
		RouteStatus: "selected", RouteOperationID: "enterprise.knowledge.search",
	}
}

func TestWorkerPersistsRouteBeforeCallingRetriever(t *testing.T) {
	run := validWorkerRun()
	run.RouteStatus = ""
	run.RouteOperationID = ""
	descriptor := capability.Descriptor{
		ID: "tool-1", OperationID: "enterprise.knowledge.search", Version: "1", Name: "Search", Summary: "Search knowledge",
		SourceType: "core", SourceID: "retrieval", Risk: "read", Permissions: []string{"knowledge:read"},
		Idempotency: "native", RetrySemantics: "safe", Audience: "passive", Timeout: time.Second,
		ParameterTerms: []string{"knowledge"}, Examples: []string{"find policy"}, OutputKinds: []string{"text"},
		InputSchema: []byte(`{"type":"object"}`), SchemaDigest: "sha256:a2c799262a3ce3c19ef5cdd983bf3d12b43ab3c426227091b909dcb7054738c0",
	}
	snapshot := capability.Snapshot{
		ID: run.CapabilitySnapshotID, SchemaVersion: 1,
		Payload: []byte(`{"schema_version":1,"tools":[{"operation_id":"enterprise.knowledge.search","version":"1"}]}`),
		Tools:   []capability.Descriptor{descriptor},
	}
	run.CapabilitySnapshotID = "capability-v1:ec15cd82639295686f2108b3ef351dc22e546e60ffd81d8b7c1b37c1f199ba75"
	snapshot.ID = run.CapabilitySnapshotID
	store := &runtimeStoreWithSnapshot{runtimeStoreStub: runtimeStoreStub{run: run, version: CatalogVersion{}}, snapshot: snapshot}
	router := &intentRouterStub{result: RouteResult{
		Status: "selected", OperationID: "enterprise.knowledge.search", ProviderResponseID: "route-1",
		RouterVersion: IntentRouterVersion, Candidates: []RouteCandidate{{OperationID: "enterprise.knowledge.search"}},
	}}
	retriever := &retrieverStub{}
	worker := NewWorker(store, &candidateGeneratorStub{}, toolPlannerStub{}, retriever, &memoryContextStub{}, intentManagerStub{}, router, &deliveryManagerStub{}, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !router.called || store.savedRoute == nil || retriever.called {
		t.Fatalf("router=%v saved=%#v retriever=%v", router.called, store.savedRoute, retriever.called)
	}
}

func TestWorkerUsesModelAndMemoryForGeneralResponse(t *testing.T) {
	run := validWorkerRun()
	run.RouteStatus = "no_tool"
	run.RouteOperationID = ""
	run.Prompt = "你好"
	version := CatalogVersion{Spec: AgentSpec{ModelRoute: DeepSeekV4ProRoute, MaxModelAttempts: 2}}
	store := &runtimeStoreStub{run: run, version: version}
	candidates := &candidateGeneratorStub{result: Candidate{Text: "你好", Model: "model", ProviderResponseID: "response", GroundingStatus: GroundingNotApplicable}}
	memoryContext := &memoryContextStub{items: []MemoryFact{{ID: "memory-1", Category: "preference", Content: "concise"}}}
	worker := NewWorker(store, candidates, toolPlannerStub{}, &retrieverStub{}, memoryContext, intentManagerStub{}, &intentRouterStub{}, &deliveryManagerStub{}, time.Second, time.Second, 5)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !candidates.called || store.savedCandidate == nil || memoryContext.reason != "general_response" {
		t.Fatalf("candidate=%v saved=%#v memory=%#v", candidates.called, store.savedCandidate, memoryContext)
	}
}

type runtimeStoreWithSnapshot struct {
	runtimeStoreStub
	snapshot capability.Snapshot
}

func (s *runtimeStoreWithSnapshot) LoadCapabilitySnapshot(context.Context, Run) (capability.Snapshot, error) {
	return s.snapshot, nil
}

func fmtCatalogError(detail string) error {
	return errors.Join(ErrInvalidCatalog, errors.New(detail))
}
