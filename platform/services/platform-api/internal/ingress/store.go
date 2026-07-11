package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Outcome string

const (
	OutcomeAccepted  Outcome = "accepted"
	OutcomeDuplicate Outcome = "duplicate"
	OutcomeUnmapped  Outcome = "unmapped_sender"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ingest(ctx context.Context, message Message) (Outcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin ingress transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text
FROM (
    SELECT tenant_id, openim_user_id
    FROM identity.identity_links
    WHERE provisioning_state = 'ready'
    UNION ALL
    SELECT tenant_id, openim_user_id
    FROM agent.bot_identities
) AS senders
WHERE openim_user_id = $1
LIMIT 1`, message.SenderID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := insertRejection(ctx, tx, message.Source, message.ServerMsgID, message.SenderID, string(OutcomeUnmapped)); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit ingress rejection: %w", err)
		}
		return OutcomeUnmapped, nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve ingress tenant: %w", err)
	}

	event, err := message.ToEvent(tenantID)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("encode ingress event: %w", err)
	}
	const insertIngress = `
INSERT INTO integration.ingress_messages (
  source_key, event_id, source_topic, source_partition, source_offset,
  tenant_id, conversation_id, server_msg_id, client_msg_id, sender_id,
  session_type, content_type, content, event_payload
) VALUES ($1, $2, $3, $4, $5, $6::uuid, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)
ON CONFLICT DO NOTHING
RETURNING event_id`
	var insertedEventID string
	err = tx.QueryRow(ctx, insertIngress,
		event.SourceKey, event.EventID, message.Source.Topic, message.Source.Partition,
		message.Source.Offset, tenantID, event.ConversationID, event.ServerMsgID,
		event.ClientMsgID, event.SenderID, event.SessionType, event.ContentType,
		event.Content, payload,
	).Scan(&insertedEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit duplicate ingress: %w", err)
		}
		return OutcomeDuplicate, nil
	}
	if err != nil {
		return "", fmt.Errorf("insert ingress message: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO integration.outbox_events (event_id, event_type, event_key, payload)
VALUES ($1, $2, $3, $4::jsonb)`, event.EventID, event.EventType, tenantID+"/"+event.ConversationID, payload); err != nil {
		return "", fmt.Errorf("insert ingress outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit ingress transaction: %w", err)
	}
	return OutcomeAccepted, nil
}

func (s *Store) Reject(ctx context.Context, source Source, serverMsgID, senderID, reason string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO integration.ingress_rejections (
  source_topic, source_partition, source_offset, server_msg_id, sender_id, reason
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING`, source.Topic, source.Partition, source.Offset, serverMsgID, senderID, reason)
	if err != nil {
		return fmt.Errorf("record ingress rejection: %w", err)
	}
	return nil
}

func insertRejection(ctx context.Context, tx pgx.Tx, source Source, serverMsgID, senderID, reason string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO integration.ingress_rejections (
  source_topic, source_partition, source_offset, server_msg_id, sender_id, reason
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING`, source.Topic, source.Partition, source.Offset, serverMsgID, senderID, reason)
	if err != nil {
		return fmt.Errorf("record ingress rejection: %w", err)
	}
	return nil
}

type OutboxRecord struct {
	EventID    string
	EventKey   string
	Payload    []byte
	LeaseToken string
	Attempts   int
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int, lease time.Duration) ([]OutboxRecord, error) {
	leaseToken, err := newID()
	if err != nil {
		return nil, err
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	rows, err := s.pool.Query(ctx, `
WITH candidates AS (
  SELECT event_id
  FROM integration.outbox_events
  WHERE available_at <= now()
    AND (state = 'pending' OR (state = 'publishing' AND lease_until < now()))
  ORDER BY created_at
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
UPDATE integration.outbox_events AS o
SET state = 'publishing',
    lease_token = $2,
    lease_until = now() + make_interval(secs => $3),
    attempts = attempts + 1,
    updated_at = now()
FROM candidates
WHERE o.event_id = candidates.event_id
RETURNING o.event_id, o.event_key, o.payload, o.lease_token, o.attempts`, limit, leaseToken, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()
	var records []OutboxRecord
	for rows.Next() {
		var record OutboxRecord
		if err := rows.Scan(&record.EventID, &record.EventKey, &record.Payload, &record.LeaseToken, &record.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read outbox events: %w", err)
	}
	return records, nil
}

func (s *Store) MarkPublished(ctx context.Context, eventID, leaseToken string) error {
	result, err := s.pool.Exec(ctx, `
UPDATE integration.outbox_events
SET state = 'published', lease_token = NULL, lease_until = NULL,
    published_at = now(), last_error = NULL, updated_at = now()
WHERE event_id = $1 AND state = 'publishing' AND lease_token = $2 AND lease_until >= now()`, eventID, leaseToken)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("mark outbox published: publishing lease is not held")
	}
	return nil
}

func (s *Store) ReleaseOutbox(ctx context.Context, eventID, leaseToken, failure string, retryAfter time.Duration) error {
	retrySeconds := int64((retryAfter + time.Second - 1) / time.Second)
	result, err := s.pool.Exec(ctx, `
UPDATE integration.outbox_events
SET state = 'pending', lease_token = NULL, lease_until = NULL,
    available_at = now() + make_interval(secs => $3), last_error = $4, updated_at = now()
WHERE event_id = $1 AND state = 'publishing' AND lease_token = $2`, eventID, leaseToken, retrySeconds, failure)
	if err != nil {
		return fmt.Errorf("release outbox event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("release outbox event: publishing lease is not held")
	}
	return nil
}
