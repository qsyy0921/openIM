package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Execution struct {
	ID, IntentID, RunID, TenantID, MemberID, IdempotencyKey, LeaseToken string
	Title, PriorState                                                   string
	Attempts                                                            int
}

type Executor struct {
	pool        *pgxpool.Pool
	poll, lease time.Duration
	maxAttempts int
}

func NewExecutor(pool *pgxpool.Pool, poll, lease time.Duration, maxAttempts int) *Executor {
	return &Executor{pool: pool, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

func (e *Executor) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()
	for {
		if err := e.runOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (e *Executor) runOnce(ctx context.Context) error {
	if _, err := e.pool.Exec(ctx, `UPDATE action.executions SET state='unknown',lease_token=NULL,lease_until=NULL,last_error='execution lease expired',updated_at=now() WHERE state='running' AND lease_until<now()`); err != nil {
		return err
	}
	execution, err := e.claim(ctx)
	if err != nil || execution == nil {
		return err
	}
	if execution.PriorState == "unknown" {
		return e.reconcile(ctx, *execution)
	}
	return e.execute(ctx, *execution)
}
func (e *Executor) claim(ctx context.Context) (*Execution, error) {
	token, err := newUUID()
	if err != nil {
		return nil, err
	}
	seconds := int64((e.lease + time.Second - 1) / time.Second)
	const query = `WITH candidate AS (
 SELECT e.id,e.state AS prior_state FROM action.executions e
 WHERE e.state IN ('queued','unknown') AND e.available_at<=now() AND e.attempts<$1
 ORDER BY e.available_at,e.created_at FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE action.executions e SET state='running',attempts=attempts+1,lease_token=$2,lease_until=now()+make_interval(secs=>$3),updated_at=now()
FROM candidate c,action.intents i WHERE e.id=c.id AND i.id=e.intent_id
RETURNING e.id::text,e.intent_id::text,i.run_id::text,e.tenant_id::text,i.requested_by::text,e.idempotency_key,e.lease_token,e.attempts,c.prior_state,i.payload`
	var x Execution
	var payload []byte
	err = e.pool.QueryRow(ctx, query, e.maxAttempts, token, seconds).Scan(&x.ID, &x.IntentID, &x.RunID, &x.TenantID, &x.MemberID, &x.IdempotencyKey, &x.LeaseToken, &x.Attempts, &x.PriorState, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim action execution: %w", err)
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, e.fail(ctx, x, err)
	}
	x.Title = body.Title
	return &x, nil
}
func (e *Executor) execute(ctx context.Context, x Execution) error {
	ticketID, err := newUUID()
	if err != nil {
		return e.fail(ctx, x, err)
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return e.fail(ctx, x, err)
	}
	var persistedID string
	if _, err = tx.Exec(ctx, `INSERT INTO collaboration.tickets(id,tenant_id,title,created_by,idempotency_key) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5) ON CONFLICT(idempotency_key) DO NOTHING`, ticketID, x.TenantID, x.Title, x.MemberID, x.IdempotencyKey); err != nil {
		_ = tx.Rollback(ctx)
		return e.fail(ctx, x, err)
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM collaboration.tickets WHERE idempotency_key=$1`, x.IdempotencyKey).Scan(&persistedID); err != nil {
		_ = tx.Rollback(ctx)
		return e.fail(ctx, x, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return e.markUnknown(ctx, x, "ticket commit outcome is unknown")
	}
	return e.verifyAndComplete(ctx, x, persistedID)
}
func (e *Executor) reconcile(ctx context.Context, x Execution) error {
	var ticketID string
	err := e.pool.QueryRow(ctx, `SELECT id::text FROM collaboration.tickets WHERE idempotency_key=$1`, x.IdempotencyKey).Scan(&ticketID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = e.pool.Exec(ctx, `UPDATE action.executions SET state='queued',lease_token=NULL,lease_until=NULL,available_at=now(),last_error='unknown outcome reconciled absent; safe retry',updated_at=now() WHERE id=$1::uuid AND lease_token=$2`, x.ID, x.LeaseToken)
		return err
	}
	if err != nil {
		return e.markUnknown(ctx, x, "reconciliation read failed")
	}
	return e.verifyAndComplete(ctx, x, ticketID)
}
func (e *Executor) verifyAndComplete(ctx context.Context, x Execution, ticketID string) error {
	var tenant, member, title, status, idem string
	if err := e.pool.QueryRow(ctx, `SELECT tenant_id::text,created_by::text,title,status,idempotency_key FROM collaboration.tickets WHERE id=$1::uuid`, ticketID).Scan(&tenant, &member, &title, &status, &idem); err != nil {
		return e.markUnknown(ctx, x, "ticket read-back failed")
	}
	if tenant != x.TenantID || member != x.MemberID || title != x.Title || status != "open" || idem != x.IdempotencyKey {
		return e.fail(ctx, x, errors.New("ticket read-back mismatch"))
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	receipt, _ := json.Marshal(map[string]string{"ticket_id": ticketID, "status": status})
	result, err := tx.Exec(ctx, `UPDATE action.executions SET state='succeeded',ticket_id=$3::uuid,receipt=$4::jsonb,lease_token=NULL,lease_until=NULL,last_error=NULL,completed_at=now(),updated_at=now() WHERE id=$1::uuid AND lease_token=$2`, x.ID, x.LeaseToken, ticketID, receipt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("complete execution: lease not held")
	}
	if _, err := tx.Exec(ctx, `UPDATE action.intents SET state='succeeded',updated_at=now() WHERE id=$1::uuid`, x.IntentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent.runs SET state='succeeded',completed_at=now(),updated_at=now() WHERE id=$1::uuid AND state='waiting_approval'`, x.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit.action_events(tenant_id,intent_id,execution_id,event_type,evidence) VALUES($1::uuid,$2::uuid,$3::uuid,'action.succeeded',$4::jsonb)`, x.TenantID, x.IntentID, x.ID, receipt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	slog.Info("action execution succeeded", "execution_id", x.ID, "intent_id", x.IntentID, "ticket_id", ticketID)
	return nil
}
func (e *Executor) markUnknown(ctx context.Context, x Execution, message string) error {
	_, err := e.pool.Exec(ctx, `UPDATE action.executions SET state='unknown',lease_token=NULL,lease_until=NULL,last_error=$3,available_at=now(),updated_at=now() WHERE id=$1::uuid AND lease_token=$2`, x.ID, x.LeaseToken, message)
	return err
}
func (e *Executor) fail(ctx context.Context, x Execution, failure error) error {
	terminal := x.Attempts >= e.maxAttempts
	state := "queued"
	if terminal {
		state = "failed"
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE action.executions SET state=$3,lease_token=NULL,lease_until=NULL,last_error=$4,available_at=now()+interval '1 second',completed_at=CASE WHEN $3='failed' THEN now() ELSE NULL END,updated_at=now() WHERE id=$1::uuid AND lease_token=$2`, x.ID, x.LeaseToken, state, failure.Error())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("fail execution: lease not held")
	}
	if terminal {
		if _, err := tx.Exec(ctx, `UPDATE action.intents SET state='failed',updated_at=now() WHERE id=$1::uuid`, x.IntentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent.runs SET state='failed',completed_at=now(),last_error=$2,updated_at=now() WHERE id=$1::uuid`, x.RunID, failure.Error()); err != nil {
			return err
		}
		evidence, _ := json.Marshal(map[string]string{"error": failure.Error()})
		if _, err := tx.Exec(ctx, `INSERT INTO audit.action_events(tenant_id,intent_id,execution_id,event_type,evidence) VALUES($1::uuid,$2::uuid,$3::uuid,'action.failed',$4::jsonb)`, x.TenantID, x.IntentID, x.ID, evidence); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	slog.Warn("action execution attempt failed", "execution_id", x.ID, "attempt", x.Attempts, "state", state, "error", failure)
	return nil
}
