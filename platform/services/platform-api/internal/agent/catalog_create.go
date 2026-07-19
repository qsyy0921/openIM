package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	agentSlugPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	mentionAliasPattern = regexp.MustCompile(`^@[a-z][a-z0-9_-]{0,62}$`)
)

type CreateAgentRequest struct {
	TenantID, ActorMemberID, Slug, DisplayName, Description, MentionAlias string
	CapabilitySnapshotID                                                  string
	Spec                                                                  AgentSpec
	SkillIDs                                                              []string
}

func (s *Store) CreateAgent(ctx context.Context, request CreateAgentRequest) (AgentSummary, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ActorMemberID = strings.TrimSpace(request.ActorMemberID)
	request.Slug = strings.TrimSpace(request.Slug)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Description = strings.TrimSpace(request.Description)
	request.MentionAlias = strings.TrimSpace(request.MentionAlias)
	request.CapabilitySnapshotID = strings.TrimSpace(request.CapabilitySnapshotID)
	if request.TenantID == "" || request.ActorMemberID == "" || !agentSlugPattern.MatchString(request.Slug) ||
		!mentionAliasPattern.MatchString(request.MentionAlias) || request.DisplayName == "" || len(request.DisplayName) > 120 ||
		len(request.Description) > 500 || request.CapabilitySnapshotID == "" {
		return AgentSummary{}, fmt.Errorf("%w: Agent creation input is invalid", ErrInvalidCatalog)
	}
	if err := validateAgentSpec(request.Spec); err != nil {
		return AgentSummary{}, err
	}
	definitionID, err := newUUID()
	if err != nil {
		return AgentSummary{}, err
	}
	deploymentID, err := newUUID()
	if err != nil {
		return AgentSummary{}, err
	}
	mentionTriggerID, err := newUUID()
	if err != nil {
		return AgentSummary{}, err
	}
	delegateTriggerID, err := newUUID()
	if err != nil {
		return AgentSummary{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AgentSummary{}, fmt.Errorf("begin Agent creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members AS member
    JOIN identity.tenants AS tenant ON tenant.id = member.tenant_id
    JOIN identity.member_roles AS role
      ON role.tenant_id = member.tenant_id AND role.member_id = member.id
     AND role.role IN ('platform_admin', 'agent_admin')
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid
      AND member.status = 'active' AND tenant.status = 'active'
)`, request.TenantID, request.ActorMemberID).Scan(&active); err != nil {
		return AgentSummary{}, fmt.Errorf("validate Agent creator: %w", err)
	}
	if !active {
		return AgentSummary{}, errors.New("Agent creator is not an active Agent administrator")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.definitions (
    id, tenant_id, slug, display_name, description, status, created_by_member_id
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'active', $6::uuid)`,
		definitionID, request.TenantID, request.Slug, request.DisplayName, request.Description, request.ActorMemberID); err != nil {
		return AgentSummary{}, fmt.Errorf("create Agent definition: %w", err)
	}
	version, err := publishVersionTx(ctx, tx, request.TenantID, definitionID, request.ActorMemberID, 1, request.CapabilitySnapshotID, request.Spec)
	if err != nil {
		return AgentSummary{}, err
	}
	if err := linkVersionSkillsTx(ctx, tx, version, request.SkillIDs); err != nil {
		return AgentSummary{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.deployments (
    id, tenant_id, agent_id, slot, active_version_id, activated_by_member_id
) VALUES ($1::uuid, $2::uuid, $3::uuid, 'production', $4::uuid, $5::uuid)`,
		deploymentID, request.TenantID, definitionID, version.VersionID, request.ActorMemberID); err != nil {
		return AgentSummary{}, fmt.Errorf("create Agent deployment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO agent.triggers (id, tenant_id, agent_id, trigger_type, trigger_value, enabled)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'mention_alias', $4, true),
       ($5::uuid, $2::uuid, $3::uuid, 'internal_delegate', $6, true)`,
		mentionTriggerID, request.TenantID, definitionID, request.MentionAlias,
		delegateTriggerID, "delegate:"+request.Slug); err != nil {
		return AgentSummary{}, fmt.Errorf("create Agent triggers: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.agent_admin_events (
    tenant_id, agent_id, actor_member_id, event_type, evidence
) VALUES ($1::uuid, $2::uuid, $3::uuid, 'agent_created',
          jsonb_build_object('slug', $4::text, 'version_id', $5::text,
                             'capability_snapshot_id', $6::text, 'mention_alias', $7::text))`,
		request.TenantID, definitionID, request.ActorMemberID, request.Slug,
		version.VersionID, request.CapabilitySnapshotID, request.MentionAlias); err != nil {
		return AgentSummary{}, fmt.Errorf("audit Agent creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSummary{}, fmt.Errorf("commit Agent creation: %w", err)
	}
	return AgentSummary{
		ID: definitionID, Slug: request.Slug, DisplayName: request.DisplayName,
		Description: request.Description, TriggerAlias: request.MentionAlias,
		VersionNumber: 1, VersionID: version.VersionID, SpecChecksum: version.Checksum,
		CapabilitySnapshotID: request.CapabilitySnapshotID, BotUserID: BotUserID(request.TenantID),
	}, nil
}

func requireAgentAdmin(ctx context.Context, tx pgx.Tx, tenantID, actorMemberID string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM identity.members AS member
    JOIN identity.tenants AS tenant ON tenant.id = member.tenant_id
    JOIN identity.member_roles AS role
      ON role.tenant_id = member.tenant_id AND role.member_id = member.id
     AND role.role IN ('platform_admin', 'agent_admin')
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid
      AND member.status = 'active' AND tenant.status = 'active'
)`, tenantID, actorMemberID).Scan(&allowed); err != nil {
		return fmt.Errorf("authorize Agent administrator: %w", err)
	}
	if !allowed {
		return errors.New("Agent administration requires an active platform_admin or agent_admin role")
	}
	return nil
}
