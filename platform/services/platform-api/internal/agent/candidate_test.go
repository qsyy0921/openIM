package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCandidateClientUsesStructuredContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/candidates" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var body struct {
			RunID             string              `json:"run_id"`
			AgentVersionID    string              `json:"agent_version_id"`
			AgentSpecChecksum string              `json:"agent_spec_checksum"`
			ModelRoute        string              `json:"model_route"`
			Content           string              `json:"content"`
			Evidence          []Evidence          `json:"evidence"`
			Memory            []MemoryFact        `json:"memory"`
			ToolResults       []ToolResultContext `json:"tool_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.RunID != "run-1" || body.AgentVersionID != "version-1" || body.AgentSpecChecksum != seedAgentSpecChecksum ||
			body.ModelRoute != DeepSeekV4ProRoute || body.Content != "question" || len(body.Evidence) != 1 || len(body.Memory) != 1 {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(Candidate{Text: "answer [C1]", Model: "model", ProviderResponseID: "resp-1", CitationIDs: []string{"C1"}, GroundingStatus: GroundingGrounded})
	}))
	defer server.Close()
	client := NewCandidateClient(server.URL, time.Second)
	spec, err := ParseAgentSpec(1, []byte(seedAgentSpecJSON), seedAgentSpecChecksum)
	if err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "run-1", TenantID: "tenant", ConversationID: "si_a_b", SenderID: "a", Prompt: "question",
		AgentID: "agent-1", AgentVersionID: "version-1", AgentSpecChecksum: seedAgentSpecChecksum}
	candidate, err := client.Generate(context.Background(), run, CatalogVersion{Spec: spec}, []Evidence{{CitationID: "C1"}}, []MemoryFact{{ID: "memory-1", Category: "preference", Checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Content: "concise"}}, nil)
	if err != nil || candidate.Text != "answer [C1]" || candidate.GroundingStatus != GroundingGrounded {
		t.Fatalf("Generate() = %#v, %v", candidate, err)
	}
}

func TestCandidateClientDoesNotFallbackOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	client := NewCandidateClient(server.URL, time.Second)
	if _, err := client.Generate(context.Background(), Run{ID: "run-1"}, CatalogVersion{}, []Evidence{{CitationID: "C1"}}, nil, nil); err == nil {
		t.Fatal("provider failure returned a candidate")
	}
}
