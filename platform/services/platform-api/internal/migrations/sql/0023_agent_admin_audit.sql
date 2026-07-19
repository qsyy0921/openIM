CREATE TABLE identity.member_roles (
    tenant_id uuid NOT NULL,
    member_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('platform_admin', 'agent_admin', 'knowledge_admin')),
    granted_by_member_id uuid,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, member_id, role),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, granted_by_member_id) REFERENCES identity.members(tenant_id, id)
);

WITH first_admin AS (
    SELECT DISTINCT ON (tenant_id) tenant_id, id AS member_id
    FROM identity.members
    WHERE status = 'active'
    ORDER BY tenant_id, created_at, id
)
INSERT INTO identity.member_roles (tenant_id, member_id, role)
SELECT tenant_id, member_id, 'platform_admin' FROM first_admin;

CREATE TABLE audit.agent_admin_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    actor_member_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type = 'agent_created'),
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, agent_id) REFERENCES agent.definitions(tenant_id, id),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE audit.capability_admin_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    actor_member_id uuid NOT NULL,
    subject_member_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('member_grant_added', 'member_grant_removed')),
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, subject_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE audit.identity_admin_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    actor_member_id uuid NOT NULL,
    subject_member_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('role_granted', 'role_revoked')),
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, subject_member_id) REFERENCES identity.members(tenant_id, id)
);
