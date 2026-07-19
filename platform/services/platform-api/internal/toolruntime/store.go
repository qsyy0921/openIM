package toolruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type Store struct{ pool *pgxpool.Pool }

var ErrApprovalConflict = errors.New("tool approval conflicts with current state or actor")

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Approval struct {
	ID, RunID, OperationID, ArgumentsDigest, Risk, PolicyReason string
	ExpiresAt                                                   time.Time
}

func (s *Store) ListPendingApprovals(ctx context.Context, tenantID, memberID string, limit int) ([]Approval, error) {
	if tenantID == "" || memberID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("tool approval list input is invalid")
	}
	const query = `
SELECT approval.id::text, call.run_id::text, descriptor.operation_id,
       approval.arguments_digest, descriptor.risk, call.policy_reason, approval.expires_at
FROM agent.tool_approvals AS approval
JOIN agent.tool_calls AS call
  ON call.tenant_id = approval.tenant_id AND call.id = approval.tool_call_id
JOIN capability.tool_descriptors AS descriptor
  ON descriptor.tenant_id = call.tenant_id AND descriptor.id = call.tool_id
JOIN agent.runs AS run
  ON run.tenant_id = call.tenant_id AND run.id = call.run_id
WHERE approval.tenant_id = $1::uuid AND approval.requested_by = $2::uuid
  AND approval.state = 'requested' AND approval.expires_at > now()
  AND call.state = 'waiting_approval' AND run.state = 'waiting_approval'
  AND run.pending_tool_approval_id = approval.id
ORDER BY approval.created_at, approval.id
LIMIT $3`
	rows, err := s.pool.Query(ctx, query, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending tool approvals: %w", err)
	}
	defer rows.Close()
	approvals := make([]Approval, 0)
	for rows.Next() {
		var approval Approval
		if err := rows.Scan(&approval.ID, &approval.RunID, &approval.OperationID,
			&approval.ArgumentsDigest, &approval.Risk, &approval.PolicyReason, &approval.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan pending tool approval: %w", err)
		}
		approvals = append(approvals, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending tool approvals: %w", err)
	}
	return approvals, nil
}

func (s *Store) MemberGrants(ctx context.Context, tenantID, memberID string) ([]string, error) {
	const query = `
SELECT grant_record.permission
FROM capability.member_grants AS grant_record
JOIN identity.members AS member
  ON member.tenant_id = grant_record.tenant_id AND member.id = grant_record.member_id
WHERE grant_record.tenant_id = $1::uuid AND grant_record.member_id = $2::uuid
  AND member.status = 'active'
ORDER BY grant_record.permission`
	rows, err := s.pool.Query(ctx, query, tenantID, memberID)
	if err != nil {
		return nil, fmt.Errorf("load member capability grants: %w", err)
	}
	defer rows.Close()
	var grants []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, fmt.Errorf("scan member capability grant: %w", err)
		}
		grants = append(grants, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate member capability grants: %w", err)
	}
	return grants, nil
}

func (s *Store) Prepare(
	ctx context.Context,
	execution agent.ExecutionContext,
	descriptor capability.Descriptor,
	request CallRequest,
	argumentsDigest string,
	decision Decision,
	approvalExpires time.Time,
) (PreparedCall, error) {
	callID, err := randomUUID()
	if err != nil {
		return PreparedCall{}, err
	}
	state := "prepared"
	switch decision.Outcome {
	case "deny":
		state = "denied"
	case "require_approval":
		state = "waiting_approval"
	case "allow":
	default:
		return PreparedCall{}, errors.New("unsupported tool policy decision")
	}
	arguments, err := json.Marshal(request.Arguments)
	if err != nil {
		return PreparedCall{}, fmt.Errorf("encode tool call arguments: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PreparedCall{}, fmt.Errorf("begin prepare tool call: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const insert = `
INSERT INTO agent.tool_calls (
    id, tenant_id, run_id, call_id, tool_id, capability_snapshot_id,
    arguments, arguments_digest, idempotency_key, policy_decision, policy_reason,
    state, completed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6,
          $7::jsonb, $8, $9, $10, $11, $12,
          CASE WHEN $12 = 'denied' THEN now() ELSE NULL END)
ON CONFLICT (run_id, call_id) DO NOTHING`
	result, err := tx.Exec(ctx, insert, callID, execution.TenantID, execution.RunID, request.CallID,
		descriptor.ID, execution.CapabilitySnapshotID, string(arguments), argumentsDigest,
		"tool:"+execution.RunID+":"+request.CallID, decision.Outcome, decision.Reason, state)
	if err != nil {
		return PreparedCall{}, fmt.Errorf("prepare tool call: %w", err)
	}
	prepared := PreparedCall{
		ID: callID, CallID: request.CallID, OperationID: request.OperationID,
		State: state, PolicyReason: decision.Reason,
	}
	if result.RowsAffected() == 0 {
		const existing = `
SELECT call_record.id::text, call_record.state, call_record.policy_reason,
       call_record.arguments_digest, descriptor.operation_id,
       COALESCE(approval.id::text, ''), COALESCE(call_record.result, 'null'::jsonb)
FROM agent.tool_calls AS call_record
JOIN capability.tool_descriptors AS descriptor
  ON descriptor.tenant_id = call_record.tenant_id AND descriptor.id = call_record.tool_id
LEFT JOIN agent.tool_approvals AS approval ON approval.tool_call_id = call_record.id
WHERE call_record.run_id = $1::uuid AND call_record.call_id = $2`
		var existingDigest string
		if err := tx.QueryRow(ctx, existing, execution.RunID, request.CallID).Scan(
			&prepared.ID, &prepared.State, &prepared.PolicyReason, &existingDigest,
			&prepared.OperationID, &prepared.ApprovalID, &prepared.Result,
		); err != nil {
			return PreparedCall{}, fmt.Errorf("load duplicate tool call: %w", err)
		}
		if existingDigest != argumentsDigest || prepared.OperationID != request.OperationID {
			return PreparedCall{}, errors.New("duplicate tool call ID has different operation or arguments")
		}
		if err := tx.Commit(ctx); err != nil {
			return PreparedCall{}, fmt.Errorf("commit duplicate tool call read: %w", err)
		}
		return prepared, nil
	}
	if decision.Outcome == "require_approval" {
		approvalID, err := randomUUID()
		if err != nil {
			return PreparedCall{}, err
		}
		const approval = `
INSERT INTO agent.tool_approvals (
    id, tenant_id, tool_call_id, arguments_digest, state, requested_by, expires_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'requested', $5::uuid, $6)`
		if _, err := tx.Exec(ctx, approval, approvalID, execution.TenantID, callID, argumentsDigest, execution.MemberID, approvalExpires); err != nil {
			return PreparedCall{}, fmt.Errorf("request tool approval: %w", err)
		}
		prepared.ApprovalID = approvalID
	}
	if err := insertToolEvent(ctx, tx, execution.TenantID, execution.RunID, callID, "prepared", execution.MemberID,
		decision.Outcome, decision.Reason); err != nil {
		return PreparedCall{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PreparedCall{}, fmt.Errorf("commit prepared tool call: %w", err)
	}
	return prepared, nil
}

func (s *Store) Start(ctx context.Context, prepared PreparedCall) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.tool_calls
SET state = 'executing', attempts = attempts + 1, updated_at = now()
WHERE id = $1::uuid AND state = 'prepared'`
	result, err := tx.Exec(ctx, query, prepared.ID)
	if err != nil {
		return fmt.Errorf("start tool call: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("start tool call: call is not prepared")
	}
	if err := insertToolLifecycleEvent(ctx, tx, prepared.ID, "started", "", false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Succeed(ctx context.Context, prepared PreparedCall, value any) error {
	resultJSON, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode tool result: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.tool_calls
SET state = 'succeeded', result = $2::jsonb, error_code = NULL,
    completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'executing'`
	result, err := tx.Exec(ctx, query, prepared.ID, string(resultJSON))
	if err != nil {
		return fmt.Errorf("complete tool call: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("complete tool call: call is not executing")
	}
	if err := insertToolLifecycleEvent(ctx, tx, prepared.ID, "succeeded", "", false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Fail(ctx context.Context, prepared PreparedCall, errorCode string, unknown bool) error {
	state := "failed"
	if unknown {
		state = "unknown"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.tool_calls
SET state = $2, error_code = $3, completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'executing'`
	result, err := tx.Exec(ctx, query, prepared.ID, state, errorCode)
	if err != nil {
		return fmt.Errorf("fail tool call: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("fail tool call: call is not executing")
	}
	if err := insertToolLifecycleEvent(ctx, tx, prepared.ID, state, errorCode, unknown); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Approve(ctx context.Context, tenantID, approvalID, actorMemberID, argumentsDigest string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin approve tool call: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const approve = `
UPDATE agent.tool_approvals AS approval
SET state = 'approved', decided_by = $3::uuid, decided_at = now()
FROM identity.members AS member
WHERE approval.tenant_id = $1::uuid AND approval.id = $2::uuid
  AND approval.state = 'requested' AND approval.expires_at > now()
  AND approval.arguments_digest = $4
  AND approval.requested_by = $3::uuid
  AND member.tenant_id = approval.tenant_id AND member.id = $3::uuid AND member.status = 'active'
RETURNING approval.tool_call_id::text`
	var toolCallID string
	if err := tx.QueryRow(ctx, approve, tenantID, approvalID, actorMemberID, argumentsDigest).Scan(&toolCallID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: approval is missing, expired, changed, or unauthorized", ErrApprovalConflict)
		}
		return fmt.Errorf("approve tool call: %w", err)
	}
	const resume = `
UPDATE agent.tool_calls
SET state = 'prepared', updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND state = 'waiting_approval'`
	result, err := tx.Exec(ctx, resume, toolCallID, tenantID)
	if err != nil {
		return fmt.Errorf("resume approved tool call: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("%w: call is not waiting for approval", ErrApprovalConflict)
	}
	var runID, traceID string
	const resumeRun = `
UPDATE agent.runs AS run
SET state = 'queued', pending_tool_approval_id = NULL, available_at = now(), updated_at = now()
FROM agent.tool_calls AS call
WHERE call.id = $1::uuid AND call.tenant_id = $2::uuid
  AND run.tenant_id = call.tenant_id AND run.id = call.run_id
  AND run.state = 'waiting_approval' AND run.pending_tool_approval_id = $3::uuid
RETURNING run.id::text, run.trace_id`
	if err := tx.QueryRow(ctx, resumeRun, toolCallID, tenantID, approvalID).Scan(&runID, &traceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: Run is not waiting for this approval", ErrApprovalConflict)
		}
		return fmt.Errorf("resume approved Agent Run: %w", err)
	}
	if err := insertToolApprovalEvent(ctx, tx, toolCallID, actorMemberID); err != nil {
		return err
	}
	if err := insertToolRunDecisionEvent(ctx, tx, tenantID, runID, traceID, "tool_approval_granted", approvalID, actorMemberID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit approved tool call: %w", err)
	}
	return nil
}

func (s *Store) Reject(ctx context.Context, tenantID, approvalID, actorMemberID, argumentsDigest string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin reject tool call: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const reject = `
UPDATE agent.tool_approvals AS approval
SET state = 'rejected', decided_by = $3::uuid, decided_at = now()
FROM identity.members AS member
WHERE approval.tenant_id = $1::uuid AND approval.id = $2::uuid
  AND approval.state = 'requested' AND approval.expires_at > now()
  AND approval.arguments_digest = $4 AND approval.requested_by = $3::uuid
  AND member.tenant_id = approval.tenant_id AND member.id = $3::uuid AND member.status = 'active'
RETURNING approval.tool_call_id::text`
	var toolCallID string
	if err := tx.QueryRow(ctx, reject, tenantID, approvalID, actorMemberID, argumentsDigest).Scan(&toolCallID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: approval is missing, expired, changed, or unauthorized", ErrApprovalConflict)
		}
		return fmt.Errorf("reject tool call: %w", err)
	}
	result, err := tx.Exec(ctx, `
UPDATE agent.tool_calls
SET state = 'denied', error_code = 'approval_rejected', completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND state = 'waiting_approval'`, toolCallID, tenantID)
	if err != nil {
		return fmt.Errorf("deny rejected tool call: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("%w: call is not waiting for approval", ErrApprovalConflict)
	}
	var runID, traceID string
	if err := tx.QueryRow(ctx, `
UPDATE agent.runs AS run
SET state = 'queued', pending_tool_approval_id = NULL, available_at = now(), updated_at = now()
FROM agent.tool_calls AS call
WHERE call.id = $1::uuid AND call.tenant_id = $2::uuid
  AND run.tenant_id = call.tenant_id AND run.id = call.run_id
  AND run.state = 'waiting_approval' AND run.pending_tool_approval_id = $3::uuid
RETURNING run.id::text, run.trace_id`, toolCallID, tenantID, approvalID).Scan(&runID, &traceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: Run is not waiting for this approval", ErrApprovalConflict)
		}
		return fmt.Errorf("resume rejected Agent Run: %w", err)
	}
	if err := insertToolDecisionEvent(ctx, tx, toolCallID, actorMemberID, "rejected"); err != nil {
		return err
	}
	if err := insertToolRunDecisionEvent(ctx, tx, tenantID, runID, traceID, "tool_approval_rejected", approvalID, actorMemberID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rejected tool call: %w", err)
	}
	return nil
}

func insertToolApprovalEvent(ctx context.Context, tx pgx.Tx, toolCallID, actorMemberID string) error {
	const query = `
INSERT INTO audit.tool_events (
    tenant_id, run_id, tool_call_id, event_type, actor_member_id, evidence
)
SELECT tenant_id, run_id, id, 'approved', $2::uuid,
       jsonb_build_object('decision', 'approved', 'reason', 'digest_bound_approval')
FROM agent.tool_calls
WHERE id = $1::uuid`
	result, err := tx.Exec(ctx, query, toolCallID, actorMemberID)
	if err != nil {
		return fmt.Errorf("record tool approval event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("record tool approval event: tool call does not exist")
	}
	return nil
}

func insertToolDecisionEvent(ctx context.Context, tx pgx.Tx, toolCallID, actorMemberID, decision string) error {
	const query = `
INSERT INTO audit.tool_events (
    tenant_id, run_id, tool_call_id, event_type, actor_member_id, evidence
)
SELECT tenant_id, run_id, id, $3, $2::uuid,
       jsonb_build_object('decision', $3::text, 'reason', 'digest_bound_approval')
FROM agent.tool_calls
WHERE id = $1::uuid`
	result, err := tx.Exec(ctx, query, toolCallID, actorMemberID, decision)
	if err != nil {
		return fmt.Errorf("record tool decision event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("record tool decision event: tool call does not exist")
	}
	return nil
}

func insertToolRunDecisionEvent(ctx context.Context, tx pgx.Tx, tenantID, runID, traceID, eventType, approvalID, actorMemberID string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO audit.agent_run_events (tenant_id, run_id, trace_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3, $4,
        jsonb_build_object('approval_id', $5::text, 'actor_member_id', $6::text))`,
		tenantID, runID, traceID, eventType, approvalID, actorMemberID)
	if err != nil {
		return fmt.Errorf("record tool approval Run event: %w", err)
	}
	return nil
}

func insertToolLifecycleEvent(ctx context.Context, tx pgx.Tx, toolCallID, eventType, errorCode string, unknown bool) error {
	const query = `
INSERT INTO audit.tool_events (
    tenant_id, run_id, tool_call_id, event_type, actor_member_id, evidence
)
SELECT tenant_id, run_id, id, $2, NULL,
       jsonb_build_object('error_code', NULLIF($3::text, ''), 'outcome_unknown', $4::boolean)
FROM agent.tool_calls
WHERE id = $1::uuid`
	result, err := tx.Exec(ctx, query, toolCallID, eventType, errorCode, unknown)
	if err != nil {
		return fmt.Errorf("record tool lifecycle event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("record tool lifecycle event: tool call does not exist")
	}
	return nil
}

func insertToolEvent(ctx context.Context, tx pgx.Tx, tenantID, runID, toolCallID, eventType, actorMemberID, decision, reason string) error {
	const query = `
INSERT INTO audit.tool_events (
    tenant_id, run_id, tool_call_id, event_type, actor_member_id, evidence
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, NULLIF($5, '')::uuid,
          jsonb_build_object('decision', $6::text, 'reason', $7::text))`
	if _, err := tx.Exec(ctx, query, tenantID, runID, toolCallID, eventType, actorMemberID, decision, reason); err != nil {
		return fmt.Errorf("record tool event: %w", err)
	}
	return nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate tool runtime ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
