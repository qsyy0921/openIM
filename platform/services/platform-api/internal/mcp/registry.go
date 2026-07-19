package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	ID, TenantID, Slug, ConfigDigest, ExpectedCatalogDigest string
	Config                                                  ServerConfig
}

type RegistryStore interface {
	ListEnabled(context.Context) ([]Server, error)
	RecordHealth(context.Context, Server, string, string, int, string) error
}

type PostgresRegistry struct{ pool *pgxpool.Pool }

func NewPostgresRegistry(pool *pgxpool.Pool) *PostgresRegistry { return &PostgresRegistry{pool: pool} }

type Health struct {
	TenantID, ServerID, Slug, InstanceID, State string
	RestartCount                                int
	LastErrorCode, CatalogDigest                string
	CheckedAt                                   time.Time
}

func (s *PostgresRegistry) Register(ctx context.Context, server Server, enabled bool) (string, error) {
	if err := server.Config.Validate(); err != nil {
		return "", err
	}
	configDigest, err := serverConfigDigest(server)
	if err != nil {
		return "", err
	}
	if server.ExpectedCatalogDigest == "" {
		return "", errors.New("MCP expected tool catalog digest is required")
	}
	if server.ID == "" {
		server.ID, err = registryUUID()
		if err != nil {
			return "", err
		}
	}
	arguments, err := json.Marshal(server.Config.Arguments)
	if err != nil {
		return "", err
	}
	const query = `
INSERT INTO capability.mcp_servers (
    id, tenant_id, slug, transport, command, arguments, working_directory,
    environment_keys, enabled, config_digest, expected_tool_catalog_digest
) VALUES ($1::uuid, $2::uuid, $3, 'stdio', $4, $5::jsonb, NULLIF($6, ''),
          $7, $8, $9, $10)
ON CONFLICT (tenant_id, slug) DO UPDATE
SET command = EXCLUDED.command, arguments = EXCLUDED.arguments,
    working_directory = EXCLUDED.working_directory,
    environment_keys = EXCLUDED.environment_keys, enabled = EXCLUDED.enabled,
    config_digest = EXCLUDED.config_digest,
    expected_tool_catalog_digest = EXCLUDED.expected_tool_catalog_digest,
    updated_at = now()
RETURNING id::text`
	if err := s.pool.QueryRow(ctx, query, server.ID, server.TenantID, server.Slug,
		server.Config.Command, string(arguments), server.Config.WorkingDirectory,
		server.Config.EnvironmentKeys, enabled, configDigest, server.ExpectedCatalogDigest).Scan(&server.ID); err != nil {
		return "", fmt.Errorf("register MCP server: %w", err)
	}
	return server.ID, nil
}

func (s *PostgresRegistry) SetEnabled(ctx context.Context, tenantID, slug string, enabled bool) error {
	result, err := s.pool.Exec(ctx, `
UPDATE capability.mcp_servers SET enabled = $3, updated_at = now()
WHERE tenant_id = $1::uuid AND slug = $2`, tenantID, slug, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("MCP server was not found")
	}
	return nil
}

func (s *PostgresRegistry) SetEnabledAsAdmin(ctx context.Context, tenantID, actorMemberID, slug string, enabled bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var allowed bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members AS member
    JOIN identity.member_roles AS role
      ON role.tenant_id = member.tenant_id AND role.member_id = member.id
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid
      AND member.status = 'active' AND role.role = 'platform_admin'
)`, tenantID, actorMemberID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errors.New("MCP administration requires an active platform_admin role")
	}
	result, err := tx.Exec(ctx, `
UPDATE capability.mcp_servers SET enabled = $3, updated_at = now()
WHERE tenant_id = $1::uuid AND slug = $2`, tenantID, slug, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("MCP server was not found")
	}
	eventType := "disabled"
	if enabled {
		eventType = "enabled"
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.catalog_admin_events
    (tenant_id, actor_member_id, resource_type, resource_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'mcp_server', $3, $4, jsonb_build_object('enabled', $5::boolean))`,
		tenantID, actorMemberID, slug, eventType, enabled); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresRegistry) ListHealth(ctx context.Context, tenantID string) ([]Health, error) {
	rows, err := s.pool.Query(ctx, `
SELECT server.tenant_id::text, server.id::text, server.slug,
       COALESCE(health.instance_id, ''), COALESCE(health.state, 'stopped'),
       COALESCE(health.restart_count, 0), COALESCE(health.last_error_code, ''),
       COALESCE(health.tool_catalog_digest, ''), COALESCE(health.checked_at, server.updated_at)
FROM capability.mcp_servers AS server
LEFT JOIN capability.mcp_health AS health
  ON health.tenant_id = server.tenant_id AND health.server_id = server.id
WHERE server.tenant_id = $1::uuid
ORDER BY server.slug`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Health
	for rows.Next() {
		var item Health
		if err := rows.Scan(&item.TenantID, &item.ServerID, &item.Slug, &item.InstanceID,
			&item.State, &item.RestartCount, &item.LastErrorCode, &item.CatalogDigest, &item.CheckedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresRegistry) ListEnabled(ctx context.Context) ([]Server, error) {
	const query = `
SELECT id::text, tenant_id::text, slug, command, arguments, COALESCE(working_directory, ''),
       environment_keys, config_digest, expected_tool_catalog_digest
FROM capability.mcp_servers
WHERE enabled AND transport = 'stdio'
ORDER BY tenant_id, slug`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list enabled MCP servers: %w", err)
	}
	defer rows.Close()
	var result []Server
	for rows.Next() {
		var server Server
		var argumentsJSON []byte
		if err := rows.Scan(
			&server.ID, &server.TenantID, &server.Slug, &server.Config.Command,
			&argumentsJSON, &server.Config.WorkingDirectory, &server.Config.EnvironmentKeys,
			&server.ConfigDigest, &server.ExpectedCatalogDigest,
		); err != nil {
			return nil, fmt.Errorf("scan MCP server: %w", err)
		}
		if err := json.Unmarshal(argumentsJSON, &server.Config.Arguments); err != nil {
			return nil, fmt.Errorf("decode MCP server arguments: %w", err)
		}
		server.Config.Name = server.Slug
		server.Config.CallTimeout = 30 * time.Second
		if err := server.Config.Validate(); err != nil {
			return nil, fmt.Errorf("validate MCP server %s: %w", server.Slug, err)
		}
		if actual, err := serverConfigDigest(server); err != nil || actual != server.ConfigDigest {
			return nil, fmt.Errorf("MCP server %s configuration digest does not match", server.Slug)
		}
		result = append(result, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate MCP servers: %w", err)
	}
	return result, nil
}

func (s *PostgresRegistry) RecordHealth(ctx context.Context, server Server, instanceID, state string, restartCount int, errorCode string) error {
	if state != "starting" && state != "healthy" && state != "degraded" && state != "stopped" {
		return errors.New("MCP health state is invalid")
	}
	const query = `
INSERT INTO capability.mcp_health (
    tenant_id, server_id, instance_id, state, tool_catalog_digest,
    restart_count, last_error_code, checked_at
) VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''), now())
ON CONFLICT (tenant_id, server_id) DO UPDATE
SET instance_id = EXCLUDED.instance_id, state = EXCLUDED.state,
    tool_catalog_digest = EXCLUDED.tool_catalog_digest,
    restart_count = EXCLUDED.restart_count, last_error_code = EXCLUDED.last_error_code,
    checked_at = now()`
	catalogDigest := ""
	if state == "healthy" {
		catalogDigest = server.ExpectedCatalogDigest
	}
	if _, err := s.pool.Exec(ctx, query, server.TenantID, server.ID, instanceID, state, catalogDigest, restartCount, errorCode); err != nil {
		return fmt.Errorf("record MCP health: %w", err)
	}
	return nil
}

func serverConfigDigest(server Server) (string, error) {
	value := struct {
		Command          string   `json:"command"`
		Arguments        []string `json:"arguments"`
		WorkingDirectory string   `json:"working_directory"`
		EnvironmentKeys  []string `json:"environment_keys"`
	}{server.Config.Command, server.Config.Arguments, server.Config.WorkingDirectory, server.Config.EnvironmentKeys}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func toolCatalogDigest(tools []Tool) (string, error) {
	type item struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	items := make([]item, len(tools))
	for index, tool := range tools {
		if tool.Name == "" || strings.TrimSpace(tool.Name) != tool.Name || len(tool.InputSchema) == 0 {
			return "", errors.New("MCP tool catalog contains an invalid descriptor")
		}
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return "", errors.New("MCP tool catalog contains an invalid input schema")
		}
		canonical, err := json.Marshal(schema)
		if err != nil {
			return "", err
		}
		items[index] = item{Name: tool.Name, InputSchema: canonical}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func ToolCatalogDigest(tools []Tool) (string, error) { return toolCatalogDigest(tools) }

func registryUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
