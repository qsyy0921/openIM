package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

type SessionService interface {
	CreateSession(ctx context.Context, rawToken, deviceID string, platformID int32) (identity.Session, error)
}
type ApprovalService interface {
	Approve(context.Context, string, string, int32, string, string) (action.ApprovalResult, error)
}

func NewHandler(version string, sessions SessionService, approvals ApprovalService) http.Handler {
	if sessions == nil || approvals == nil {
		panic("session and approval services are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(healthResponse{
			Status:  "ready",
			Service: "platform-api",
			Version: version,
		})
	})
	mux.Handle("POST /v1/im/session", &sessionHandler{service: sessions})
	mux.Handle("POST /v1/agent/intents/{intent_id}/approve", &approvalHandler{service: approvals})
	return requestLogger(mux)
}

type approvalHandler struct{ service ApprovalService }
type approveRequest struct {
	PlatformID    int32  `json:"platform_id"`
	DeviceID      string `json:"device_id"`
	PayloadDigest string `json:"payload_digest"`
}
type approveResponse struct {
	IntentID    string `json:"intent_id"`
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
}

func (h *approvalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "a valid bearer token is required", false, correlationID)
		return
	}
	intentID := strings.TrimSpace(r.PathValue("intent_id"))
	if intentID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "intent_id is invalid", false, correlationID)
		return
	}
	var input approveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is invalid", false, correlationID)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain one JSON object", false, correlationID)
		return
	}
	if !validPlatformID(input.PlatformID) || strings.TrimSpace(input.DeviceID) == "" || !validDigest(input.PayloadDigest) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "approval fields are invalid", false, correlationID)
		return
	}
	result, err := h.service.Approve(r.Context(), token, input.DeviceID, input.PlatformID, intentID, input.PayloadDigest)
	if err != nil {
		slog.Warn("approve action failed", "correlation_id", correlationID, "intent_id", intentID, "error", err)
		switch {
		case errors.Is(err, identity.ErrUnauthenticated):
			writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
		case errors.Is(err, identity.ErrForbidden), errors.Is(err, action.ErrForbidden):
			writeError(w, http.StatusForbidden, "APPROVAL_FORBIDDEN", "member or device cannot approve this intent", false, correlationID)
		case errors.Is(err, action.ErrExpired):
			writeError(w, http.StatusConflict, "INTENT_EXPIRED", "action intent has expired", false, correlationID)
		case errors.Is(err, action.ErrConflict):
			writeError(w, http.StatusConflict, "APPROVAL_CONFLICT", "intent digest or state conflicts", false, correlationID)
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "the approval could not be completed", false, correlationID)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, approveResponse{IntentID: result.IntentID, ExecutionID: result.ExecutionID, State: result.State})
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

type sessionHandler struct {
	service SessionService
}

type createSessionRequest struct {
	PlatformID int32  `json:"platform_id"`
	DeviceID   string `json:"device_id"`
}

type createSessionResponse struct {
	UserID    string `json:"user_id"`
	WSURL     string `json:"ws_url"`
	UserToken string `json:"user_token"`
	ExpiresAt string `json:"expires_at"`
}

type errorResponse struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	CorrelationID string `json:"correlation_id"`
}

func (h *sessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "a valid bearer token is required", false, correlationID)
		return
	}

	var input createSessionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is invalid", false, correlationID)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain one JSON object", false, correlationID)
		return
	}
	if !validPlatformID(input.PlatformID) || strings.TrimSpace(input.DeviceID) == "" || len(input.DeviceID) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "platform_id or device_id is invalid", false, correlationID)
		return
	}

	session, err := h.service.CreateSession(r.Context(), token, input.DeviceID, input.PlatformID)
	if err != nil {
		slog.Warn("create IM session failed", "correlation_id", correlationID, "error", err)
		writeSessionError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, createSessionResponse{
		UserID:    session.UserID,
		WSURL:     session.WSURL,
		UserToken: session.UserToken,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
	})
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func validPlatformID(platformID int32) bool {
	return platformID >= 1 && platformID <= 11 && platformID != 10
}

func writeSessionError(w http.ResponseWriter, err error, correlationID string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "MEMBER_OR_DEVICE_FORBIDDEN", "member or device is not active", false, correlationID)
	case errors.Is(err, identity.ErrProvisioningInProgress):
		writeError(w, http.StatusConflict, "IM_PROVISIONING_IN_PROGRESS", "OpenIM identity provisioning is in progress", true, correlationID)
	case errors.Is(err, identity.ErrDependencyUnavailable):
		writeError(w, http.StatusBadGateway, "DEPENDENCY_UNAVAILABLE", "a required dependency is unavailable", true, correlationID)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "the request could not be completed", false, correlationID)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, retryable bool, correlationID string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message, Retryable: retryable, CorrelationID: correlationID})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func correlationID(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if value != "" && len(value) <= 128 {
		return value
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "correlation-id-unavailable"
	}
	return hex.EncodeToString(random[:])
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Info("http request",
			"correlation_id", wrapped.Header().Get("X-Correlation-ID"),
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}
