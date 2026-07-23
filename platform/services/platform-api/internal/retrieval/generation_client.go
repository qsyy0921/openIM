package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type HTTPGenerationClient struct {
	baseURL string
	model   string
	http    *http.Client
}

const LockedGenerationModel = "gpt-5.6-terra"

func NewHTTPGenerationClient(baseURL string, timeout time.Duration, model string) (*HTTPGenerationClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("generation evaluation URL is invalid")
	}
	if timeout <= 0 || strings.TrimSpace(model) != LockedGenerationModel {
		return nil, errors.New("generation evaluation model contract is invalid")
	}
	return &HTTPGenerationClient{baseURL: strings.TrimRight(baseURL, "/"), model: model, http: &http.Client{Timeout: timeout}}, nil
}

func (c *HTTPGenerationClient) Generate(ctx context.Context, request GenerationRequest) (GenerationCandidate, error) {
	if request.CaseID == "" || request.Question == "" || len(request.Evidence) == 0 || len(request.Evidence) > 8 {
		return GenerationCandidate{}, errors.New("generation evaluation request is invalid")
	}
	body, err := json.Marshal(struct {
		RunID              string     `json:"run_id"`
		TenantID           string     `json:"tenant_id"`
		ConversationID     string     `json:"conversation_id"`
		SenderID           string     `json:"sender_id"`
		AgentID            string     `json:"agent_id"`
		AgentVersionID     string     `json:"agent_version_id"`
		AgentSpecChecksum  string     `json:"agent_spec_checksum"`
		Instructions       string     `json:"instructions"`
		ModelRoute         string     `json:"model_route"`
		AllowedActionTypes []string   `json:"allowed_action_types"`
		Content            string     `json:"content"`
		Evidence           []Evidence `json:"evidence"`
		Memory             []any      `json:"memory"`
		ToolResults        []any      `json:"tool_results"`
		Skills             []any      `json:"skills"`
	}{
		RunID: "generation-eval:" + request.CaseID, TenantID: "evaluation", ConversationID: "evaluation",
		SenderID: "evaluation", AgentID: "enterprise-knowledge-evaluation", AgentVersionID: "1",
		AgentSpecChecksum: "sha256:" + strings.Repeat("0", 64),
		Instructions:      "只基于提供的企业证据回答。证据不足时必须明确拒答，不得补充外部知识或猜测。",
		ModelRoute:        c.model, Content: request.Question, AllowedActionTypes: []string{}, Evidence: request.Evidence,
		Memory: []any{}, ToolResults: []any{}, Skills: []any{},
	})
	if err != nil {
		return GenerationCandidate{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/candidates", bytes.NewReader(body))
	if err != nil {
		return GenerationCandidate{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return GenerationCandidate{}, fmt.Errorf("call generation evaluation provider: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return GenerationCandidate{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GenerationCandidate{}, fmt.Errorf("generation evaluation provider returned status %d", response.StatusCode)
	}
	var payload struct {
		GenerationCandidate
		ActionIntent json.RawMessage `json:"action_intent"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return GenerationCandidate{}, fmt.Errorf("decode generation evaluation candidate: %w", err)
	}
	if len(payload.ActionIntent) > 0 && string(payload.ActionIntent) != "null" {
		return GenerationCandidate{}, errors.New("generation evaluation candidate proposed an action")
	}
	if err := validateGenerationCandidate(payload.GenerationCandidate, request.Evidence, c.model); err != nil {
		return GenerationCandidate{}, err
	}
	return payload.GenerationCandidate, nil
}

func validateGenerationCandidate(candidate GenerationCandidate, evidence []Evidence, model string) error {
	candidate.Text = strings.TrimSpace(candidate.Text)
	if candidate.Text == "" || len(candidate.Text) > maxCandidateAnswerBytes ||
		!utf8.ValidString(candidate.Text) || candidate.Model != model ||
		candidate.ProviderResponseID == "" || len(candidate.ProviderResponseID) > 256 {
		return errors.New("generation evaluation candidate violates the model contract")
	}
	if candidate.GroundingStatus != GenerationGrounded &&
		candidate.GroundingStatus != GenerationInsufficientEvidence {
		return errors.New("generation evaluation candidate has an invalid grounding status")
	}
	if len(candidate.CitationIDs) > maxEvidenceItems {
		return errors.New("generation evaluation candidate exceeds the citation bound")
	}
	available := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if item.CitationID == "" {
			return errors.New("generation evaluation evidence is missing a citation ID")
		}
		available[item.CitationID] = struct{}{}
	}
	declared := make(map[string]struct{}, len(candidate.CitationIDs))
	for _, citationID := range candidate.CitationIDs {
		if _, ok := available[citationID]; !ok {
			return errors.New("generation evaluation candidate cited unavailable evidence")
		}
		if _, duplicate := declared[citationID]; duplicate {
			return errors.New("generation evaluation candidate contains duplicate citations")
		}
		declared[citationID] = struct{}{}
		if !strings.Contains(candidate.Text, "["+citationID+"]") {
			return errors.New("generation evaluation candidate omitted a declared citation")
		}
	}
	for _, match := range generatedCitationPattern.FindAllStringSubmatch(candidate.Text, -1) {
		if _, ok := declared[match[1]]; !ok {
			return errors.New("generation evaluation candidate contains an undeclared citation")
		}
	}
	if candidate.GroundingStatus == GenerationGrounded && len(declared) == 0 {
		return errors.New("grounded generation evaluation candidate requires a citation")
	}
	if candidate.GroundingStatus == GenerationInsufficientEvidence && len(declared) != 0 {
		return errors.New("insufficient-evidence candidate cannot contain a citation")
	}
	return nil
}
