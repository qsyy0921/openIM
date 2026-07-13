package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Store) LoadCatalogVersion(ctx context.Context, run Run) (CatalogVersion, error) {
	const query = `
SELECT v.agent_id::text, v.id::text, v.version_number, v.spec_schema_version,
       v.spec, v.spec_checksum
FROM agent.versions v
WHERE v.tenant_id = $1::uuid
  AND v.agent_id = $2::uuid
  AND v.id = $3::uuid`
	var version CatalogVersion
	var raw []byte
	if err := s.pool.QueryRow(ctx, query, run.TenantID, run.AgentID, run.AgentVersionID).Scan(
		&version.AgentID, &version.VersionID, &version.VersionNumber, &version.SchemaVersion, &raw, &version.Checksum,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CatalogVersion{}, fmt.Errorf("%w: pinned Agent version is missing", ErrInvalidCatalog)
		}
		return CatalogVersion{}, fmt.Errorf("load pinned Agent version: %w", err)
	}
	if version.Checksum != run.AgentSpecChecksum {
		return CatalogVersion{}, fmt.Errorf("%w: Run checksum does not match pinned version", ErrInvalidCatalog)
	}
	spec, err := ParseAgentSpec(version.SchemaVersion, raw, version.Checksum)
	if err != nil {
		return CatalogVersion{}, err
	}
	version.Spec = spec
	return version, nil
}

func (s *Store) ListAgents(ctx context.Context, tenantID string) ([]AgentSummary, error) {
	if tenantID == "" {
		return nil, errors.New("tenant ID is required")
	}
	const query = `
SELECT d.id::text, d.slug, d.display_name, d.description,
       tr.trigger_value, v.version_number, v.id::text, v.spec_checksum
FROM agent.definitions d
JOIN agent.deployments dep
  ON dep.tenant_id = d.tenant_id AND dep.agent_id = d.id AND dep.slot = 'production'
JOIN agent.versions v
  ON v.tenant_id = dep.tenant_id AND v.agent_id = dep.agent_id AND v.id = dep.active_version_id
JOIN agent.triggers tr
  ON tr.tenant_id = d.tenant_id AND tr.agent_id = d.id
 AND tr.trigger_type = 'mention_alias' AND tr.enabled
WHERE d.tenant_id = $1::uuid AND d.status = 'active'
ORDER BY d.slug`
	rows, err := s.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list Agent catalog: %w", err)
	}
	defer rows.Close()
	result := make([]AgentSummary, 0)
	for rows.Next() {
		var item AgentSummary
		if err := rows.Scan(&item.ID, &item.Slug, &item.DisplayName, &item.Description, &item.TriggerAlias,
			&item.VersionNumber, &item.VersionID, &item.SpecChecksum); err != nil {
			return nil, fmt.Errorf("scan Agent catalog: %w", err)
		}
		item.BotUserID = BotUserID(tenantID)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent catalog: %w", err)
	}
	return result, nil
}

func (s *Store) ActivateVersion(ctx context.Context, tenantID, agentID, versionID, actorMemberID string, expectedRevision int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Agent activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateVersionTx(ctx, tx, tenantID, agentID, versionID, actorMemberID, expectedRevision); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent activation: %w", err)
	}
	return nil
}

func activateVersionTx(ctx context.Context, tx pgx.Tx, tenantID, agentID, versionID, actorMemberID string, expectedRevision int64) error {
	const update = `
WITH current AS (
    SELECT dep.id, dep.active_version_id
    FROM agent.deployments dep
    WHERE dep.tenant_id = $1::uuid AND dep.agent_id = $2::uuid
      AND dep.slot = 'production' AND dep.revision = $5
    FOR UPDATE
), target AS (
    SELECT v.id
    FROM agent.versions v
    WHERE v.tenant_id = $1::uuid AND v.agent_id = $2::uuid AND v.id = $3::uuid
), updated AS (
    UPDATE agent.deployments dep
    SET active_version_id = target.id,
        activated_by_member_id = NULLIF($4, '')::uuid,
        activated_at = now(), revision = dep.revision + 1, updated_at = now()
    FROM current, target
    WHERE dep.id = current.id
    RETURNING dep.id
)
SELECT updated.id::text, current.active_version_id::text, target.id::text
FROM updated, current, target`
	var deploymentID, oldVersionID, newVersionID string
	if err := tx.QueryRow(ctx, update, tenantID, agentID, versionID, actorMemberID, expectedRevision).Scan(
		&deploymentID, &oldVersionID, &newVersionID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: activation target or expected revision is invalid", ErrInvalidCatalog)
		}
		return fmt.Errorf("activate Agent version: %w", err)
	}
	const audit = `
INSERT INTO audit.agent_catalog_events (
    tenant_id, agent_id, deployment_id, actor_member_id, event_type, old_version_id, new_version_id
) VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, 'deployment_activated', $5::uuid, $6::uuid)`
	if _, err := tx.Exec(ctx, audit, tenantID, agentID, deploymentID, actorMemberID, oldVersionID, newVersionID); err != nil {
		return fmt.Errorf("audit Agent activation: %w", err)
	}
	return nil
}
