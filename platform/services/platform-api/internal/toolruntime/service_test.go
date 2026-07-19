package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type ledgerStub struct {
	grants  []string
	state   string
	unknown bool
}

func (s *ledgerStub) MemberGrants(context.Context, string, string) ([]string, error) {
	return s.grants, nil
}
func (s *ledgerStub) Prepare(_ context.Context, _ agent.ExecutionContext, _ capability.Descriptor, request CallRequest, _ string, decision Decision, _ time.Time) (PreparedCall, error) {
	state := "prepared"
	if decision.Outcome == "deny" {
		state = "denied"
	}
	if decision.Outcome == "require_approval" {
		state = "waiting_approval"
	}
	s.state = state
	return PreparedCall{ID: "call-1", CallID: request.CallID, OperationID: request.OperationID, State: state}, nil
}
func (s *ledgerStub) Start(context.Context, PreparedCall) error { s.state = "executing"; return nil }
func (s *ledgerStub) Succeed(context.Context, PreparedCall, any) error {
	s.state = "succeeded"
	return nil
}
func (s *ledgerStub) Fail(_ context.Context, _ PreparedCall, _ string, unknown bool) error {
	s.state, s.unknown = "failed", unknown
	return nil
}

type invokerStub struct{ err error }

func (s invokerStub) Invoke(context.Context, capability.Descriptor, map[string]any, string) (any, error) {
	return map[string]any{"ok": true}, s.err
}

func TestServiceExecutesPinnedAuthorizedRead(t *testing.T) {
	snapshot, execution := testSnapshotAndExecution(t, "read", "native")
	ledger := &ledgerStub{grants: []string{"knowledge:read"}}
	service, _ := NewService(ledger, invokerStub{}, time.Minute)
	ctx, _ := agent.BindExecutionContext(context.Background(), execution)
	prepared, result, err := service.Execute(ctx, snapshot, CallRequest{
		CallID: "search-1", OperationID: "enterprise.knowledge.search", Arguments: map[string]any{"query": "policy"},
	})
	if err != nil || prepared.State != "succeeded" || result == nil || ledger.state != "succeeded" {
		t.Fatalf("prepared=%#v result=%#v state=%q err=%v", prepared, result, ledger.state, err)
	}
}

func TestServiceRejectsArgumentsOutsidePinnedSchema(t *testing.T) {
	snapshot, execution := testSnapshotAndExecution(t, "read", "native")
	snapshot.Tools[0].InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"]}`)
	digest := sha256.Sum256(snapshot.Tools[0].InputSchema)
	snapshot.Tools[0].SchemaDigest = "sha256:" + hex.EncodeToString(digest[:])
	ledger := &ledgerStub{grants: []string{"knowledge:read"}}
	service, _ := NewService(ledger, invokerStub{}, time.Minute)
	ctx, _ := agent.BindExecutionContext(context.Background(), execution)
	if _, _, err := service.Execute(ctx, snapshot, CallRequest{
		CallID: "search-1", OperationID: "enterprise.knowledge.search", Arguments: map[string]any{"limit": 3},
	}); err == nil {
		t.Fatal("schema-invalid tool arguments were accepted")
	}
	if ledger.state != "" {
		t.Fatalf("schema-invalid call reached ledger state %q", ledger.state)
	}
}

func TestServiceDoesNotInvokeSideEffectBeforeApproval(t *testing.T) {
	snapshot, execution := testSnapshotAndExecution(t, "write", "keyed")
	ledger := &ledgerStub{grants: []string{"knowledge:read"}}
	service, _ := NewService(ledger, invokerStub{err: errors.New("must not execute")}, time.Minute)
	ctx, _ := agent.BindExecutionContext(context.Background(), execution)
	prepared, result, err := service.Execute(ctx, snapshot, CallRequest{
		CallID: "write-1", OperationID: "enterprise.knowledge.search", Arguments: map[string]any{"query": "policy"},
	})
	if err != nil || prepared.State != "waiting_approval" || result != nil || ledger.state != "waiting_approval" {
		t.Fatalf("prepared=%#v result=%#v state=%q err=%v", prepared, result, ledger.state, err)
	}
}

func TestServiceMarksSideEffectFailureUnknown(t *testing.T) {
	snapshot, execution := testSnapshotAndExecution(t, "write", "keyed")
	ledger := &ledgerStub{grants: []string{"knowledge:read"}}
	service, _ := NewService(ledger, invokerStub{err: errors.New("connection lost")}, time.Minute)
	ctx, _ := agent.BindExecutionContext(context.Background(), execution)
	// Simulate a digest-bound approval already moving the durable call back to prepared.
	service.ledger = approvedLedger{ledgerStub: ledger}
	_, _, err := service.Execute(ctx, snapshot, CallRequest{
		CallID: "write-1", OperationID: "enterprise.knowledge.search", Arguments: map[string]any{"query": "policy"},
	})
	if err == nil || !ledger.unknown {
		t.Fatalf("err=%v unknown=%v", err, ledger.unknown)
	}
}

type approvedLedger struct{ *ledgerStub }

func (s approvedLedger) Prepare(_ context.Context, _ agent.ExecutionContext, _ capability.Descriptor, request CallRequest, _ string, _ Decision, _ time.Time) (PreparedCall, error) {
	s.ledgerStub.state = "prepared"
	return PreparedCall{ID: "call-1", CallID: request.CallID, OperationID: request.OperationID, State: "prepared"}, nil
}

func testSnapshotAndExecution(t *testing.T, risk, idempotency string) (capability.Snapshot, agent.ExecutionContext) {
	t.Helper()
	descriptor := capability.Descriptor{
		ID: "tool-1", OperationID: "enterprise.knowledge.search", Version: "1", Name: "Search", Summary: "Search enterprise knowledge",
		SourceType: "core", SourceID: "test", Risk: risk, Permissions: []string{"knowledge:read"},
		Idempotency: idempotency, RetrySemantics: "safe", Audience: "passive", Timeout: time.Second,
		ParameterTerms: []string{"query"}, Examples: []string{"find policy"}, OutputKinds: []string{"text"},
		InputSchema: json.RawMessage(`{"type":"object"}`), SchemaDigest: "sha256:a2c799262a3ce3c19ef5cdd983bf3d12b43ab3c426227091b909dcb7054738c0",
	}
	payload := json.RawMessage(`{"schema_version":1,"tools":[{"operation_id":"enterprise.knowledge.search","version":"1"}]}`)
	snapshot := capability.Snapshot{
		ID:            "capability-v1:ec15cd82639295686f2108b3ef351dc22e546e60ffd81d8b7c1b37c1f199ba75",
		SchemaVersion: 1, Payload: payload, Tools: []capability.Descriptor{descriptor},
	}
	execution := agent.ExecutionContext{
		RunID: "run-1", TraceID: "trace-1", TenantID: "tenant-1", MemberID: "member-1",
		SourceChannel: "openim", ConversationID: "si_a_b", ExecutionPlane: "passive",
		AgentID: "agent-1", AgentVersionID: "version-1", AgentSpecChecksum: "sha256:abc",
		CapabilitySnapshotID: snapshot.ID,
	}
	return snapshot, execution
}
