package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

func (s *Store) LoadCatalogVersion(ctx context.Context, run Run) (CatalogVersion, error) {
	const query = `
SELECT v.agent_id::text, v.id::text, v.version_number, v.spec_schema_version,
       v.spec, v.spec_checksum, v.capability_snapshot_id
FROM agent.versions v
WHERE v.tenant_id = $1::uuid
  AND v.agent_id = $2::uuid
  AND v.id = $3::uuid`
	var version CatalogVersion
	version.TenantID = run.TenantID
	var raw []byte
	if err := s.pool.QueryRow(ctx, query, run.TenantID, run.AgentID, run.AgentVersionID).Scan(
		&version.AgentID, &version.VersionID, &version.VersionNumber, &version.SchemaVersion, &raw, &version.Checksum,
		&version.CapabilitySnapshotID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CatalogVersion{}, fmt.Errorf("%w: pinned Agent version is missing", ErrInvalidCatalog)
		}
		return CatalogVersion{}, fmt.Errorf("load pinned Agent version: %w", err)
	}
	if version.Checksum != run.AgentSpecChecksum {
		return CatalogVersion{}, fmt.Errorf("%w: Run checksum does not match pinned version", ErrInvalidCatalog)
	}
	if version.CapabilitySnapshotID != run.CapabilitySnapshotID {
		return CatalogVersion{}, fmt.Errorf("%w: Run capability snapshot does not match pinned version", ErrInvalidCatalog)
	}
	spec, err := ParseAgentSpec(version.SchemaVersion, raw, version.Checksum)
	if err != nil {
		return CatalogVersion{}, err
	}
	version.Spec = spec
	const skillsQuery = `
SELECT skill.id::text, skill.tenant_id::text, skill.skill_id, skill.version,
       skill.name, skill.summary, skill.instructions, skill.tool_operations,
       skill.audience, skill.content_digest
FROM agent.version_skills AS link
JOIN capability.skills AS skill
  ON skill.tenant_id = link.tenant_id AND skill.id = link.skill_id
WHERE link.tenant_id = $1::uuid AND link.agent_id = $2::uuid
  AND link.agent_version_id = $3::uuid
ORDER BY link.ordinal`
	rows, err := s.pool.Query(ctx, skillsQuery, run.TenantID, run.AgentID, run.AgentVersionID)
	if err != nil {
		return CatalogVersion{}, fmt.Errorf("load pinned Agent Skills: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var skill capability.Skill
		if err := rows.Scan(
			&skill.ID, &skill.TenantID, &skill.SkillID, &skill.Version,
			&skill.Name, &skill.Summary, &skill.Instructions, &skill.ToolOperations,
			&skill.Audience, &skill.ContentDigest,
		); err != nil {
			return CatalogVersion{}, fmt.Errorf("scan pinned Agent Skill: %w", err)
		}
		digest, err := skill.Digest()
		if err != nil || digest != skill.ContentDigest {
			return CatalogVersion{}, fmt.Errorf("%w: pinned Skill digest mismatch", ErrInvalidCatalog)
		}
		version.Skills = append(version.Skills, skill)
	}
	if err := rows.Err(); err != nil {
		return CatalogVersion{}, fmt.Errorf("iterate pinned Agent Skills: %w", err)
	}
	return version, nil
}

func (s *Store) ListAgents(ctx context.Context, tenantID string) ([]AgentSummary, error) {
	if tenantID == "" {
		return nil, errors.New("tenant ID is required")
	}
	const query = `
SELECT d.id::text, d.slug, d.display_name, d.description,
       tr.trigger_value, v.version_number, v.id::text, v.spec_checksum, v.capability_snapshot_id
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
			&item.VersionNumber, &item.VersionID, &item.SpecChecksum, &item.CapabilitySnapshotID); err != nil {
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
	if err := requireAgentAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return err
	}
	var schemaVersion int
	var raw []byte
	var checksum string
	if err := tx.QueryRow(ctx, `
SELECT spec_schema_version, spec, spec_checksum
FROM agent.versions
WHERE tenant_id = $1::uuid AND agent_id = $2::uuid AND id = $3::uuid`,
		tenantID, agentID, versionID,
	).Scan(&schemaVersion, &raw, &checksum); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: activation target is missing", ErrInvalidCatalog)
		}
		return fmt.Errorf("load Agent activation target: %w", err)
	}
	if _, err := ParseAgentSpec(schemaVersion, raw, checksum); err != nil {
		return err
	}
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
	if oldVersionID == newVersionID {
		return fmt.Errorf("%w: production already uses the target version", ErrInvalidCatalog)
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

func (s *Store) PublishVersion(ctx context.Context, tenantID, agentID, actorMemberID string, expectedVersion int, capabilitySnapshotID string, spec AgentSpec) (CatalogVersion, error) {
	return s.PublishVersionWithSkills(ctx, tenantID, agentID, actorMemberID, expectedVersion, capabilitySnapshotID, spec, nil)
}

func (s *Store) PublishVersionWithSkills(ctx context.Context, tenantID, agentID, actorMemberID string, expectedVersion int, capabilitySnapshotID string, spec AgentSpec, skillIDs []string) (CatalogVersion, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CatalogVersion{}, fmt.Errorf("begin Agent publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	version, err := publishVersionTx(ctx, tx, tenantID, agentID, actorMemberID, expectedVersion, capabilitySnapshotID, spec)
	if err != nil {
		return CatalogVersion{}, err
	}
	if err := linkVersionSkillsTx(ctx, tx, version, skillIDs); err != nil {
		return CatalogVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CatalogVersion{}, fmt.Errorf("commit Agent publication: %w", err)
	}
	return version, nil
}

func linkVersionSkillsTx(ctx context.Context, tx pgx.Tx, version CatalogVersion, skillIDs []string) error {
	if len(skillIDs) > 32 {
		return fmt.Errorf("%w: Agent version has too many Skills", ErrInvalidCatalog)
	}
	seen := make(map[string]struct{}, len(skillIDs))
	for ordinal, skillID := range skillIDs {
		if skillID == "" {
			return fmt.Errorf("%w: Agent Skill ID is empty", ErrInvalidCatalog)
		}
		if _, duplicate := seen[skillID]; duplicate {
			return fmt.Errorf("%w: Agent version contains duplicate Skills", ErrInvalidCatalog)
		}
		seen[skillID] = struct{}{}
		var audience string
		var operations []string
		if err := tx.QueryRow(ctx, `
SELECT audience, tool_operations
FROM capability.skills
WHERE tenant_id = $1::uuid AND id = $2::uuid`, version.TenantID, skillID).Scan(&audience, &operations); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: pinned Skill is missing", ErrInvalidCatalog)
			}
			return err
		}
		var matched int
		if err := tx.QueryRow(ctx, `
SELECT count(DISTINCT descriptor.operation_id)::integer
FROM capability.snapshot_tools AS entry
JOIN capability.tool_descriptors AS descriptor
  ON descriptor.tenant_id = entry.tenant_id AND descriptor.id = entry.tool_id
WHERE entry.tenant_id = $1::uuid AND entry.snapshot_id = $2
  AND descriptor.operation_id = ANY($3::text[]) AND descriptor.audience = $4`,
			version.TenantID, version.CapabilitySnapshotID, operations, audience).Scan(&matched); err != nil {
			return err
		}
		if matched != len(operations) {
			return fmt.Errorf("%w: Skill operation is absent from the pinned capability snapshot", ErrInvalidCatalog)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO agent.version_skills (tenant_id, agent_id, agent_version_id, skill_id, ordinal)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5)`,
			version.TenantID, version.AgentID, version.VersionID, skillID, ordinal); err != nil {
			return fmt.Errorf("link Agent version Skill: %w", err)
		}
	}
	return nil
}

func publishVersionTx(ctx context.Context, tx pgx.Tx, tenantID, agentID, actorMemberID string, expectedVersion int, capabilitySnapshotID string, spec AgentSpec) (CatalogVersion, error) {
	if tenantID == "" || agentID == "" || actorMemberID == "" || capabilitySnapshotID == "" || expectedVersion < 1 {
		return CatalogVersion{}, fmt.Errorf("%w: publication identity or expected version is invalid", ErrInvalidCatalog)
	}
	if err := validateAgentSpec(spec); err != nil {
		return CatalogVersion{}, err
	}
	if err := requireAgentAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return CatalogVersion{}, err
	}
	var status string
	if err := tx.QueryRow(ctx, `
SELECT status FROM agent.definitions
WHERE tenant_id = $1::uuid AND id = $2::uuid
FOR UPDATE`, tenantID, agentID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CatalogVersion{}, fmt.Errorf("%w: Agent definition is missing", ErrInvalidCatalog)
		}
		return CatalogVersion{}, fmt.Errorf("lock Agent definition: %w", err)
	}
	if status == "archived" {
		return CatalogVersion{}, fmt.Errorf("%w: archived Agent cannot publish versions", ErrInvalidCatalog)
	}
	var snapshotExists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM capability.snapshots
    WHERE tenant_id = $1::uuid AND id = $2
)`, tenantID, capabilitySnapshotID).Scan(&snapshotExists); err != nil {
		return CatalogVersion{}, fmt.Errorf("validate Agent capability snapshot: %w", err)
	}
	if !snapshotExists {
		return CatalogVersion{}, fmt.Errorf("%w: capability snapshot is missing", ErrInvalidCatalog)
	}
	var nextVersion int
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(version_number), 0) + 1
FROM agent.versions
WHERE tenant_id = $1::uuid AND agent_id = $2::uuid`, tenantID, agentID).Scan(&nextVersion); err != nil {
		return CatalogVersion{}, fmt.Errorf("resolve next Agent version: %w", err)
	}
	if nextVersion != expectedVersion {
		return CatalogVersion{}, fmt.Errorf("%w: expected version %d but next version is %d", ErrInvalidCatalog, expectedVersion, nextVersion)
	}
	checksum, err := AgentSpecChecksum(spec)
	if err != nil {
		return CatalogVersion{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return CatalogVersion{}, fmt.Errorf("marshal Agent version: %w", err)
	}
	versionID, err := newUUID()
	if err != nil {
		return CatalogVersion{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.versions (
    id, tenant_id, agent_id, version_number, spec_schema_version, spec,
	 spec_checksum, created_by_member_id, capability_snapshot_id
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::jsonb, $7, $8::uuid, $9)`,
		versionID, tenantID, agentID, expectedVersion, AgentSpecSchemaV1, string(raw), checksum, actorMemberID, capabilitySnapshotID,
	); err != nil {
		return CatalogVersion{}, fmt.Errorf("publish Agent version: %w", err)
	}
	return CatalogVersion{
		TenantID: tenantID, AgentID: agentID, VersionID: versionID, VersionNumber: expectedVersion,
		SchemaVersion: AgentSpecSchemaV1, Checksum: checksum, CapabilitySnapshotID: capabilitySnapshotID, Spec: spec,
	}, nil
}
