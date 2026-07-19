package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type ProactiveRequest struct {
	EventID, TenantID, MemberID, AgentID string
	SourceType, SourceChannel, TargetID  string
	Prompt                               string
}

func (r ProactiveRequest) Validate() error {
	if r.EventID == "" || r.TenantID == "" || r.MemberID == "" || r.AgentID == "" ||
		r.SourceType != "arxiv" || (r.SourceChannel != "openim" && r.SourceChannel != "telegram") ||
		r.TargetID == "" || r.Prompt == "" {
		return errors.New("proactive Agent Run request is invalid")
	}
	return nil
}

func (s *Store) EnqueueProactive(ctx context.Context, request ProactiveRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return "", fmt.Errorf("begin proactive Agent Run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	sourceEventID := "proactive:" + request.SourceType + ":" + request.EventID
	var existing string
	err = tx.QueryRow(ctx, `SELECT id::text FROM agent.runs WHERE source_event_id = $1`, sourceEventID).Scan(&existing)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("read proactive Agent Run: %w", err)
	}
	const resolve = `
SELECT deployment.id::text, version.id::text, version.spec_checksum,
       version.capability_snapshot_id, trigger.id::text
FROM agent.definitions AS definition
JOIN agent.deployments AS deployment
  ON deployment.tenant_id = definition.tenant_id AND deployment.agent_id = definition.id
 AND deployment.slot = 'production'
JOIN agent.versions AS version
  ON version.tenant_id = deployment.tenant_id AND version.agent_id = deployment.agent_id
 AND version.id = deployment.active_version_id
JOIN agent.triggers AS trigger
  ON trigger.tenant_id = definition.tenant_id AND trigger.agent_id = definition.id
 AND trigger.trigger_type = 'system_source' AND trigger.trigger_value = $3 AND trigger.enabled
JOIN identity.members AS member
  ON member.tenant_id = definition.tenant_id AND member.id = $4::uuid AND member.status = 'active'
WHERE definition.tenant_id = $1::uuid AND definition.id = $2::uuid AND definition.status = 'active'`
	var deploymentID, versionID, checksum, snapshotID, triggerID string
	if err := tx.QueryRow(ctx, resolve, request.TenantID, request.AgentID, "source:"+request.SourceType, request.MemberID).
		Scan(&deploymentID, &versionID, &checksum, &snapshotID, &triggerID); err != nil {
		return "", fmt.Errorf("resolve proactive Agent deployment: %w", err)
	}
	conversationID := "proactive_" + request.SourceChannel + "_" + request.TargetID
	if request.SourceChannel == "telegram" {
		conversationID = "tg_" + request.TargetID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.conversation_lanes (tenant_id, source_channel, conversation_id)
VALUES ($1::uuid, $2, $3)
ON CONFLICT (tenant_id, source_channel, conversation_id) DO NOTHING`,
		request.TenantID, request.SourceChannel, conversationID); err != nil {
		return "", fmt.Errorf("ensure proactive conversation lane: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `
UPDATE agent.conversation_lanes
SET next_enqueue_sequence = next_enqueue_sequence + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
RETURNING next_enqueue_sequence - 1`, request.TenantID, request.SourceChannel, conversationID).Scan(&sequence); err != nil {
		return "", fmt.Errorf("allocate proactive conversation sequence: %w", err)
	}
	runID, err := newUUID()
	if err != nil {
		return "", err
	}
	traceID := "agent-run:" + runID
	const insert = `
INSERT INTO agent.runs (
    id, source_event_id, tenant_id, principal_member_id,
    agent_id, agent_version_id, agent_deployment_id, agent_trigger_id, agent_spec_checksum,
    source_channel, conversation_id, conversation_sequence, sender_id, session_type, prompt,
    execution_plane, trace_id, capability_snapshot_id
) VALUES (
    $1::uuid, $2, $3::uuid, $4::uuid,
    $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9,
    $10, $11, $12, $13, 1, $14,
    'proactive_source', $15, $16
)`
	if _, err := tx.Exec(ctx, insert, runID, sourceEventID, request.TenantID, request.MemberID,
		request.AgentID, versionID, deploymentID, triggerID, checksum,
		request.SourceChannel, conversationID, sequence, request.TargetID, request.Prompt,
		traceID, snapshotID); err != nil {
		return "", fmt.Errorf("insert proactive Agent Run: %w", err)
	}
	if err := insertRunEvent(ctx, tx, request.TenantID, runID, traceID, "proactive_enqueued",
		`jsonb_build_object('source_type', $5::text, 'source_event_id', $6::text)`, request.SourceType, request.EventID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit proactive Agent Run: %w", err)
	}
	return runID, nil
}
