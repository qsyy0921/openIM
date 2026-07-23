package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/admincontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
)

type adminControlStub struct {
	setErr error
	calls  int
}

func (s *adminControlStub) GetSnapshot(context.Context, string, string, int32) (admincontrol.Snapshot, error) {
	s.calls++
	return admincontrol.Snapshot{}, nil
}

func (s *adminControlStub) SetRuntimeControl(context.Context, string, string, int32, string, bool, string, int64) (runtimecontrol.Control, error) {
	s.calls++
	return runtimecontrol.Control{}, s.setErr
}

func (s *adminControlStub) GetCatalog(context.Context, string, string, int32) (admincontrol.CatalogSnapshot, error) {
	s.calls++
	return admincontrol.CatalogSnapshot{}, s.setErr
}

func (s *adminControlStub) CreateAgent(context.Context, string, string, int32, admincontrol.CreateAgentInput) (agent.AgentSummary, error) {
	s.calls++
	return agent.AgentSummary{}, s.setErr
}

func (s *adminControlStub) PublishSkill(context.Context, string, string, int32, capability.Skill) (capability.Skill, error) {
	s.calls++
	return capability.Skill{}, s.setErr
}

func (s *adminControlStub) PublishTool(context.Context, string, string, int32, admincontrol.CatalogTool) (capability.Descriptor, error) {
	s.calls++
	return capability.Descriptor{}, s.setErr
}

func (s *adminControlStub) PublishCapabilitySnapshot(context.Context, string, string, int32, []capability.ToolRef) (capability.Snapshot, error) {
	s.calls++
	return capability.Snapshot{}, s.setErr
}

func (s *adminControlStub) SetMCPEnabled(context.Context, string, string, int32, string, bool) error {
	s.calls++
	return s.setErr
}

func (s *adminControlStub) SetMemberRole(context.Context, string, string, int32, string, string, bool) error {
	s.calls++
	return s.setErr
}

func (s *adminControlStub) RegisterRemoteAgent(context.Context, string, string, int32, remotea2a.RegisterInput) (remotea2a.RemoteAgent, error) {
	s.calls++
	return remotea2a.RemoteAgent{}, s.setErr
}

func (s *adminControlStub) VerifyRemoteAgent(context.Context, string, string, int32, string, int64) (remotea2a.RemoteAgent, error) {
	s.calls++
	return remotea2a.RemoteAgent{}, s.setErr
}

func (s *adminControlStub) SetRemoteAgentEnabled(context.Context, string, string, int32, string, bool, int64) (remotea2a.RemoteAgent, error) {
	s.calls++
	return remotea2a.RemoteAgent{}, s.setErr
}

func TestAdminOperationsRequiresBearerBeforeServiceCall(t *testing.T) {
	service := &adminControlStub{}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/agent/operations?platform_id=5&device_id=browser", nil)
	response := httptest.NewRecorder()
	(&adminOperationsHandler{service: service}).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
}

func TestRuntimeControlMapsOptimisticConflict(t *testing.T) {
	service := &adminControlStub{setErr: runtimecontrol.ErrConflict}
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/agent/runtime-controls/agent_execution", strings.NewReader(`{"platform_id":5,"device_id":"browser","paused":true,"reason":"incident","expected_revision":2}`))
	request.SetPathValue("component", "agent_execution")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	(&adminRuntimeControlHandler{service: service}).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
}

func TestRuntimeControlRejectsUnknownJSONFields(t *testing.T) {
	service := &adminControlStub{}
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/agent/runtime-controls/agent_execution", strings.NewReader(`{"platform_id":5,"device_id":"browser","paused":true,"reason":"incident","expected_revision":0,"tenant_id":"forged"}`))
	request.SetPathValue("component", "agent_execution")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	(&adminRuntimeControlHandler{service: service}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
}

func TestAdminCatalogRequiresDeviceContext(t *testing.T) {
	service := &adminControlStub{}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/agent/catalog", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	(&adminCatalogHandler{service: service}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
}

func TestAdminMemberRoleRejectsUnknownRole(t *testing.T) {
	service := &adminControlStub{}
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/agent/catalog/members/member/roles/owner", strings.NewReader(`{"platform_id":5,"device_id":"browser","enabled":true}`))
	request.SetPathValue("member_id", "member")
	request.SetPathValue("role", "owner")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	(&adminMemberRoleHandler{service: service}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
}
