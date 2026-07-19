package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPGenerationClientUsesStrictCandidateContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/candidates" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		var body struct {
			ModelRoute string     `json:"model_route"`
			Evidence   []Evidence `json:"evidence"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ModelRoute != "local-eval" || len(body.Evidence) != 1 {
			t.Fatalf("unexpected request: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text": "answer [C1]", "model": "local-eval", "provider_response_id": "response",
			"citation_ids": []string{"C1"}, "grounding_status": "grounded", "action_intent": nil,
		})
	}))
	defer server.Close()
	client, err := NewHTTPGenerationClient(server.URL, time.Second, "local-eval")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := client.Generate(context.Background(), GenerationRequest{
		CaseID: "case", Question: "question", Evidence: []Evidence{{CitationID: "C1", Content: "evidence"}},
	})
	if err != nil || candidate.GroundingStatus != GenerationGrounded {
		t.Fatalf("candidate=%#v err=%v", candidate, err)
	}
}

func TestHTTPGenerationClientRejectsActionCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text": "answer [C1]", "model": "local-eval", "provider_response_id": "response",
			"citation_ids": []string{"C1"}, "grounding_status": "grounded",
			"action_intent": map[string]any{"type": "create_ticket", "title": "forbidden"},
		})
	}))
	defer server.Close()
	client, err := NewHTTPGenerationClient(server.URL, time.Second, "local-eval")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerationRequest{
		CaseID: "case", Question: "question", Evidence: []Evidence{{CitationID: "C1", Content: "evidence"}},
	})
	if err == nil {
		t.Fatal("generation evaluation accepted an action candidate")
	}
}
