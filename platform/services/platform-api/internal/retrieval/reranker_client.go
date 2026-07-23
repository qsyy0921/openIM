package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	LockedRerankerModel    = "BAAI/bge-reranker-v2-m3"
	LockedRerankerRevision = "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"
)

type RerankCandidate struct {
	CandidateID string `json:"candidate_id"`
	Content     string `json:"content"`
}

type RerankScore struct {
	CandidateID string  `json:"candidate_id"`
	Score       float64 `json:"score"`
}

type RerankResponse struct {
	Model    string        `json:"model"`
	Revision string        `json:"revision"`
	Scores   []RerankScore `json:"scores"`
}

type Reranker interface {
	Rerank(context.Context, string, []RerankCandidate) (RerankResponse, error)
}

type HTTPReranker struct {
	endpoint string
	client   *http.Client
	model    string
	revision string
}

func NewHTTPReranker(baseURL string, timeout time.Duration, model, revision string) (*HTTPReranker, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("reranker service base URL is invalid")
	}
	if timeout <= 0 || model != LockedRerankerModel || revision != LockedRerankerRevision {
		return nil, errors.New("reranker service contract is invalid")
	}
	return &HTTPReranker{
		endpoint: strings.TrimRight(baseURL, "/") + "/v1/rerank",
		client:   &http.Client{Timeout: timeout}, model: model, revision: revision,
	}, nil
}

func (c *HTTPReranker) Rerank(ctx context.Context, query string, candidates []RerankCandidate) (RerankResponse, error) {
	if len(query) < 1 || len(query) > 2000 || len(candidates) < 1 || len(candidates) > 32 {
		return RerankResponse{}, errors.New("reranker request exceeds its bounded contract")
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.CandidateID == "" || len(candidate.CandidateID) > 128 ||
			candidate.Content == "" || len(candidate.Content) > 8000 {
			return RerankResponse{}, errors.New("reranker candidate is invalid")
		}
		if _, exists := seen[candidate.CandidateID]; exists {
			return RerankResponse{}, errors.New("reranker candidate IDs must be unique")
		}
		seen[candidate.CandidateID] = struct{}{}
	}
	payload, err := json.Marshal(struct {
		Query      string            `json:"query"`
		Candidates []RerankCandidate `json:"candidates"`
	}{Query: query, Candidates: candidates})
	if err != nil {
		return RerankResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return RerankResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return RerankResponse{}, fmt.Errorf("call required reranker: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RerankResponse{}, fmt.Errorf("required reranker returned HTTP %d", response.StatusCode)
	}
	var result RerankResponse
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return RerankResponse{}, fmt.Errorf("decode reranker response: %w", err)
	}
	if result.Model != c.model || result.Revision != c.revision || len(result.Scores) != len(candidates) {
		return RerankResponse{}, errors.New("reranker response violates the locked model contract")
	}
	returned := make(map[string]struct{}, len(result.Scores))
	for _, score := range result.Scores {
		if _, expected := seen[score.CandidateID]; !expected ||
			math.IsNaN(score.Score) || math.IsInf(score.Score, 0) {
			return RerankResponse{}, errors.New("reranker response contains an invalid candidate or score")
		}
		if _, duplicate := returned[score.CandidateID]; duplicate {
			return RerankResponse{}, errors.New("reranker response contains duplicate candidate IDs")
		}
		returned[score.CandidateID] = struct{}{}
	}
	return result, nil
}

var _ Reranker = (*HTTPReranker)(nil)
