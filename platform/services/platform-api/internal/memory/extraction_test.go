package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractionResultRejectsDuplicateAndSensitiveFacts(t *testing.T) {
	fact := ExtractedFact{Category: "preference", Subject: "style", Content: "concise", Confidence: 0.9}
	if err := (ExtractionResult{ProviderResponseID: "response", Facts: []ExtractedFact{fact, fact}}).Validate(); err == nil {
		t.Fatal("duplicate extracted facts accepted")
	}
	secret := ExtractedFact{Category: "context", Subject: "credential", Content: "API Key 是 sk-secret", Confidence: 0.99}
	if err := secret.Validate(); err == nil {
		t.Fatal("sensitive extracted fact accepted")
	}
}

func TestExtractionClientUsesStrictEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/memory-extractions" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["run_id"] != "run-1" || body["user_message"] != "remember" {
			t.Fatalf("body=%#v", body)
		}
		_ = json.NewEncoder(w).Encode(ExtractionResult{
			ProviderResponseID: "response-1",
			Facts:              []ExtractedFact{{Category: "preference", Subject: "style", Content: "concise", Confidence: 0.9}},
		})
	}))
	defer server.Close()
	result, err := NewExtractionClient(server.URL, time.Second).Extract(context.Background(), ExtractionRequest{
		RunID: "run-1", UserMessage: "remember", AssistantResponse: "ok",
	})
	if err != nil || len(result.Facts) != 1 {
		t.Fatalf("Extract()=%#v, %v", result, err)
	}
}

type extractionJobStoreStub struct {
	job           *ExtractionJob
	saved         *ExtractionResult
	completed     bool
	groupProposed bool
	retryReason   string
}

func (*extractionJobStoreStub) EnqueueDelivered(context.Context) (bool, error) { return false, nil }
func (s *extractionJobStoreStub) Claim(context.Context, time.Duration, int) (*ExtractionJob, error) {
	return s.job, nil
}
func (s *extractionJobStoreStub) SaveResult(_ context.Context, _ ExtractionJob, result ExtractionResult) error {
	s.saved = &result
	return nil
}
func (s *extractionJobStoreStub) SaveGroupProposals(context.Context, ExtractionJob) error {
	s.groupProposed = true
	return nil
}
func (s *extractionJobStoreStub) Complete(context.Context, ExtractionJob) error {
	s.completed = true
	return nil
}
func (s *extractionJobStoreStub) Retry(_ context.Context, _ ExtractionJob, failure string, _ int, _ time.Duration) error {
	s.retryReason = failure
	return nil
}

type extractionModelStub struct {
	result ExtractionResult
	err    error
}

func (s extractionModelStub) Extract(context.Context, ExtractionRequest) (ExtractionResult, error) {
	return s.result, s.err
}

type eventAppenderStub struct {
	keys []string
}

func (s *eventAppenderStub) AppendUpsert(_ context.Context, _ Scope, factKey, _, _ string, _ FactPayload) (string, error) {
	s.keys = append(s.keys, factKey)
	return "event", nil
}

func TestExtractionWorkerPersistsModelResultBeforeProjection(t *testing.T) {
	jobs := &extractionJobStoreStub{}
	worker := NewExtractionWorker(jobs, extractionModelStub{result: ExtractionResult{
		ProviderResponseID: "response-1",
		Facts:              []ExtractedFact{{Category: "preference", Subject: "style", Content: "concise", Confidence: 0.9}},
	}}, &eventAppenderStub{}, time.Second, time.Second, 3)
	worker.process(context.Background(), ExtractionJob{ID: "job", RunID: "run", Phase: "extracting"})
	if jobs.saved == nil || jobs.completed || jobs.retryReason != "" {
		t.Fatalf("jobs=%#v", jobs)
	}
}

func TestExtractionWorkerProjectsPersistedFactsIdempotently(t *testing.T) {
	jobs := &extractionJobStoreStub{}
	events := &eventAppenderStub{}
	fact := ExtractedFact{Category: "profile", Subject: "role", Content: "backend engineer", Confidence: 0.9}
	worker := NewExtractionWorker(jobs, extractionModelStub{}, events, time.Second, time.Second, 3)
	worker.process(context.Background(), ExtractionJob{
		ID: "job", TenantID: "tenant", RunID: "run", MemberID: "member", Phase: "projecting", ExtractedFacts: []ExtractedFact{fact},
	})
	if !jobs.completed || len(events.keys) != 1 || events.keys[0] != fact.FactKey() {
		t.Fatalf("completed=%v keys=%v", jobs.completed, events.keys)
	}
}

func TestExtractionWorkerRequiresReviewForGroupFacts(t *testing.T) {
	jobs := &extractionJobStoreStub{}
	events := &eventAppenderStub{}
	worker := NewExtractionWorker(jobs, extractionModelStub{}, events, time.Second, time.Second, 3)
	worker.process(context.Background(), ExtractionJob{
		ID: "job", TenantID: "tenant", RunID: "run", MemberID: "member", Phase: "projecting",
		SessionType: 2, SourceChannel: "openim", ConversationID: "sg_group",
		ExtractedFacts: []ExtractedFact{{Category: "procedure", Subject: "release", Content: "双人复核", Confidence: 0.9}},
	})
	if !jobs.groupProposed || !jobs.completed || len(events.keys) != 0 {
		t.Fatalf("groupProposed=%v completed=%v directEvents=%v", jobs.groupProposed, jobs.completed, events.keys)
	}
}

func TestExtractionWorkerRetriesProviderFailure(t *testing.T) {
	jobs := &extractionJobStoreStub{}
	worker := NewExtractionWorker(jobs, extractionModelStub{err: errors.New("provider unavailable")}, &eventAppenderStub{}, time.Second, time.Second, 3)
	worker.process(context.Background(), ExtractionJob{ID: "job", RunID: "run", Phase: "extracting", ModelAttempts: 1})
	if jobs.retryReason != "provider unavailable" {
		t.Fatalf("retry=%q", jobs.retryReason)
	}
}
