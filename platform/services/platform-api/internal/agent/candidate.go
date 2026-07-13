package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Candidate struct {
	Text               string                 `json:"text"`
	Model              string                 `json:"model"`
	ProviderResponseID string                 `json:"provider_response_id"`
	CitationIDs        []string               `json:"citation_ids"`
	ActionIntent       *ActionIntentCandidate `json:"action_intent"`
}

type ActionIntentCandidate struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

type Evidence struct {
	CitationID string `json:"citation_id"`
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
	ChunkID    string `json:"chunk_id"`
	Title      string `json:"title"`
	SourceURI  string `json:"source_uri"`
	Checksum   string `json:"checksum"`
	Content    string `json:"content"`
}

type CandidateClient struct {
	baseURL string
	http    *http.Client
}

func NewCandidateClient(baseURL string, timeout time.Duration) *CandidateClient {
	return &CandidateClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

func (c *CandidateClient) Generate(ctx context.Context, run Run, version CatalogVersion, evidence []Evidence) (Candidate, error) {
	body, err := json.Marshal(struct {
		RunID             string     `json:"run_id"`
		TenantID          string     `json:"tenant_id"`
		ConversationID    string     `json:"conversation_id"`
		SenderID          string     `json:"sender_id"`
		AgentID           string     `json:"agent_id"`
		AgentVersionID    string     `json:"agent_version_id"`
		AgentSpecChecksum string     `json:"agent_spec_checksum"`
		Instructions      string     `json:"instructions"`
		ModelRoute        string     `json:"model_route"`
		AllowedActions    []string   `json:"allowed_action_types"`
		Content           string     `json:"content"`
		Evidence          []Evidence `json:"evidence"`
	}{run.ID, run.TenantID, run.ConversationID, run.SenderID, run.AgentID, run.AgentVersionID,
		run.AgentSpecChecksum, version.Spec.Instructions, version.Spec.ModelRoute,
		version.Spec.AllowedActionTypes, run.Prompt, evidence})
	if err != nil {
		return Candidate{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/candidates", bytes.NewReader(body))
	if err != nil {
		return Candidate{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return Candidate{}, fmt.Errorf("call intelligence worker: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Candidate{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Candidate{}, fmt.Errorf("intelligence worker returned status %d", response.StatusCode)
	}
	var candidate Candidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		return Candidate{}, fmt.Errorf("decode candidate: %w", err)
	}
	if candidate.Text == "" || candidate.Model == "" || candidate.ProviderResponseID == "" {
		return Candidate{}, errors.New("intelligence worker returned incomplete candidate")
	}
	return candidate, nil
}
