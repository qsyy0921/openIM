package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

func TestHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler("test-version", stubSessionService{}, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}

	var response healthResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := healthResponse{Status: "ready", Service: "platform-api", Version: "test-version"}
	if response != want {
		t.Fatalf("response = %#v, want %#v", response, want)
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler("test-version", stubSessionService{}, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

type stubSessionService struct{}
type stubApprovalService struct{}
type stubAgentWorkspaceService struct{}

func (stubAgentWorkspaceService) Get(context.Context, string, string, int32) (agent.Workspace, error) {
	return agent.Workspace{}, nil
}

func (stubApprovalService) Approve(context.Context, string, string, int32, string, string) (action.ApprovalResult, error) {
	return action.ApprovalResult{}, nil
}

func (stubSessionService) CreateSession(context.Context, string, string, int32) (identity.Session, error) {
	return identity.Session{}, nil
}

func TestCreateSession(t *testing.T) {
	service := &recordingSessionService{
		session: identity.Session{
			UserID:    "ent_user",
			WSURL:     "ws://openim.test",
			UserToken: "user-token",
			ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/im/session", bytes.NewBufferString(`{"platform_id":5,"device_id":"browser-1"}`))
	request.Header.Set("Authorization", "Bearer enterprise-token")
	request.Header.Set("X-Correlation-ID", "test-correlation")

	NewHandler("test-version", service, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if service.rawToken != "enterprise-token" || service.deviceID != "browser-1" || service.platformID != 5 {
		t.Fatalf("service input = token %q, device %q, platform %d", service.rawToken, service.deviceID, service.platformID)
	}
	var response createSessionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UserToken != "user-token" || response.ExpiresAt != "2030-01-02T03:04:05Z" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateSessionRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		body          string
		wantStatus    int
	}{
		{name: "missing bearer", body: `{"platform_id":5,"device_id":"browser"}`, wantStatus: http.StatusUnauthorized},
		{name: "admin platform", authorization: "Bearer token", body: `{"platform_id":10,"device_id":"browser"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", authorization: "Bearer token", body: `{"platform_id":5,"device_id":"browser","tenant_id":"forged"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/im/session", bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", tt.authorization)
			NewHandler("test-version", &recordingSessionService{}, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
		})
	}
}

func TestCreateSessionMapsDependencyError(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/im/session", bytes.NewBufferString(`{"platform_id":5,"device_id":"browser"}`))
	request.Header.Set("Authorization", "Bearer token")
	service := &recordingSessionService{err: identity.ErrDependencyUnavailable}

	NewHandler("test-version", service, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", recorder.Code)
	}
	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "DEPENDENCY_UNAVAILABLE" || !response.Retryable || response.CorrelationID == "" {
		t.Fatalf("response = %#v", response)
	}
}

func TestApproveAction(t *testing.T) {
	approvals := &recordingApprovalService{result: action.ApprovalResult{IntentID: "intent-1", ExecutionID: "execution-1", State: "queued"}}
	recorder := httptest.NewRecorder()
	digest := "sha256:" + strings.Repeat("a", 64)
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/intents/intent-1/approve", bytes.NewBufferString(`{"platform_id":5,"device_id":"browser-1","payload_digest":"`+digest+`"}`))
	request.Header.Set("Authorization", "Bearer enterprise-token")
	NewHandler("test-version", stubSessionService{}, approvals, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if approvals.intentID != "intent-1" || approvals.digest != digest || approvals.token != "enterprise-token" {
		t.Fatalf("approval input=%#v", approvals)
	}
}

func TestGetAgentWorkspace(t *testing.T) {
	service := &recordingAgentWorkspaceService{workspace: agent.Workspace{
		AgentUserID: "agent-1",
		Runs:        []agent.WorkspaceRun{{ID: "run-1", State: "waiting_approval"}},
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/agent/workspace?platform_id=5&device_id=browser-1", nil)
	request.Header.Set("Authorization", "Bearer enterprise-token")
	NewHandler("test-version", stubSessionService{}, stubApprovalService{}, service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.token != "enterprise-token" || service.deviceID != "browser-1" || service.platformID != 5 {
		t.Fatalf("workspace service input=%#v", service)
	}
	var response agent.Workspace
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentUserID != "agent-1" || len(response.Runs) != 1 || response.Runs[0].ID != "run-1" {
		t.Fatalf("workspace response=%#v", response)
	}
}

func TestGetAgentWorkspaceRejectsInvalidDeviceContext(t *testing.T) {
	for _, target := range []string{
		"/v1/agent/workspace?platform_id=10&device_id=browser-1",
		"/v1/agent/workspace?platform_id=5",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer enterprise-token")
		NewHandler("test-version", stubSessionService{}, stubApprovalService{}, stubAgentWorkspaceService{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d", target, recorder.Code)
		}
	}
}

type recordingSessionService struct {
	session    identity.Session
	err        error
	rawToken   string
	deviceID   string
	platformID int32
}

type recordingApprovalService struct {
	result                  action.ApprovalResult
	err                     error
	intentID, digest, token string
}

type recordingAgentWorkspaceService struct {
	workspace       agent.Workspace
	err             error
	token, deviceID string
	platformID      int32
}

func (s *recordingAgentWorkspaceService) Get(_ context.Context, token, deviceID string, platformID int32) (agent.Workspace, error) {
	s.token, s.deviceID, s.platformID = token, deviceID, platformID
	return s.workspace, s.err
}

func (s *recordingApprovalService) Approve(_ context.Context, token, device string, platform int32, intentID, digest string) (action.ApprovalResult, error) {
	s.token = token
	s.intentID = intentID
	s.digest = digest
	return s.result, s.err
}

func (s *recordingSessionService) CreateSession(_ context.Context, rawToken, deviceID string, platformID int32) (identity.Session, error) {
	s.rawToken = rawToken
	s.deviceID = deviceID
	s.platformID = platformID
	return s.session, s.err
}
