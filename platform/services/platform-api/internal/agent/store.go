package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type EnqueueResult struct {
	RunID           string
	RejectionReason string
}

type resolvedTrigger struct {
	TriggerID, Alias     string
	Enabled              bool
	AgentID, Status      string
	DeploymentID         string
	VersionID            string
	Checksum             string
	CapabilitySnapshotID string
}

func (s *Store) Enqueue(ctx context.Context, source Source, trigger Trigger) (EnqueueResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin enqueue Agent Run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := enqueueTx(ctx, tx, source, trigger)
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit Agent Run resolution: %w", err)
	}
	return result, nil
}

func enqueueTx(ctx context.Context, tx pgx.Tx, source Source, trigger Trigger) (EnqueueResult, error) {
	runID, err := newUUID()
	if err != nil {
		return EnqueueResult{}, err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT id::text FROM agent.runs WHERE source_event_id = $1`, trigger.EventID).Scan(&existing)
	if err == nil {
		return EnqueueResult{RunID: existing}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EnqueueResult{}, fmt.Errorf("read duplicate Agent Run: %w", err)
	}
	var activeMember bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members
    WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'active'
)`, trigger.MemberID, trigger.TenantID).Scan(&activeMember); err != nil {
		return EnqueueResult{}, fmt.Errorf("validate Agent Run member: %w", err)
	}
	if !activeMember {
		return rejectEnqueueTx(ctx, tx, source, trigger.EventID, "member_inactive")
	}
	aliases := make([]string, 0, len(trigger.Mentions))
	prompts := make(map[string]string, len(trigger.Mentions))
	for _, mention := range trigger.Mentions {
		aliases = append(aliases, mention.Alias)
		prompts[mention.Alias] = mention.Prompt
	}
	const resolve = `
SELECT tr.id::text, tr.trigger_value, tr.enabled, d.id::text, d.status,
       COALESCE(dep.id::text, ''), COALESCE(v.id::text, ''), COALESCE(v.spec_checksum, ''),
       COALESCE(v.capability_snapshot_id, '')
FROM agent.triggers tr
JOIN agent.definitions d ON d.tenant_id = tr.tenant_id AND d.id = tr.agent_id
LEFT JOIN agent.deployments dep
  ON dep.tenant_id = d.tenant_id AND dep.agent_id = d.id AND dep.slot = 'production'
LEFT JOIN agent.versions v
  ON v.tenant_id = dep.tenant_id AND v.agent_id = dep.agent_id AND v.id = dep.active_version_id
WHERE tr.tenant_id = $1::uuid AND tr.trigger_type = 'mention_alias'
  AND tr.trigger_value = ANY($2::text[])
ORDER BY array_position($2::text[], tr.trigger_value)
LIMIT 2`
	rows, err := tx.Query(ctx, resolve, trigger.TenantID, aliases)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("resolve Agent trigger: %w", err)
	}
	resolved := make([]resolvedTrigger, 0, 2)
	for rows.Next() {
		var item resolvedTrigger
		if err := rows.Scan(&item.TriggerID, &item.Alias, &item.Enabled, &item.AgentID, &item.Status,
			&item.DeploymentID, &item.VersionID, &item.Checksum, &item.CapabilitySnapshotID); err != nil {
			rows.Close()
			return EnqueueResult{}, fmt.Errorf("scan Agent trigger: %w", err)
		}
		resolved = append(resolved, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return EnqueueResult{}, fmt.Errorf("iterate Agent triggers: %w", err)
	}
	rows.Close()
	if len(resolved) == 0 {
		return EnqueueResult{}, nil
	}
	if len(resolved) > 1 {
		return rejectEnqueueTx(ctx, tx, source, trigger.EventID, "ambiguous_trigger")
	}
	selection := resolved[0]
	reason := ""
	switch {
	case !selection.Enabled:
		reason = "trigger_disabled"
	case selection.Status != "active":
		reason = "agent_disabled"
	case selection.DeploymentID == "":
		reason = "deployment_missing"
	case selection.VersionID == "" || selection.Checksum == "":
		reason = "version_missing"
	case selection.CapabilitySnapshotID == "":
		reason = "capability_snapshot_missing"
	case prompts[selection.Alias] == "":
		reason = "empty_prompt"
	}
	if reason != "" {
		return rejectEnqueueTx(ctx, tx, source, trigger.EventID, reason)
	}
	const ensureLane = `
INSERT INTO agent.conversation_lanes (tenant_id, source_channel, conversation_id)
VALUES ($1::uuid, $2, $3)
ON CONFLICT (tenant_id, source_channel, conversation_id) DO NOTHING`
	if _, err := tx.Exec(ctx, ensureLane, trigger.TenantID, trigger.SourceChannel, trigger.ConversationID); err != nil {
		return EnqueueResult{}, fmt.Errorf("ensure Agent conversation lane: %w", err)
	}
	const allocateSequence = `
UPDATE agent.conversation_lanes
SET next_enqueue_sequence = next_enqueue_sequence + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
RETURNING next_enqueue_sequence - 1`
	var conversationSequence int64
	if err := tx.QueryRow(ctx, allocateSequence, trigger.TenantID, trigger.SourceChannel, trigger.ConversationID).Scan(&conversationSequence); err != nil {
		return EnqueueResult{}, fmt.Errorf("allocate Agent conversation sequence: %w", err)
	}
	traceID := "agent-run:" + runID
	const query = `
INSERT INTO agent.runs (
    id, source_event_id, tenant_id, principal_member_id,
	agent_id, agent_version_id, agent_deployment_id, agent_trigger_id, agent_spec_checksum,
	source_channel, conversation_id, conversation_sequence, sender_id, session_type, prompt,
	execution_plane, trace_id, capability_snapshot_id
)
VALUES ($1::uuid, $2, $3::uuid, $4::uuid,
       $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9,
	   $10, $11, $12, $13, $14, $15, 'passive', $16, $17)
ON CONFLICT (source_event_id) DO UPDATE SET source_event_id = EXCLUDED.source_event_id
RETURNING id::text`
	if err := tx.QueryRow(ctx, query, runID, trigger.EventID, trigger.TenantID, trigger.MemberID,
		selection.AgentID, selection.VersionID, selection.DeploymentID, selection.TriggerID, selection.Checksum,
		trigger.SourceChannel, trigger.ConversationID, conversationSequence, trigger.SenderID, trigger.SessionType,
		prompts[selection.Alias], traceID, selection.CapabilitySnapshotID).Scan(&runID); err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue Agent Run: %w", err)
	}
	if err := insertRunEvent(ctx, tx, trigger.TenantID, runID, traceID, "enqueued",
		`jsonb_build_object('channel', $5::text, 'conversation_sequence', $6::bigint)`, trigger.SourceChannel, conversationSequence); err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{RunID: runID}, nil
}

func rejectEnqueueTx(ctx context.Context, tx pgx.Tx, source Source, eventID, reason string) (EnqueueResult, error) {
	const query = `
INSERT INTO agent.event_rejections (source_topic, source_partition, source_offset, event_id, reason)
VALUES ($1, $2, $3, NULLIF($4, ''), $5)
ON CONFLICT (source_topic, source_partition, source_offset) DO NOTHING`
	if _, err := tx.Exec(ctx, query, source.Topic, source.Partition, source.Offset, eventID, reason); err != nil {
		return EnqueueResult{}, fmt.Errorf("record Agent catalog rejection: %w", err)
	}
	return EnqueueResult{RejectionReason: reason}, nil
}

func (s *Store) Reject(ctx context.Context, source Source, eventID, reason string) error {
	const query = `
INSERT INTO agent.event_rejections (source_topic, source_partition, source_offset, event_id, reason)
VALUES ($1, $2, $3, NULLIF($4, ''), $5)
ON CONFLICT (source_topic, source_partition, source_offset) DO NOTHING`
	_, err := s.pool.Exec(ctx, query, source.Topic, source.Partition, source.Offset, eventID, reason)
	if err != nil {
		return fmt.Errorf("record Agent event rejection: %w", err)
	}
	return nil
}

func (s *Store) EnsureBotIdentity(ctx context.Context, tenantID, openIMUserID string) error {
	const query = `
INSERT INTO agent.bot_identities (tenant_id, openim_user_id)
VALUES ($1::uuid, $2)
ON CONFLICT (tenant_id) DO UPDATE SET openim_user_id = EXCLUDED.openim_user_id
WHERE agent.bot_identities.openim_user_id = EXCLUDED.openim_user_id`
	result, err := s.pool.Exec(ctx, query, tenantID, openIMUserID)
	if err != nil {
		return fmt.Errorf("ensure Agent bot identity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("ensure Agent bot identity: tenant is mapped to a different OpenIM user")
	}
	return nil
}

type Run struct {
	ID                    string
	SourceEventID         string
	TenantID              string
	MemberID              string
	AgentID               string
	AgentVersionID        string
	AgentDeploymentID     string
	AgentTriggerID        string
	AgentSpecChecksum     string
	CapabilitySnapshotID  string
	SourceChannel         string
	ConversationID        string
	ConversationSequence  int64
	ExecutionPlane        string
	TraceID               string
	SenderID              string
	SessionType           int32
	Prompt                string
	CandidateText         string
	LeaseToken            string
	Attempts              int
	Model                 string
	ProviderResponseID    string
	ActionType            string
	ActionTitle           string
	RouteStatus           string
	RouteOperationID      string
	RouteClarification    string
	RouteAttempts         int
	ModelAttempts         int
	FinalizationAttempts  int
	ToolArguments         json.RawMessage
	ToolResult            json.RawMessage
	ToolPlanAttempts      int
	ToolExecutionAttempts int
}

func (s *Store) Claim(ctx context.Context, lease time.Duration, maxAttempts int) (*Run, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim Agent Run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := expireToolApprovals(ctx, tx, 100); err != nil {
		return nil, err
	}
	const query = `
WITH candidate AS (
    SELECT r.id
    FROM agent.runs AS r
    JOIN agent.conversation_lanes AS lane
      ON lane.tenant_id = r.tenant_id
     AND lane.source_channel = r.source_channel
     AND lane.conversation_id = r.conversation_id
     AND lane.next_dispatch_sequence = r.conversation_sequence
	WHERE r.available_at <= now()
	  AND NOT EXISTS (
	      SELECT 1 FROM platform_meta.runtime_controls AS control
	      WHERE control.tenant_id = r.tenant_id
	        AND control.component = 'agent_execution' AND control.paused
	  )
	  AND (
	      (r.route_status IS NULL AND r.route_attempts < $1)
	      OR (
	          r.route_status = 'selected'
	          AND r.route_operation_id NOT IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	          AND r.tool_arguments IS NULL AND r.tool_plan_attempts < $1
	      )
	      OR (
	          r.route_status = 'selected'
	          AND r.route_operation_id NOT IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	          AND r.tool_arguments IS NOT NULL AND r.tool_result IS NULL
	          AND r.tool_execution_attempts < $1
	      )
	      OR (
	          r.route_status IS NOT NULL AND r.candidate_text IS NULL AND r.model_attempts < $1
	          AND (
	              r.route_status <> 'selected'
	              OR r.route_operation_id IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	              OR r.tool_result IS NOT NULL
	          )
	      )
	      OR (r.candidate_text IS NOT NULL AND r.finalization_attempts < $1)
	  )
      AND (r.state IN ('queued', 'reply_pending') OR (r.state = 'running' AND r.lease_until < now()))
    ORDER BY r.available_at, r.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE agent.runs AS r
SET state = 'running', attempts = attempts + 1,
	route_attempts = route_attempts + CASE WHEN route_status IS NULL THEN 1 ELSE 0 END,
	tool_plan_attempts = tool_plan_attempts + CASE
	    WHEN route_status = 'selected'
	     AND route_operation_id NOT IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	     AND tool_arguments IS NULL THEN 1 ELSE 0 END,
	tool_execution_attempts = tool_execution_attempts + CASE
	    WHEN route_status = 'selected'
	     AND route_operation_id NOT IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	     AND tool_arguments IS NOT NULL AND tool_result IS NULL THEN 1 ELSE 0 END,
	model_attempts = model_attempts + CASE
	    WHEN route_status IS NOT NULL AND candidate_text IS NULL
	     AND (
	         route_status <> 'selected'
	         OR route_operation_id IN ('enterprise.knowledge.search', 'collaboration.ticket.create')
	         OR tool_result IS NOT NULL
	     ) THEN 1 ELSE 0 END,
	finalization_attempts = finalization_attempts + CASE WHEN candidate_text IS NOT NULL THEN 1 ELSE 0 END,
    lease_token = $2, lease_until = now() + make_interval(secs => $3), updated_at = now()
FROM candidate
WHERE r.id = candidate.id
	RETURNING r.id::text, r.source_event_id, r.tenant_id::text, r.principal_member_id::text,
	          r.agent_id::text, r.agent_version_id::text, r.agent_deployment_id::text,
	          r.agent_trigger_id::text, r.agent_spec_checksum, r.capability_snapshot_id, r.source_channel,
	          r.conversation_id, r.conversation_sequence, r.execution_plane, r.trace_id,
	          r.sender_id, r.session_type,
          r.prompt, COALESCE(r.candidate_text, ''), r.lease_token, r.attempts,
		  COALESCE(r.model, ''), COALESCE(r.provider_response_id, ''),
		  COALESCE(r.action_type, ''), COALESCE(r.action_title, ''),
		  COALESCE(r.route_status, ''), COALESCE(r.route_operation_id, ''),
		  COALESCE(r.route_clarification, ''), r.route_attempts, r.model_attempts, r.finalization_attempts,
		  COALESCE(r.tool_arguments, 'null'::jsonb), COALESCE(r.tool_result, 'null'::jsonb),
		  r.tool_plan_attempts, r.tool_execution_attempts`
	var run Run
	err = tx.QueryRow(ctx, query, maxAttempts, token, leaseSeconds).Scan(
		&run.ID, &run.SourceEventID, &run.TenantID, &run.MemberID,
		&run.AgentID, &run.AgentVersionID, &run.AgentDeploymentID, &run.AgentTriggerID, &run.AgentSpecChecksum, &run.CapabilitySnapshotID, &run.SourceChannel,
		&run.ConversationID, &run.ConversationSequence, &run.ExecutionPlane, &run.TraceID, &run.SenderID, &run.SessionType,
		&run.Prompt, &run.CandidateText, &run.LeaseToken, &run.Attempts, &run.Model, &run.ProviderResponseID,
		&run.ActionType, &run.ActionTitle, &run.RouteStatus, &run.RouteOperationID,
		&run.RouteClarification, &run.RouteAttempts, &run.ModelAttempts, &run.FinalizationAttempts,
		&run.ToolArguments, &run.ToolResult, &run.ToolPlanAttempts, &run.ToolExecutionAttempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim Agent Run: %w", err)
	}
	if string(run.ToolArguments) == "null" {
		run.ToolArguments = nil
	}
	if string(run.ToolResult) == "null" {
		run.ToolResult = nil
	}
	if err := markDelegationRunning(ctx, tx, run); err != nil {
		return nil, err
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "claimed",
		`jsonb_build_object('attempt', $5::integer, 'execution_plane', $6::text)`, run.Attempts, run.ExecutionPlane); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Agent Run claim: %w", err)
	}
	return &run, nil
}

func markDelegationRunning(ctx context.Context, tx pgx.Tx, run Run) error {
	if run.ExecutionPlane != "internal" {
		return nil
	}
	var delegationID string
	err := tx.QueryRow(ctx, `
UPDATE agent.delegations
SET state = 'running', updated_at = now()
WHERE tenant_id = $1::uuid AND child_run_id = $2::uuid AND state = 'queued'
RETURNING id::text`, run.TenantID, run.ID).Scan(&delegationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mark Agent delegation running: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'running', jsonb_build_object('child_run_id', $3::text))`,
		run.TenantID, delegationID, run.ID); err != nil {
		return fmt.Errorf("audit running Agent delegation: %w", err)
	}
	return nil
}

func (s *Store) SaveCandidate(ctx context.Context, run Run, candidate Candidate, evidence []Evidence) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save Agent candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockedEvidence := make([]Evidence, len(evidence))
	for index, item := range evidence {
		lockedEvidence[index], err = lockAuthorizedCitation(ctx, tx, run, item)
		if err != nil {
			return fmt.Errorf("save Agent citation: %w", err)
		}
	}
	const query = `
UPDATE agent.runs
SET state = 'reply_pending', candidate_text = $3, model = $4, provider_response_id = $5,
	 action_type = NULLIF($6, ''), action_title = NULLIF($7, ''),
    lease_token = NULL, lease_until = NULL, available_at = now(), last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2 AND lease_until >= now()`
	actionType, actionTitle := "", ""
	if candidate.ActionIntent != nil {
		actionType, actionTitle = candidate.ActionIntent.Type, candidate.ActionIntent.Title
	}
	result, err := tx.Exec(ctx, query, run.ID, run.LeaseToken, candidate.Text, candidate.Model, candidate.ProviderResponseID, actionType, actionTitle)
	if err != nil {
		return fmt.Errorf("save Agent candidate: %w", err)
	}
	if err := requireOne(result.RowsAffected(), "save Agent candidate"); err != nil {
		return err
	}
	for ordinal, item := range lockedEvidence {
		excerpt := citationExcerpt(item.Content)
		if excerpt == "" {
			return errors.New("save Agent citation: authorized excerpt is required")
		}
		const insert = `
INSERT INTO agent.run_citations (
    run_id, citation_id, ordinal, document_id, version_id, chunk_id,
    title, source_uri, checksum, authorized_excerpt
) VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9, $10)`
		if _, err := tx.Exec(ctx, insert, run.ID, item.CitationID, ordinal, item.DocumentID, item.VersionID, item.ChunkID, item.Title, item.SourceURI, item.Checksum, excerpt); err != nil {
			return fmt.Errorf("save Agent citation: %w", err)
		}
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "candidate_persisted",
		`jsonb_build_object('model', $5::text, 'citation_count', $6::integer, 'has_action', $7::boolean, 'grounding_status', $8::text)`,
		candidate.Model, len(evidence), candidate.ActionIntent != nil, candidate.GroundingStatus); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent candidate: %w", err)
	}
	return nil
}

func lockAuthorizedCitation(ctx context.Context, tx pgx.Tx, run Run, item Evidence) (Evidence, error) {
	if run.TenantID == "" || run.MemberID == "" || item.CitationID == "" ||
		item.DocumentID == "" || item.VersionID == "" || item.ChunkID == "" || item.Checksum == "" {
		return Evidence{}, errors.New("citation persistence identity is incomplete")
	}
	const query = `
SELECT document.id::text, version.id::text, chunk.id::text,
       document.title, document.source_uri, chunk.checksum, chunk.content
FROM identity.members AS member
JOIN knowledge.documents AS document
  ON document.tenant_id = member.tenant_id
JOIN authz.document_grants AS document_grant
  ON document_grant.tenant_id = document.tenant_id
 AND document_grant.document_id = document.id
 AND document_grant.member_id = member.id
 AND document_grant.permission = 'read'
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
WHERE member.tenant_id = $1::uuid
  AND member.id = $2::uuid
  AND member.status = 'active'
  AND document.id = $3::uuid
  AND version.id = $4::uuid
  AND chunk.id = $5::uuid
  AND chunk.checksum = $6
  AND document.status = 'active'
  AND document.classification IN ('public', 'internal')
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
FOR SHARE OF member, document, document_grant, version, chunk`
	result := Evidence{CitationID: item.CitationID}
	err := tx.QueryRow(ctx, query,
		run.TenantID, run.MemberID, item.DocumentID, item.VersionID, item.ChunkID, item.Checksum,
	).Scan(
		&result.DocumentID, &result.VersionID, &result.ChunkID,
		&result.Title, &result.SourceURI, &result.Checksum, &result.Content,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evidence{}, errors.New("citation is no longer authorized or current")
	}
	if err != nil {
		return Evidence{}, fmt.Errorf("lock current citation authorization: %w", err)
	}
	return result, nil
}

func citationExcerpt(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) > 1200 {
		runes = runes[:1200]
	}
	for len(runes) > 0 && len(string(runes)) > 8000 {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes))
}

func (s *Store) WaitForToolApproval(ctx context.Context, run Run, approvalID string) error {
	if approvalID == "" {
		return errors.New("wait for tool approval: approval ID is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin wait for tool approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.runs AS run
SET state = 'waiting_approval', pending_tool_approval_id = $3::uuid,
    lease_token = NULL, lease_until = NULL, last_error = NULL, updated_at = now()
WHERE run.id = $1::uuid AND run.state = 'running' AND run.lease_token = $2
  AND EXISTS (
      SELECT 1
      FROM agent.tool_approvals AS approval
      JOIN agent.tool_calls AS call ON call.id = approval.tool_call_id
      WHERE approval.tenant_id = run.tenant_id AND approval.id = $3::uuid
        AND approval.state = 'requested' AND approval.requested_by = run.principal_member_id
        AND call.run_id = run.id AND call.state = 'waiting_approval'
  )`
	result, err := tx.Exec(ctx, query, run.ID, run.LeaseToken, approvalID)
	if err != nil {
		return fmt.Errorf("wait for tool approval: %w", err)
	}
	if err := requireOne(result.RowsAffected(), "wait for tool approval"); err != nil {
		return err
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "tool_approval_requested",
		`jsonb_build_object('approval_id', $5::text, 'operation_id', $6::text)`, approvalID, run.RouteOperationID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tool approval wait: %w", err)
	}
	return nil
}

func (s *Store) FailOrRetry(ctx context.Context, run Run, failure string, maxAttempts int, retryAfter time.Duration) error {
	if len(failure) > 1000 {
		failure = failure[:1000]
	}
	retrySeconds := int64((retryAfter + time.Second - 1) / time.Second)
	phaseAttempts := run.phaseAttempts()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin retry Agent Run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.runs
SET state = CASE
		WHEN $6::integer >= $4::integer THEN 'failed'
        WHEN candidate_text IS NULL THEN 'queued'
        ELSE 'reply_pending'
    END,
	available_at = CASE WHEN $6::integer >= $4::integer THEN available_at ELSE now() + make_interval(secs => ($5::bigint)::double precision) END,
	completed_at = CASE WHEN $6::integer >= $4::integer THEN now() ELSE NULL END,
    lease_token = NULL, lease_until = NULL, last_error = $3, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2
RETURNING state`
	var state string
	err = tx.QueryRow(ctx, query, run.ID, run.LeaseToken, failure, maxAttempts, retrySeconds, phaseAttempts).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("retry Agent Run: execution lease is not held")
	}
	if err != nil {
		return fmt.Errorf("retry Agent Run: %w", err)
	}
	if state == "failed" {
		if err := advanceConversationLane(ctx, tx, run); err != nil {
			return err
		}
		if err := failDelegationForRun(ctx, tx, run, failure); err != nil {
			return err
		}
	}
	eventType := "retry_scheduled"
	if state == "failed" {
		eventType = "failed"
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, eventType,
		`jsonb_build_object('attempt', $5::integer, 'error', $6::text)`, run.Attempts, failure); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit retry Agent Run: %w", err)
	}
	return nil
}

func (run Run) phaseAttempts() int {
	if run.RouteStatus == "" {
		return run.RouteAttempts
	}
	if run.CandidateText != "" {
		return run.FinalizationAttempts
	}
	if run.RouteStatus == "selected" &&
		run.RouteOperationID != "enterprise.knowledge.search" &&
		run.RouteOperationID != "collaboration.ticket.create" {
		if len(run.ToolArguments) == 0 {
			return run.ToolPlanAttempts
		}
		if len(run.ToolResult) == 0 {
			return run.ToolExecutionAttempts
		}
	}
	return run.ModelAttempts
}

func (s *Store) Fail(ctx context.Context, run Run, failure string) error {
	if len(failure) > 1000 {
		failure = failure[:1000]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail Agent Run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.runs
SET state = 'failed', completed_at = now(), lease_token = NULL, lease_until = NULL,
    last_error = $3, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2`
	result, err := tx.Exec(ctx, query, run.ID, run.LeaseToken, failure)
	if err != nil {
		return fmt.Errorf("fail Agent Run: %w", err)
	}
	if err := requireOne(result.RowsAffected(), "fail Agent Run"); err != nil {
		return err
	}
	if err := advanceConversationLane(ctx, tx, run); err != nil {
		return err
	}
	if err := failDelegationForRun(ctx, tx, run, failure); err != nil {
		return err
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "failed",
		`jsonb_build_object('attempt', $5::integer, 'error', $6::text)`, run.Attempts, failure); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed Agent Run: %w", err)
	}
	return nil
}

func failDelegationForRun(ctx context.Context, tx pgx.Tx, run Run, failure string) error {
	if run.ExecutionPlane != "internal" {
		return nil
	}
	var delegationID string
	err := tx.QueryRow(ctx, `
UPDATE agent.delegations
SET state = 'failed', last_error = $3, completed_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND child_run_id = $2::uuid AND state IN ('queued', 'running')
RETURNING id::text`, run.TenantID, run.ID, failure).Scan(&delegationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fail Agent delegation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'failed',
        jsonb_build_object('child_run_id', $3::text, 'error', $4::text))`,
		run.TenantID, delegationID, run.ID, failure); err != nil {
		return fmt.Errorf("audit failed Agent delegation: %w", err)
	}
	return nil
}

func advanceConversationLane(ctx context.Context, tx pgx.Tx, run Run) error {
	const query = `
UPDATE agent.conversation_lanes
SET next_dispatch_sequence = next_dispatch_sequence + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
  AND next_dispatch_sequence = $4`
	result, err := tx.Exec(ctx, query, run.TenantID, run.SourceChannel, run.ConversationID, run.ConversationSequence)
	if err != nil {
		return fmt.Errorf("advance Agent conversation lane: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("advance Agent conversation lane: Run does not own the dispatch position")
	}
	return nil
}

func expireToolApprovals(ctx context.Context, tx pgx.Tx, limit int) error {
	const query = `
WITH candidates AS (
    SELECT approval.id
    FROM agent.tool_approvals AS approval
    WHERE approval.state = 'requested' AND approval.expires_at <= now()
    ORDER BY approval.expires_at, approval.id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), expired AS (
    UPDATE agent.tool_approvals AS approval
    SET state = 'expired', decided_by = approval.requested_by, decided_at = now()
    FROM candidates
    WHERE approval.id = candidates.id
    RETURNING approval.id, approval.tenant_id, approval.tool_call_id, approval.requested_by
), calls AS (
    UPDATE agent.tool_calls AS call
    SET state = 'denied', error_code = 'approval_expired', completed_at = now(), updated_at = now()
    FROM expired
    WHERE call.id = expired.tool_call_id AND call.state = 'waiting_approval'
    RETURNING call.id, call.tenant_id, call.run_id, expired.requested_by
), resumed AS (
    UPDATE agent.runs AS run
    SET state = 'queued', pending_tool_approval_id = NULL, available_at = now(), updated_at = now()
    FROM calls
    WHERE run.tenant_id = calls.tenant_id AND run.id = calls.run_id
      AND run.state = 'waiting_approval'
    RETURNING run.tenant_id::text, run.id::text, run.trace_id,
              calls.id::text AS tool_call_id, calls.requested_by::text
)
SELECT tenant_id, id, trace_id, tool_call_id, requested_by FROM resumed`
	rows, err := tx.Query(ctx, query, limit)
	if err != nil {
		return fmt.Errorf("expire tool approvals: %w", err)
	}
	type expiredApproval struct{ tenantID, runID, traceID, toolCallID, actorID string }
	items := make([]expiredApproval, 0)
	for rows.Next() {
		var item expiredApproval
		if err := rows.Scan(&item.tenantID, &item.runID, &item.traceID, &item.toolCallID, &item.actorID); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired tool approval: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired tool approvals: %w", err)
	}
	rows.Close()
	for _, item := range items {
		if _, err := tx.Exec(ctx, `
INSERT INTO audit.tool_events (tenant_id, run_id, tool_call_id, event_type, actor_member_id, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'expired', $4::uuid,
        jsonb_build_object('decision', 'expired', 'reason', 'approval_ttl_elapsed'))`,
			item.tenantID, item.runID, item.toolCallID, item.actorID); err != nil {
			return fmt.Errorf("audit expired tool approval: %w", err)
		}
		if err := insertRunEvent(ctx, tx, item.tenantID, item.runID, item.traceID, "tool_approval_expired",
			`jsonb_build_object('tool_call_id', $5::text)`, item.toolCallID); err != nil {
			return err
		}
	}
	return nil
}

func insertRunEvent(ctx context.Context, tx pgx.Tx, tenantID, runID, traceID, eventType, evidenceSQL string, evidenceArgs ...any) error {
	query := `
INSERT INTO audit.agent_run_events (tenant_id, run_id, trace_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3, $4, ` + evidenceSQL + `)`
	args := []any{tenantID, runID, traceID, eventType}
	args = append(args, evidenceArgs...)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("record Agent Run lifecycle event: %w", err)
	}
	return nil
}

func requireOne(rows int64, operation string) error {
	if rows != 1 {
		return fmt.Errorf("%s: execution lease is not held", operation)
	}
	return nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}

func newToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
