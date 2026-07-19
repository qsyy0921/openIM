package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCallsTelegramWithoutExposingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottest-token/getMe" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 42, "username": "enterprise_bot"}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := client.GetMe(context.Background())
	if err != nil || bot.ID != 42 || bot.Username != "enterprise_bot" {
		t.Fatalf("GetMe() = %#v, %v", bot, err)
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "error_code": 429, "description": "slow down",
			"parameters": map[string]any{"retry_after": 2},
		})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetUpdates(context.Background(), 0, 1)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != 429 || apiErr.RetryAfter != 2*time.Second {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatal("Bot Token leaked through the error")
	}
}
