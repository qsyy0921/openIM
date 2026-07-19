package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/admincontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
)

type AdminControlService interface {
	GetSnapshot(context.Context, string, string, int32) (admincontrol.Snapshot, error)
	SetRuntimeControl(context.Context, string, string, int32, string, bool, string, int64) (runtimecontrol.Control, error)
	GetCatalog(context.Context, string, string, int32) (admincontrol.CatalogSnapshot, error)
	CreateAgent(context.Context, string, string, int32, admincontrol.CreateAgentInput) (agent.AgentSummary, error)
	PublishSkill(context.Context, string, string, int32, capability.Skill) (capability.Skill, error)
	PublishTool(context.Context, string, string, int32, admincontrol.CatalogTool) (capability.Descriptor, error)
	PublishCapabilitySnapshot(context.Context, string, string, int32, []capability.ToolRef) (capability.Snapshot, error)
	SetMCPEnabled(context.Context, string, string, int32, string, bool) error
	SetMemberRole(context.Context, string, string, int32, string, string, bool) error
	RegisterRemoteAgent(context.Context, string, string, int32, remotea2a.RegisterInput) (remotea2a.RemoteAgent, error)
	VerifyRemoteAgent(context.Context, string, string, int32, string, int64) (remotea2a.RemoteAgent, error)
	SetRemoteAgentEnabled(context.Context, string, string, int32, string, bool, int64) (remotea2a.RemoteAgent, error)
}

func registerAdminControlRoutes(mux *http.ServeMux, service AdminControlService) {
	mux.Handle("GET /v1/admin/agent/operations", &adminOperationsHandler{service: service})
	mux.Handle("PUT /v1/admin/agent/runtime-controls/{component}", &adminRuntimeControlHandler{service: service})
	mux.Handle("GET /v1/admin/agent/catalog", &adminCatalogHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/agents", &adminAgentCreateHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/skills", &adminSkillPublishHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/tools", &adminToolPublishHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/capability-snapshots", &adminSnapshotPublishHandler{service: service})
	mux.Handle("PUT /v1/admin/agent/catalog/mcp/{slug}", &adminMCPStateHandler{service: service})
	mux.Handle("PUT /v1/admin/agent/catalog/members/{member_id}/roles/{role}", &adminMemberRoleHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/remote-agents", &adminRemoteAgentRegisterHandler{service: service})
	mux.Handle("POST /v1/admin/agent/catalog/remote-agents/{slug}/verify", &adminRemoteAgentVerifyHandler{service: service})
	mux.Handle("PUT /v1/admin/agent/catalog/remote-agents/{slug}", &adminRemoteAgentStateHandler{service: service})
}

type adminCatalogHandler struct{ service AdminControlService }

func (h *adminCatalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.GetCatalog(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAdminControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type adminAgentCreateRequest struct {
	deviceMutation
	admincontrol.CreateAgentInput
}

type adminAgentCreateHandler struct{ service AdminControlService }

func (h *adminAgentCreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminAgentCreateRequest](w, r, correlationID, 1<<20)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) || strings.TrimSpace(input.Slug) == "" || len(input.Spec) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Agent publication request is invalid", false, correlationID)
		return
	}
	created, err := h.service.CreateAgent(r.Context(), token, input.DeviceID, input.PlatformID, input.CreateAgentInput)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type adminSkillPublishRequest struct {
	deviceMutation
	SkillID        string   `json:"skill_id"`
	Version        string   `json:"version"`
	Name           string   `json:"name"`
	Summary        string   `json:"summary"`
	Instructions   string   `json:"instructions"`
	ToolOperations []string `json:"tool_operations"`
	Audience       string   `json:"audience"`
}

type adminSkillPublishHandler struct{ service AdminControlService }

func (h *adminSkillPublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminSkillPublishRequest](w, r, correlationID, 1<<20)
	if !ok {
		return
	}
	skill := capability.Skill{SkillID: input.SkillID, Version: input.Version, Name: input.Name, Summary: input.Summary, Instructions: input.Instructions, ToolOperations: input.ToolOperations, Audience: input.Audience}
	if !validDeviceMutation(input.deviceMutation) || skill.Validate() != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Skill publication request is invalid", false, correlationID)
		return
	}
	published, err := h.service.PublishSkill(r.Context(), token, input.DeviceID, input.PlatformID, skill)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, published)
}

type adminToolPublishRequest struct {
	deviceMutation
	admincontrol.CatalogTool
}

type adminToolPublishHandler struct{ service AdminControlService }

func (h *adminToolPublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminToolPublishRequest](w, r, correlationID, 1<<20)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) || strings.TrimSpace(input.OperationID) == "" || input.TimeoutMS <= 0 || len(input.InputSchema) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "tool publication request is invalid", false, correlationID)
		return
	}
	published, err := h.service.PublishTool(r.Context(), token, input.DeviceID, input.PlatformID, input.CatalogTool)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, published)
}

type adminSnapshotPublishRequest struct {
	deviceMutation
	Tools []capability.ToolRef `json:"tools"`
}

type adminSnapshotPublishHandler struct{ service AdminControlService }

func (h *adminSnapshotPublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminSnapshotPublishRequest](w, r, correlationID, 128<<10)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) || len(input.Tools) == 0 || len(input.Tools) > 64 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "capability snapshot request is invalid", false, correlationID)
		return
	}
	published, err := h.service.PublishCapabilitySnapshot(r.Context(), token, input.DeviceID, input.PlatformID, input.Tools)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, published)
}

type adminEnabledRequest struct {
	deviceMutation
	Enabled bool `json:"enabled"`
}

type adminMCPStateHandler struct{ service AdminControlService }

type adminMemberRoleHandler struct{ service AdminControlService }

type adminRemoteAgentRegisterHandler struct{ service AdminControlService }
type adminRemoteAgentVerifyHandler struct{ service AdminControlService }
type adminRemoteAgentStateHandler struct{ service AdminControlService }

func (h *adminMCPStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminEnabledRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !validDeviceMutation(input.deviceMutation) || slug == "" || len(slug) > 120 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "MCP state request is invalid", false, correlationID)
		return
	}
	if err := h.service.SetMCPEnabled(r.Context(), token, input.DeviceID, input.PlatformID, slug, input.Enabled); err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "enabled": input.Enabled})
}

func (h *adminMemberRoleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminEnabledRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	memberID, role := strings.TrimSpace(r.PathValue("member_id")), strings.TrimSpace(r.PathValue("role"))
	if !validDeviceMutation(input.deviceMutation) || memberID == "" || !oneOf(role, "platform_admin", "agent_admin", "knowledge_admin") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "member role request is invalid", false, correlationID)
		return
	}
	if err := h.service.SetMemberRole(r.Context(), token, input.DeviceID, input.PlatformID, memberID, role, input.Enabled); err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"member_id": memberID, "role": role, "enabled": input.Enabled})
}

type adminRemoteAgentRegisterRequest struct {
	deviceMutation
	Slug               string `json:"slug"`
	DisplayName        string `json:"display_name"`
	CardURL            string `json:"card_url"`
	ExpectedCardDigest string `json:"expected_card_digest"`
	AuthEnvKey         string `json:"auth_env_key"`
}

func (h *adminRemoteAgentRegisterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminRemoteAgentRegisterRequest](w, r, correlationID, 32<<10)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) || strings.TrimSpace(input.Slug) == "" || strings.TrimSpace(input.CardURL) == "" || strings.TrimSpace(input.ExpectedCardDigest) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "remote A2A registration is invalid", false, correlationID)
		return
	}
	created, err := h.service.RegisterRemoteAgent(r.Context(), token, input.DeviceID, input.PlatformID, remotea2a.RegisterInput{
		Slug: input.Slug, DisplayName: input.DisplayName, CardURL: input.CardURL,
		ExpectedCardDigest: input.ExpectedCardDigest, AuthEnvKey: input.AuthEnvKey,
	})
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type adminRemoteAgentRevisionRequest struct {
	deviceMutation
	ExpectedRevision int64 `json:"expected_revision"`
	Enabled          bool  `json:"enabled,omitempty"`
}

func (h *adminRemoteAgentVerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminRemoteAgentRevisionRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !validDeviceMutation(input.deviceMutation) || slug == "" || input.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "remote A2A verification is invalid", false, correlationID)
		return
	}
	verified, err := h.service.VerifyRemoteAgent(r.Context(), token, input.DeviceID, input.PlatformID, slug, input.ExpectedRevision)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, verified)
}

func (h *adminRemoteAgentStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[adminRemoteAgentRevisionRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !validDeviceMutation(input.deviceMutation) || slug == "" || input.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "remote A2A state request is invalid", false, correlationID)
		return
	}
	updated, err := h.service.SetRemoteAgentEnabled(r.Context(), token, input.DeviceID, input.PlatformID, slug, input.Enabled, input.ExpectedRevision)
	if err != nil {
		writeAdminMutationError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func oneOf(value string, expected ...string) bool {
	for _, candidate := range expected {
		if value == candidate {
			return true
		}
	}
	return false
}

func writeAdminMutationError(w http.ResponseWriter, err error, correlationID string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, admincontrol.ErrForbidden):
		writeError(w, http.StatusForbidden, "ADMINISTRATION_FORBIDDEN", "an active platform administrator role and device are required", false, correlationID)
	default:
		writeError(w, http.StatusConflict, "CATALOG_MUTATION_REJECTED", "catalog mutation was rejected", false, correlationID)
	}
}

type adminOperationsHandler struct{ service AdminControlService }

func (h *adminOperationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.GetSnapshot(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAdminControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type runtimeControlRequest struct {
	deviceMutation
	Paused           bool   `json:"paused"`
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type adminRuntimeControlHandler struct{ service AdminControlService }

func (h *adminRuntimeControlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[runtimeControlRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if !validDeviceMutation(input.deviceMutation) || input.Reason == "" || len(input.Reason) > 500 || input.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "runtime control request is invalid", false, correlationID)
		return
	}
	control, err := h.service.SetRuntimeControl(
		r.Context(), token, input.DeviceID, input.PlatformID, strings.TrimSpace(r.PathValue("component")),
		input.Paused, input.Reason, input.ExpectedRevision,
	)
	if err != nil {
		writeAdminControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, control)
}

func writeAdminControlError(w http.ResponseWriter, err error, correlationID string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, admincontrol.ErrForbidden):
		writeError(w, http.StatusForbidden, "ADMINISTRATION_FORBIDDEN", "an active administrator role and device are required", false, correlationID)
	case errors.Is(err, runtimecontrol.ErrConflict):
		writeError(w, http.StatusConflict, "REVISION_CONFLICT", "runtime control changed; refresh before retrying", false, correlationID)
	default:
		writeError(w, http.StatusInternalServerError, "ADMIN_CONTROL_FAILED", "administrator operation could not be completed", false, correlationID)
	}
}

var _ AdminControlService = (*admincontrol.Service)(nil)
