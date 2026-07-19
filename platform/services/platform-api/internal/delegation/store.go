package delegation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
)

type Job struct {
	ID, ParentRunID, ChildRunID, TargetAgentID, TargetAgentSlug string
	Task, State, LastError                                      string
	CreatedAt, UpdatedAt                                        time.Time
}

type Store struct{ pool *pgxpool.Pool }

var agentSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Spawn(ctx context.Context, execution agent.ExecutionContext, targetSlug, task, idempotencyKey string) (Job, error) {
	targetSlug, task, idempotencyKey = strings.TrimSpace(targetSlug), strings.TrimSpace(task), strings.TrimSpace(idempotencyKey)
	if err := validateSpawn(execution, targetSlug, task, idempotencyKey); err != nil {
		return Job{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Job{}, fmt.Errorf("begin Agent delegation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if job, found, err := readExisting(ctx, tx, execution.TenantID, execution.RunID, idempotencyKey); err != nil {
		return Job{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return Job{}, fmt.Errorf("commit Agent delegation replay: %w", err)
		}
		return job, nil
	}
	var sourceChannel, conversationID, senderID, parentTraceID string
	var sessionType int32
	if err := tx.QueryRow(ctx, `
SELECT source_channel, conversation_id, sender_id, session_type, trace_id
FROM agent.runs
WHERE tenant_id = $1::uuid AND id = $2::uuid AND principal_member_id = $3::uuid
  AND state = 'running' AND execution_plane = 'passive'
FOR UPDATE`, execution.TenantID, execution.RunID, execution.MemberID).Scan(
		&sourceChannel, &conversationID, &senderID, &sessionType, &parentTraceID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, errors.New("delegating Agent Run is not active")
		}
		return Job{}, fmt.Errorf("lock delegating Agent Run: %w", err)
	}
	var job Job
	var versionID, deploymentID, triggerID, checksum, snapshotID string
	const resolve = `
SELECT definition.id::text, definition.slug, deployment.id::text, version.id::text,
       trigger.id::text, version.spec_checksum, version.capability_snapshot_id
FROM agent.definitions AS definition
JOIN agent.deployments AS deployment
  ON deployment.tenant_id = definition.tenant_id AND deployment.agent_id = definition.id
 AND deployment.slot = 'production'
JOIN agent.versions AS version
  ON version.tenant_id = deployment.tenant_id AND version.agent_id = deployment.agent_id
 AND version.id = deployment.active_version_id
JOIN agent.triggers AS trigger
  ON trigger.tenant_id = definition.tenant_id AND trigger.agent_id = definition.id
 AND trigger.trigger_type = 'internal_delegate'
 AND trigger.trigger_value = 'delegate:' || definition.slug AND trigger.enabled
WHERE definition.tenant_id = $1::uuid AND definition.slug = $2 AND definition.status = 'active'`
	if err := tx.QueryRow(ctx, resolve, execution.TenantID, targetSlug).Scan(
		&job.TargetAgentID, &job.TargetAgentSlug, &deploymentID, &versionID, &triggerID, &checksum, &snapshotID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, errors.New("target Agent is unavailable for delegation")
		}
		return Job{}, fmt.Errorf("resolve delegated Agent: %w", err)
	}
	job.ID, err = randomUUID()
	if err != nil {
		return Job{}, err
	}
	job.ChildRunID, err = randomUUID()
	if err != nil {
		return Job{}, err
	}
	job.ParentRunID, job.Task, job.State = execution.RunID, task, "queued"
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.conversation_lanes (tenant_id, source_channel, conversation_id)
VALUES ($1::uuid, $2, $3)
ON CONFLICT (tenant_id, source_channel, conversation_id) DO NOTHING`,
		execution.TenantID, sourceChannel, conversationID); err != nil {
		return Job{}, fmt.Errorf("ensure delegation conversation lane: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `
UPDATE agent.conversation_lanes
SET next_enqueue_sequence = next_enqueue_sequence + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
RETURNING next_enqueue_sequence - 1`, execution.TenantID, sourceChannel, conversationID).Scan(&sequence); err != nil {
		return Job{}, fmt.Errorf("allocate delegation conversation sequence: %w", err)
	}
	traceID := parentTraceID + "/delegate:" + job.ID
	if len(traceID) > 256 {
		traceID = "agent-run:" + job.ChildRunID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.runs (
    id, source_event_id, tenant_id, principal_member_id,
    agent_id, agent_version_id, agent_deployment_id, agent_trigger_id, agent_spec_checksum,
    source_channel, conversation_id, conversation_sequence, sender_id, session_type, prompt,
    execution_plane, trace_id, capability_snapshot_id
) VALUES (
    $1::uuid, $2, $3::uuid, $4::uuid,
    $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9,
    $10, $11, $12, $13, $14, $15,
    'internal', $16, $17
)`, job.ChildRunID, "delegation:"+job.ID, execution.TenantID, execution.MemberID,
		job.TargetAgentID, versionID, deploymentID, triggerID, checksum,
		sourceChannel, conversationID, sequence, senderID, sessionType, task, traceID, snapshotID); err != nil {
		return Job{}, fmt.Errorf("enqueue delegated Agent Run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.delegations (
    id, tenant_id, parent_run_id, child_run_id, requested_by_member_id,
    target_agent_id, task, idempotency_key
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8)`,
		job.ID, execution.TenantID, execution.RunID, job.ChildRunID, execution.MemberID,
		job.TargetAgentID, task, idempotencyKey); err != nil {
		return Job{}, fmt.Errorf("persist Agent delegation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'queued',
        jsonb_build_object('parent_run_id', $3::text, 'child_run_id', $4::text,
                           'target_agent_id', $5::text, 'conversation_sequence', $6::bigint))`,
		execution.TenantID, job.ID, execution.RunID, job.ChildRunID, job.TargetAgentID, sequence); err != nil {
		return Job{}, fmt.Errorf("audit Agent delegation: %w", err)
	}
	for _, event := range []struct {
		runID, traceID, eventType, counterpartID string
		evidence                                 string
	}{
		{execution.RunID, parentTraceID, "delegation_queued", job.ChildRunID, `jsonb_build_object('delegation_id', $5::text, 'child_run_id', $6::text)`},
		{job.ChildRunID, traceID, "delegated_enqueued", execution.RunID, `jsonb_build_object('delegation_id', $5::text, 'parent_run_id', $6::text)`},
	} {
		if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_run_events (tenant_id, run_id, trace_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3, $4, `+event.evidence+`)`,
			execution.TenantID, event.runID, event.traceID, event.eventType, job.ID, event.counterpartID); err != nil {
			return Job{}, fmt.Errorf("audit delegated Agent Run: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("commit Agent delegation: %w", err)
	}
	job.CreatedAt, job.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	return job, nil
}

func validateSpawn(execution agent.ExecutionContext, targetSlug, task, idempotencyKey string) error {
	if execution.ExecutionPlane != "passive" || execution.RunID == "" || execution.TenantID == "" || execution.MemberID == "" {
		return errors.New("Agent delegation requires a passive authenticated Run")
	}
	if !agentSlugPattern.MatchString(targetSlug) || task == "" || utf8.RuneCountInString(task) > 4000 ||
		idempotencyKey == "" || utf8.RuneCountInString(idempotencyKey) > 256 {
		return errors.New("Agent delegation input is invalid")
	}
	return nil
}

func (s *Store) ListMember(ctx context.Context, tenantID, memberID string, limit int) ([]Job, error) {
	if tenantID == "" || memberID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("Agent delegation list input is invalid")
	}
	rows, err := s.pool.Query(ctx, `
SELECT delegation.id::text, delegation.parent_run_id::text, delegation.child_run_id::text,
       definition.id::text, definition.slug, delegation.task, delegation.state,
       COALESCE(delegation.last_error, ''), delegation.created_at, delegation.updated_at
FROM agent.delegations AS delegation
JOIN agent.definitions AS definition
  ON definition.tenant_id = delegation.tenant_id AND definition.id = delegation.target_agent_id
WHERE delegation.tenant_id = $1::uuid AND delegation.requested_by_member_id = $2::uuid
ORDER BY delegation.created_at DESC, delegation.id
LIMIT $3`, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("list Agent delegations: %w", err)
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.ParentRunID, &job.ChildRunID, &job.TargetAgentID,
			&job.TargetAgentSlug, &job.Task, &job.State, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan Agent delegation: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent delegations: %w", err)
	}
	return jobs, nil
}

func readExisting(ctx context.Context, tx pgx.Tx, tenantID, parentRunID, idempotencyKey string) (Job, bool, error) {
	var job Job
	err := tx.QueryRow(ctx, `
SELECT delegation.id::text, delegation.parent_run_id::text, delegation.child_run_id::text,
       definition.id::text, definition.slug, delegation.task, delegation.state,
       COALESCE(delegation.last_error, ''), delegation.created_at, delegation.updated_at
FROM agent.delegations AS delegation
JOIN agent.definitions AS definition
  ON definition.tenant_id = delegation.tenant_id AND definition.id = delegation.target_agent_id
WHERE delegation.tenant_id = $1::uuid AND delegation.parent_run_id = $2::uuid
  AND delegation.idempotency_key = $3`, tenantID, parentRunID, idempotencyKey).Scan(
		&job.ID, &job.ParentRunID, &job.ChildRunID, &job.TargetAgentID, &job.TargetAgentSlug,
		&job.Task, &job.State, &job.LastError, &job.CreatedAt, &job.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("read existing Agent delegation: %w", err)
	}
	return job, true, nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate Agent delegation ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
