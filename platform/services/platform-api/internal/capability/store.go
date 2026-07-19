package capability

import (
	"context"
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

var ErrSnapshotNotFound = errors.New("capability snapshot was not found")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type ToolRef struct {
	OperationID string `json:"operation_id"`
	Version     string `json:"version"`
}

func (s *Store) PublishSnapshot(ctx context.Context, tenantID, actorMemberID string, refs []ToolRef) (Snapshot, error) {
	tenantID, actorMemberID = strings.TrimSpace(tenantID), strings.TrimSpace(actorMemberID)
	if tenantID == "" || actorMemberID == "" {
		return Snapshot{}, errors.New("capability snapshot tenant and 1 to 64 tools are required")
	}
	refs, err := normalizeToolRefs(refs)
	if err != nil {
		return Snapshot{}, err
	}
	payload, err := json.Marshal(struct {
		SchemaVersion int       `json:"schema_version"`
		Tools         []ToolRef `json:"tools"`
	}{SchemaVersion: 1, Tools: refs})
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode capability snapshot: %w", err)
	}
	digest := sha256.Sum256(payload)
	snapshot := Snapshot{ID: "capability-v1:" + hex.EncodeToString(digest[:]), SchemaVersion: 1, Payload: payload}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin capability snapshot publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return Snapshot{}, err
	}
	for _, ref := range refs {
		var descriptor Descriptor
		var timeoutMS int64
		if err := tx.QueryRow(ctx, `
SELECT id::text, operation_id, version, name, summary, source_type, source_id,
       source_operation, risk, permissions, idempotency, retry_semantics,
       timeout_ms, audience, parameter_terms, examples, output_kinds,
       input_schema, schema_digest
FROM capability.tool_descriptors
WHERE tenant_id = $1::uuid AND operation_id = $2 AND version = $3`,
			tenantID, ref.OperationID, ref.Version).Scan(
			&descriptor.ID, &descriptor.OperationID, &descriptor.Version, &descriptor.Name, &descriptor.Summary,
			&descriptor.SourceType, &descriptor.SourceID, &descriptor.SourceOperation, &descriptor.Risk,
			&descriptor.Permissions, &descriptor.Idempotency, &descriptor.RetrySemantics, &timeoutMS,
			&descriptor.Audience, &descriptor.ParameterTerms, &descriptor.Examples, &descriptor.OutputKinds,
			&descriptor.InputSchema, &descriptor.SchemaDigest,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Snapshot{}, fmt.Errorf("capability tool %s@%s is not published", ref.OperationID, ref.Version)
			}
			return Snapshot{}, fmt.Errorf("load capability snapshot tool: %w", err)
		}
		descriptor.Timeout = time.Duration(timeoutMS) * time.Millisecond
		if err := descriptor.Validate(); err != nil {
			return Snapshot{}, fmt.Errorf("validate capability tool %s@%s: %w", ref.OperationID, ref.Version, err)
		}
		snapshot.Tools = append(snapshot.Tools, descriptor)
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO capability.snapshots (tenant_id, id, schema_version, payload)
VALUES ($1::uuid, $2, 1, $3::jsonb)
ON CONFLICT (tenant_id, id) DO NOTHING`, tenantID, snapshot.ID, string(snapshot.Payload)); err != nil {
		return Snapshot{}, fmt.Errorf("publish capability snapshot: %w", err)
	}
	for ordinal, descriptor := range snapshot.Tools {
		if _, err := tx.Exec(ctx, `
INSERT INTO capability.snapshot_tools (tenant_id, snapshot_id, tool_id, ordinal)
VALUES ($1::uuid, $2, $3::uuid, $4)
ON CONFLICT (tenant_id, snapshot_id, tool_id) DO NOTHING`, tenantID, snapshot.ID, descriptor.ID, ordinal); err != nil {
			return Snapshot{}, fmt.Errorf("link capability snapshot tool: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.catalog_admin_events
    (tenant_id, actor_member_id, resource_type, resource_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'capability_snapshot', $3, 'published',
        jsonb_build_object('tool_count', $4::integer))`, tenantID, actorMemberID, snapshot.ID, len(snapshot.Tools)); err != nil {
		return Snapshot{}, fmt.Errorf("audit capability snapshot publication: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("commit capability snapshot publication: %w", err)
	}
	return snapshot, nil
}

func normalizeToolRefs(input []ToolRef) ([]ToolRef, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, errors.New("capability snapshot tenant and 1 to 64 tools are required")
	}
	refs := append([]ToolRef(nil), input...)
	seen := make(map[string]struct{}, len(refs))
	for index := range refs {
		refs[index].OperationID = strings.TrimSpace(refs[index].OperationID)
		refs[index].Version = strings.TrimSpace(refs[index].Version)
		key := refs[index].OperationID + "@" + refs[index].Version
		if refs[index].OperationID == "" || refs[index].Version == "" {
			return nil, errors.New("capability snapshot tool reference is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("capability snapshot contains duplicate tool references")
		}
		seen[key] = struct{}{}
	}
	return refs, nil
}

func (s *Store) SetMemberGrant(ctx context.Context, tenantID, actorMemberID, subjectMemberID, permission string, enabled bool) error {
	tenantID, actorMemberID = strings.TrimSpace(tenantID), strings.TrimSpace(actorMemberID)
	subjectMemberID, permission = strings.TrimSpace(subjectMemberID), strings.TrimSpace(permission)
	if tenantID == "" || actorMemberID == "" || subjectMemberID == "" || !permissionPattern.MatchString(permission) {
		return errors.New("capability member grant input is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin capability member grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members
    WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'active'
)`, tenantID, subjectMemberID).Scan(&active); err != nil {
		return fmt.Errorf("validate capability grant member: %w", err)
	}
	if !active {
		return errors.New("capability grant member is not active")
	}
	eventType := "member_grant_added"
	if enabled {
		result, err := tx.Exec(ctx, `
INSERT INTO capability.member_grants (tenant_id, member_id, permission)
VALUES ($1::uuid, $2::uuid, $3)
ON CONFLICT (tenant_id, member_id, permission) DO NOTHING`, tenantID, subjectMemberID, permission)
		if err != nil {
			return fmt.Errorf("add capability member grant: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("capability member grant already exists")
		}
	} else {
		eventType = "member_grant_removed"
		result, err := tx.Exec(ctx, `
DELETE FROM capability.member_grants
WHERE tenant_id = $1::uuid AND member_id = $2::uuid AND permission = $3`,
			tenantID, subjectMemberID, permission)
		if err != nil {
			return fmt.Errorf("remove capability member grant: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("capability member grant does not exist")
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.capability_admin_events (
    tenant_id, actor_member_id, subject_member_id, event_type, evidence
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4,
          jsonb_build_object('permission', $5::text))`,
		tenantID, actorMemberID, subjectMemberID, eventType, permission); err != nil {
		return fmt.Errorf("audit capability member grant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit capability member grant: %w", err)
	}
	return nil
}

func requirePlatformAdmin(ctx context.Context, tx pgx.Tx, tenantID, actorMemberID string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM identity.members AS member
    JOIN identity.tenants AS tenant ON tenant.id = member.tenant_id
    JOIN identity.member_roles AS role
      ON role.tenant_id = member.tenant_id AND role.member_id = member.id
     AND role.role = 'platform_admin'
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid
      AND member.status = 'active' AND tenant.status = 'active'
)`, tenantID, actorMemberID).Scan(&allowed); err != nil {
		return fmt.Errorf("authorize platform administrator: %w", err)
	}
	if !allowed {
		return errors.New("capability administration requires an active platform_admin role")
	}
	return nil
}

func (s *Store) LoadSnapshot(ctx context.Context, tenantID, snapshotID string) (Snapshot, error) {
	const snapshotQuery = `
SELECT id, schema_version, payload
FROM capability.snapshots
WHERE tenant_id = $1::uuid AND id = $2`
	var snapshot Snapshot
	if err := s.pool.QueryRow(ctx, snapshotQuery, tenantID, snapshotID).Scan(
		&snapshot.ID, &snapshot.SchemaVersion, &snapshot.Payload,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Snapshot{}, ErrSnapshotNotFound
		}
		return Snapshot{}, fmt.Errorf("load capability snapshot: %w", err)
	}
	const toolsQuery = `
SELECT tool.id::text, tool.operation_id, tool.version, tool.name, tool.summary,
       tool.source_type, tool.source_id, tool.source_operation, tool.risk, tool.permissions,
       tool.idempotency, tool.retry_semantics, tool.timeout_ms, tool.audience,
       tool.parameter_terms, tool.examples, tool.output_kinds,
       tool.input_schema, tool.schema_digest
FROM capability.snapshot_tools AS entry
JOIN capability.tool_descriptors AS tool
  ON tool.tenant_id = entry.tenant_id AND tool.id = entry.tool_id
WHERE entry.tenant_id = $1::uuid AND entry.snapshot_id = $2
ORDER BY entry.ordinal`
	rows, err := s.pool.Query(ctx, toolsQuery, tenantID, snapshotID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load capability snapshot tools: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var descriptor Descriptor
		var timeoutMS int64
		if err := rows.Scan(
			&descriptor.ID, &descriptor.OperationID, &descriptor.Version, &descriptor.Name, &descriptor.Summary,
			&descriptor.SourceType, &descriptor.SourceID, &descriptor.SourceOperation, &descriptor.Risk, &descriptor.Permissions,
			&descriptor.Idempotency, &descriptor.RetrySemantics, &timeoutMS, &descriptor.Audience,
			&descriptor.ParameterTerms, &descriptor.Examples, &descriptor.OutputKinds,
			&descriptor.InputSchema, &descriptor.SchemaDigest,
		); err != nil {
			return Snapshot{}, fmt.Errorf("scan capability descriptor: %w", err)
		}
		descriptor.Timeout = time.Duration(timeoutMS) * time.Millisecond
		snapshot.Tools = append(snapshot.Tools, descriptor)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate capability descriptors: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("validate capability snapshot: %w", err)
	}
	return snapshot, nil
}
