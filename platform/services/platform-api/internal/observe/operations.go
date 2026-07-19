package observe

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type QueueSnapshot struct {
	States             map[string]int64 `json:"states"`
	OldestReadySeconds float64          `json:"oldest_ready_seconds"`
}

type OperationalSnapshot struct {
	GeneratedAt       time.Time     `json:"generated_at"`
	AgentRuns         QueueSnapshot `json:"agent_runs"`
	Deliveries        QueueSnapshot `json:"deliveries"`
	ProactiveEvents   QueueSnapshot `json:"proactive_events"`
	MemoryExtractions QueueSnapshot `json:"memory_extractions"`
	Delegations       QueueSnapshot `json:"delegations"`
	ToolApprovals     QueueSnapshot `json:"tool_approvals"`
	MCPHealth         QueueSnapshot `json:"mcp_health"`
}

func (s *Store) ReadOperationalSnapshot(ctx context.Context, tenantID string) (OperationalSnapshot, error) {
	if tenantID == "" {
		return OperationalSnapshot{}, errors.New("operational snapshot tenant is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return OperationalSnapshot{}, fmt.Errorf("begin operational snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := []struct {
		target *QueueSnapshot
		query  string
	}{
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM agent.runs WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM agent.deliveries WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM proactive.source_events WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM memory.extraction_jobs WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM agent.delegations WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT state, count(*)::bigint FROM agent.tool_approvals WHERE tenant_id = $1::uuid GROUP BY state`},
		{target: new(QueueSnapshot), query: `SELECT health.state, count(*)::bigint FROM capability.mcp_health AS health WHERE health.tenant_id = $1::uuid GROUP BY health.state`},
	}
	for _, item := range queries {
		states, err := readStateCounts(ctx, tx, item.query, tenantID)
		if err != nil {
			return OperationalSnapshot{}, err
		}
		item.target.States = states
	}
	ageQueries := []struct {
		target *QueueSnapshot
		query  string
	}{
		{queries[0].target, `SELECT COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM agent.runs WHERE tenant_id = $1::uuid AND state IN ('queued', 'reply_pending') AND available_at <= now()`},
		{queries[1].target, `SELECT COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM agent.deliveries WHERE tenant_id = $1::uuid AND state = 'pending' AND available_at <= now()`},
		{queries[2].target, `SELECT COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM proactive.source_events WHERE tenant_id = $1::uuid AND state IN ('pending', 'ranked') AND available_at <= now()`},
		{queries[3].target, `SELECT COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM memory.extraction_jobs WHERE tenant_id = $1::uuid AND state IN ('pending', 'projection_pending') AND available_at <= now()`},
		{queries[4].target, `SELECT COALESCE(extract(epoch FROM now() - min(created_at)), 0) FROM agent.delegations WHERE tenant_id = $1::uuid AND state = 'queued'`},
		{queries[5].target, `SELECT COALESCE(extract(epoch FROM now() - min(created_at)), 0) FROM agent.tool_approvals WHERE tenant_id = $1::uuid AND state = 'requested'`},
		{queries[6].target, `SELECT 0::double precision FROM identity.tenants WHERE id = $1::uuid`},
	}
	for _, item := range ageQueries {
		if err := tx.QueryRow(ctx, item.query, tenantID).Scan(&item.target.OldestReadySeconds); err != nil {
			return OperationalSnapshot{}, fmt.Errorf("read operational queue age: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationalSnapshot{}, fmt.Errorf("commit operational snapshot: %w", err)
	}
	return OperationalSnapshot{
		GeneratedAt: time.Now().UTC(), AgentRuns: *queries[0].target, Deliveries: *queries[1].target,
		ProactiveEvents: *queries[2].target, MemoryExtractions: *queries[3].target,
		Delegations: *queries[4].target, ToolApprovals: *queries[5].target, MCPHealth: *queries[6].target,
	}, nil
}

func readStateCounts(ctx context.Context, tx pgx.Tx, query, tenantID string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("read operational state counts: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan operational state count: %w", err)
		}
		result[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational state counts: %w", err)
	}
	return result, nil
}
