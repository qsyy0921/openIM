package delivery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type PrepareRequest struct {
	RunID, LeaseToken, TenantID, Channel, TargetID, Content string
	SessionType                                             int32
	WaitingApproval                                         bool
}

type Record struct {
	ID, RunID, TenantID, Channel, TargetID, Content string
	SessionType                                     int32
	WaitingApproval                                 bool
	LeaseToken                                      string
	Attempts                                        int
}

func (s *Store) Prepare(ctx context.Context, request PrepareRequest) error {
	deliveryID, err := randomUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin prepare Agent delivery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
INSERT INTO agent.deliveries (
    id, run_id, tenant_id, channel, target_id, session_type, content, waiting_approval
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
ON CONFLICT (run_id) DO NOTHING`
	result, err := tx.Exec(ctx, insert, deliveryID, request.RunID, request.TenantID, request.Channel,
		request.TargetID, request.SessionType, request.Content, request.WaitingApproval)
	if err != nil {
		return fmt.Errorf("insert Agent delivery: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("prepare Agent delivery: Run already has a delivery")
	}

	const update = `
UPDATE agent.runs
SET state = 'delivery_pending', lease_token = NULL, lease_until = NULL,
    available_at = now(), last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND state = 'running'
  AND lease_token = $3 AND lease_until >= now()`
	result, err = tx.Exec(ctx, update, request.RunID, request.TenantID, request.LeaseToken)
	if err != nil {
		return fmt.Errorf("transition Agent Run to delivery pending: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("prepare Agent delivery: execution lease is not held")
	}
	if err := insertRunLifecycle(ctx, tx, request.RunID, "delivery_prepared",
		`jsonb_build_object('channel', $3::text, 'waiting_approval', $4::boolean)`, request.Channel, request.WaitingApproval); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent delivery: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, lease time.Duration, maxAttempts int) (*Record, error) {
	if err := s.failExhausted(ctx, maxAttempts); err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	leaseSeconds := durationSeconds(lease)
	const query = `
WITH candidate AS (
    SELECT delivery.id
    FROM agent.deliveries AS delivery
    WHERE delivery.attempts < $1 AND delivery.available_at <= now()
      AND NOT EXISTS (
          SELECT 1 FROM platform_meta.runtime_controls AS control
          WHERE control.tenant_id = delivery.tenant_id
            AND control.component = 'agent_delivery' AND control.paused
      )
      AND (delivery.state = 'pending' OR (delivery.state = 'sending' AND delivery.lease_until < now()))
    ORDER BY delivery.available_at, delivery.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE agent.deliveries AS d
SET state = 'sending', attempts = attempts + 1, lease_token = $2,
    lease_until = now() + make_interval(secs => $3), updated_at = now()
FROM candidate
WHERE d.id = candidate.id
RETURNING d.id::text, d.run_id::text, d.tenant_id::text, d.channel,
          d.target_id, d.session_type, d.content, d.waiting_approval,
          d.lease_token, d.attempts`
	var record Record
	err = s.pool.QueryRow(ctx, query, maxAttempts, token, leaseSeconds).Scan(
		&record.ID, &record.RunID, &record.TenantID, &record.Channel,
		&record.TargetID, &record.SessionType, &record.Content, &record.WaitingApproval,
		&record.LeaseToken, &record.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim Agent delivery: %w", err)
	}
	return &record, nil
}

func (s *Store) MarkSent(ctx context.Context, record Record, externalMessageID string) error {
	if externalMessageID == "" {
		return errors.New("complete Agent delivery: external message ID is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete Agent delivery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const updateDelivery = `
UPDATE agent.deliveries
SET state = 'sent', external_message_id = $3, lease_token = NULL, lease_until = NULL,
    last_error = NULL, completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'sending' AND lease_token = $2`
	result, err := tx.Exec(ctx, updateDelivery, record.ID, record.LeaseToken, externalMessageID)
	if err != nil {
		return fmt.Errorf("complete Agent delivery: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("complete Agent delivery: delivery lease is not held")
	}
	state := "succeeded"
	if record.WaitingApproval {
		state = "waiting_approval"
	}
	const updateRun = `
UPDATE agent.runs
SET state = $2, reply_server_msg_id = $3, completed_at = CASE WHEN $2 = 'succeeded' THEN now() ELSE NULL END,
    last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'delivery_pending'`
	result, err = tx.Exec(ctx, updateRun, record.RunID, state, externalMessageID)
	if err != nil {
		return fmt.Errorf("complete Agent Run delivery: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("complete Agent Run delivery: Run is not delivery pending")
	}
	if err := completeDelegationForRun(ctx, tx, record, externalMessageID); err != nil {
		return err
	}
	if err := advanceLaneForRun(ctx, tx, record.RunID); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, record, "sent", ""); err != nil {
		return err
	}
	if err := insertRunLifecycle(ctx, tx, record.RunID, "delivery_sent",
		`jsonb_build_object('channel', $3::text, 'attempts', $4::integer)`, record.Channel, record.Attempts); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit completed Agent delivery: %w", err)
	}
	return nil
}

func (s *Store) MarkRetry(ctx context.Context, record Record, failure string, maxAttempts int, retryAfter time.Duration) error {
	failure = boundedError(failure)
	if record.Attempts >= maxAttempts {
		return s.finishFailure(ctx, record, "failed", "failed", failure, "retry_exhausted")
	}
	const query = `
UPDATE agent.deliveries
SET state = 'pending', available_at = now() + make_interval(secs => $3),
    lease_token = NULL, lease_until = NULL, last_error = $4, updated_at = now()
WHERE id = $1::uuid AND state = 'sending' AND lease_token = $2`
	result, err := s.pool.Exec(ctx, query, record.ID, record.LeaseToken, durationSeconds(retryAfter), failure)
	if err != nil {
		return fmt.Errorf("retry Agent delivery: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("retry Agent delivery: delivery lease is not held")
	}
	return nil
}

func (s *Store) MarkPermanent(ctx context.Context, record Record, failure string) error {
	return s.finishFailure(ctx, record, "failed", "failed", boundedError(failure), "permanent_failure")
}

func (s *Store) MarkUncertain(ctx context.Context, record Record, failure string) error {
	return s.finishFailure(ctx, record, "uncertain", "delivery_unknown", boundedError(failure), "outcome_unknown")
}

func (s *Store) finishFailure(ctx context.Context, record Record, deliveryState, runState, failure, eventType string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish Agent delivery failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const updateDelivery = `
UPDATE agent.deliveries
SET state = $3, lease_token = NULL, lease_until = NULL, last_error = $4,
    completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'sending' AND lease_token = $2`
	result, err := tx.Exec(ctx, updateDelivery, record.ID, record.LeaseToken, deliveryState, failure)
	if err != nil {
		return fmt.Errorf("finish Agent delivery failure: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("finish Agent delivery failure: delivery lease is not held")
	}
	const updateRun = `
UPDATE agent.runs
SET state = $2, last_error = $3, completed_at = CASE WHEN $2 = 'failed' THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1::uuid AND state = 'delivery_pending'`
	result, err = tx.Exec(ctx, updateRun, record.RunID, runState, failure)
	if err != nil {
		return fmt.Errorf("finish Agent Run delivery failure: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("finish Agent Run delivery failure: Run is not delivery pending")
	}
	if err := failDelegationForRun(ctx, tx, record, runState, failure); err != nil {
		return err
	}
	if err := advanceLaneForRun(ctx, tx, record.RunID); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, record, eventType, failure); err != nil {
		return err
	}
	if err := insertRunLifecycle(ctx, tx, record.RunID, "delivery_"+eventType,
		`jsonb_build_object('channel', $3::text, 'attempts', $4::integer, 'error', $5::text)`, record.Channel, record.Attempts, failure); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent delivery failure: %w", err)
	}
	return nil
}

func (s *Store) failExhausted(ctx context.Context, maxAttempts int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin recover exhausted Agent deliveries: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
WITH exhausted AS (
    UPDATE agent.deliveries
    SET state = 'failed', lease_token = NULL, lease_until = NULL,
        last_error = 'delivery worker lease expired after final attempt',
        completed_at = now(), updated_at = now()
    WHERE state = 'sending' AND lease_until < now() AND attempts >= $1
    RETURNING id, run_id, tenant_id, attempts
), failed_runs AS (
    UPDATE agent.runs AS r
    SET state = 'failed', last_error = 'delivery worker lease expired after final attempt',
        completed_at = now(), updated_at = now()
    FROM exhausted AS e
    WHERE r.id = e.run_id AND r.state = 'delivery_pending'
	RETURNING e.id, e.run_id, e.tenant_id, e.attempts,
	          r.source_channel, r.conversation_id, r.conversation_sequence, r.trace_id
), advanced_lanes AS (
	UPDATE agent.conversation_lanes AS lane
	SET next_dispatch_sequence = next_dispatch_sequence + 1, updated_at = now()
	FROM failed_runs AS failed
	WHERE lane.tenant_id = failed.tenant_id
	  AND lane.source_channel = failed.source_channel
	  AND lane.conversation_id = failed.conversation_id
	  AND lane.next_dispatch_sequence = failed.conversation_sequence
	RETURNING failed.id
), run_events AS (
	INSERT INTO audit.agent_run_events (tenant_id, run_id, trace_id, event_type, evidence)
	SELECT tenant_id, run_id, trace_id, 'delivery_lease_exhausted', jsonb_build_object('attempts', attempts)
	FROM failed_runs
	RETURNING run_id
), failed_delegations AS (
	UPDATE agent.delegations AS delegation
	SET state = 'failed', last_error = 'delivery worker lease expired after final attempt',
	    completed_at = now(), updated_at = now()
	FROM failed_runs AS failed
	WHERE delegation.tenant_id = failed.tenant_id AND delegation.child_run_id = failed.run_id
	  AND delegation.state IN ('queued', 'running')
	RETURNING delegation.tenant_id, delegation.id, delegation.child_run_id
), delegation_events AS (
	INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
	SELECT tenant_id, id, 'failed',
	       jsonb_build_object('child_run_id', child_run_id::text, 'reason', 'delivery_lease_exhausted')
	FROM failed_delegations
	RETURNING delegation_id
)
INSERT INTO audit.delivery_events (tenant_id, run_id, delivery_id, event_type, evidence)
SELECT tenant_id, run_id, id, 'lease_exhausted', jsonb_build_object('attempts', attempts)
FROM failed_runs`
	if _, err := tx.Exec(ctx, query, maxAttempts); err != nil {
		return fmt.Errorf("recover exhausted Agent deliveries: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit exhausted Agent delivery recovery: %w", err)
	}
	return nil
}

func completeDelegationForRun(ctx context.Context, tx pgx.Tx, record Record, externalMessageID string) error {
	digest := sha256.Sum256([]byte(record.Content))
	checksum := "sha256:" + hex.EncodeToString(digest[:])
	var delegationID string
	err := tx.QueryRow(ctx, `
UPDATE agent.delegations
SET state = 'completed', result_checksum = $3, last_error = NULL,
    completed_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND child_run_id = $2::uuid AND state IN ('queued', 'running')
RETURNING id::text`, record.TenantID, record.RunID, checksum).Scan(&delegationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("complete Agent delegation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'completed',
        jsonb_build_object('child_run_id', $3::text, 'external_message_id', $4::text,
                           'result_checksum', $5::text))`,
		record.TenantID, delegationID, record.RunID, externalMessageID, checksum); err != nil {
		return fmt.Errorf("audit completed Agent delegation: %w", err)
	}
	return nil
}

func failDelegationForRun(ctx context.Context, tx pgx.Tx, record Record, runState, failure string) error {
	state := "failed"
	if runState == "delivery_unknown" {
		state = "unknown"
	}
	var delegationID string
	err := tx.QueryRow(ctx, `
UPDATE agent.delegations
SET state = $3, last_error = $4, completed_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND child_run_id = $2::uuid AND state IN ('queued', 'running')
RETURNING id::text`, record.TenantID, record.RunID, state, failure).Scan(&delegationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finish Agent delegation delivery: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_delegation_events (tenant_id, delegation_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3,
        jsonb_build_object('child_run_id', $4::text, 'error', $5::text))`,
		record.TenantID, delegationID, state, record.RunID, failure); err != nil {
		return fmt.Errorf("audit Agent delegation delivery failure: %w", err)
	}
	return nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, record Record, eventType, failure string) error {
	const query = `
INSERT INTO audit.delivery_events (tenant_id, run_id, delivery_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4,
        jsonb_build_object('channel', $5::text, 'attempts', $6::integer, 'error', NULLIF($7::text, '')))`
	if _, err := tx.Exec(ctx, query, record.TenantID, record.RunID, record.ID, eventType, record.Channel, record.Attempts, failure); err != nil {
		return fmt.Errorf("record Agent delivery audit event: %w", err)
	}
	return nil
}

func advanceLaneForRun(ctx context.Context, tx pgx.Tx, runID string) error {
	const query = `
UPDATE agent.conversation_lanes AS lane
SET next_dispatch_sequence = next_dispatch_sequence + 1, updated_at = now()
FROM agent.runs AS run
WHERE run.id = $1::uuid
  AND lane.tenant_id = run.tenant_id
  AND lane.source_channel = run.source_channel
  AND lane.conversation_id = run.conversation_id
  AND lane.next_dispatch_sequence = run.conversation_sequence`
	result, err := tx.Exec(ctx, query, runID)
	if err != nil {
		return fmt.Errorf("advance delivered Agent conversation lane: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("advance delivered Agent conversation lane: Run does not own the dispatch position")
	}
	return nil
}

func insertRunLifecycle(ctx context.Context, tx pgx.Tx, runID, eventType, evidenceSQL string, evidenceArgs ...any) error {
	query := `
INSERT INTO audit.agent_run_events (tenant_id, run_id, trace_id, event_type, evidence)
SELECT tenant_id, id, trace_id, $2, ` + evidenceSQL + `
FROM agent.runs WHERE id = $1::uuid`
	args := []any{runID, eventType}
	args = append(args, evidenceArgs...)
	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("record Agent delivery lifecycle event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("record Agent delivery lifecycle event: Run does not exist")
	}
	return nil
}

func boundedError(value string) string {
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}

func durationSeconds(value time.Duration) int64 {
	return int64((value + time.Second - 1) / time.Second)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate delivery ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}

func randomToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate delivery lease token: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
