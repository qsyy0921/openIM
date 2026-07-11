package action

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden = errors.New("action approval is forbidden")
	ErrConflict  = errors.New("action approval conflicts with current intent")
	ErrExpired   = errors.New("action intent is expired")
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type IntentRequest struct{ RunID, TenantID, MemberID, ActionType, Title string }
type Intent struct{ ID, Digest string }

func (s *Store) EnsureIntent(ctx context.Context, request IntentRequest) (Intent, error) {
	request.Title = strings.TrimSpace(request.Title)
	if request.ActionType != "create_ticket" || request.Title == "" || len(request.Title) > 200 {
		return Intent{}, errors.New("unsupported or invalid action intent")
	}
	payload, err := json.Marshal(struct {
		Title string `json:"title"`
	}{request.Title})
	if err != nil {
		return Intent{}, err
	}
	digestBytes := sha256.Sum256(append([]byte(request.ActionType+"\x00"), payload...))
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	id, err := newUUID()
	if err != nil {
		return Intent{}, err
	}
	const query = `
INSERT INTO action.intents (id, run_id, tenant_id, requested_by, action_type, payload, payload_digest, expires_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::jsonb, $7, now() + interval '15 minutes')
ON CONFLICT (run_id) DO UPDATE SET run_id = EXCLUDED.run_id
RETURNING id::text, payload_digest`
	var result Intent
	if err := s.pool.QueryRow(ctx, query, id, request.RunID, request.TenantID, request.MemberID, request.ActionType, payload, digest).Scan(&result.ID, &result.Digest); err != nil {
		return Intent{}, fmt.Errorf("ensure action intent: %w", err)
	}
	if result.Digest != digest {
		return Intent{}, errors.New("existing action intent digest differs from Run candidate")
	}
	return result, nil
}

type Principal struct{ TenantID, MemberID string }
type ApprovalResult struct{ IntentID, ExecutionID, State string }

func (s *Store) Approve(ctx context.Context, principal Principal, intentID, digest string) (ApprovalResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApprovalResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const read = `SELECT tenant_id::text, requested_by::text, payload_digest, state, expires_at FROM action.intents WHERE id=$1::uuid FOR UPDATE`
	var tenantID, memberID, expectedDigest, state string
	var expires time.Time
	if err := tx.QueryRow(ctx, read, intentID).Scan(&tenantID, &memberID, &expectedDigest, &state, &expires); errors.Is(err, pgx.ErrNoRows) {
		return ApprovalResult{}, ErrForbidden
	} else if err != nil {
		return ApprovalResult{}, err
	}
	if tenantID != principal.TenantID || memberID != principal.MemberID {
		return ApprovalResult{}, ErrForbidden
	}
	if digest == "" || digest != expectedDigest {
		return ApprovalResult{}, ErrConflict
	}
	if time.Now().After(expires) && state == "pending_approval" {
		_, _ = tx.Exec(ctx, "UPDATE action.intents SET state='expired',updated_at=now() WHERE id=$1::uuid", intentID)
		_ = tx.Commit(ctx)
		return ApprovalResult{}, ErrExpired
	}
	if state != "pending_approval" {
		var executionID, executionState string
		if err := tx.QueryRow(ctx, "SELECT id::text,state FROM action.executions WHERE intent_id=$1::uuid", intentID).Scan(&executionID, &executionState); err != nil {
			return ApprovalResult{}, ErrConflict
		}
		return ApprovalResult{IntentID: intentID, ExecutionID: executionID, State: executionState}, tx.Commit(ctx)
	}
	approvalID, err := newUUID()
	if err != nil {
		return ApprovalResult{}, err
	}
	executionID, err := newUUID()
	if err != nil {
		return ApprovalResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO action.approvals (id,intent_id,tenant_id,approved_by,payload_digest,decision) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'approved')`, approvalID, intentID, tenantID, memberID, digest); err != nil {
		return ApprovalResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO action.executions (id,intent_id,tenant_id,idempotency_key) VALUES ($1::uuid,$2::uuid,$3::uuid,$4)`, executionID, intentID, tenantID, "intent:"+intentID); err != nil {
		return ApprovalResult{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE action.intents SET state='approved',updated_at=now() WHERE id=$1::uuid", intentID); err != nil {
		return ApprovalResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit.action_events (tenant_id,intent_id,execution_id,actor_member_id,event_type,evidence) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'action.approved',$5::jsonb)`, tenantID, intentID, executionID, memberID, []byte(`{"digest":"`+digest+`"}`)); err != nil {
		return ApprovalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalResult{}, err
	}
	return ApprovalResult{IntentID: intentID, ExecutionID: executionID, State: "queued"}, nil
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
