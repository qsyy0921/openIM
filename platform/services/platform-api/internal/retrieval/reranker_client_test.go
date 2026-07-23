package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRerankerUsesLockedBoundedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/rerank" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Query      string            `json:"query"`
			Candidates []RerankCandidate `json:"candidates"`
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Query != "retention policy" || len(payload.Candidates) != 1 ||
			payload.Candidates[0].CandidateID != "chunk-1" {
			t.Fatalf("payload = %#v", payload)
		}
		_ = json.NewEncoder(writer).Encode(RerankResponse{
			Model: LockedRerankerModel, Revision: LockedRerankerRevision,
			Scores: []RerankScore{{CandidateID: "chunk-1", Score: 0.75}},
		})
	}))
	defer server.Close()
	client, err := NewHTTPReranker(server.URL, time.Second, LockedRerankerModel, LockedRerankerRevision)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Rerank(context.Background(), "retention policy", []RerankCandidate{{
		CandidateID: "chunk-1", Content: "Retention is seven years.",
	}})
	if err != nil || len(result.Scores) != 1 || result.Scores[0].Score != 0.75 {
		t.Fatalf("Rerank() = %#v, %v", result, err)
	}
}

func TestHTTPRerankerFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewHTTPReranker(server.URL, time.Second, LockedRerankerModel, LockedRerankerRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Rerank(context.Background(), "query", []RerankCandidate{{
		CandidateID: "chunk-1", Content: "content",
	}}); err == nil {
		t.Fatal("reranker failure silently returned candidates")
	}
}

func TestHTTPRerankerRejectsWrongRevisionAndUnknownCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(RerankResponse{
			Model: LockedRerankerModel, Revision: LockedRerankerRevision,
			Scores: []RerankScore{{CandidateID: "unknown", Score: 1}},
		})
	}))
	defer server.Close()
	if _, err := NewHTTPReranker(server.URL, time.Second, LockedRerankerModel, "moving-main"); err == nil {
		t.Fatal("moving reranker revision was accepted")
	}
	client, err := NewHTTPReranker(server.URL, time.Second, LockedRerankerModel, LockedRerankerRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Rerank(context.Background(), "query", []RerankCandidate{{
		CandidateID: "chunk-1", Content: "content",
	}}); err == nil {
		t.Fatal("unknown reranker candidate was accepted")
	}
}
