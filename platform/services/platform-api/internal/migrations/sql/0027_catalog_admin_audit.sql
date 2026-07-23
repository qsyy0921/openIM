CREATE TABLE audit.catalog_admin_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    actor_member_id uuid NOT NULL,
    resource_type text NOT NULL CHECK (resource_type IN ('skill', 'tool', 'capability_snapshot', 'mcp_server')),
    resource_id text NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('published', 'enabled', 'disabled')),
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE INDEX catalog_admin_events_tenant_created_idx
    ON audit.catalog_admin_events (tenant_id, created_at DESC);
