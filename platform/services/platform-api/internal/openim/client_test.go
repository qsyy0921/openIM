package openim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientUsesAdminTokenAndCachesIt(t *testing.T) {
	var adminCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("operationID") == "" {
			t.Error("operationID header is missing")
		}
		switch r.URL.Path {
		case "/auth/get_admin_token":
			adminCalls.Add(1)
			if r.Header.Get("token") != "" {
				t.Error("admin token request must not carry a token")
			}
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/user/user_register":
			if r.Header.Get("token") != "admin-token" {
				t.Errorf("register token = %q", r.Header.Get("token"))
			}
			var body struct {
				Users []struct {
					UserID string `json:"userID"`
					Ex     string `json:"ex"`
				} `json:"users"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode register: %v", err)
			}
			if len(body.Users) != 1 || body.Users[0].UserID != "ent_user" || body.Users[0].Ex != "platform-identity:tenant/member" {
				t.Errorf("register body = %#v", body)
			}
			writeEnvelope(t, w, map[string]any{})
		case "/auth/get_user_token":
			if r.Header.Get("token") != "admin-token" {
				t.Errorf("user token header = %q", r.Header.Get("token"))
			}
			writeEnvelope(t, w, map[string]any{"token": "user-token", "expireTimeSeconds": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})
	if err := client.EnsureUser(context.Background(), "ent_user", "Member", "tenant/member"); err != nil {
		t.Fatalf("EnsureUser() error = %v", err)
	}
	token, expiresAt, err := client.GetUserToken(context.Background(), "ent_user", 5)
	if err != nil {
		t.Fatalf("GetUserToken() error = %v", err)
	}
	if token != "user-token" || !expiresAt.After(time.Now().Add(59*time.Minute)) {
		t.Fatalf("token = %q, expiresAt = %v", token, expiresAt)
	}
	if adminCalls.Load() != 1 {
		t.Fatalf("admin token calls = %d", adminCalls.Load())
	}
}

func TestEnsureUserVerifiesExistingUserOwnership(t *testing.T) {
	ownerMatches := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/get_admin_token":
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/user/user_register":
			writeAPIError(t, w, registeredAlreadyCode, "already registered")
		case "/user/get_users_info":
			marker := "platform-identity:another/member"
			if ownerMatches {
				marker = "platform-identity:tenant/member"
			}
			writeEnvelope(t, w, map[string]any{"usersInfo": []map[string]string{{"userID": "ent_user", "ex": marker}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})

	if err := client.EnsureUser(context.Background(), "ent_user", "Member", "tenant/member"); err != nil {
		t.Fatalf("matching ownership error = %v", err)
	}
	ownerMatches = false
	if err := client.EnsureUser(context.Background(), "ent_user", "Member", "tenant/member"); err == nil {
		t.Fatal("mismatched ownership was accepted")
	}
}

func TestClientSendsAgentTextWithAdminToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/get_admin_token":
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/msg/send_msg":
			if r.Header.Get("token") != "admin-token" {
				t.Fatalf("token = %q", r.Header.Get("token"))
			}
			var body struct {
				RecvID      string            `json:"recvID"`
				SendID      string            `json:"sendID"`
				Content     map[string]string `json:"content"`
				ContentType int32             `json:"contentType"`
				SessionType int32             `json:"sessionType"`
				Ex          string            `json:"ex"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.RecvID != "user-1" || body.SendID != "agent-1" || body.Content["content"] != "answer" || body.ContentType != 101 || body.SessionType != 1 || body.Ex != "platform-agent-run:run-1" {
				t.Fatalf("body = %#v", body)
			}
			writeEnvelope(t, w, map[string]any{"serverMsgID": "server-1", "clientMsgID": "client-1", "sendTime": 123})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})
	result, err := client.SendText(context.Background(), "agent-1", TextTarget{SessionType: 1, ReceiverID: "user-1"}, "answer", "run-1")
	if err != nil || result.ServerMsgID != "server-1" {
		t.Fatalf("SendText() = %#v, %v", result, err)
	}
}

func TestClientProjectsOnlinePlatformsWithoutReturningConnectionSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/get_admin_token":
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/user/get_users_online_status":
			if r.Header.Get("token") != "admin-token" {
				t.Fatalf("token = %q", r.Header.Get("token"))
			}
			var request struct {
				UserIDs []string `json:"userIDs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if len(request.UserIDs) != 1 || request.UserIDs[0] != "ent_user" {
				t.Fatalf("request = %#v", request)
			}
			writeEnvelope(t, w, []map[string]any{{
				"userID": "ent_user", "status": 1,
				"detailPlatformStatus": []map[string]any{{"platformID": 5, "token": "must-not-escape", "connID": "private"}, {"platformID": 3, "token": "other"}, {"platformID": 5}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})

	platforms, err := client.GetOnlinePlatforms(context.Background(), "ent_user")
	if err != nil {
		t.Fatalf("GetOnlinePlatforms() error = %v", err)
	}
	if len(platforms) != 2 || platforms[0] != 3 || platforms[1] != 5 {
		t.Fatalf("platforms = %#v", platforms)
	}
}

func TestClientForceLogoutUsesResolvedUserAndPlatform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/get_admin_token":
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/auth/force_logout":
			if r.Header.Get("token") != "admin-token" {
				t.Fatalf("token = %q", r.Header.Get("token"))
			}
			var request struct {
				UserID     string `json:"userID"`
				PlatformID int32  `json:"platformID"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.UserID != "ent_user" || request.PlatformID != 3 {
				t.Fatalf("request = %#v", request)
			}
			writeEnvelope(t, w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})
	if err := client.ForceLogout(context.Background(), "ent_user", 3); err != nil {
		t.Fatalf("ForceLogout() error = %v", err)
	}
}

func TestEnsureAgentBotAcceptsEmptySuccessData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/get_admin_token":
			writeEnvelope(t, w, map[string]any{"token": "admin-token", "expireTimeSeconds": 3600})
		case "/user/user_register":
			var body struct {
				Users []struct {
					Ex string `json:"ex"`
				} `json:"users"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Users) != 1 || body.Users[0].Ex != "platform-agent-bot:tenant-1" {
				t.Fatalf("body = %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "errMsg": "", "data": nil})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, Secret: "secret", AdminUser: "imAdmin", Timeout: time.Second})
	if err := client.EnsureAgentBot(context.Background(), "agent-1", "tenant-1"); err != nil {
		t.Fatalf("EnsureAgentBot() error = %v", err)
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "errMsg": "", "errDlt": "", "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func writeAPIError(t *testing.T, w http.ResponseWriter, code int, message string) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"errCode": code, "errMsg": message, "data": map[string]any{}}); err != nil {
		t.Errorf("encode error: %v", err)
	}
}
