package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) SaveRoute(ctx context.Context, run Run, route RouteResult) error {
	candidates, err := json.Marshal(route.Candidates)
	if err != nil {
		return fmt.Errorf("encode intent route candidates: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save intent route: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const query = `
UPDATE agent.runs
SET state = 'queued', route_status = $3, route_operation_id = NULLIF($4, ''),
    route_clarification = NULLIF($5, ''), route_provider_response_id = $6,
    route_router_version = $7, route_candidates = $8::jsonb,
    lease_token = NULL, lease_until = NULL, available_at = now(), last_error = NULL, updated_at = now()
WHERE id = $1::uuid AND state = 'running' AND lease_token = $2 AND lease_until >= now()`
	result, err := tx.Exec(ctx, query, run.ID, run.LeaseToken, route.Status, route.OperationID,
		route.Clarification, route.ProviderResponseID, route.RouterVersion, string(candidates))
	if err != nil {
		return fmt.Errorf("save intent route: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("save intent route: execution lease is not held")
	}
	if err := insertRunEvent(ctx, tx, run.TenantID, run.ID, run.TraceID, "route_persisted",
		`jsonb_build_object('status', $5::text, 'operation_id', NULLIF($6::text, ''), 'router_version', $7::text, 'candidate_count', $8::integer)`,
		route.Status, route.OperationID, route.RouterVersion, len(route.Candidates)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit intent route: %w", err)
	}
	return nil
}
