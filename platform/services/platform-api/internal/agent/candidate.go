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
	GroundingStatus    string                 `json:"grounding_status"`
	ActionIntent       *ActionIntentCandidate `json:"action_intent"`
}

const (
	GroundingGrounded             = "grounded"
	GroundingInsufficientEvidence = "insufficient_evidence"
	GroundingNotApplicable        = "not_applicable"
)

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

type MemoryFact struct {
	ID       string `json:"memory_id"`
	Category string `json:"category"`
	Checksum string `json:"checksum"`
	Content  string `json:"content"`
}

type ToolResultContext struct {
	OperationID string         `json:"operation_id"`
	Result      map[string]any `json:"result"`
}

type SkillContext struct {
	SkillID       string `json:"skill_id"`
	Version       string `json:"version"`
	Name          string `json:"name"`
	Instructions  string `json:"instructions"`
	ContentDigest string `json:"content_digest"`
}

type CandidateClient struct {
	baseURL string
	http    *http.Client
}

func NewCandidateClient(baseURL string, timeout time.Duration) *CandidateClient {
	return &CandidateClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

func (c *CandidateClient) Generate(ctx context.Context, run Run, version CatalogVersion, evidence []Evidence, memories []MemoryFact, toolResults []ToolResultContext) (Candidate, error) {
	skills := make([]SkillContext, 0, len(version.Skills))
	for _, skill := range version.Skills {
		if skill.Audience == run.ExecutionPlane {
			skills = append(skills, SkillContext{
				SkillID: skill.SkillID, Version: skill.Version, Name: skill.Name,
				Instructions: skill.Instructions, ContentDigest: skill.ContentDigest,
			})
		}
	}
	allowedActions := append([]string{}, version.Spec.AllowedActionTypes...)
	evidence = append([]Evidence{}, evidence...)
	memories = append([]MemoryFact{}, memories...)
	toolResults = append([]ToolResultContext{}, toolResults...)
	body, err := json.Marshal(struct {
		RunID             string              `json:"run_id"`
		TenantID          string              `json:"tenant_id"`
		ConversationID    string              `json:"conversation_id"`
		SenderID          string              `json:"sender_id"`
		AgentID           string              `json:"agent_id"`
		AgentVersionID    string              `json:"agent_version_id"`
		AgentSpecChecksum string              `json:"agent_spec_checksum"`
		Instructions      string              `json:"instructions"`
		ModelRoute        string              `json:"model_route"`
		AllowedActions    []string            `json:"allowed_action_types"`
		Content           string              `json:"content"`
		Evidence          []Evidence          `json:"evidence"`
		Memory            []MemoryFact        `json:"memory"`
		ToolResults       []ToolResultContext `json:"tool_results"`
		Skills            []SkillContext      `json:"skills"`
	}{run.ID, run.TenantID, run.ConversationID, run.SenderID, run.AgentID, run.AgentVersionID,
		run.AgentSpecChecksum, version.Spec.Instructions, version.Spec.ModelRoute,
		allowedActions, run.Prompt, evidence, memories, toolResults, skills})
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
	if candidate.Text == "" || candidate.Model == "" || candidate.ProviderResponseID == "" || candidate.GroundingStatus == "" {
		return Candidate{}, errors.New("intelligence worker returned incomplete candidate")
	}
	return candidate, nil
}
