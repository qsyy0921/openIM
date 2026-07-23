package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

func TestToolPlannerValidatesArgumentsAgainstPinnedSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/tool-plans" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		_ = json.NewEncoder(response).Encode(ToolPlan{
			Arguments: map[string]any{"query": "agent memory"}, ProviderResponseID: "plan-1",
		})
	}))
	defer server.Close()
	descriptor := capability.Descriptor{
		OperationID: "mcp.arxiv.search", Name: "Search", Summary: "Search papers",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"]}`),
	}
	plan, err := NewToolPlannerClient(server.URL, time.Second).Plan(context.Background(), Run{
		ID: "run-1", Prompt: "search",
	}, CatalogVersion{}, descriptor)
	if err != nil || plan.Arguments["query"] != "agent memory" {
		t.Fatalf("Plan()=%#v, %v", plan, err)
	}
}
