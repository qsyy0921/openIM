package toolruntime

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

const (
	toolRuntimeTestTenant = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	toolRuntimeTestMember = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestStoreRetriesOnlyFailedSafeReadWithSameDurableCall(t *testing.T) {
	pool, ctx := toolRuntimeTestPool(t)
	runID, snapshotID := seedToolRuntimeRun(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent.runs WHERE id = $1::uuid`, runID)
	})

	var toolID string
	if err := pool.QueryRow(ctx, `
SELECT id::text
FROM capability.tool_descriptors
WHERE tenant_id = $1::uuid AND operation_id = 'enterprise.knowledge.search' AND version = '1'`,
		toolRuntimeTestTenant).Scan(&toolID); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: toolID, OperationID: "enterprise.knowledge.search", Risk: "read", RetrySemantics: "safe",
	}
	execution := agent.ExecutionContext{
		RunID: runID, TenantID: toolRuntimeTestTenant, MemberID: toolRuntimeTestMember,
		CapabilitySnapshotID: snapshotID,
	}
	request := CallRequest{
		CallID: "retry-read", OperationID: descriptor.OperationID, Arguments: map[string]any{"query": "policy"},
	}
	digest, err := argumentsDigest(request.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{Outcome: "allow", Reason: "integration_test"}
	store := NewStore(pool)

	prepared, err := store.Prepare(ctx, execution, descriptor, request, digest, decision, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Start(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(ctx, prepared, "temporary_read_failure", false); err != nil {
		t.Fatal(err)
	}

	retried, err := store.Prepare(ctx, execution, descriptor, request, digest, decision, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != prepared.ID || retried.State != "prepared" {
		t.Fatalf("retried=%#v original=%#v", retried, prepared)
	}
	var state string
	var attempts, retryEvents int
	var errorCode sql.NullString
	var completedAt sql.NullTime
	if err := pool.QueryRow(ctx, `
SELECT state, attempts, error_code, completed_at,
       (SELECT count(*) FROM audit.tool_events WHERE tool_call_id = call_record.id AND event_type = 'retry_prepared')
FROM agent.tool_calls AS call_record
WHERE id = $1::uuid`, retried.ID).Scan(&state, &attempts, &errorCode, &completedAt, &retryEvents); err != nil {
		t.Fatal(err)
	}
	if state != "prepared" || attempts != 1 || errorCode.Valid || completedAt.Valid || retryEvents != 1 {
		t.Fatalf("state=%q attempts=%d error=%#v completed=%#v retry_events=%d", state, attempts, errorCode, completedAt, retryEvents)
	}
	if err := store.Start(ctx, retried); err != nil {
		t.Fatal(err)
	}
	if err := store.Succeed(ctx, retried, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state, attempts FROM agent.tool_calls WHERE id = $1::uuid`, retried.ID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || attempts != 2 {
		t.Fatalf("state=%q attempts=%d", state, attempts)
	}

	unknownRequest := CallRequest{
		CallID: "unknown-read", OperationID: descriptor.OperationID, Arguments: map[string]any{"query": "uncertain"},
	}
	unknownDigest, err := argumentsDigest(unknownRequest.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := store.Prepare(ctx, execution, descriptor, unknownRequest, unknownDigest, decision, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Start(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(ctx, unknown, "uncertain_outcome", true); err != nil {
		t.Fatal(err)
	}
	unknownReplay, err := store.Prepare(ctx, execution, descriptor, unknownRequest, unknownDigest, decision, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if unknownReplay.ID != unknown.ID || unknownReplay.State != "unknown" {
		t.Fatalf("unknown replay=%#v original=%#v", unknownReplay, unknown)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM audit.tool_events
WHERE tool_call_id = $1::uuid AND event_type = 'retry_prepared'`, unknown.ID).Scan(&retryEvents); err != nil {
		t.Fatal(err)
	}
	if retryEvents != 0 {
		t.Fatalf("unknown call recorded %d retry events", retryEvents)
	}
}

func toolRuntimeTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func seedToolRuntimeRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	runID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	var snapshotID string
	if err := pool.QueryRow(ctx, `
INSERT INTO agent.runs (
    id, source_event_id, tenant_id, principal_member_id, conversation_id, sender_id,
    source_channel, conversation_sequence, execution_plane, trace_id,
    session_type, prompt, state, agent_id, agent_version_id,
    agent_deployment_id, agent_trigger_id, agent_spec_checksum, capability_snapshot_id
)
SELECT $1::uuid, $2, $3::uuid, $4::uuid, $2, 'sender',
       'openim', 1, 'passive', 'agent-run:' || $1::text,
       1, 'retry safe read', 'queued', d.id, v.id, dep.id, tr.id, v.spec_checksum,
       v.capability_snapshot_id
FROM agent.definitions AS d
JOIN agent.deployments AS dep
  ON dep.tenant_id = d.tenant_id AND dep.agent_id = d.id AND dep.slot = 'production'
JOIN agent.versions AS v
  ON v.tenant_id = d.tenant_id AND v.agent_id = d.id AND v.id = dep.active_version_id
JOIN agent.triggers AS tr
  ON tr.tenant_id = d.tenant_id AND tr.agent_id = d.id
 AND tr.trigger_type = 'mention_alias' AND tr.trigger_value = '@agent'
WHERE d.tenant_id = $3::uuid AND d.slug = 'knowledge-agent'
RETURNING capability_snapshot_id`, runID, sourceID, toolRuntimeTestTenant, toolRuntimeTestMember).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	return runID, snapshotID
}
