package remotea2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

var (
	remoteSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	authEnvPattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
)

type RemoteAgent struct {
	ID                 string          `json:"id"`
	Slug               string          `json:"slug"`
	DisplayName        string          `json:"display_name"`
	CardURL            string          `json:"card_url"`
	EndpointURL        string          `json:"endpoint_url,omitempty"`
	ExpectedCardDigest string          `json:"expected_card_digest"`
	ObservedCardDigest string          `json:"observed_card_digest,omitempty"`
	AgentCard          json.RawMessage `json:"agent_card,omitempty"`
	AuthEnvKey         string          `json:"auth_env_key,omitempty"`
	Enabled            bool            `json:"enabled"`
	LifecycleState     string          `json:"lifecycle_state"`
	LifecycleRevision  int64           `json:"lifecycle_revision"`
	LastErrorCode      string          `json:"last_error_code,omitempty"`
	LastVerifiedAt     *time.Time      `json:"last_verified_at,omitempty"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type RegisterInput struct {
	Slug, DisplayName, CardURL, ExpectedCardDigest, AuthEnvKey string
}

type Job struct {
	ID, ParentRunID, RemoteAgentID, RemoteAgentSlug, MessageID string
	Task, State, RemoteTaskID, ResponseChecksum, LastErrorCode string
	Response                                                   json.RawMessage
	CreatedAt, UpdatedAt                                       time.Time
}

type Store struct {
	pool   *pgxpool.Pool
	client *Client
}

func NewStore(pool *pgxpool.Pool, client *Client) *Store {
	if pool == nil || client == nil {
		panic("A2A database and client are required")
	}
	return &Store{pool: pool, client: client}
}

func (s *Store) List(ctx context.Context, tenantID string) ([]RemoteAgent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id::text, slug, display_name, card_url, COALESCE(endpoint_url, ''),
       expected_card_digest, COALESCE(observed_card_digest, ''), COALESCE(agent_card, '{}'::jsonb),
       COALESCE(auth_env_key, ''), enabled, lifecycle_state, lifecycle_revision,
       COALESCE(last_error_code, ''), last_verified_at, updated_at
FROM agent.remote_agents WHERE tenant_id = $1::uuid ORDER BY slug`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list remote A2A Agents: %w", err)
	}
	defer rows.Close()
	result := make([]RemoteAgent, 0)
	for rows.Next() {
		var item RemoteAgent
		if err := rows.Scan(&item.ID, &item.Slug, &item.DisplayName, &item.CardURL, &item.EndpointURL,
			&item.ExpectedCardDigest, &item.ObservedCardDigest, &item.AgentCard, &item.AuthEnvKey,
			&item.Enabled, &item.LifecycleState, &item.LifecycleRevision, &item.LastErrorCode,
			&item.LastVerifiedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Register(ctx context.Context, tenantID, actorMemberID string, input RegisterInput) (RemoteAgent, error) {
	input.Slug, input.DisplayName = strings.TrimSpace(input.Slug), strings.TrimSpace(input.DisplayName)
	input.CardURL, input.ExpectedCardDigest = strings.TrimSpace(input.CardURL), strings.TrimSpace(input.ExpectedCardDigest)
	input.AuthEnvKey = strings.TrimSpace(input.AuthEnvKey)
	if !remoteSlugPattern.MatchString(input.Slug) || input.DisplayName == "" || len(input.DisplayName) > 120 ||
		!strings.HasPrefix(input.CardURL, "https://") || !digestPattern.MatchString(input.ExpectedCardDigest) ||
		(input.AuthEnvKey != "" && !authEnvPattern.MatchString(input.AuthEnvKey)) {
		return RemoteAgent{}, errors.New("remote A2A Agent registration is invalid")
	}
	id, err := randomUUID()
	if err != nil {
		return RemoteAgent{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RemoteAgent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return RemoteAgent{}, err
	}
	var item RemoteAgent
	if err := tx.QueryRow(ctx, `
INSERT INTO agent.remote_agents
    (id, tenant_id, slug, display_name, card_url, expected_card_digest, auth_env_key, created_by_member_id)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, ''), $8::uuid)
RETURNING id::text, slug, display_name, card_url, expected_card_digest, COALESCE(auth_env_key, ''),
          enabled, lifecycle_state, lifecycle_revision, updated_at`,
		id, tenantID, input.Slug, input.DisplayName, input.CardURL, input.ExpectedCardDigest, input.AuthEnvKey, actorMemberID).Scan(
		&item.ID, &item.Slug, &item.DisplayName, &item.CardURL, &item.ExpectedCardDigest, &item.AuthEnvKey,
		&item.Enabled, &item.LifecycleState, &item.LifecycleRevision, &item.UpdatedAt); err != nil {
		return RemoteAgent{}, fmt.Errorf("register remote A2A Agent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'registered',
        jsonb_build_object('slug', $4::text, 'card_url', $5::text, 'expected_card_digest', $6::text))`,
		tenantID, item.ID, actorMemberID, item.Slug, item.CardURL, item.ExpectedCardDigest); err != nil {
		return RemoteAgent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RemoteAgent{}, err
	}
	return item, nil
}

func (s *Store) Verify(ctx context.Context, tenantID, actorMemberID, slug string, expectedRevision int64) (RemoteAgent, error) {
	slug = strings.TrimSpace(slug)
	var current RemoteAgent
	if err := s.pool.QueryRow(ctx, `
SELECT id::text, slug, display_name, card_url, expected_card_digest, COALESCE(auth_env_key, ''),
       lifecycle_revision, enabled
FROM agent.remote_agents WHERE tenant_id = $1::uuid AND slug = $2`, tenantID, slug).Scan(
		&current.ID, &current.Slug, &current.DisplayName, &current.CardURL, &current.ExpectedCardDigest,
		&current.AuthEnvKey, &current.LifecycleRevision, &current.Enabled); err != nil {
		return RemoteAgent{}, fmt.Errorf("load remote A2A Agent: %w", err)
	}
	if current.LifecycleRevision != expectedRevision {
		return RemoteAgent{}, errors.New("remote A2A Agent revision changed")
	}
	resolved, resolveErr := s.client.ResolveCard(ctx, current.CardURL, current.ExpectedCardDigest, current.AuthEnvKey)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RemoteAgent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return RemoteAgent{}, err
	}
	if resolveErr != nil {
		if _, err := tx.Exec(ctx, `
UPDATE agent.remote_agents
SET enabled = false, lifecycle_state = 'degraded', lifecycle_revision = lifecycle_revision + 1,
    last_error_code = 'CARD_VERIFICATION_FAILED', updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND lifecycle_revision = $3`, tenantID, current.ID, expectedRevision); err != nil {
			return RemoteAgent{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'verification_failed', jsonb_build_object('error_code', 'CARD_VERIFICATION_FAILED'))`,
			tenantID, current.ID, actorMemberID); err != nil {
			return RemoteAgent{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return RemoteAgent{}, err
		}
		return RemoteAgent{}, resolveErr
	}
	if err := tx.QueryRow(ctx, `
UPDATE agent.remote_agents
SET endpoint_url = $4, protocol_binding = 'HTTP+JSON', observed_card_digest = $5,
    agent_card = $6::jsonb, enabled = false, lifecycle_state = 'verified',
    lifecycle_revision = lifecycle_revision + 1, last_error_code = NULL,
    last_verified_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND lifecycle_revision = $3
RETURNING id::text, slug, display_name, card_url, endpoint_url, expected_card_digest,
          observed_card_digest, agent_card, COALESCE(auth_env_key, ''), enabled,
          lifecycle_state, lifecycle_revision, last_error_code, last_verified_at, updated_at`,
		tenantID, current.ID, expectedRevision, resolved.EndpointURL, resolved.Digest, string(resolved.Raw)).Scan(
		&current.ID, &current.Slug, &current.DisplayName, &current.CardURL, &current.EndpointURL,
		&current.ExpectedCardDigest, &current.ObservedCardDigest, &current.AgentCard, &current.AuthEnvKey,
		&current.Enabled, &current.LifecycleState, &current.LifecycleRevision, &current.LastErrorCode,
		&current.LastVerifiedAt, &current.UpdatedAt); err != nil {
		return RemoteAgent{}, fmt.Errorf("verify remote A2A Agent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'verified',
        jsonb_build_object('card_digest', $4::text, 'endpoint_url', $5::text, 'protocol_version', '1.0'))`,
		tenantID, current.ID, actorMemberID, resolved.Digest, resolved.EndpointURL); err != nil {
		return RemoteAgent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RemoteAgent{}, err
	}
	return current, nil
}

func (s *Store) SetEnabled(ctx context.Context, tenantID, actorMemberID, slug string, enabled bool, expectedRevision int64) (RemoteAgent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RemoteAgent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return RemoteAgent{}, err
	}
	state := "disabled"
	if enabled {
		state = "verified"
	}
	var item RemoteAgent
	if err := tx.QueryRow(ctx, `
UPDATE agent.remote_agents
SET enabled = $4, lifecycle_state = $5, lifecycle_revision = lifecycle_revision + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND slug = $2 AND lifecycle_revision = $3
  AND ($4 = false OR (lifecycle_state = 'verified' AND observed_card_digest = expected_card_digest))
RETURNING id::text, slug, display_name, card_url, COALESCE(endpoint_url, ''), expected_card_digest,
          COALESCE(observed_card_digest, ''), COALESCE(agent_card, '{}'::jsonb), COALESCE(auth_env_key, ''),
          enabled, lifecycle_state, lifecycle_revision, COALESCE(last_error_code, ''), last_verified_at, updated_at`,
		tenantID, strings.TrimSpace(slug), expectedRevision, enabled, state).Scan(
		&item.ID, &item.Slug, &item.DisplayName, &item.CardURL, &item.EndpointURL, &item.ExpectedCardDigest,
		&item.ObservedCardDigest, &item.AgentCard, &item.AuthEnvKey, &item.Enabled, &item.LifecycleState,
		&item.LifecycleRevision, &item.LastErrorCode, &item.LastVerifiedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RemoteAgent{}, errors.New("remote A2A Agent is unverified, missing, or changed")
		}
		return RemoteAgent{}, err
	}
	eventType := "disabled"
	if enabled {
		eventType = "enabled"
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, jsonb_build_object('lifecycle_revision', $5::bigint))`,
		tenantID, item.ID, actorMemberID, eventType, item.LifecycleRevision); err != nil {
		return RemoteAgent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RemoteAgent{}, err
	}
	return item, nil
}

func (s *Store) Delegate(ctx context.Context, execution agent.ExecutionContext, targetSlug, task, idempotencyKey string) (Job, error) {
	targetSlug, task, idempotencyKey = strings.TrimSpace(targetSlug), strings.TrimSpace(task), strings.TrimSpace(idempotencyKey)
	if execution.ExecutionPlane != "passive" || execution.RunID == "" || execution.TenantID == "" || execution.MemberID == "" ||
		!remoteSlugPattern.MatchString(targetSlug) || task == "" || utf8.RuneCountInString(task) > 4000 || idempotencyKey == "" || utf8.RuneCountInString(idempotencyKey) > 256 {
		return Job{}, errors.New("remote A2A delegation input is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if existing, ok, err := readExistingJob(ctx, tx, execution.TenantID, execution.RunID, idempotencyKey); err != nil {
		return Job{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return Job{}, err
		}
		return existing, nil
	}
	var remote RemoteAgent
	if err := tx.QueryRow(ctx, `
SELECT remote.id::text, remote.slug, remote.endpoint_url, COALESCE(remote.auth_env_key, '')
FROM agent.remote_agents AS remote
JOIN agent.runs AS run ON run.tenant_id = remote.tenant_id
WHERE remote.tenant_id = $1::uuid AND remote.slug = $2 AND remote.enabled
  AND remote.lifecycle_state = 'verified' AND remote.observed_card_digest = remote.expected_card_digest
  AND run.id = $3::uuid AND run.principal_member_id = $4::uuid
  AND run.state = 'running' AND run.execution_plane = 'passive'
FOR UPDATE OF run`, execution.TenantID, targetSlug, execution.RunID, execution.MemberID).Scan(
		&remote.ID, &remote.Slug, &remote.EndpointURL, &remote.AuthEnvKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, errors.New("remote A2A Agent or delegating Run is unavailable")
		}
		return Job{}, err
	}
	jobID, err := randomUUID()
	if err != nil {
		return Job{}, err
	}
	messageID, err := randomUUID()
	if err != nil {
		return Job{}, err
	}
	job := Job{ID: jobID, ParentRunID: execution.RunID, RemoteAgentID: remote.ID, RemoteAgentSlug: remote.Slug, MessageID: messageID, Task: task, State: "queued"}
	if err := tx.QueryRow(ctx, `
INSERT INTO agent.remote_a2a_jobs
    (id, tenant_id, parent_run_id, requested_by_member_id, remote_agent_id, idempotency_key, message_id, task)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7::uuid, $8)
RETURNING created_at, updated_at`, job.ID, execution.TenantID, job.ParentRunID, execution.MemberID,
		job.RemoteAgentID, idempotencyKey, job.MessageID, job.Task).Scan(&job.CreatedAt, &job.UpdatedAt); err != nil {
		return Job{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, job_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'queued',
        jsonb_build_object('parent_run_id', $5::text, 'message_id', $6::text))`,
		execution.TenantID, remote.ID, job.ID, execution.MemberID, execution.RunID, job.MessageID); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, err
	}

	result, sendErr := s.client.SendMessage(ctx, remote.EndpointURL, job.MessageID, task, idempotencyKey, remote.AuthEnvKey)
	if sendErr != nil {
		job.State, job.LastErrorCode = "unknown", "A2A_DELIVERY_UNCERTAIN"
		if err := s.finishJob(ctx, execution.TenantID, execution.MemberID, job, nil); err != nil {
			return Job{}, errors.Join(sendErr, err)
		}
		return job, sendErr
	}
	job.State, job.RemoteTaskID, job.Response, job.ResponseChecksum = result.State, result.RemoteTaskID, result.Raw, result.Checksum
	if err := s.finishJob(ctx, execution.TenantID, execution.MemberID, job, &result); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) finishJob(ctx context.Context, tenantID, memberID string, job Job, result *SendResult) error {
	terminal := job.State == "completed" || job.State == "failed" || job.State == "rejected"
	completedAt := any(nil)
	if terminal {
		completedAt = time.Now().UTC()
	}
	response := any(nil)
	if result != nil {
		response = string(result.Raw)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
UPDATE agent.remote_a2a_jobs
SET state = $3, remote_task_id = NULLIF($4, ''), response = $5::jsonb,
    response_checksum = NULLIF($6, ''), last_error_code = NULLIF($7, ''),
    updated_at = now(), completed_at = $8
WHERE tenant_id = $1::uuid AND id = $2::uuid AND state = 'queued'`,
		tenantID, job.ID, job.State, job.RemoteTaskID, response, job.ResponseChecksum, job.LastErrorCode, completedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.remote_a2a_events (tenant_id, remote_agent_id, job_id, actor_member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5,
        jsonb_build_object('remote_task_id', NULLIF($6::text, ''), 'response_checksum', NULLIF($7::text, ''), 'error_code', NULLIF($8::text, '')))`,
		tenantID, job.RemoteAgentID, job.ID, memberID, job.State, job.RemoteTaskID, job.ResponseChecksum, job.LastErrorCode); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func readExistingJob(ctx context.Context, tx pgx.Tx, tenantID, parentRunID, idempotencyKey string) (Job, bool, error) {
	var job Job
	err := tx.QueryRow(ctx, `
SELECT job.id::text, job.parent_run_id::text, job.remote_agent_id::text, remote.slug,
       job.message_id::text, job.task, job.state, COALESCE(job.remote_task_id, ''),
       COALESCE(job.response, '{}'::jsonb), COALESCE(job.response_checksum, ''),
       COALESCE(job.last_error_code, ''), job.created_at, job.updated_at
FROM agent.remote_a2a_jobs AS job
JOIN agent.remote_agents AS remote ON remote.tenant_id = job.tenant_id AND remote.id = job.remote_agent_id
WHERE job.tenant_id = $1::uuid AND job.parent_run_id = $2::uuid AND job.idempotency_key = $3`,
		tenantID, parentRunID, idempotencyKey).Scan(&job.ID, &job.ParentRunID, &job.RemoteAgentID,
		&job.RemoteAgentSlug, &job.MessageID, &job.Task, &job.State, &job.RemoteTaskID, &job.Response,
		&job.ResponseChecksum, &job.LastErrorCode, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	return job, err == nil, err
}

func requirePlatformAdmin(ctx context.Context, tx pgx.Tx, tenantID, actorMemberID string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members AS member
    JOIN identity.tenants AS tenant ON tenant.id = member.tenant_id
    JOIN identity.member_roles AS role ON role.tenant_id = member.tenant_id AND role.member_id = member.id
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid AND member.status = 'active'
      AND tenant.status = 'active' AND role.role = 'platform_admin'
)`, tenantID, actorMemberID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errors.New("remote A2A administration requires an active platform_admin role")
	}
	return nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
