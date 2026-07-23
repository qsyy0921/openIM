package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

var administrativeRoles = []string{"platform_admin", "agent_admin", "knowledge_admin"}

func (s *PostgresStore) ListMemberRoles(ctx context.Context, tenantID, memberID string) ([]string, error) {
	tenantID, memberID = strings.TrimSpace(tenantID), strings.TrimSpace(memberID)
	if tenantID == "" || memberID == "" {
		return nil, errors.New("member role query is invalid")
	}
	rows, err := s.pool.Query(ctx, `
SELECT role.role
FROM identity.member_roles AS role
JOIN identity.members AS member
  ON member.tenant_id = role.tenant_id AND member.id = role.member_id
WHERE role.tenant_id = $1::uuid AND role.member_id = $2::uuid
  AND member.status = 'active'
ORDER BY role.role`, tenantID, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member roles: %w", err)
	}
	defer rows.Close()
	roles := make([]string, 0, len(administrativeRoles))
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scan member role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate member roles: %w", err)
	}
	return roles, nil
}

func (s *PostgresStore) SetMemberRole(ctx context.Context, tenantID, actorMemberID, subjectMemberID, role string, enabled bool) error {
	tenantID, actorMemberID = strings.TrimSpace(tenantID), strings.TrimSpace(actorMemberID)
	subjectMemberID, role = strings.TrimSpace(subjectMemberID), strings.TrimSpace(role)
	if tenantID == "" || actorMemberID == "" || subjectMemberID == "" || !slices.Contains(administrativeRoles, role) {
		return errors.New("member role update input is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin member role update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actorAdmin, subjectActive bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.member_roles AS role
    JOIN identity.members AS member
      ON member.tenant_id = role.tenant_id AND member.id = role.member_id
    WHERE role.tenant_id = $1::uuid AND role.member_id = $2::uuid
      AND role.role = 'platform_admin' AND member.status = 'active'
), EXISTS (
    SELECT 1 FROM identity.members
    WHERE tenant_id = $1::uuid AND id = $3::uuid AND status = 'active'
)`, tenantID, actorMemberID, subjectMemberID).Scan(&actorAdmin, &subjectActive); err != nil {
		return fmt.Errorf("authorize member role update: %w", err)
	}
	if !actorAdmin {
		return errors.New("member role update requires an active platform_admin role")
	}
	if !subjectActive {
		return errors.New("member role subject is not active")
	}
	eventType := "role_granted"
	if enabled {
		result, err := tx.Exec(ctx, `
INSERT INTO identity.member_roles (tenant_id, member_id, role, granted_by_member_id)
VALUES ($1::uuid, $2::uuid, $3, $4::uuid)
ON CONFLICT (tenant_id, member_id, role) DO NOTHING`, tenantID, subjectMemberID, role, actorMemberID)
		if err != nil {
			return fmt.Errorf("grant member role: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("member role already exists")
		}
	} else {
		eventType = "role_revoked"
		if role == "platform_admin" {
			var count int
			if err := tx.QueryRow(ctx, `
SELECT count(*)::integer
FROM (
    SELECT member_id FROM identity.member_roles
    WHERE tenant_id = $1::uuid AND role = 'platform_admin'
    FOR UPDATE
) AS administrators`, tenantID).Scan(&count); err != nil {
				return fmt.Errorf("count platform administrators: %w", err)
			}
			if count <= 1 {
				return errors.New("the final platform_admin role cannot be revoked")
			}
		}
		result, err := tx.Exec(ctx, `
DELETE FROM identity.member_roles
WHERE tenant_id = $1::uuid AND member_id = $2::uuid AND role = $3`, tenantID, subjectMemberID, role)
		if err != nil {
			return fmt.Errorf("revoke member role: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("member role does not exist")
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.identity_admin_events (
    tenant_id, actor_member_id, subject_member_id, event_type, evidence
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, jsonb_build_object('role', $5::text))`,
		tenantID, actorMemberID, subjectMemberID, eventType, role); err != nil {
		return fmt.Errorf("audit member role update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit member role update: %w", err)
	}
	return nil
}
