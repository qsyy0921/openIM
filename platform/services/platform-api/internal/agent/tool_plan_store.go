package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) SaveToolPlan(ctx context.Context, run Run, plan ToolPlan) error {
	arguments, err := json.Marshal(plan.Arguments)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
UPDATE agent.runs
SET state = 'queued', tool_arguments = $3::jsonb, tool_plan_provider_response_id = $4,
    lease_token = NULL, lease_until = NULL, available_at = now(), last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2 AND lease_until >= now()
  AND tool_arguments IS NULL`, run.ID, run.LeaseToken, string(arguments), plan.ProviderResponseID)
	if err != nil {
		return fmt.Errorf("save Agent tool plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("save Agent tool plan: execution lease is not held or plan already exists")
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "tool_plan_persisted",
		`jsonb_build_object('operation_id', $5::text, 'provider_response_id', $6::text)`,
		run.RouteOperationID, plan.ProviderResponseID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SaveToolResult(ctx context.Context, run Run, resultValue any) error {
	data, err := json.Marshal(resultValue)
	if err != nil {
		return fmt.Errorf("encode Agent tool result: %w", err)
	}
	if len(data) > 1<<20 {
		return errors.New("Agent tool result exceeds 1 MiB")
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return errors.New("Agent tool result must be a JSON object")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	updated, err := tx.Exec(ctx, `
UPDATE agent.runs
SET state = 'queued', tool_result = $3::jsonb,
    lease_token = NULL, lease_until = NULL, available_at = now(), last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2 AND lease_until >= now()
  AND tool_arguments IS NOT NULL AND tool_result IS NULL`, run.ID, run.LeaseToken, string(data))
	if err != nil {
		return fmt.Errorf("save Agent tool result: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return errors.New("save Agent tool result: execution lease is not held or result already exists")
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "tool_result_persisted",
		`jsonb_build_object('operation_id', $5::text, 'result_bytes', $6::integer)`,
		run.RouteOperationID, len(data)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
