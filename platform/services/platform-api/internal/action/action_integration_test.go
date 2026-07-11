package action

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testTenant = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testMember = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestApprovedExecutionIsIdempotentAndVerified(t *testing.T) {
	pool, ctx := actionTestPool(t)
	runID := seedWaitingRun(t, ctx, pool, "create a verified ticket")
	store := NewStore(pool)
	intent, err := store.EnsureIntent(ctx, IntentRequest{RunID: runID, TenantID: testTenant, MemberID: testMember, ActionType: "create_ticket", Title: "Verify production incident"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAction(t, pool, runID)
	assertTicketCount(t, ctx, pool, intent.ID, 0)
	if _, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, "sha256:wrong"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong digest error=%v", err)
	}
	if _, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: newUUIDForTest(t)}, intent.ID, intent.Digest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong member error=%v", err)
	}
	first, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, intent.Digest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, intent.Digest)
	if err != nil || second.ExecutionID != first.ExecutionID {
		t.Fatalf("duplicate approval=%#v,%v", second, err)
	}
	assertTicketCount(t, ctx, pool, intent.ID, 0)
	executor := NewExecutor(pool, 10*time.Millisecond, 5*time.Second, 3)
	if err := executor.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertTicketCount(t, ctx, pool, intent.ID, 1)
	if err := executor.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertTicketCount(t, ctx, pool, intent.ID, 1)
	var runState, intentState, executionState string
	if err := pool.QueryRow(ctx, `SELECT r.state,i.state,e.state FROM agent.runs r JOIN action.intents i ON i.run_id=r.id JOIN action.executions e ON e.intent_id=i.id WHERE r.id=$1::uuid`, runID).Scan(&runState, &intentState, &executionState); err != nil {
		t.Fatal(err)
	}
	if runState != "succeeded" || intentState != "succeeded" || executionState != "succeeded" {
		t.Fatalf("states=%s/%s/%s", runState, intentState, executionState)
	}
}

func TestUnknownExecutionReconcilesExistingEffect(t *testing.T) {
	pool, ctx := actionTestPool(t)
	runID := seedWaitingRun(t, ctx, pool, "unknown reconciliation")
	store := NewStore(pool)
	intent, err := store.EnsureIntent(ctx, IntentRequest{RunID: runID, TenantID: testTenant, MemberID: testMember, ActionType: "create_ticket", Title: "Reconcile uncertain commit"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAction(t, pool, runID)
	approval, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, intent.Digest)
	if err != nil {
		t.Fatal(err)
	}
	ticketID, _ := newUUID()
	if _, err := pool.Exec(ctx, `INSERT INTO collaboration.tickets(id,tenant_id,title,created_by,idempotency_key) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5)`, ticketID, testTenant, "Reconcile uncertain commit", testMember, "intent:"+intent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE action.executions SET state='unknown',available_at=now() WHERE id=$1::uuid`, approval.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := NewExecutor(pool, time.Millisecond, 5*time.Second, 3).runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var state, receiptTicket string
	if err := pool.QueryRow(ctx, `SELECT state,ticket_id::text FROM action.executions WHERE id=$1::uuid`, approval.ExecutionID).Scan(&state, &receiptTicket); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || receiptTicket != ticketID {
		t.Fatalf("reconciled=%s/%s", state, receiptTicket)
	}
}

func TestUnknownExecutionWithoutEffectRequeuesSameKey(t *testing.T) {
	pool, ctx := actionTestPool(t)
	runID := seedWaitingRun(t, ctx, pool, "unknown absent")
	store := NewStore(pool)
	intent, err := store.EnsureIntent(ctx, IntentRequest{RunID: runID, TenantID: testTenant, MemberID: testMember, ActionType: "create_ticket", Title: "Retry same effect"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAction(t, pool, runID)
	approval, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, intent.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE action.executions SET state='unknown',available_at=now() WHERE id=$1::uuid`, approval.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := NewExecutor(pool, time.Millisecond, 5*time.Second, 3).runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var state, key string
	if err := pool.QueryRow(ctx, `SELECT state,idempotency_key FROM action.executions WHERE id=$1::uuid`, approval.ExecutionID).Scan(&state, &key); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || key != "intent:"+intent.ID {
		t.Fatalf("requeued=%s/%s", state, key)
	}
	assertTicketCount(t, ctx, pool, intent.ID, 0)
}

func TestTerminalExecutionFailureClosesIntentAndRun(t *testing.T) {
	pool, ctx := actionTestPool(t)
	runID := seedWaitingRun(t, ctx, pool, "terminal failure")
	store := NewStore(pool)
	intent, err := store.EnsureIntent(ctx, IntentRequest{RunID: runID, TenantID: testTenant, MemberID: testMember, ActionType: "create_ticket", Title: "Will become invalid"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAction(t, pool, runID)
	if _, err := store.Approve(ctx, Principal{TenantID: testTenant, MemberID: testMember}, intent.ID, intent.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE action.intents SET payload='{}'::jsonb WHERE id=$1::uuid`, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := NewExecutor(pool, time.Millisecond, 5*time.Second, 1).runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var runState, intentState, executionState string
	if err := pool.QueryRow(ctx, `SELECT r.state,i.state,e.state FROM agent.runs r JOIN action.intents i ON i.run_id=r.id JOIN action.executions e ON e.intent_id=i.id WHERE r.id=$1::uuid`, runID).Scan(&runState, &intentState, &executionState); err != nil {
		t.Fatal(err)
	}
	if runState != "failed" || intentState != "failed" || executionState != "failed" {
		t.Fatalf("terminal states=%s/%s/%s", runState, intentState, executionState)
	}
}

func actionTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}
func seedWaitingRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prompt string) string {
	t.Helper()
	id, _ := newUUID()
	source, _ := newUUID()
	_, err := pool.Exec(ctx, `INSERT INTO agent.runs(id,source_event_id,tenant_id,principal_member_id,conversation_id,sender_id,session_type,prompt,state,candidate_text,model,provider_response_id,reply_server_msg_id,action_type,action_title) SELECT $1::uuid,$2,$3::uuid,$4::uuid,'si_test','sender',1,$5,'waiting_approval','candidate','test','resp','reply','create_ticket',$5`, id, source, testTenant, testMember, prompt)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func cleanupAction(t *testing.T, pool *pgxpool.Pool, runID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM audit.action_events WHERE intent_id IN(SELECT id FROM action.intents WHERE run_id=$1::uuid)`, runID)
		_, _ = pool.Exec(ctx, `UPDATE action.executions SET ticket_id=NULL WHERE intent_id IN(SELECT id FROM action.intents WHERE run_id=$1::uuid)`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM collaboration.tickets WHERE idempotency_key IN(SELECT idempotency_key FROM action.executions WHERE intent_id IN(SELECT id FROM action.intents WHERE run_id=$1::uuid))`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM action.executions WHERE intent_id IN(SELECT id FROM action.intents WHERE run_id=$1::uuid)`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM action.approvals WHERE intent_id IN(SELECT id FROM action.intents WHERE run_id=$1::uuid)`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM action.intents WHERE run_id=$1::uuid`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM agent.runs WHERE id=$1::uuid`, runID)
	})
}
func assertTicketCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, intentID string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collaboration.tickets WHERE idempotency_key=$1`, "intent:"+intentID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("tickets=%d want=%d", got, want)
	}
}
func newUUIDForTest(t *testing.T) string {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
