package admincontrol

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/mcp"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
)

type CatalogStore struct {
	pool         *pgxpool.Pool
	agents       *agent.Store
	capabilities *capability.Store
	mcp          *mcp.PostgresRegistry
	members      *identity.PostgresStore
}

func NewCatalogStore(pool *pgxpool.Pool) *CatalogStore {
	return &CatalogStore{pool: pool, agents: agent.NewStore(pool), capabilities: capability.NewStore(pool), mcp: mcp.NewPostgresRegistry(pool), members: identity.NewPostgresStore(pool)}
}

type CatalogAgent struct {
	ID                   string `json:"id"`
	Slug                 string `json:"slug"`
	DisplayName          string `json:"display_name"`
	Description          string `json:"description"`
	Status               string `json:"status"`
	ActiveVersionID      string `json:"active_version_id"`
	CapabilitySnapshotID string `json:"capability_snapshot_id"`
	VersionNumber        int    `json:"version_number"`
	DeploymentRevision   int64  `json:"deployment_revision"`
}

type CatalogSkill struct {
	ID             string   `json:"id"`
	SkillID        string   `json:"skill_id"`
	Version        string   `json:"version"`
	Name           string   `json:"name"`
	Summary        string   `json:"summary"`
	Audience       string   `json:"audience"`
	ContentDigest  string   `json:"content_digest"`
	ToolOperations []string `json:"tool_operations"`
}

type CatalogTool struct {
	ID              string          `json:"id,omitempty"`
	OperationID     string          `json:"operation_id"`
	Version         string          `json:"version"`
	Name            string          `json:"name"`
	Summary         string          `json:"summary"`
	SourceType      string          `json:"source_type,omitempty"`
	SourceID        string          `json:"source_id"`
	SourceOperation string          `json:"source_operation"`
	Risk            string          `json:"risk"`
	Idempotency     string          `json:"idempotency"`
	RetrySemantics  string          `json:"retry_semantics"`
	Audience        string          `json:"audience"`
	SchemaDigest    string          `json:"schema_digest,omitempty"`
	Permissions     []string        `json:"permissions"`
	ParameterTerms  []string        `json:"parameter_terms"`
	Examples        []string        `json:"examples"`
	OutputKinds     []string        `json:"output_kinds"`
	TimeoutMS       int64           `json:"timeout_ms"`
	InputSchema     json.RawMessage `json:"input_schema"`
}

type CatalogMCP struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	State         string    `json:"state"`
	LastErrorCode string    `json:"last_error_code"`
	CatalogDigest string    `json:"catalog_digest"`
	Enabled       bool      `json:"enabled"`
	RestartCount  int       `json:"restart_count"`
	CheckedAt     time.Time `json:"checked_at"`
}

type CatalogMember struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

type CatalogSnapshotRef struct {
	ID        string    `json:"id"`
	ToolCount int       `json:"tool_count"`
	CreatedAt time.Time `json:"created_at"`
}

type CatalogSnapshot struct {
	Roles               []string                `json:"roles"`
	Agents              []CatalogAgent          `json:"agents"`
	Skills              []CatalogSkill          `json:"skills"`
	Tools               []CatalogTool           `json:"tools"`
	MCPServers          []CatalogMCP            `json:"mcp_servers"`
	Members             []CatalogMember         `json:"members"`
	CapabilitySnapshots []CatalogSnapshotRef    `json:"capability_snapshots"`
	RemoteA2AEnabled    bool                    `json:"remote_a2a_enabled"`
	RemoteAgents        []remotea2a.RemoteAgent `json:"remote_agents"`
}

func (s *CatalogStore) Read(ctx context.Context, tenantID string) (CatalogSnapshot, error) {
	result := CatalogSnapshot{Agents: []CatalogAgent{}, Skills: []CatalogSkill{}, Tools: []CatalogTool{}, MCPServers: []CatalogMCP{}, Members: []CatalogMember{}, CapabilitySnapshots: []CatalogSnapshotRef{}}
	rows, err := s.pool.Query(ctx, `
SELECT definition.id::text, definition.slug, definition.display_name, definition.description,
       definition.status, version.id::text, version.version_number, version.capability_snapshot_id,
       deployment.revision
FROM agent.definitions AS definition
JOIN agent.deployments AS deployment
  ON deployment.tenant_id = definition.tenant_id AND deployment.agent_id = definition.id AND deployment.slot = 'production'
JOIN agent.versions AS version
  ON version.tenant_id = deployment.tenant_id AND version.agent_id = deployment.agent_id AND version.id = deployment.active_version_id
WHERE definition.tenant_id = $1::uuid
ORDER BY definition.slug`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogAgent
		if err := rows.Scan(&item.ID, &item.Slug, &item.DisplayName, &item.Description, &item.Status,
			&item.ActiveVersionID, &item.VersionNumber, &item.CapabilitySnapshotID, &item.DeploymentRevision); err != nil {
			rows.Close()
			return result, err
		}
		result.Agents = append(result.Agents, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT id::text, skill_id, version, name, summary, tool_operations, audience, content_digest FROM capability.skills WHERE tenant_id = $1::uuid ORDER BY skill_id, version`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogSkill
		if err := rows.Scan(&item.ID, &item.SkillID, &item.Version, &item.Name, &item.Summary, &item.ToolOperations, &item.Audience, &item.ContentDigest); err != nil {
			rows.Close()
			return result, err
		}
		result.Skills = append(result.Skills, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT id::text, operation_id, version, name, summary, source_type, source_id, source_operation,
       risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
       parameter_terms, examples, output_kinds, input_schema, schema_digest
FROM capability.tool_descriptors WHERE tenant_id = $1::uuid ORDER BY operation_id, version`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogTool
		if err := rows.Scan(&item.ID, &item.OperationID, &item.Version, &item.Name, &item.Summary,
			&item.SourceType, &item.SourceID, &item.SourceOperation, &item.Risk, &item.Permissions,
			&item.Idempotency, &item.RetrySemantics, &item.TimeoutMS, &item.Audience,
			&item.ParameterTerms, &item.Examples, &item.OutputKinds, &item.InputSchema, &item.SchemaDigest); err != nil {
			rows.Close()
			return result, err
		}
		result.Tools = append(result.Tools, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT server.id::text, server.slug, server.enabled, COALESCE(health.state, 'stopped'),
       COALESCE(health.last_error_code, ''), COALESCE(health.tool_catalog_digest, ''),
       COALESCE(health.restart_count, 0), COALESCE(health.checked_at, server.updated_at)
FROM capability.mcp_servers AS server
LEFT JOIN capability.mcp_health AS health ON health.tenant_id = server.tenant_id AND health.server_id = server.id
WHERE server.tenant_id = $1::uuid ORDER BY server.slug`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogMCP
		if err := rows.Scan(&item.ID, &item.Slug, &item.Enabled, &item.State, &item.LastErrorCode, &item.CatalogDigest, &item.RestartCount, &item.CheckedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.MCPServers = append(result.MCPServers, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT member.id::text, member.display_name, member.status,
       COALESCE(array_agg(role.role ORDER BY role.role) FILTER (WHERE role.role IS NOT NULL), ARRAY[]::text[])
FROM identity.members AS member
LEFT JOIN identity.member_roles AS role ON role.tenant_id = member.tenant_id AND role.member_id = member.id
WHERE member.tenant_id = $1::uuid
GROUP BY member.id, member.display_name, member.status ORDER BY member.display_name, member.id`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogMember
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Status, &item.Roles); err != nil {
			rows.Close()
			return result, err
		}
		result.Members = append(result.Members, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT snapshot.id, count(tool.tool_id)::integer, snapshot.created_at
FROM capability.snapshots AS snapshot
LEFT JOIN capability.snapshot_tools AS tool ON tool.tenant_id = snapshot.tenant_id AND tool.snapshot_id = snapshot.id
WHERE snapshot.tenant_id = $1::uuid
GROUP BY snapshot.id, snapshot.created_at ORDER BY snapshot.created_at DESC, snapshot.id`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item CatalogSnapshotRef
		if err := rows.Scan(&item.ID, &item.ToolCount, &item.CreatedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.CapabilitySnapshots = append(result.CapabilitySnapshots, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	return result, nil
}

type CreateAgentInput struct {
	Slug                 string          `json:"slug"`
	DisplayName          string          `json:"display_name"`
	Description          string          `json:"description"`
	MentionAlias         string          `json:"mention_alias"`
	CapabilitySnapshotID string          `json:"capability_snapshot_id"`
	SkillIDs             []string        `json:"skill_ids"`
	Spec                 json.RawMessage `json:"spec"`
}

func (s *CatalogStore) CreateAgent(ctx context.Context, tenantID, actorMemberID string, input CreateAgentInput) (agent.AgentSummary, error) {
	spec, err := agent.DecodeAgentSpec(agent.AgentSpecSchemaV1, input.Spec)
	if err != nil {
		return agent.AgentSummary{}, err
	}
	return s.agents.CreateAgent(ctx, agent.CreateAgentRequest{TenantID: tenantID, ActorMemberID: actorMemberID,
		Slug: input.Slug, DisplayName: input.DisplayName, Description: input.Description,
		MentionAlias: input.MentionAlias, CapabilitySnapshotID: input.CapabilitySnapshotID, Spec: spec, SkillIDs: input.SkillIDs})
}

func (s *CatalogStore) PublishSkill(ctx context.Context, tenantID, actorMemberID string, skill capability.Skill) (capability.Skill, error) {
	skill.TenantID = tenantID
	return s.capabilities.PublishSkillAsAdmin(ctx, actorMemberID, skill)
}

func (s *CatalogStore) PublishTool(ctx context.Context, tenantID, actorMemberID string, tool CatalogTool) (capability.Descriptor, error) {
	return s.capabilities.PublishDescriptor(ctx, tenantID, actorMemberID, capability.Descriptor{
		OperationID: tool.OperationID, Version: tool.Version, Name: tool.Name, Summary: tool.Summary,
		SourceType: "mcp", SourceID: tool.SourceID, SourceOperation: tool.SourceOperation,
		Risk: tool.Risk, Permissions: tool.Permissions, Idempotency: tool.Idempotency,
		RetrySemantics: tool.RetrySemantics, Timeout: time.Duration(tool.TimeoutMS) * time.Millisecond,
		Audience: tool.Audience, ParameterTerms: tool.ParameterTerms, Examples: tool.Examples,
		OutputKinds: tool.OutputKinds, InputSchema: tool.InputSchema,
	})
}

func (s *CatalogStore) PublishSnapshot(ctx context.Context, tenantID, actorMemberID string, refs []capability.ToolRef) (capability.Snapshot, error) {
	return s.capabilities.PublishSnapshot(ctx, tenantID, actorMemberID, refs)
}

func (s *CatalogStore) SetMCPEnabled(ctx context.Context, tenantID, actorMemberID, slug string, enabled bool) error {
	return s.mcp.SetEnabledAsAdmin(ctx, tenantID, actorMemberID, slug, enabled)
}

func (s *CatalogStore) SetMemberRole(ctx context.Context, tenantID, actorMemberID, memberID, role string, enabled bool) error {
	return s.members.SetMemberRole(ctx, tenantID, actorMemberID, memberID, role, enabled)
}

func (s *Service) SetCatalogStore(store *CatalogStore) {
	if store == nil {
		panic("administrator catalog store is required")
	}
	s.catalog = store
}

func (s *Service) SetRemoteA2AStore(store *remotea2a.Store) {
	if store == nil {
		panic("remote A2A store is required")
	}
	s.remoteA2A = store
}

func (s *Service) GetCatalog(ctx context.Context, token, deviceID string, platformID int32) (CatalogSnapshot, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	if s.catalog == nil || !hasAnyRole(roles, "platform_admin", "agent_admin", "knowledge_admin") {
		return CatalogSnapshot{}, ErrForbidden
	}
	snapshot, err := s.catalog.Read(ctx, member.TenantID)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	snapshot.RemoteA2AEnabled = s.remoteA2A != nil
	snapshot.RemoteAgents = []remotea2a.RemoteAgent{}
	if s.remoteA2A != nil {
		snapshot.RemoteAgents, err = s.remoteA2A.List(ctx, member.TenantID)
		if err != nil {
			return CatalogSnapshot{}, err
		}
	}
	snapshot.Roles = roles
	return snapshot, nil
}

func (s *Service) CreateAgent(ctx context.Context, token, deviceID string, platformID int32, input CreateAgentInput) (agent.AgentSummary, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return agent.AgentSummary{}, err
	}
	if s.catalog == nil || !hasAnyRole(roles, "platform_admin", "agent_admin") {
		return agent.AgentSummary{}, ErrForbidden
	}
	return s.catalog.CreateAgent(ctx, member.TenantID, member.ID, input)
}

func (s *Service) PublishSkill(ctx context.Context, token, deviceID string, platformID int32, skill capability.Skill) (capability.Skill, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return capability.Skill{}, err
	}
	if s.catalog == nil || !slices.Contains(roles, "platform_admin") {
		return capability.Skill{}, ErrForbidden
	}
	return s.catalog.PublishSkill(ctx, member.TenantID, member.ID, skill)
}

func (s *Service) PublishTool(ctx context.Context, token, deviceID string, platformID int32, tool CatalogTool) (capability.Descriptor, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return capability.Descriptor{}, err
	}
	if s.catalog == nil || !slices.Contains(roles, "platform_admin") {
		return capability.Descriptor{}, ErrForbidden
	}
	return s.catalog.PublishTool(ctx, member.TenantID, member.ID, tool)
}

func (s *Service) PublishCapabilitySnapshot(ctx context.Context, token, deviceID string, platformID int32, refs []capability.ToolRef) (capability.Snapshot, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return capability.Snapshot{}, err
	}
	if s.catalog == nil || !slices.Contains(roles, "platform_admin") {
		return capability.Snapshot{}, ErrForbidden
	}
	return s.catalog.PublishSnapshot(ctx, member.TenantID, member.ID, refs)
}

func (s *Service) SetMCPEnabled(ctx context.Context, token, deviceID string, platformID int32, slug string, enabled bool) error {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return err
	}
	if s.catalog == nil || !slices.Contains(roles, "platform_admin") {
		return ErrForbidden
	}
	return s.catalog.SetMCPEnabled(ctx, member.TenantID, member.ID, strings.TrimSpace(slug), enabled)
}

func (s *Service) SetMemberRole(ctx context.Context, token, deviceID string, platformID int32, memberID, role string, enabled bool) error {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return err
	}
	if s.catalog == nil || !slices.Contains(roles, "platform_admin") {
		return ErrForbidden
	}
	return s.catalog.SetMemberRole(ctx, member.TenantID, member.ID, strings.TrimSpace(memberID), strings.TrimSpace(role), enabled)
}

func (s *Service) RegisterRemoteAgent(ctx context.Context, token, deviceID string, platformID int32, input remotea2a.RegisterInput) (remotea2a.RemoteAgent, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return remotea2a.RemoteAgent{}, err
	}
	if s.remoteA2A == nil || !slices.Contains(roles, "platform_admin") {
		return remotea2a.RemoteAgent{}, ErrForbidden
	}
	return s.remoteA2A.Register(ctx, member.TenantID, member.ID, input)
}

func (s *Service) VerifyRemoteAgent(ctx context.Context, token, deviceID string, platformID int32, slug string, expectedRevision int64) (remotea2a.RemoteAgent, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return remotea2a.RemoteAgent{}, err
	}
	if s.remoteA2A == nil || !slices.Contains(roles, "platform_admin") {
		return remotea2a.RemoteAgent{}, ErrForbidden
	}
	return s.remoteA2A.Verify(ctx, member.TenantID, member.ID, strings.TrimSpace(slug), expectedRevision)
}

func (s *Service) SetRemoteAgentEnabled(ctx context.Context, token, deviceID string, platformID int32, slug string, enabled bool, expectedRevision int64) (remotea2a.RemoteAgent, error) {
	member, roles, err := s.resolve(ctx, token, deviceID, platformID)
	if err != nil {
		return remotea2a.RemoteAgent{}, err
	}
	if s.remoteA2A == nil || !slices.Contains(roles, "platform_admin") {
		return remotea2a.RemoteAgent{}, ErrForbidden
	}
	return s.remoteA2A.SetEnabled(ctx, member.TenantID, member.ID, strings.TrimSpace(slug), enabled, expectedRevision)
}

func hasAnyRole(roles []string, expected ...string) bool {
	for _, role := range expected {
		if slices.Contains(roles, role) {
			return true
		}
	}
	return false
}
