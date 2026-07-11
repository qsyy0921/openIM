package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Enqueue(ctx context.Context, trigger Trigger) (string, error) {
	runID, err := newUUID()
	if err != nil {
		return "", err
	}
	const query = `
INSERT INTO agent.runs (
    id, source_event_id, tenant_id, principal_member_id,
    conversation_id, sender_id, session_type, prompt
)
SELECT $1::uuid, $2, $3::uuid, l.member_id, $4, $5, $6, $7
FROM identity.identity_links AS l
WHERE l.tenant_id = $3::uuid
  AND l.openim_user_id = $5
  AND l.provisioning_state = 'ready'
ON CONFLICT (source_event_id) DO UPDATE SET source_event_id = EXCLUDED.source_event_id
RETURNING id::text`
	if err := s.pool.QueryRow(ctx, query, runID, trigger.EventID, trigger.TenantID, trigger.ConversationID, trigger.SenderID, trigger.SessionType, trigger.Prompt).Scan(&runID); err != nil {
		return "", fmt.Errorf("enqueue Agent Run: %w", err)
	}
	return runID, nil
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
	ID                 string
	TenantID           string
	MemberID           string
	ConversationID     string
	SenderID           string
	SessionType        int32
	Prompt             string
	CandidateText      string
	LeaseToken         string
	Attempts           int
	Model              string
	ProviderResponseID string
	ActionType         string
	ActionTitle        string
}

func (s *Store) Claim(ctx context.Context, lease time.Duration, maxAttempts int) (*Run, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	const query = `
WITH candidate AS (
    SELECT id
    FROM agent.runs
    WHERE attempts < $1
      AND available_at <= now()
      AND (state IN ('queued', 'reply_pending') OR (state = 'running' AND lease_until < now()))
    ORDER BY available_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE agent.runs AS r
SET state = 'running', attempts = attempts + 1,
    lease_token = $2, lease_until = now() + make_interval(secs => $3), updated_at = now()
FROM candidate
WHERE r.id = candidate.id
RETURNING r.id::text, r.tenant_id::text, r.principal_member_id::text, r.conversation_id, r.sender_id, r.session_type,
          r.prompt, COALESCE(r.candidate_text, ''), r.lease_token, r.attempts,
		  COALESCE(r.model, ''), COALESCE(r.provider_response_id, ''),
		  COALESCE(r.action_type, ''), COALESCE(r.action_title, '')`
	var run Run
	err = s.pool.QueryRow(ctx, query, maxAttempts, token, leaseSeconds).Scan(
		&run.ID, &run.TenantID, &run.MemberID, &run.ConversationID, &run.SenderID, &run.SessionType,
		&run.Prompt, &run.CandidateText, &run.LeaseToken, &run.Attempts, &run.Model, &run.ProviderResponseID,
		&run.ActionType, &run.ActionTitle,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim Agent Run: %w", err)
	}
	return &run, nil
}

func (s *Store) SaveCandidate(ctx context.Context, run Run, candidate Candidate, evidence []Evidence) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save Agent candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	for ordinal, item := range evidence {
		const insert = `
INSERT INTO agent.run_citations (
    run_id, citation_id, ordinal, document_id, version_id, chunk_id, title, source_uri, checksum
) VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9)`
		if _, err := tx.Exec(ctx, insert, run.ID, item.CitationID, ordinal, item.DocumentID, item.VersionID, item.ChunkID, item.Title, item.SourceURI, item.Checksum); err != nil {
			return fmt.Errorf("save Agent citation: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent candidate: %w", err)
	}
	return nil
}

func (s *Store) CompleteReply(ctx context.Context, run Run, serverMsgID string, waitingApproval bool) error {
	targetState := "succeeded"
	if waitingApproval {
		targetState = "waiting_approval"
	}
	const query = `
UPDATE agent.runs
SET state = $4, reply_server_msg_id = $3,
    completed_at = CASE WHEN $4 = 'succeeded' THEN now() ELSE NULL END,
    lease_token = NULL, lease_until = NULL, last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2 AND lease_until >= now()`
	result, err := s.pool.Exec(ctx, query, run.ID, run.LeaseToken, serverMsgID, targetState)
	if err != nil {
		return fmt.Errorf("complete Agent Run: %w", err)
	}
	return requireOne(result.RowsAffected(), "complete Agent Run")
}

func (s *Store) FailOrRetry(ctx context.Context, run Run, failure string, maxAttempts int, retryAfter time.Duration) error {
	if len(failure) > 1000 {
		failure = failure[:1000]
	}
	retrySeconds := int64((retryAfter + time.Second - 1) / time.Second)
	const query = `
UPDATE agent.runs
SET state = CASE
        WHEN attempts >= $4 THEN 'failed'
        WHEN candidate_text IS NULL THEN 'queued'
        ELSE 'reply_pending'
    END,
    available_at = CASE WHEN attempts >= $4 THEN available_at ELSE now() + make_interval(secs => $5) END,
    completed_at = CASE WHEN attempts >= $4 THEN now() ELSE NULL END,
    lease_token = NULL, lease_until = NULL, last_error = $3, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2`
	result, err := s.pool.Exec(ctx, query, run.ID, run.LeaseToken, failure, maxAttempts, retrySeconds)
	if err != nil {
		return fmt.Errorf("retry Agent Run: %w", err)
	}
	return requireOne(result.RowsAffected(), "retry Agent Run")
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
