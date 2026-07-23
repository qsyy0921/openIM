package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

var ErrFactNotFound = errors.New("personal memory fact is not found")

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) AppendUpsert(ctx context.Context, scope Scope, factKey, sourceRunID, idempotencyKey string, payload FactPayload) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if factKey == "" || strings.TrimSpace(factKey) != factKey || idempotencyKey == "" {
		return "", errors.New("memory fact key and idempotency key are required")
	}
	expected, err := NewFactPayload(payload.Category, payload.Content)
	if err != nil || expected.Checksum != payload.Checksum {
		return "", errors.New("memory fact payload checksum is invalid")
	}
	return s.append(ctx, scope, "fact_upserted", factKey, sourceRunID, idempotencyKey, payload)
}

func (s *Store) AppendDelete(ctx context.Context, scope Scope, factKey, sourceRunID, idempotencyKey string) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if factKey == "" || idempotencyKey == "" {
		return "", errors.New("memory delete fact and idempotency keys are required")
	}
	return s.append(ctx, scope, "fact_deleted", factKey, sourceRunID, idempotencyKey, map[string]any{})
}

func (s *Store) AppendErase(ctx context.Context, scope Scope, sourceRunID, idempotencyKey string) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if idempotencyKey == "" {
		return "", errors.New("memory erase idempotency key is required")
	}
	return s.append(ctx, scope, "stream_erased", "", sourceRunID, idempotencyKey, map[string]any{})
}

func (s *Store) append(ctx context.Context, scope Scope, eventType, factKey, sourceRunID, idempotencyKey string, payload any) (string, error) {
	eventID, err := randomUUID()
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode memory event payload: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", fmt.Errorf("begin append memory event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	streamID, err := ensureStream(ctx, tx, scope)
	if err != nil {
		return "", err
	}
	var existingEventID string
	err = tx.QueryRow(ctx, `
SELECT id::text FROM memory.events
WHERE tenant_id = $1::uuid AND idempotency_key = $2`, scope.TenantID, idempotencyKey).Scan(&existingEventID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit duplicate memory event read: %w", err)
		}
		return existingEventID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("read duplicate memory event: %w", err)
	}
	const sequenceQuery = `
UPDATE memory.streams
SET next_sequence = next_sequence + 1, updated_at = now()
WHERE id = $1::uuid
RETURNING next_sequence - 1`
	var sequence int64
	if err := tx.QueryRow(ctx, sequenceQuery, streamID).Scan(&sequence); err != nil {
		return "", fmt.Errorf("allocate memory event sequence: %w", err)
	}
	const insert = `
INSERT INTO memory.events (
    id, tenant_id, stream_id, sequence, event_type, fact_key, payload,
    source_run_id, idempotency_key
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NULLIF($6, ''), $7::jsonb,
          NULLIF($8, '')::uuid, $9)`
	result, err := tx.Exec(ctx, insert, eventID, scope.TenantID, streamID, sequence, eventType,
		factKey, string(payloadJSON), sourceRunID, idempotencyKey)
	if err != nil {
		return "", fmt.Errorf("append memory event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return "", errors.New("append memory event inserted no row")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit memory event: %w", err)
	}
	return eventID, nil
}

func ensureStream(ctx context.Context, tx pgx.Tx, scope Scope) (string, error) {
	streamID, err := randomUUID()
	if err != nil {
		return "", err
	}
	const insert = `
INSERT INTO memory.streams (
    id, tenant_id, scope_type, owner_member_id, source_channel, conversation_id
) VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, '')::uuid, NULLIF($5, ''), NULLIF($6, ''))
ON CONFLICT DO NOTHING`
	if _, err := tx.Exec(ctx, insert, streamID, scope.TenantID, scope.Type, scope.MemberID, scope.SourceChannel, scope.ConversationID); err != nil {
		return "", fmt.Errorf("ensure memory stream: %w", err)
	}
	const read = `
SELECT id::text FROM memory.streams
WHERE tenant_id = $1::uuid AND scope_type = $2
  AND (($2 = 'personal' AND owner_member_id = NULLIF($3, '')::uuid)
       OR ($2 = 'group' AND source_channel = $4 AND conversation_id = $5))
FOR UPDATE`
	if err := tx.QueryRow(ctx, read, scope.TenantID, scope.Type, scope.MemberID, scope.SourceChannel, scope.ConversationID).Scan(&streamID); err != nil {
		return "", fmt.Errorf("lock memory stream: %w", err)
	}
	return streamID, nil
}

func (s *Store) ProjectNext(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin memory projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const selectEvent = `
SELECT stream.id::text, stream.tenant_id::text, event.id::text, event.sequence,
       event.event_type, COALESCE(event.fact_key, ''), event.payload,
       COALESCE(event.source_run_id::text, '')
FROM memory.streams AS stream
JOIN memory.events AS event
  ON event.stream_id = stream.id AND event.sequence = stream.projected_sequence + 1
WHERE stream.projected_sequence < stream.next_sequence - 1
ORDER BY stream.updated_at, stream.id
FOR UPDATE OF stream SKIP LOCKED
LIMIT 1`
	var streamID, tenantID, eventID, eventType, factKey, sourceRunID string
	var sequence int64
	var payloadJSON []byte
	if err := tx.QueryRow(ctx, selectEvent).Scan(
		&streamID, &tenantID, &eventID, &sequence, &eventType, &factKey, &payloadJSON, &sourceRunID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("claim memory projection event: %w", err)
	}
	if err := applyEvent(ctx, tx, tenantID, streamID, eventID, sourceRunID, eventType, factKey, payloadJSON); err != nil {
		return false, err
	}
	const advance = `
UPDATE memory.streams
SET projected_sequence = $2, updated_at = now()
WHERE id = $1::uuid AND projected_sequence = $2 - 1`
	result, err := tx.Exec(ctx, advance, streamID, sequence)
	if err != nil || result.RowsAffected() != 1 {
		return false, errors.New("advance memory projection cursor failed")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.memory_projector_events (tenant_id, stream_id, memory_event_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'projected', jsonb_build_object('sequence', $4::bigint, 'event_type', $5::text))`,
		tenantID, streamID, eventID, sequence, eventType); err != nil {
		return false, fmt.Errorf("audit memory projection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit memory projection: %w", err)
	}
	return true, nil
}

func applyEvent(ctx context.Context, tx pgx.Tx, tenantID, streamID, eventID, sourceRunID, eventType, factKey string, payloadJSON []byte) error {
	switch eventType {
	case "fact_upserted":
		var payload FactPayload
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return fmt.Errorf("decode memory fact event: %w", err)
		}
		expected, err := NewFactPayload(payload.Category, payload.Content)
		if err != nil || expected.Checksum != payload.Checksum {
			return errors.New("projected memory fact payload is invalid")
		}
		factID, err := randomUUID()
		if err != nil {
			return err
		}
		const upsert = `
INSERT INTO memory.facts (
    id, tenant_id, stream_id, fact_key, category, content,
    source_event_id, source_run_id, checksum, state
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6,
          $7::uuid, NULLIF($8, '')::uuid, $9, 'active')
ON CONFLICT (stream_id, fact_key) DO UPDATE
SET category = EXCLUDED.category, content = EXCLUDED.content,
    source_event_id = EXCLUDED.source_event_id, source_run_id = EXCLUDED.source_run_id,
    checksum = EXCLUDED.checksum, state = 'active', updated_at = now()`
		if _, err := tx.Exec(ctx, upsert, factID, tenantID, streamID, factKey, payload.Category,
			payload.Content, eventID, sourceRunID, payload.Checksum); err != nil {
			return fmt.Errorf("project memory fact upsert: %w", err)
		}
	case "fact_deleted":
		if _, err := tx.Exec(ctx, `
UPDATE memory.facts
SET state = 'deleted', source_event_id = $3::uuid, source_run_id = NULLIF($4, '')::uuid, updated_at = now()
WHERE stream_id = $1::uuid AND fact_key = $2`, streamID, factKey, eventID, sourceRunID); err != nil {
			return fmt.Errorf("project memory fact delete: %w", err)
		}
	case "stream_erased":
		if _, err := tx.Exec(ctx, `
UPDATE memory.facts SET state = 'deleted', source_event_id = $2::uuid, updated_at = now()
WHERE stream_id = $1::uuid AND state = 'active'`, streamID, eventID); err != nil {
			return fmt.Errorf("project memory stream erase: %w", err)
		}
	default:
		return errors.New("unsupported memory event type")
	}
	return nil
}

func (s *Store) SearchPersonal(ctx context.Context, tenantID, memberID, query string, limit int) ([]Fact, error) {
	if tenantID == "" || memberID == "" || strings.TrimSpace(query) == "" || limit < 1 || limit > 8 {
		return nil, errors.New("personal memory search input is invalid")
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, errors.New("personal memory search has no terms")
	}
	const statement = `
SELECT fact.id::text, fact.stream_id::text, fact.fact_key, fact.category, fact.content, fact.checksum,
       fact.updated_at, matches.score
FROM memory.streams AS stream
JOIN memory.facts AS fact ON fact.stream_id = stream.id AND fact.state = 'active'
JOIN LATERAL (
    SELECT count(*)::integer AS score
    FROM unnest($3::text[]) AS term
    WHERE strpos(lower(fact.content), lower(term)) > 0
) AS matches ON matches.score > 0
WHERE stream.tenant_id = $1::uuid AND stream.scope_type = 'personal'
  AND stream.owner_member_id = $2::uuid
ORDER BY matches.score DESC, fact.updated_at DESC, fact.id
LIMIT $4`
	rows, err := s.pool.Query(ctx, statement, tenantID, memberID, terms, limit)
	if err != nil {
		return nil, fmt.Errorf("search personal memory: %w", err)
	}
	defer rows.Close()
	var facts []Fact
	for rows.Next() {
		var fact Fact
		var score int
		if err := rows.Scan(&fact.ID, &fact.StreamID, &fact.FactKey, &fact.Category, &fact.Content, &fact.Checksum, &fact.UpdatedAt, &score); err != nil {
			return nil, fmt.Errorf("scan personal memory: %w", err)
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (s *Store) SearchGroup(ctx context.Context, scope Scope, query string, limit int) ([]Fact, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.Type != "group" || strings.TrimSpace(query) == "" || limit < 1 || limit > 8 {
		return nil, errors.New("group memory search input is invalid")
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, errors.New("group memory search has no terms")
	}
	const statement = `
SELECT fact.id::text, fact.stream_id::text, fact.fact_key, fact.category, fact.content, fact.checksum,
       fact.updated_at, matches.score
FROM memory.streams AS stream
JOIN memory.facts AS fact ON fact.stream_id = stream.id AND fact.state = 'active'
JOIN LATERAL (
    SELECT count(*)::integer AS score
    FROM unnest($4::text[]) AS term
    WHERE strpos(lower(fact.content), lower(term)) > 0
) AS matches ON matches.score > 0
WHERE stream.tenant_id = $1::uuid AND stream.scope_type = 'group'
  AND stream.source_channel = $2 AND stream.conversation_id = $3
ORDER BY matches.score DESC, fact.updated_at DESC, fact.id
LIMIT $5`
	rows, err := s.pool.Query(ctx, statement, scope.TenantID, scope.SourceChannel, scope.ConversationID, terms, limit)
	if err != nil {
		return nil, fmt.Errorf("search group memory: %w", err)
	}
	defer rows.Close()
	facts := make([]Fact, 0)
	for rows.Next() {
		var fact Fact
		var score int
		if err := rows.Scan(&fact.ID, &fact.StreamID, &fact.FactKey, &fact.Category, &fact.Content, &fact.Checksum, &fact.UpdatedAt, &score); err != nil {
			return nil, fmt.Errorf("scan group memory: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group memory: %w", err)
	}
	return facts, nil
}

func (s *Store) ListPersonal(ctx context.Context, tenantID, memberID string, limit int) ([]Fact, error) {
	if tenantID == "" || memberID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("personal memory list input is invalid")
	}
	const statement = `
SELECT fact.id::text, fact.stream_id::text, fact.fact_key, fact.category,
       fact.content, fact.checksum, fact.updated_at
FROM memory.streams AS stream
JOIN memory.facts AS fact ON fact.stream_id = stream.id AND fact.state = 'active'
WHERE stream.tenant_id = $1::uuid AND stream.scope_type = 'personal'
  AND stream.owner_member_id = $2::uuid
ORDER BY fact.updated_at DESC, fact.id
LIMIT $3`
	rows, err := s.pool.Query(ctx, statement, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("list personal memory: %w", err)
	}
	defer rows.Close()
	facts := make([]Fact, 0)
	for rows.Next() {
		var fact Fact
		if err := rows.Scan(&fact.ID, &fact.StreamID, &fact.FactKey, &fact.Category,
			&fact.Content, &fact.Checksum, &fact.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan personal memory list: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate personal memory list: %w", err)
	}
	return facts, nil
}

func (s *Store) DeletePersonalFact(ctx context.Context, tenantID, memberID, factID, idempotencyKey string) (string, error) {
	if tenantID == "" || memberID == "" || factID == "" || idempotencyKey == "" {
		return "", errors.New("personal memory delete input is invalid")
	}
	const lookup = `
SELECT fact.fact_key
FROM memory.streams AS stream
JOIN memory.facts AS fact ON fact.stream_id = stream.id AND fact.state = 'active'
WHERE stream.tenant_id = $1::uuid AND stream.scope_type = 'personal'
  AND stream.owner_member_id = $2::uuid AND fact.id = $3::uuid`
	var factKey string
	if err := s.pool.QueryRow(ctx, lookup, tenantID, memberID, factID).Scan(&factKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrFactNotFound
		}
		return "", fmt.Errorf("resolve personal memory fact: %w", err)
	}
	return s.AppendDelete(ctx, Scope{Type: "personal", TenantID: tenantID, MemberID: memberID},
		factKey, "", idempotencyKey)
}

func (s *Store) ListPersonalExposures(ctx context.Context, tenantID, memberID string, limit int) ([]Exposure, error) {
	if tenantID == "" || memberID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("personal memory exposure input is invalid")
	}
	const statement = `
SELECT exposure.id::text, exposure.run_id::text, exposure.fact_id::text,
       fact.category, exposure.content_snapshot, exposure.retrieval_reason,
       COALESCE(feedback.signal, ''), exposure.created_at
FROM memory.exposures AS exposure
JOIN agent.runs AS run
  ON run.tenant_id = exposure.tenant_id AND run.id = exposure.run_id
JOIN memory.facts AS fact
  ON fact.tenant_id = exposure.tenant_id AND fact.id = exposure.fact_id
LEFT JOIN memory.feedback AS feedback
  ON feedback.tenant_id = exposure.tenant_id AND feedback.exposure_id = exposure.id
WHERE exposure.tenant_id = $1::uuid AND run.principal_member_id = $2::uuid
ORDER BY exposure.created_at DESC, exposure.ordinal
LIMIT $3`
	rows, err := s.pool.Query(ctx, statement, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("list personal memory exposures: %w", err)
	}
	defer rows.Close()
	exposures := make([]Exposure, 0)
	for rows.Next() {
		var exposure Exposure
		if err := rows.Scan(&exposure.ID, &exposure.RunID, &exposure.FactID, &exposure.Category,
			&exposure.Content, &exposure.RetrievalReason, &exposure.Feedback, &exposure.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan personal memory exposure: %w", err)
		}
		exposures = append(exposures, exposure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate personal memory exposures: %w", err)
	}
	return exposures, nil
}

func (s *Store) RecordExposures(ctx context.Context, tenantID, runID string, facts []Fact, reason string) error {
	if tenantID == "" || runID == "" || reason == "" || len(facts) > 8 {
		return errors.New("memory exposure input is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin memory exposures: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for ordinal, fact := range facts {
		exposureID, err := randomUUID()
		if err != nil {
			return err
		}
		const insert = `
INSERT INTO memory.exposures (
    id, tenant_id, run_id, fact_id, fact_checksum, content_snapshot, ordinal, retrieval_reason
)
SELECT $1::uuid, $2::uuid, $3::uuid, fact.id, fact.checksum, fact.content, $5, $6
FROM memory.facts AS fact
WHERE fact.tenant_id = $2::uuid AND fact.id = $4::uuid AND fact.state = 'active'
ON CONFLICT (run_id, fact_id) DO NOTHING`
		result, err := tx.Exec(ctx, insert, exposureID, tenantID, runID, fact.ID, ordinal, reason)
		if err != nil {
			return fmt.Errorf("record memory exposure: %w", err)
		}
		if result.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM memory.exposures WHERE run_id = $1::uuid AND fact_id = $2::uuid)`, runID, fact.ID).Scan(&exists); err != nil || !exists {
				return errors.New("memory exposure fact is missing or unauthorized")
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordFeedback(ctx context.Context, tenantID, exposureID, actorMemberID, signal string) error {
	if signal != "helpful" && signal != "not_helpful" && signal != "incorrect" {
		return errors.New("memory feedback signal is invalid")
	}
	feedbackID, err := randomUUID()
	if err != nil {
		return err
	}
	const query = `
INSERT INTO memory.feedback (id, tenant_id, exposure_id, actor_member_id, signal)
SELECT $1::uuid, $2::uuid, exposure.id, $4::uuid, $5
FROM memory.exposures AS exposure
JOIN agent.runs AS run ON run.id = exposure.run_id AND run.tenant_id = exposure.tenant_id
JOIN identity.members AS member
  ON member.tenant_id = run.tenant_id AND member.id = $4::uuid AND member.status = 'active'
WHERE exposure.tenant_id = $2::uuid AND exposure.id = $3::uuid
  AND run.principal_member_id = member.id
ON CONFLICT (exposure_id) DO NOTHING`
	result, err := s.pool.Exec(ctx, query, feedbackID, tenantID, exposureID, actorMemberID, signal)
	if err != nil {
		return fmt.Errorf("record memory feedback: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("memory feedback is duplicate or unauthorized")
	}
	return nil
}

func (s *Store) RebuildStream(ctx context.Context, tenantID, streamID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin memory stream rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var nextSequence int64
	if err := tx.QueryRow(ctx, `
SELECT next_sequence FROM memory.streams
WHERE tenant_id = $1::uuid AND id = $2::uuid
FOR UPDATE`, tenantID, streamID).Scan(&nextSequence); err != nil {
		return fmt.Errorf("lock memory stream for rebuild: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE memory.facts SET state = 'deleted', updated_at = now()
WHERE tenant_id = $1::uuid AND stream_id = $2::uuid`, tenantID, streamID); err != nil {
		return fmt.Errorf("reset memory facts for rebuild: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT id::text, event_type, COALESCE(fact_key, ''), payload,
       COALESCE(source_run_id::text, '')
FROM memory.events
WHERE tenant_id = $1::uuid AND stream_id = $2::uuid
ORDER BY sequence`, tenantID, streamID)
	if err != nil {
		return fmt.Errorf("load memory events for rebuild: %w", err)
	}
	type replayEvent struct {
		id, eventType, factKey, sourceRunID string
		payload                             []byte
	}
	var events []replayEvent
	for rows.Next() {
		var event replayEvent
		if err := rows.Scan(&event.id, &event.eventType, &event.factKey, &event.payload, &event.sourceRunID); err != nil {
			rows.Close()
			return fmt.Errorf("scan memory rebuild event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, event := range events {
		if err := applyEvent(ctx, tx, tenantID, streamID, event.id, event.sourceRunID, event.eventType, event.factKey, event.payload); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE memory.streams SET projected_sequence = $3, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, streamID, nextSequence-1); err != nil {
		return fmt.Errorf("finish memory stream rebuild: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.memory_projector_events (tenant_id, stream_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'rebuilt', jsonb_build_object('event_count', $3::integer))`,
		tenantID, streamID, len(events)); err != nil {
		return fmt.Errorf("audit memory stream rebuild: %w", err)
	}
	return tx.Commit(ctx)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate memory ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
