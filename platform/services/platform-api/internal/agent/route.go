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

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

const IntentRouterVersion = "openim-intent-routing-v1"

type RouteCandidate struct {
	OperationID         string   `json:"operation_id"`
	OriginalLexicalRank *int     `json:"original_lexical_rank"`
	OriginalDenseRank   *int     `json:"original_dense_rank"`
	IntentViewRank      *int     `json:"intent_view_rank"`
	ReasonCodes         []string `json:"reason_codes"`
}

type RouteResult struct {
	Status             string           `json:"status"`
	OperationID        string           `json:"operation_id"`
	Clarification      string           `json:"clarification"`
	ProviderResponseID string           `json:"provider_response_id"`
	RouterVersion      string           `json:"router_version"`
	Candidates         []RouteCandidate `json:"candidates"`
}

func (r RouteResult) Validate(snapshot capability.Snapshot, executionPlane string) error {
	if r.RouterVersion != IntentRouterVersion || r.ProviderResponseID == "" || len(r.Candidates) > 3 {
		return errors.New("intent route identity or candidate bound is invalid")
	}
	for _, candidate := range r.Candidates {
		if _, visible := snapshot.Tool(candidate.OperationID, executionPlane); !visible {
			return fmt.Errorf("intent route contains operation outside pinned snapshot: %s", candidate.OperationID)
		}
	}
	switch r.Status {
	case "selected":
		if r.OperationID == "" || r.Clarification != "" {
			return errors.New("selected intent route shape is invalid")
		}
		if _, visible := snapshot.Tool(r.OperationID, executionPlane); !visible {
			return errors.New("selected intent operation is outside pinned snapshot")
		}
	case "clarify":
		if r.OperationID != "" || r.Clarification == "" {
			return errors.New("clarification intent route shape is invalid")
		}
	case "no_tool":
		if r.OperationID != "" || r.Clarification != "" {
			return errors.New("tool-free intent route shape is invalid")
		}
	default:
		return errors.New("intent route status is invalid")
	}
	return nil
}

type RouterClient struct {
	baseURL string
	http    *http.Client
}

func NewRouterClient(baseURL string, timeout time.Duration) *RouterClient {
	return &RouterClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

func (c *RouterClient) Route(ctx context.Context, run Run, snapshot capability.Snapshot) (RouteResult, error) {
	type operation struct {
		OperationID    string   `json:"operation_id"`
		Name           string   `json:"name"`
		Summary        string   `json:"summary"`
		ParameterTerms []string `json:"parameter_terms"`
		Examples       []string `json:"examples"`
		OutputKinds    []string `json:"output_kinds"`
	}
	operations := make([]operation, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		if tool.Audience != run.ExecutionPlane {
			continue
		}
		operations = append(operations, operation{
			OperationID: tool.OperationID, Name: tool.Name, Summary: tool.Summary,
			ParameterTerms: tool.ParameterTerms, Examples: tool.Examples, OutputKinds: tool.OutputKinds,
		})
	}
	if len(operations) == 0 {
		return RouteResult{}, errors.New("pinned capability snapshot has no operations for this execution plane")
	}
	body, err := json.Marshal(struct {
		RunID                string      `json:"run_id"`
		CapabilitySnapshotID string      `json:"capability_snapshot_id"`
		Content              string      `json:"content"`
		Operations           []operation `json:"operations"`
	}{run.ID, run.CapabilitySnapshotID, run.Prompt, operations})
	if err != nil {
		return RouteResult{}, fmt.Errorf("encode intent route request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/routes", bytes.NewReader(body))
	if err != nil {
		return RouteResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return RouteResult{}, fmt.Errorf("call intent router: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RouteResult{}, fmt.Errorf("read intent route response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RouteResult{}, fmt.Errorf("intent router returned status %d", response.StatusCode)
	}
	var result RouteResult
	if err := json.Unmarshal(data, &result); err != nil {
		return RouteResult{}, fmt.Errorf("decode intent route response: %w", err)
	}
	if err := result.Validate(snapshot, run.ExecutionPlane); err != nil {
		return RouteResult{}, err
	}
	return result, nil
}
