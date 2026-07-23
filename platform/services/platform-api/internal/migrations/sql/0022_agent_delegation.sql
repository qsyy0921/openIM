ALTER TABLE agent.triggers DROP CONSTRAINT triggers_trigger_type_check;
ALTER TABLE agent.triggers DROP CONSTRAINT triggers_trigger_value_check;
ALTER TABLE agent.triggers
    ADD CONSTRAINT triggers_trigger_type_check
        CHECK (trigger_type IN ('mention_alias', 'system_source', 'internal_delegate')),
    ADD CONSTRAINT triggers_trigger_value_check
        CHECK (
            (trigger_type = 'mention_alias' AND trigger_value ~ '^@[a-z][a-z0-9_-]{0,62}$')
            OR (trigger_type = 'system_source' AND trigger_value ~ '^source:[a-z][a-z0-9_-]{0,62}$')
            OR (trigger_type = 'internal_delegate' AND trigger_value ~ '^delegate:[a-z][a-z0-9-]{0,62}$')
        );

INSERT INTO agent.triggers (id, tenant_id, agent_id, trigger_type, trigger_value, enabled)
SELECT md5('agent-trigger-delegate:' || definition.id::text)::uuid,
       definition.tenant_id, definition.id, 'internal_delegate',
       'delegate:' || definition.slug, definition.status = 'active'
FROM agent.definitions AS definition
ON CONFLICT (tenant_id, trigger_type, trigger_value) DO NOTHING;

CREATE TABLE agent.delegations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    parent_run_id uuid NOT NULL,
    child_run_id uuid NOT NULL,
    requested_by_member_id uuid NOT NULL,
    target_agent_id uuid NOT NULL,
    task text NOT NULL CHECK (length(task) BETWEEN 1 AND 4000),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'completed', 'failed', 'unknown')),
    result_checksum text CHECK (result_checksum IS NULL OR result_checksum ~ '^sha256:[0-9a-f]{64}$'),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, child_run_id),
    UNIQUE (tenant_id, parent_run_id, idempotency_key),
    FOREIGN KEY (tenant_id, parent_run_id) REFERENCES agent.runs(tenant_id, id),
    FOREIGN KEY (tenant_id, child_run_id) REFERENCES agent.runs(tenant_id, id),
    FOREIGN KEY (tenant_id, requested_by_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, target_agent_id) REFERENCES agent.definitions(tenant_id, id)
);

CREATE INDEX agent_delegations_member_idx
    ON agent.delegations (tenant_id, requested_by_member_id, created_at DESC);

CREATE TABLE audit.agent_delegation_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    delegation_id uuid NOT NULL,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, delegation_id)
        REFERENCES agent.delegations(tenant_id, id) ON DELETE CASCADE
);

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    source_operation, risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:agent.delegate:1:' || id::text)::uuid, id,
       'agent.delegate', '1', 'Delegate to enterprise Agent',
       'Queue one bounded background task for another published Agent and deliver its result to the originating conversation.',
       'core', 'agent-runtime', 'agent.delegate', 'write', ARRAY['agent:delegate'],
       'keyed', 'reconcile_first', 10000, 'passive',
       ARRAY['delegate', 'background', 'specialist', '委派', '后台', '专家'],
       ARRAY['让研究 Agent 调查这个问题', '把有界分析任务交给另一个 Agent'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"target_agent_slug":{"maxLength":63,"minLength":1,"pattern":"^[a-z][a-z0-9-]{0,62}$","type":"string"},"task":{"maxLength":4000,"minLength":1,"type":"string"}},"required":["target_agent_slug","task"],"type":"object"}'::jsonb,
       'sha256:8900764ffa74f9f48b206c2078af49c45477760290a77603a681c749c163167c'
FROM identity.tenants;
