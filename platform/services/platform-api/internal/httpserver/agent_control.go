package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agentcontrol"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
)

type AgentControlService interface {
	GetMemory(context.Context, string, string, int32) (agentcontrol.MemorySnapshot, error)
	DeleteMemoryFact(context.Context, string, string, int32, string, string) (string, error)
	FeedbackMemory(context.Context, string, string, int32, string, string) error
	GetProactive(context.Context, string, string, int32) (agentcontrol.ProactiveSnapshot, error)
	CreateSubscription(context.Context, string, string, int32, agentcontrol.CreateSubscriptionRequest) (string, error)
	SetSubscriptionEnabled(context.Context, string, string, int32, string, bool) error
	UpdatePreferences(context.Context, string, string, int32, proactive.Preference) error
	Acknowledge(context.Context, string, string, int32, string, string) error
	ListToolApprovals(context.Context, string, string, int32) ([]agentcontrol.ToolApproval, error)
	DecideToolApproval(context.Context, string, string, int32, string, string, string) error
	GetReplay(context.Context, string, string, int32, string) (observe.Bundle, error)
	ListDelegations(context.Context, string, string, int32) ([]agentcontrol.Delegation, error)
	GetGroupMemory(context.Context, string, string, int32, string, string) (agentcontrol.GroupMemorySnapshot, error)
	ReviewGroupMemory(context.Context, string, string, int32, string, string, string, string) (string, error)
}

func registerAgentControlRoutes(mux *http.ServeMux, service AgentControlService) {
	mux.Handle("GET /v1/agent/memory", &agentMemoryHandler{service: service})
	mux.Handle("GET /v1/agent/group-memory", &agentGroupMemoryHandler{service: service})
	mux.Handle("POST /v1/agent/group-memory/proposals/{proposal_id}/decision", &agentGroupMemoryReviewHandler{service: service})
	mux.Handle("DELETE /v1/agent/memory/facts/{fact_id}", &agentMemoryDeleteHandler{service: service})
	mux.Handle("POST /v1/agent/memory/exposures/{exposure_id}/feedback", &agentMemoryFeedbackHandler{service: service})
	mux.Handle("GET /v1/agent/proactive", &agentProactiveHandler{service: service})
	mux.Handle("POST /v1/agent/proactive/subscriptions", &agentProactiveSubscriptionHandler{service: service})
	mux.Handle("PATCH /v1/agent/proactive/subscriptions/{subscription_id}", &agentProactiveSubscriptionStateHandler{service: service})
	mux.Handle("PUT /v1/agent/proactive/preferences", &agentProactivePreferenceHandler{service: service})
	mux.Handle("POST /v1/agent/proactive/events/{event_id}/acknowledge", &agentProactiveAcknowledgeHandler{service: service})
	mux.Handle("GET /v1/agent/tool-approvals", &agentToolApprovalListHandler{service: service})
	mux.Handle("POST /v1/agent/tool-approvals/{approval_id}/decision", &agentToolApprovalDecisionHandler{service: service})
	mux.Handle("GET /v1/agent/runs/{run_id}/replay", &agentReplayHandler{service: service})
	mux.Handle("GET /v1/agent/delegations", &agentDelegationListHandler{service: service})
}

type agentMemoryHandler struct{ service AgentControlService }

func (h *agentMemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.GetMemory(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type deviceMutation struct {
	PlatformID int32  `json:"platform_id"`
	DeviceID   string `json:"device_id"`
}

type agentMemoryDeleteHandler struct{ service AgentControlService }

func (h *agentMemoryDeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[deviceMutation](w, r, correlationID, 4096)
	if !ok || !validDeviceMutation(input) {
		if ok {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "platform_id or device_id is invalid", false, correlationID)
		}
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "a valid Idempotency-Key header is required", false, correlationID)
		return
	}
	eventID, err := h.service.DeleteMemoryFact(r.Context(), token, input.DeviceID, input.PlatformID,
		strings.TrimSpace(r.PathValue("fact_id")), idempotencyKey)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"event_id": eventID, "state": "projection_pending"})
}

type memoryFeedbackRequest struct {
	deviceMutation
	Signal string `json:"signal"`
}

type agentMemoryFeedbackHandler struct{ service AgentControlService }

func (h *agentMemoryFeedbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[memoryFeedbackRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) ||
		(input.Signal != "helpful" && input.Signal != "not_helpful" && input.Signal != "incorrect") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "memory feedback request is invalid", false, correlationID)
		return
	}
	if err := h.service.FeedbackMemory(r.Context(), token, input.DeviceID, input.PlatformID,
		strings.TrimSpace(r.PathValue("exposure_id")), input.Signal); err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "recorded"})
}

type agentProactiveHandler struct{ service AgentControlService }

func (h *agentProactiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.GetProactive(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type proactiveSubscriptionRequest struct {
	deviceMutation
	AgentID          string   `json:"agent_id"`
	Query            string   `json:"query"`
	Categories       []string `json:"categories"`
	SourceChannel    string   `json:"source_channel"`
	PollIntervalSecs int      `json:"poll_interval_seconds"`
}

type agentProactiveSubscriptionHandler struct{ service AgentControlService }

func (h *agentProactiveSubscriptionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[proactiveSubscriptionRequest](w, r, correlationID, 16*1024)
	if !ok {
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if !validDeviceMutation(input.deviceMutation) || strings.TrimSpace(input.AgentID) == "" || input.Query == "" ||
		len(input.Query) > 500 || len(input.Categories) > 20 || input.PollIntervalSecs < 300 || input.PollIntervalSecs > 86400 ||
		(input.SourceChannel != "openim" && input.SourceChannel != "telegram") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "proactive subscription request is invalid", false, correlationID)
		return
	}
	id, err := h.service.CreateSubscription(r.Context(), token, input.DeviceID, input.PlatformID, agentcontrol.CreateSubscriptionRequest{
		AgentID: input.AgentID, Query: input.Query, Categories: input.Categories,
		SourceChannel: input.SourceChannel, PollInterval: time.Duration(input.PollIntervalSecs) * time.Second,
	})
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"subscription_id": id})
}

type proactiveSubscriptionStateRequest struct {
	deviceMutation
	Enabled bool `json:"enabled"`
}

type agentProactiveSubscriptionStateHandler struct{ service AgentControlService }

func (h *agentProactiveSubscriptionStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[proactiveSubscriptionStateRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "platform_id or device_id is invalid", false, correlationID)
		return
	}
	if err := h.service.SetSubscriptionEnabled(r.Context(), token, input.DeviceID, input.PlatformID,
		strings.TrimSpace(r.PathValue("subscription_id")), input.Enabled); err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "updated", "enabled": input.Enabled})
}

type proactivePreferenceRequest struct {
	deviceMutation
	Enabled      bool    `json:"enabled"`
	Timezone     string  `json:"timezone"`
	QuietStart   string  `json:"quiet_start"`
	QuietEnd     string  `json:"quiet_end"`
	DailyBudget  int     `json:"daily_budget"`
	MinimumScore float64 `json:"minimum_score"`
}

type agentProactivePreferenceHandler struct{ service AgentControlService }

func (h *agentProactivePreferenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[proactivePreferenceRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	quietStart, startErr := parseClock(input.QuietStart)
	quietEnd, endErr := parseClock(input.QuietEnd)
	if !validDeviceMutation(input.deviceMutation) || startErr != nil || endErr != nil ||
		strings.TrimSpace(input.Timezone) == "" || input.DailyBudget < 0 || input.DailyBudget > 50 ||
		input.MinimumScore < 0 || input.MinimumScore > 1 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "proactive preference request is invalid", false, correlationID)
		return
	}
	if err := h.service.UpdatePreferences(r.Context(), token, input.DeviceID, input.PlatformID, proactive.Preference{
		Enabled: input.Enabled, Timezone: input.Timezone, QuietStart: quietStart, QuietEnd: quietEnd,
		DailyBudget: input.DailyBudget, MinimumScore: input.MinimumScore,
	}); err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "updated"})
}

type proactiveAcknowledgeRequest struct {
	deviceMutation
	Signal string `json:"signal"`
}

type agentProactiveAcknowledgeHandler struct{ service AgentControlService }

func (h *agentProactiveAcknowledgeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[proactiveAcknowledgeRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) ||
		(input.Signal != "interesting" && input.Signal != "not_interesting" && input.Signal != "dismissed") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "proactive acknowledgement request is invalid", false, correlationID)
		return
	}
	if err := h.service.Acknowledge(r.Context(), token, input.DeviceID, input.PlatformID,
		strings.TrimSpace(r.PathValue("event_id")), input.Signal); err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "acknowledged"})
}

type agentToolApprovalListHandler struct{ service AgentControlService }

func (h *agentToolApprovalListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	approvals, err := h.service.ListToolApprovals(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
}

type toolApprovalDecisionRequest struct {
	deviceMutation
	ArgumentsDigest string `json:"arguments_digest"`
	Decision        string `json:"decision"`
}

type agentToolApprovalDecisionHandler struct{ service AgentControlService }

func (h *agentToolApprovalDecisionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[toolApprovalDecisionRequest](w, r, correlationID, 4096)
	if !ok {
		return
	}
	if !validDeviceMutation(input.deviceMutation) || !validDigest(input.ArgumentsDigest) ||
		(input.Decision != "approve" && input.Decision != "reject") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "tool approval decision is invalid", false, correlationID)
		return
	}
	if err := h.service.DecideToolApproval(r.Context(), token, input.DeviceID, input.PlatformID,
		strings.TrimSpace(r.PathValue("approval_id")), input.ArgumentsDigest, input.Decision); err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"state": input.Decision + "d"})
}

type agentReplayHandler struct{ service AgentControlService }

func (h *agentReplayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	bundle, err := h.service.GetReplay(r.Context(), token, deviceID, platformID, strings.TrimSpace(r.PathValue("run_id")))
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

type agentDelegationListHandler struct{ service AgentControlService }

func (h *agentDelegationListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	jobs, err := h.service.ListDelegations(r.Context(), token, deviceID, platformID)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegations": jobs})
}

func authenticatedQuery(w http.ResponseWriter, r *http.Request) (string, string, string, int32, bool) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return correlationID, "", "", 0, false
	}
	deviceID, platformID, ok := requestDeviceContext(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "platform_id or device_id is invalid", false, correlationID)
		return correlationID, "", "", 0, false
	}
	return correlationID, token, deviceID, platformID, true
}

func authenticatedToken(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "a valid bearer token is required", false, correlationID)
		return correlationID, "", false
	}
	return correlationID, token, true
}

func decodeBody[T any](w http.ResponseWriter, r *http.Request, correlationID string, maxBytes int64) (T, bool) {
	var input T
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is invalid", false, correlationID)
		return input, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain one JSON object", false, correlationID)
		return input, false
	}
	return input, true
}

func validDeviceMutation(input deviceMutation) bool {
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	return validPlatformID(input.PlatformID) && input.DeviceID != "" && len(input.DeviceID) <= 128
}

func parseClock(value string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}

func writeAgentControlError(w http.ResponseWriter, err error, correlationID string) {
	slog.Warn("Agent control request failed", "correlation_id", correlationID, "error", err)
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "MEMBER_OR_DEVICE_FORBIDDEN", "member or device is not active", false, correlationID)
	case errors.Is(err, identity.ErrIdentityLinkNotReady):
		writeError(w, http.StatusConflict, "CHANNEL_IDENTITY_NOT_READY", "the selected channel identity is not linked", false, correlationID)
	case errors.Is(err, agentcontrol.ErrGroupMemoryForbidden):
		writeError(w, http.StatusForbidden, "GROUP_MEMORY_FORBIDDEN", "current member is not in the OpenIM group", false, correlationID)
	case errors.Is(err, agentcontrol.ErrGroupMemoryInvalid):
		writeError(w, http.StatusBadRequest, "INVALID_GROUP_MEMORY_REQUEST", "group memory request is invalid", false, correlationID)
	case errors.Is(err, memory.ErrFactNotFound), errors.Is(err, memory.ErrGroupProposalNotFound), errors.Is(err, proactive.ErrSubscriptionNotFound), errors.Is(err, observe.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "the requested Agent resource was not found", false, correlationID)
	case errors.Is(err, toolruntime.ErrApprovalConflict):
		writeError(w, http.StatusConflict, "TOOL_APPROVAL_CONFLICT", "the tool approval is expired, changed, or unauthorized", false, correlationID)
	default:
		writeError(w, http.StatusInternalServerError, "AGENT_CONTROL_FAILED", "the Agent control operation could not be completed", false, correlationID)
	}
}

var _ AgentControlService = (*agentcontrol.Service)(nil)
