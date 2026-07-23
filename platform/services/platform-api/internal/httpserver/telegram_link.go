package httpserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

type telegramLinkStatusHandler struct{ service TelegramLinkService }
type telegramLinkChallengeHandler struct{ service TelegramLinkService }

type telegramLinkResponse struct {
	State     string  `json:"state"`
	Code      string  `json:"code,omitempty"`
	Command   string  `json:"command,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (h *telegramLinkStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	w.Header().Set("Cache-Control", "no-store")
	token, deviceID, platformID, ok := telegramLinkRequestContext(w, r, correlationID)
	if !ok {
		return
	}
	status, err := h.service.Status(r.Context(), token, deviceID, platformID)
	if err != nil {
		slog.Warn("read Telegram link status failed", "correlation_id", correlationID, "error", err)
		writeTelegramLinkError(w, err, correlationID)
		return
	}
	response := telegramLinkResponse{State: status.State}
	if status.ExpiresAt != nil {
		value := status.ExpiresAt.UTC().Format(time.RFC3339)
		response.ExpiresAt = &value
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *telegramLinkChallengeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	w.Header().Set("Cache-Control", "no-store")
	token, deviceID, platformID, ok := telegramLinkRequestContext(w, r, correlationID)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64))
	if err != nil || strings.TrimSpace(string(body)) != "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be empty", false, correlationID)
		return
	}
	challenge, err := h.service.Issue(r.Context(), token, deviceID, platformID)
	if err != nil {
		slog.Warn("issue Telegram link challenge failed", "correlation_id", correlationID, "error", err)
		writeTelegramLinkError(w, err, correlationID)
		return
	}
	expiresAt := challenge.ExpiresAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusCreated, telegramLinkResponse{
		State: "pending", Code: challenge.Code, Command: challenge.Command, ExpiresAt: &expiresAt,
	})
}

func telegramLinkRequestContext(w http.ResponseWriter, r *http.Request, correlationID string) (string, string, int32, bool) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "a valid bearer token is required", false, correlationID)
		return "", "", 0, false
	}
	deviceID, platformID, ok := requestDeviceContext(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "platform_id or device_id is invalid", false, correlationID)
		return "", "", 0, false
	}
	return token, deviceID, platformID, true
}

func writeTelegramLinkError(w http.ResponseWriter, err error, correlationID string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "MEMBER_OR_DEVICE_FORBIDDEN", "member or device is not active", false, correlationID)
	case errors.Is(err, telegram.ErrAlreadyBound):
		writeError(w, http.StatusConflict, "TELEGRAM_ALREADY_BOUND", "the enterprise member already has a Telegram binding", false, correlationID)
	case errors.Is(err, telegram.ErrChallengeRateLimited):
		w.Header().Set("Retry-After", "15")
		writeError(w, http.StatusTooManyRequests, "TELEGRAM_LINK_RATE_LIMITED", "a new Telegram link challenge cannot be issued yet", true, correlationID)
	case errors.Is(err, identity.ErrDependencyUnavailable):
		writeError(w, http.StatusServiceUnavailable, "TELEGRAM_LINK_UNAVAILABLE", "Telegram identity linking is temporarily unavailable", true, correlationID)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "the Telegram identity operation could not be completed", false, correlationID)
	}
}
