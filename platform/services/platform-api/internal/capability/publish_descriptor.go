package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PublishDescriptor(ctx context.Context, tenantID, actorMemberID string, descriptor Descriptor) (Descriptor, error) {
	if descriptor.SourceType != "mcp" {
		return Descriptor{}, errors.New("Web-published tool descriptors must target a registered MCP server")
	}
	canonical, err := canonicalJSON(descriptor.InputSchema)
	if err != nil {
		return Descriptor{}, errors.New("tool input schema is invalid")
	}
	digest := sha256.Sum256(canonical)
	descriptor.InputSchema = canonical
	descriptor.SchemaDigest = "sha256:" + hex.EncodeToString(digest[:])
	if descriptor.ID == "" {
		descriptor.ID, err = capabilityUUID()
		if err != nil {
			return Descriptor{}, err
		}
	}
	if err := descriptor.Validate(); err != nil {
		return Descriptor{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Descriptor{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, tenantID, actorMemberID); err != nil {
		return Descriptor{}, err
	}
	var serverExists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM capability.mcp_servers
    WHERE tenant_id = $1::uuid AND slug = $2
)`, tenantID, descriptor.SourceID).Scan(&serverExists); err != nil {
		return Descriptor{}, err
	}
	if !serverExists {
		return Descriptor{}, errors.New("tool descriptor references an unregistered MCP server")
	}
	result, err := tx.Exec(ctx, `
INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    source_operation, risk, permissions, idempotency, retry_semantics, timeout_ms,
    audience, parameter_terms, examples, output_kinds, input_schema, schema_digest
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
          $14, $15, $16, $17, $18, $19::jsonb, $20)
ON CONFLICT (tenant_id, operation_id, version) DO NOTHING`, descriptor.ID, tenantID,
		descriptor.OperationID, descriptor.Version, descriptor.Name, descriptor.Summary,
		descriptor.SourceType, descriptor.SourceID, descriptor.SourceOperation, descriptor.Risk,
		descriptor.Permissions, descriptor.Idempotency, descriptor.RetrySemantics,
		int64(descriptor.Timeout/time.Millisecond), descriptor.Audience, descriptor.ParameterTerms,
		descriptor.Examples, descriptor.OutputKinds, string(descriptor.InputSchema), descriptor.SchemaDigest)
	if err != nil {
		return Descriptor{}, fmt.Errorf("publish tool descriptor: %w", err)
	}
	if result.RowsAffected() != 1 {
		return Descriptor{}, errors.New("tool descriptor version already exists")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.catalog_admin_events
    (tenant_id, actor_member_id, resource_type, resource_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'tool', $3, 'published',
        jsonb_build_object('operation_id', $4::text, 'version', $5::text, 'schema_digest', $6::text))`,
		tenantID, actorMemberID, descriptor.ID, descriptor.OperationID, descriptor.Version, descriptor.SchemaDigest); err != nil {
		return Descriptor{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}
