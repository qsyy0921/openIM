package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type ToolPlan struct {
	Arguments          map[string]any `json:"arguments"`
	ProviderResponseID string         `json:"provider_response_id"`
}

func (p ToolPlan) Validate(descriptor capability.Descriptor) error {
	if p.ProviderResponseID == "" || p.Arguments == nil {
		return errors.New("tool plan is incomplete")
	}
	decoder := json.NewDecoder(bytes.NewReader(descriptor.InputSchema))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode tool plan schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("urn:openim:tool-plan", document); err != nil {
		return err
	}
	schema, err := compiler.Compile("urn:openim:tool-plan")
	if err != nil {
		return err
	}
	if err := schema.Validate(p.Arguments); err != nil {
		return fmt.Errorf("planned tool arguments violate pinned schema: %w", err)
	}
	return nil
}

type ToolPlannerClient struct {
	baseURL string
	http    *http.Client
}

func NewToolPlannerClient(baseURL string, timeout time.Duration) *ToolPlannerClient {
	return &ToolPlannerClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

func (c *ToolPlannerClient) Plan(ctx context.Context, run Run, version CatalogVersion, descriptor capability.Descriptor) (ToolPlan, error) {
	var inputSchema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(descriptor.InputSchema))
	decoder.UseNumber()
	if err := decoder.Decode(&inputSchema); err != nil {
		return ToolPlan{}, fmt.Errorf("decode selected tool schema: %w", err)
	}
	skillInstructions := make([]string, 0, len(version.Skills))
	for _, skill := range version.Skills {
		if skill.Audience != run.ExecutionPlane {
			continue
		}
		for _, operation := range skill.ToolOperations {
			if operation == descriptor.OperationID {
				skillInstructions = append(skillInstructions, skill.Instructions)
				break
			}
		}
	}
	body, err := json.Marshal(map[string]any{
		"run_id": run.ID, "content": run.Prompt,
		"operation": map[string]any{
			"operation_id": descriptor.OperationID, "name": descriptor.Name,
			"summary": descriptor.Summary, "input_schema": inputSchema,
			"skill_instructions": skillInstructions,
		},
	})
	if err != nil {
		return ToolPlan{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/tool-plans", bytes.NewReader(body))
	if err != nil {
		return ToolPlan{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ToolPlan{}, fmt.Errorf("call tool planner: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ToolPlan{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ToolPlan{}, fmt.Errorf("tool planner returned status %d", response.StatusCode)
	}
	var plan ToolPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return ToolPlan{}, fmt.Errorf("decode tool plan: %w", err)
	}
	if err := plan.Validate(descriptor); err != nil {
		return ToolPlan{}, err
	}
	return plan, nil
}
