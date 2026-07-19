package httpserver

import (
	"net/http"
	"strings"
)

type agentGroupMemoryHandler struct{ service AgentControlService }

func (h *agentGroupMemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, deviceID, platformID, ok := authenticatedQuery(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.GetGroupMemory(r.Context(), token, deviceID, platformID,
		strings.TrimSpace(r.URL.Query().Get("source_channel")), strings.TrimSpace(r.URL.Query().Get("conversation_id")))
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type groupMemoryReviewRequest struct {
	deviceMutation
	SourceChannel  string `json:"source_channel"`
	ConversationID string `json:"conversation_id"`
	Decision       string `json:"decision"`
}

type agentGroupMemoryReviewHandler struct{ service AgentControlService }

func (h *agentGroupMemoryReviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID, token, ok := authenticatedToken(w, r)
	if !ok {
		return
	}
	input, ok := decodeBody[groupMemoryReviewRequest](w, r, correlationID, 8192)
	if !ok {
		return
	}
	input.SourceChannel, input.ConversationID, input.Decision = strings.TrimSpace(input.SourceChannel), strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.Decision)
	if !validDeviceMutation(input.deviceMutation) || (input.Decision != "approve" && input.Decision != "reject") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "group memory review request is invalid", false, correlationID)
		return
	}
	eventID, err := h.service.ReviewGroupMemory(r.Context(), token, input.DeviceID, input.PlatformID,
		input.SourceChannel, input.ConversationID, strings.TrimSpace(r.PathValue("proposal_id")), input.Decision)
	if err != nil {
		writeAgentControlError(w, err, correlationID)
		return
	}
	state := "rejected"
	if input.Decision == "approve" {
		state = "approved"
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": state, "event_id": eventID})
}
