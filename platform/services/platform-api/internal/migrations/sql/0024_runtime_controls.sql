CREATE TABLE platform_meta.runtime_controls (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id) ON DELETE CASCADE,
    component text NOT NULL CHECK (component IN ('agent_execution', 'agent_delivery', 'proactive_dispatch')),
    paused boolean NOT NULL,
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by_member_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, component),
    FOREIGN KEY (tenant_id, updated_by_member_id)
        REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE audit.runtime_control_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    actor_member_id uuid NOT NULL,
    component text NOT NULL,
    paused boolean NOT NULL,
    revision bigint NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, actor_member_id)
        REFERENCES identity.members(tenant_id, id)
);

CREATE INDEX runtime_control_events_tenant_created_idx
    ON audit.runtime_control_events (tenant_id, created_at DESC);
