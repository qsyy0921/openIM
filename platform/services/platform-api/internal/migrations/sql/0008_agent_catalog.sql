CREATE TABLE agent.definitions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    slug text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,62}$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 120),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    status text NOT NULL CHECK (status IN ('active', 'disabled', 'archived')),
    created_by_member_id uuid,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, slug),
    FOREIGN KEY (tenant_id, created_by_member_id)
        REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE agent.versions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    version_number integer NOT NULL CHECK (version_number > 0),
    spec_schema_version integer NOT NULL CHECK (spec_schema_version > 0),
    spec jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object'),
    spec_checksum text NOT NULL CHECK (spec_checksum ~ '^sha256:[0-9a-f]{64}$'),
    created_by_member_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_id, id),
    UNIQUE (agent_id, version_number),
    UNIQUE (agent_id, spec_checksum),
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agent.definitions(tenant_id, id),
    FOREIGN KEY (tenant_id, created_by_member_id)
        REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE agent.deployments (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    slot text NOT NULL CHECK (slot = 'production'),
    active_version_id uuid NOT NULL,
    activated_by_member_id uuid,
    activated_at timestamptz NOT NULL DEFAULT now(),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_id, id),
    UNIQUE (tenant_id, agent_id, slot),
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agent.definitions(tenant_id, id),
    FOREIGN KEY (tenant_id, agent_id, active_version_id)
        REFERENCES agent.versions(tenant_id, agent_id, id),
    FOREIGN KEY (tenant_id, activated_by_member_id)
        REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE agent.triggers (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    trigger_type text NOT NULL CHECK (trigger_type = 'mention_alias'),
    trigger_value text NOT NULL CHECK (trigger_value ~ '^@[a-z][a-z0-9_-]{0,62}$'),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_id, id),
    UNIQUE (tenant_id, trigger_type, trigger_value),
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agent.definitions(tenant_id, id)
);

CREATE TABLE audit.agent_catalog_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    actor_member_id uuid,
    event_type text NOT NULL CHECK (event_type = 'deployment_activated'),
    old_version_id uuid NOT NULL,
    new_version_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agent.definitions(tenant_id, id),
    FOREIGN KEY (tenant_id, actor_member_id)
        REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, agent_id, deployment_id)
        REFERENCES agent.deployments(tenant_id, agent_id, id),
    FOREIGN KEY (tenant_id, agent_id, old_version_id)
        REFERENCES agent.versions(tenant_id, agent_id, id),
    FOREIGN KEY (tenant_id, agent_id, new_version_id)
        REFERENCES agent.versions(tenant_id, agent_id, id)
);

INSERT INTO agent.definitions (id, tenant_id, slug, display_name, description, status)
SELECT md5('agent-definition:' || t.id::text)::uuid, t.id, 'knowledge-agent',
       'Enterprise Agent', 'Enterprise knowledge and governed ticket actions',
       CASE WHEN t.status = 'active' THEN 'active' ELSE 'disabled' END
FROM identity.tenants t;

INSERT INTO agent.versions (
    id, tenant_id, agent_id, version_number, spec_schema_version, spec, spec_checksum
)
SELECT md5('agent-version-v1:' || t.id::text)::uuid,
       t.id,
       md5('agent-definition:' || t.id::text)::uuid,
       1,
       1,
       '{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and abstain when evidence is absent.","model_route":"deepseek-v4-pro","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}'::jsonb,
       'sha256:27dcf2cfab60cb915d584127e1524bcda821ce29c8383346adb8f8d2b31bb8d8'
FROM identity.tenants t;

INSERT INTO agent.deployments (id, tenant_id, agent_id, slot, active_version_id)
SELECT md5('agent-deployment-production:' || t.id::text)::uuid,
       t.id,
       md5('agent-definition:' || t.id::text)::uuid,
       'production',
       md5('agent-version-v1:' || t.id::text)::uuid
FROM identity.tenants t;

INSERT INTO agent.triggers (id, tenant_id, agent_id, trigger_type, trigger_value, enabled)
SELECT md5('agent-trigger-at-agent:' || t.id::text)::uuid,
       t.id,
       md5('agent-definition:' || t.id::text)::uuid,
       'mention_alias', '@agent', t.status = 'active'
FROM identity.tenants t;

ALTER TABLE agent.runs
    ADD COLUMN agent_id uuid,
    ADD COLUMN agent_version_id uuid,
    ADD COLUMN agent_deployment_id uuid,
    ADD COLUMN agent_trigger_id uuid,
    ADD COLUMN agent_spec_checksum text;

UPDATE agent.runs r
SET agent_id = d.id,
    agent_version_id = v.id,
    agent_deployment_id = dep.id,
    agent_trigger_id = tr.id,
    agent_spec_checksum = v.spec_checksum
FROM agent.definitions d
JOIN agent.versions v
  ON v.tenant_id = d.tenant_id AND v.agent_id = d.id AND v.version_number = 1
JOIN agent.deployments dep
  ON dep.tenant_id = d.tenant_id AND dep.agent_id = d.id AND dep.slot = 'production'
JOIN agent.triggers tr
  ON tr.tenant_id = d.tenant_id AND tr.agent_id = d.id
 AND tr.trigger_type = 'mention_alias' AND tr.trigger_value = '@agent'
WHERE r.tenant_id = d.tenant_id AND d.slug = 'knowledge-agent';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agent.runs
        WHERE agent_id IS NULL OR agent_version_id IS NULL OR agent_deployment_id IS NULL
           OR agent_trigger_id IS NULL OR agent_spec_checksum IS NULL
    ) THEN
        RAISE EXCEPTION 'Agent catalog migration left unresolved Runs';
    END IF;
END $$;

ALTER TABLE agent.runs
    ALTER COLUMN agent_id SET NOT NULL,
    ALTER COLUMN agent_version_id SET NOT NULL,
    ALTER COLUMN agent_deployment_id SET NOT NULL,
    ALTER COLUMN agent_trigger_id SET NOT NULL,
    ALTER COLUMN agent_spec_checksum SET NOT NULL,
    ADD CONSTRAINT runs_agent_fk FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agent.definitions(tenant_id, id),
    ADD CONSTRAINT runs_agent_version_fk FOREIGN KEY (tenant_id, agent_id, agent_version_id)
        REFERENCES agent.versions(tenant_id, agent_id, id),
    ADD CONSTRAINT runs_agent_deployment_fk FOREIGN KEY (tenant_id, agent_id, agent_deployment_id)
        REFERENCES agent.deployments(tenant_id, agent_id, id),
    ADD CONSTRAINT runs_agent_trigger_fk FOREIGN KEY (tenant_id, agent_id, agent_trigger_id)
        REFERENCES agent.triggers(tenant_id, agent_id, id),
    ADD CONSTRAINT runs_agent_checksum_check CHECK (agent_spec_checksum ~ '^sha256:[0-9a-f]{64}$');

CREATE OR REPLACE FUNCTION agent.reject_published_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'published Agent versions are immutable';
END;
$$;

CREATE TRIGGER agent_versions_immutable_update
BEFORE UPDATE ON agent.versions
FOR EACH ROW EXECUTE FUNCTION agent.reject_published_version_mutation();

CREATE TRIGGER agent_versions_immutable_delete
BEFORE DELETE ON agent.versions
FOR EACH ROW EXECUTE FUNCTION agent.reject_published_version_mutation();
