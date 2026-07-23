package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

type telegramLinkStub struct {
	status     telegram.LinkStatus
	challenge  telegram.LinkChallenge
	err        error
	token      string
	deviceID   string
	platformID int32
	issueCalls int
}

func (s *telegramLinkStub) Status(_ context.Context, token, deviceID string, platformID int32) (telegram.LinkStatus, error) {
	s.token, s.deviceID, s.platformID = token, deviceID, platformID
	return s.status, s.err
}

func (s *telegramLinkStub) Issue(_ context.Context, token, deviceID string, platformID int32) (telegram.LinkChallenge, error) {
	s.token, s.deviceID, s.platformID, s.issueCalls = token, deviceID, platformID, s.issueCalls+1
	return s.challenge, s.err
}

func telegramLinkHandler(service TelegramLinkService) http.Handler {
	return NewHandlerWithTelegramLinks("test-version", stubSessionService{}, stubDeviceService{}, stubApprovalService{}, stubAgentWorkspaceService{}, stubAgentCatalogService{}, service)
}

func TestTelegramLinkStatusUsesAuthenticatedDeviceContext(t *testing.T) {
	expiresAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	service := &telegramLinkStub{status: telegram.LinkStatus{State: "pending", ExpiresAt: &expiresAt}}
	request := httptest.NewRequest(http.MethodGet, "/v1/agent/channels/telegram/link?platform_id=5&device_id=browser", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()

	telegramLinkHandler(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.token != "token" || service.deviceID != "browser" || service.platformID != 5 {
		t.Fatalf("status/context = %d %q/%q/%d body=%s", recorder.Code, service.token, service.deviceID, service.platformID, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var body telegramLinkResponse
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != "pending" || body.ExpiresAt == nil || *body.ExpiresAt != expiresAt.Format(time.RFC3339) || body.Code != "" {
		t.Fatalf("body = %#v", body)
	}
}

func TestTelegramLinkChallengeReturnsPlaintextOnce(t *testing.T) {
	expiresAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	service := &telegramLinkStub{challenge: telegram.LinkChallenge{Code: "ABCD-EFGH", Command: "/link ABCD-EFGH", ExpiresAt: expiresAt}}
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/channels/telegram/link-challenges?platform_id=5&device_id=browser", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()

	telegramLinkHandler(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || service.issueCalls != 1 {
		t.Fatalf("status/calls = %d/%d body=%s", recorder.Code, service.issueCalls, recorder.Body.String())
	}
	var body telegramLinkResponse
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != "pending" || body.Code != "ABCD-EFGH" || body.Command != "/link ABCD-EFGH" {
		t.Fatalf("body = %#v", body)
	}
}

func TestTelegramLinkChallengeRejectsBodyAndInvalidContext(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		authorize  bool
		body       string
		wantStatus int
	}{
		{name: "missing auth", url: "/v1/agent/channels/telegram/link-challenges?platform_id=5&device_id=browser", wantStatus: http.StatusUnauthorized},
		{name: "invalid device", url: "/v1/agent/channels/telegram/link-challenges?platform_id=5", authorize: true, wantStatus: http.StatusBadRequest},
		{name: "identity body", url: "/v1/agent/channels/telegram/link-challenges?platform_id=5&device_id=browser", authorize: true, body: `{"member_id":"forged"}`, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &telegramLinkStub{}
			request := httptest.NewRequest(http.MethodPost, test.url, strings.NewReader(test.body))
			if test.authorize {
				request.Header.Set("Authorization", "Bearer token")
			}
			recorder := httptest.NewRecorder()
			telegramLinkHandler(service).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || service.issueCalls != 0 {
				t.Fatalf("status/calls = %d/%d body=%s", recorder.Code, service.issueCalls, recorder.Body.String())
			}
		})
	}
}

func TestTelegramLinkErrorsHaveStableHTTPMeaning(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "authentication", err: identity.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "device", err: identity.ErrForbidden, wantStatus: http.StatusForbidden, wantCode: "MEMBER_OR_DEVICE_FORBIDDEN"},
		{name: "bound", err: telegram.ErrAlreadyBound, wantStatus: http.StatusConflict, wantCode: "TELEGRAM_ALREADY_BOUND"},
		{name: "rate", err: telegram.ErrChallengeRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: "TELEGRAM_LINK_RATE_LIMITED"},
		{name: "dependency", err: identity.ErrDependencyUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "TELEGRAM_LINK_UNAVAILABLE"},
		{name: "unknown", err: errors.New("unknown"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &telegramLinkStub{err: test.err}
			request := httptest.NewRequest(http.MethodGet, "/v1/agent/channels/telegram/link?platform_id=5&device_id=browser", nil)
			request.Header.Set("Authorization", "Bearer token")
			recorder := httptest.NewRecorder()
			telegramLinkHandler(service).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
