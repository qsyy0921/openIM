package runtimecontrol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConflict = errors.New("runtime control revision conflict")
	components  = []string{"agent_execution", "agent_delivery", "proactive_dispatch"}
)

type Control struct {
	Component string    `json:"component"`
	Paused    bool      `json:"paused"`
	Reason    string    `json:"reason"`
	Revision  int64     `json:"revision"`
	UpdatedBy string    `json:"updated_by_member_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Set(ctx context.Context, tenantID, actorMemberID, component string, paused bool, reason string, expectedRevision int64) (Control, error) {
	tenantID, actorMemberID = strings.TrimSpace(tenantID), strings.TrimSpace(actorMemberID)
	component, reason = strings.TrimSpace(component), strings.TrimSpace(reason)
	if tenantID == "" || actorMemberID == "" || !slices.Contains(components, component) ||
		reason == "" || len(reason) > 500 || expectedRevision < 0 {
		return Control{}, errors.New("runtime control update is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Control{}, fmt.Errorf("begin runtime control update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var allowed bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM identity.member_roles AS role
    JOIN identity.members AS member
      ON member.tenant_id = role.tenant_id AND member.id = role.member_id
    JOIN identity.tenants AS tenant ON tenant.id = member.tenant_id
    WHERE role.tenant_id = $1::uuid AND role.member_id = $2::uuid
      AND role.role = 'platform_admin'
      AND member.status = 'active' AND tenant.status = 'active'
)`, tenantID, actorMemberID).Scan(&allowed); err != nil {
		return Control{}, fmt.Errorf("authorize runtime control update: %w", err)
	}
	if !allowed {
		return Control{}, errors.New("runtime control update requires an active platform_admin role")
	}
	var control Control
	err = tx.QueryRow(ctx, `
INSERT INTO platform_meta.runtime_controls (
    tenant_id, component, paused, reason, revision, updated_by_member_id
) SELECT $1::uuid, $2, $3, $4, 1, $5::uuid
  WHERE $6 = 0
ON CONFLICT (tenant_id, component) DO UPDATE
SET paused = EXCLUDED.paused,
    reason = EXCLUDED.reason,
    revision = platform_meta.runtime_controls.revision + 1,
    updated_by_member_id = EXCLUDED.updated_by_member_id,
    updated_at = now()
WHERE platform_meta.runtime_controls.revision = $6
RETURNING component, paused, reason, revision, updated_by_member_id::text, updated_at`,
		tenantID, component, paused, reason, actorMemberID, expectedRevision).Scan(
		&control.Component, &control.Paused, &control.Reason, &control.Revision,
		&control.UpdatedBy, &control.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Control{}, ErrConflict
	}
	if err != nil {
		return Control{}, fmt.Errorf("persist runtime control: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.runtime_control_events (
    tenant_id, actor_member_id, component, paused, revision, reason
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
		tenantID, actorMemberID, component, paused, control.Revision, reason); err != nil {
		return Control{}, fmt.Errorf("audit runtime control: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Control{}, fmt.Errorf("commit runtime control update: %w", err)
	}
	return control, nil
}

func (s *Store) List(ctx context.Context, tenantID string) ([]Control, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("runtime control tenant is required")
	}
	rows, err := s.pool.Query(ctx, `
SELECT component, paused, reason, revision, updated_by_member_id::text, updated_at
FROM platform_meta.runtime_controls
WHERE tenant_id = $1::uuid
ORDER BY component`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list runtime controls: %w", err)
	}
	defer rows.Close()
	result := make([]Control, 0, len(components))
	for rows.Next() {
		var control Control
		if err := rows.Scan(&control.Component, &control.Paused, &control.Reason, &control.Revision, &control.UpdatedBy, &control.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan runtime control: %w", err)
		}
		result = append(result, control)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime controls: %w", err)
	}
	return result, nil
}
