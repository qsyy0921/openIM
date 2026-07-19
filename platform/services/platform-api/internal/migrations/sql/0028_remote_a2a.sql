CREATE TABLE agent.remote_agents (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    slug text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,62}$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 120),
    card_url text NOT NULL CHECK (card_url ~ '^https://'),
    endpoint_url text CHECK (endpoint_url IS NULL OR endpoint_url ~ '^https://'),
    protocol_version text NOT NULL DEFAULT '1.0' CHECK (protocol_version = '1.0'),
    protocol_binding text CHECK (protocol_binding IS NULL OR protocol_binding = 'HTTP+JSON'),
    expected_card_digest text NOT NULL CHECK (expected_card_digest ~ '^sha256:[0-9a-f]{64}$'),
    observed_card_digest text CHECK (observed_card_digest IS NULL OR observed_card_digest ~ '^sha256:[0-9a-f]{64}$'),
    agent_card jsonb,
    auth_env_key text CHECK (auth_env_key IS NULL OR auth_env_key ~ '^[A-Z][A-Z0-9_]{0,127}$'),
    enabled boolean NOT NULL DEFAULT false,
    lifecycle_state text NOT NULL DEFAULT 'registered'
        CHECK (lifecycle_state IN ('registered', 'verified', 'degraded', 'disabled')),
    lifecycle_revision bigint NOT NULL DEFAULT 1 CHECK (lifecycle_revision > 0),
    last_error_code text,
    last_verified_at timestamptz,
    created_by_member_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, slug),
    FOREIGN KEY (tenant_id, created_by_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE INDEX remote_agents_enabled_idx
    ON agent.remote_agents (tenant_id, enabled, lifecycle_state, slug);

CREATE TABLE agent.remote_a2a_jobs (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    parent_run_id uuid NOT NULL,
    requested_by_member_id uuid NOT NULL,
    remote_agent_id uuid NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
    message_id uuid NOT NULL,
    task text NOT NULL CHECK (length(task) BETWEEN 1 AND 4000),
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'submitted', 'completed', 'failed', 'rejected', 'unknown')),
    remote_task_id text,
    response jsonb,
    response_checksum text CHECK (response_checksum IS NULL OR response_checksum ~ '^sha256:[0-9a-f]{64}$'),
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, parent_run_id, idempotency_key),
    UNIQUE (tenant_id, message_id),
    FOREIGN KEY (tenant_id, parent_run_id) REFERENCES agent.runs(tenant_id, id),
    FOREIGN KEY (tenant_id, requested_by_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, remote_agent_id) REFERENCES agent.remote_agents(tenant_id, id)
);

CREATE INDEX remote_a2a_jobs_member_idx
    ON agent.remote_a2a_jobs (tenant_id, requested_by_member_id, created_at DESC);

CREATE TABLE audit.remote_a2a_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    remote_agent_id uuid NOT NULL,
    job_id uuid,
    actor_member_id uuid,
    event_type text NOT NULL CHECK (event_type IN (
        'registered', 'verified', 'verification_failed', 'enabled', 'disabled',
        'queued', 'submitted', 'completed', 'failed', 'rejected', 'unknown'
    )),
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, remote_agent_id) REFERENCES agent.remote_agents(tenant_id, id),
    FOREIGN KEY (tenant_id, job_id) REFERENCES agent.remote_a2a_jobs(tenant_id, id),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id)
);

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id, source_operation,
    risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:agent.remote_delegate:1:' || id::text)::uuid, id,
       'agent.remote_delegate', '1', 'Delegate to registered A2A Agent',
       'Send one bounded text task to an explicitly registered and verified A2A 1.0 remote Agent.',
       'core', 'agent-runtime', 'agent.remote_delegate', 'external_side_effect', ARRAY['agent:remote_delegate'],
       'keyed', 'reconcile_first', 30000, 'passive',
       ARRAY['remote', 'a2a', 'delegate', '远程', '委派'],
       ARRAY['让已注册的远程研究 Agent 分析这个问题'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"target_agent_slug":{"maxLength":63,"minLength":1,"pattern":"^[a-z][a-z0-9-]{0,62}$","type":"string"},"task":{"maxLength":4000,"minLength":1,"type":"string"}},"required":["target_agent_slug","task"],"type":"object"}'::jsonb,
       'sha256:8900764ffa74f9f48b206c2078af49c45477760290a77603a681c749c163167c'
FROM identity.tenants;
