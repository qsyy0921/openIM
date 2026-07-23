package agent

import (
	"context"
	"testing"
)

func TestExecutionContextRequiresCompletePinnedIdentity(t *testing.T) {
	execution := ExecutionContext{
		RunID: "run-1", TraceID: "trace-1", TenantID: "tenant-1", MemberID: "member-1",
		SourceChannel: "openim", ConversationID: "si_a_b", ExecutionPlane: "passive",
		AgentID: "agent-1", AgentVersionID: "version-1", AgentSpecChecksum: "checksum", CapabilitySnapshotID: "capability-v1:abc",
	}
	ctx, err := BindExecutionContext(context.Background(), execution)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := RequireExecutionContext(ctx)
	if err != nil || actual != execution {
		t.Fatalf("RequireExecutionContext() = %#v, %v", actual, err)
	}
}

func TestExecutionContextFailsClosedWhenMissing(t *testing.T) {
	if _, err := RequireExecutionContext(context.Background()); err == nil {
		t.Fatal("missing execution context was accepted")
	}
	if _, err := BindExecutionContext(context.Background(), ExecutionContext{ExecutionPlane: "passive"}); err == nil {
		t.Fatal("incomplete execution context was accepted")
	}
}
