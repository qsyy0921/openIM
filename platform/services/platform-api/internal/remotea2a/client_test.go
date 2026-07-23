package remotea2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClientResolvesPinnedCardAndSendsBoundedMessage(t *testing.T) {
	var serverURL string
	var receivedMessageID, receivedKey string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/agent-card.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": "1.0", "name": "Research Agent", "description": "bounded research",
				"supportedInterfaces": []map[string]string{{"url": serverURL + "/a2a", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"}},
				"skills":              []map[string]any{{"id": "research", "name": "Research", "description": "research", "tags": []string{"research"}}},
			})
		case "/a2a/message:send":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			receivedKey = r.Header.Get("Idempotency-Key")
			var body struct {
				Message struct {
					MessageID string `json:"messageId"`
				} `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			receivedMessageID = body.Message.MessageID
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"id": "remote-task-1", "status": map[string]string{"state": "TASK_STATE_COMPLETED"}}})
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	serverURL = server.URL
	parsed, _ := url.Parse(server.URL)
	client, err := NewClient(Config{AllowedHosts: []string{parsed.Hostname()}, Timeout: 5 * time.Second, HTTPClient: server.Client()}, func(key string) (string, bool) {
		return "secret", key == "REMOTE_A2A_TOKEN"
	})
	if err != nil {
		t.Fatal(err)
	}
	cardRaw, _ := json.Marshal(map[string]any{
		"protocolVersion": "1.0", "name": "Research Agent", "description": "bounded research",
		"supportedInterfaces": []map[string]string{{"url": serverURL + "/a2a", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"}},
		"skills":              []map[string]any{{"id": "research", "name": "Research", "description": "research", "tags": []string{"research"}}},
	})
	canonical, _ := canonicalJSON(cardRaw)
	resolved, err := client.ResolveCard(context.Background(), server.URL+"/.well-known/agent-card.json", digestBytes(canonical), "REMOTE_A2A_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendMessage(context.Background(), resolved.EndpointURL, "11111111-1111-4111-8111-111111111111", "analyze", "run:call", "REMOTE_A2A_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "completed" || result.RemoteTaskID != "remote-task-1" || receivedMessageID == "" || receivedKey != "run:call" {
		t.Fatalf("result=%#v message=%q key=%q", result, receivedMessageID, receivedKey)
	}
}

func TestClientRejectsUnpinnedOrUnallowlistedCards(t *testing.T) {
	client, err := NewClient(Config{AllowedHosts: []string{"allowed.example"}, Timeout: time.Second}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResolveCard(context.Background(), "https://blocked.example/.well-known/agent-card.json", "sha256:bad", ""); err == nil {
		t.Fatal("unallowlisted Agent Card was accepted")
	}
}

func TestPrivateAddressRequiresExplicitCIDR(t *testing.T) {
	client, err := NewClient(Config{AllowedHosts: []string{"127.0.0.1"}, Timeout: time.Second}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if client.allowedIP([]byte{127, 0, 0, 1}) {
		t.Fatal("loopback address was accepted without a CIDR pin")
	}
	client, err = NewClient(Config{AllowedHosts: []string{"127.0.0.1"}, AllowedPrivateCIDRs: []string{"127.0.0.1/32"}, Timeout: time.Second}, func(string) (string, bool) { return "", false })
	if err != nil || !client.allowedIP([]byte{127, 0, 0, 1}) {
		t.Fatal("explicitly pinned loopback CIDR was rejected")
	}
}
