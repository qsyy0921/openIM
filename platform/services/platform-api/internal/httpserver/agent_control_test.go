package httpserver

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agentcontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
)

type agentControlStub struct {
	deviceID     string
	platformID   int32
	deleteErr    error
	subscription agentcontrol.CreateSubscriptionRequest
}

func (s *agentControlStub) GetMemory(_ context.Context, _, deviceID string, platformID int32) (agentcontrol.MemorySnapshot, error) {
	s.deviceID, s.platformID = deviceID, platformID
	return agentcontrol.MemorySnapshot{Facts: []agentcontrol.MemoryFact{{ID: "fact-1", Category: "preference", Content: "concise"}}}, nil
}
func (s *agentControlStub) DeleteMemoryFact(context.Context, string, string, int32, string, string) (string, error) {
	return "event-1", s.deleteErr
}
func (*agentControlStub) FeedbackMemory(context.Context, string, string, int32, string, string) error {
	return nil
}
func (*agentControlStub) GetProactive(context.Context, string, string, int32) (agentcontrol.ProactiveSnapshot, error) {
	return agentcontrol.ProactiveSnapshot{}, nil
}
func (s *agentControlStub) CreateSubscription(_ context.Context, _ string, _ string, _ int32, request agentcontrol.CreateSubscriptionRequest) (string, error) {
	s.subscription = request
	return "subscription-1", nil
}
func (*agentControlStub) SetSubscriptionEnabled(context.Context, string, string, int32, string, bool) error {
	return nil
}
func (*agentControlStub) UpdatePreferences(context.Context, string, string, int32, proactive.Preference) error {
	return nil
}
func (*agentControlStub) Acknowledge(context.Context, string, string, int32, string, string) error {
	return nil
}
func (*agentControlStub) ListToolApprovals(context.Context, string, string, int32) ([]agentcontrol.ToolApproval, error) {
	return nil, nil
}
func (*agentControlStub) DecideToolApproval(context.Context, string, string, int32, string, string, string) error {
	return nil
}
func (*agentControlStub) GetReplay(context.Context, string, string, int32, string) (observe.Bundle, error) {
	return observe.Bundle{SchemaVersion: 1}, nil
}
func (*agentControlStub) ListDelegations(context.Context, string, string, int32) ([]agentcontrol.Delegation, error) {
	return []agentcontrol.Delegation{{ID: "job-1", State: "running"}}, nil
}
func (*agentControlStub) GetGroupMemory(context.Context, string, string, int32, string, string) (agentcontrol.GroupMemorySnapshot, error) {
	return agentcontrol.GroupMemorySnapshot{}, nil
}
func (*agentControlStub) ReviewGroupMemory(context.Context, string, string, int32, string, string, string, string) (string, error) {
	return "", nil
}

func controlHandler(service AgentControlService) http.Handler {
	return newHandler("test-version", stubSessionService{}, stubDeviceService{}, stubApprovalService{},
		stubAgentWorkspaceService{}, stubAgentCatalogService{}, service, nil)
}

func TestAgentMemoryUsesAuthenticatedDeviceContext(t *testing.T) {
	service := &agentControlStub{}
	request := httptest.NewRequest(http.MethodGet, "/v1/agent/memory?platform_id=5&device_id=browser", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()

	controlHandler(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if service.deviceID != "browser" || service.platformID != 5 {
		t.Fatalf("device context = %q/%d", service.deviceID, service.platformID)
	}
}

func TestAgentMemoryDeleteRequiresIdempotencyKey(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/v1/agent/memory/facts/fact-1",
		bytes.NewBufferString(`{"platform_id":5,"device_id":"browser"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	controlHandler(&agentControlStub{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentMemoryDeleteMapsOwnedNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/v1/agent/memory/facts/fact-1",
		bytes.NewBufferString(`{"platform_id":5,"device_id":"browser"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Idempotency-Key", "request-1")
	recorder := httptest.NewRecorder()

	controlHandler(&agentControlStub{deleteErr: memory.ErrFactNotFound}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestProactiveSubscriptionRejectsCallerSuppliedTarget(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/proactive/subscriptions", bytes.NewBufferString(
		`{"platform_id":5,"device_id":"browser","agent_id":"agent","query":"agents","categories":["cs.AI"],"source_channel":"openim","target_id":"victim","poll_interval_seconds":1800}`,
	))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()

	controlHandler(&agentControlStub{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentControlUnexpectedFailureDoesNotReportSuccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/v1/agent/memory/facts/fact-1",
		bytes.NewBufferString(`{"platform_id":5,"device_id":"browser"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Idempotency-Key", "request-1")
	recorder := httptest.NewRecorder()

	controlHandler(&agentControlStub{deleteErr: errors.New("database unavailable")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
