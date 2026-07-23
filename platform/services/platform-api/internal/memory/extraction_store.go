package memory

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
)

type ExtractionJob struct {
	ID, TenantID, RunID, MemberID, Phase, LeaseToken string
	SourceChannel, ConversationID                    string
	SessionType                                      int
	UserMessage, AssistantResponse                   string
	ExtractedFacts                                   []ExtractedFact
	ModelAttempts, ProjectionAttempts                int
}

type ExtractionStore struct{ pool *pgxpool.Pool }

func NewExtractionStore(pool *pgxpool.Pool) *ExtractionStore { return &ExtractionStore{pool: pool} }

func (s *ExtractionStore) EnqueueDelivered(ctx context.Context) (bool, error) {
	const query = `
INSERT INTO memory.extraction_jobs (id, tenant_id, run_id, member_id)
SELECT run.id, run.tenant_id, run.id, run.principal_member_id
FROM agent.runs AS run
WHERE run.state IN ('succeeded', 'waiting_approval')
  AND run.reply_server_msg_id IS NOT NULL
  AND run.prompt <> '' AND run.candidate_text IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM memory.extraction_jobs AS job
      WHERE job.tenant_id = run.tenant_id AND job.run_id = run.id
  )
ORDER BY run.updated_at, run.id
LIMIT 1
ON CONFLICT (tenant_id, run_id) DO NOTHING`
	result, err := s.pool.Exec(ctx, query)
	if err != nil {
		return false, fmt.Errorf("enqueue delivered memory extraction: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *ExtractionStore) Claim(ctx context.Context, lease time.Duration, maxAttempts int) (*ExtractionJob, error) {
	if err := s.failExhausted(ctx, maxAttempts); err != nil {
		return nil, err
	}
	token, err := extractionToken()
	if err != nil {
		return nil, err
	}
	const query = `
WITH candidate AS (
    SELECT id, state
    FROM memory.extraction_jobs
    WHERE available_at <= now() AND (
        (state = 'pending' AND model_attempts < $1)
        OR (state = 'extracting' AND lease_until < now() AND model_attempts < $1)
        OR (state = 'projection_pending' AND projection_attempts < $1)
        OR (state = 'projecting' AND lease_until < now() AND projection_attempts < $1)
    )
    ORDER BY CASE WHEN state IN ('projection_pending', 'projecting') THEN 0 ELSE 1 END,
             available_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE memory.extraction_jobs AS job
SET state = CASE WHEN candidate.state IN ('projection_pending', 'projecting')
                 THEN 'projecting' ELSE 'extracting' END,
    model_attempts = model_attempts + CASE WHEN candidate.state IN ('pending', 'extracting') THEN 1 ELSE 0 END,
    projection_attempts = projection_attempts + CASE WHEN candidate.state IN ('projection_pending', 'projecting') THEN 1 ELSE 0 END,
    lease_token = $2, lease_until = now() + make_interval(secs => $3), updated_at = now()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.id::text, job.tenant_id::text, job.run_id::text, job.member_id::text,
          job.state, job.lease_token, job.model_attempts, job.projection_attempts,
          COALESCE(job.extracted_facts, '[]'::jsonb),
          (SELECT prompt FROM agent.runs WHERE id = job.run_id),
	      (SELECT candidate_text FROM agent.runs WHERE id = job.run_id),
	      (SELECT source_channel FROM agent.runs WHERE id = job.run_id),
	      (SELECT conversation_id FROM agent.runs WHERE id = job.run_id),
	      (SELECT session_type FROM agent.runs WHERE id = job.run_id)`
	var job ExtractionJob
	var factsJSON []byte
	err = s.pool.QueryRow(ctx, query, maxAttempts, token, durationSeconds(lease)).Scan(
		&job.ID, &job.TenantID, &job.RunID, &job.MemberID, &job.Phase, &job.LeaseToken,
		&job.ModelAttempts, &job.ProjectionAttempts, &factsJSON, &job.UserMessage, &job.AssistantResponse,
		&job.SourceChannel, &job.ConversationID, &job.SessionType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim memory extraction: %w", err)
	}
	if err := json.Unmarshal(factsJSON, &job.ExtractedFacts); err != nil {
		return nil, fmt.Errorf("decode persisted memory extraction: %w", err)
	}
	return &job, nil
}

func (s *ExtractionStore) SaveGroupProposals(ctx context.Context, job ExtractionJob) error {
	if job.SessionType != 2 || (job.SourceChannel != "openim" && job.SourceChannel != "telegram") || job.ConversationID == "" {
		return errors.New("group memory extraction context is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin group memory proposals: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, fact := range job.ExtractedFacts {
		payload, err := NewFactPayload(fact.Category, fact.Content)
		if err != nil {
			return err
		}
		proposalID, err := randomUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO memory.group_fact_proposals (
    id, tenant_id, source_channel, conversation_id, source_run_id, proposed_by_member_id,
    fact_key, category, content, checksum, confidence
) VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, $7, $8, $9, $10, $11)
ON CONFLICT (tenant_id, source_run_id, fact_key) DO NOTHING`, proposalID, job.TenantID,
			job.SourceChannel, job.ConversationID, job.RunID, job.MemberID, fact.FactKey(),
			fact.Category, fact.Content, payload.Checksum, fact.Confidence); err != nil {
			return fmt.Errorf("persist group memory proposal: %w", err)
		}
	}
	if err := auditExtraction(ctx, tx, job, "group_review_pending", map[string]any{"fact_count": len(job.ExtractedFacts)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ExtractionStore) SaveResult(ctx context.Context, job ExtractionJob, result ExtractionResult) error {
	factsJSON, checksum, err := result.FactsJSONAndChecksum()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin memory extraction result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE memory.extraction_jobs
SET state = 'projection_pending', extracted_facts = $3::jsonb,
    provider_response_id = $4, result_checksum = $5,
    lease_token = NULL, lease_until = NULL, last_error = NULL, available_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'extracting' AND lease_token = $2`
	update, err := tx.Exec(ctx, query, job.ID, job.LeaseToken, string(factsJSON), result.ProviderResponseID, checksum)
	if err != nil {
		return fmt.Errorf("save memory extraction result: %w", err)
	}
	if update.RowsAffected() != 1 {
		return errors.New("save memory extraction result: lease is not held")
	}
	if err := auditExtraction(ctx, tx, job, "model_result_saved", map[string]any{"fact_count": len(result.Facts), "checksum": checksum}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ExtractionStore) Complete(ctx context.Context, job ExtractionJob) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin memory extraction completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE memory.extraction_jobs
SET state = 'succeeded', lease_token = NULL, lease_until = NULL,
    last_error = NULL, completed_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'projecting' AND lease_token = $2`
	result, err := tx.Exec(ctx, query, job.ID, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete memory extraction: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("complete memory extraction: lease is not held")
	}
	if err := auditExtraction(ctx, tx, job, "projection_succeeded", map[string]any{"fact_count": len(job.ExtractedFacts)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ExtractionStore) Retry(ctx context.Context, job ExtractionJob, failure string, maxAttempts int, delay time.Duration) error {
	failure = boundedMemoryError(failure)
	attempts := job.ModelAttempts
	pendingState := "pending"
	if job.Phase == "projecting" {
		attempts = job.ProjectionAttempts
		pendingState = "projection_pending"
	}
	nextState := pendingState
	completed := any(nil)
	if attempts >= maxAttempts {
		nextState = "failed"
		completed = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin memory extraction retry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE memory.extraction_jobs
SET state = $3, lease_token = NULL, lease_until = NULL,
    available_at = now() + make_interval(secs => $4), last_error = $5,
    completed_at = $6, updated_at = now()
WHERE id = $1::uuid AND state = $7 AND lease_token = $2`
	result, err := tx.Exec(ctx, query, job.ID, job.LeaseToken, nextState,
		durationSeconds(delay), failure, completed, job.Phase)
	if err != nil {
		return fmt.Errorf("retry memory extraction: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("retry memory extraction: lease is not held")
	}
	if err := auditExtraction(ctx, tx, job, "phase_failed", map[string]any{"phase": job.Phase, "error": failure, "terminal": nextState == "failed"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ExtractionStore) failExhausted(ctx context.Context, maxAttempts int) error {
	const query = `
UPDATE memory.extraction_jobs
SET state = 'failed', lease_token = NULL, lease_until = NULL,
    last_error = 'memory extraction lease expired after final attempt',
    completed_at = now(), updated_at = now()
WHERE (state = 'extracting' AND lease_until < now() AND model_attempts >= $1)
   OR (state = 'projecting' AND lease_until < now() AND projection_attempts >= $1)`
	_, err := s.pool.Exec(ctx, query, maxAttempts)
	return err
}

func auditExtraction(ctx context.Context, tx pgx.Tx, job ExtractionJob, eventType string, evidence map[string]any) error {
	data, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO audit.memory_extraction_events (tenant_id, job_id, run_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb)`,
		job.TenantID, job.ID, job.RunID, eventType, string(data))
	return err
}

func extractionToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func durationSeconds(value time.Duration) int64 {
	return int64((value + time.Second - 1) / time.Second)
}

func boundedMemoryError(value string) string {
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}
