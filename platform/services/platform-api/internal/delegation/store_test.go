package delegation

import (
	"strings"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
)

func TestValidateSpawnForbidsRecursiveInternalDelegation(t *testing.T) {
	execution := agent.ExecutionContext{
		RunID: "run", TenantID: "tenant", MemberID: "member", ExecutionPlane: "internal",
	}
	if err := validateSpawn(execution, "research-agent", "task", "key"); err == nil {
		t.Fatal("internal Agent was allowed to delegate recursively")
	}
}

func TestValidateSpawnAcceptsBoundedPassiveTask(t *testing.T) {
	execution := agent.ExecutionContext{
		RunID: "run", TenantID: "tenant", MemberID: "member", ExecutionPlane: "passive",
	}
	if err := validateSpawn(execution, "research-agent", strings.Repeat("研", 4000), "tool:run:call"); err != nil {
		t.Fatalf("valid delegation rejected: %v", err)
	}
	if err := validateSpawn(execution, "Research Agent", "task", "key"); err == nil {
		t.Fatal("invalid Agent slug was accepted")
	}
}
