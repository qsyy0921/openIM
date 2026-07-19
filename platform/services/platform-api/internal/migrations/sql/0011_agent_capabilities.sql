CREATE SCHEMA capability;

CREATE TABLE capability.tool_descriptors (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    operation_id text NOT NULL CHECK (operation_id ~ '^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$'),
    version text NOT NULL CHECK (version ~ '^[1-9][0-9]{0,8}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    summary text NOT NULL CHECK (length(summary) BETWEEN 1 AND 512),
    source_type text NOT NULL CHECK (source_type IN ('core', 'plugin', 'mcp')),
    source_id text NOT NULL CHECK (length(source_id) BETWEEN 1 AND 256),
    risk text NOT NULL CHECK (risk IN ('read', 'write', 'external_side_effect', 'privileged')),
    permissions text[] NOT NULL,
    idempotency text NOT NULL CHECK (idempotency IN ('native', 'keyed', 'none', 'unknown')),
    retry_semantics text NOT NULL CHECK (retry_semantics IN ('safe', 'reconcile_first', 'never')),
    timeout_ms integer NOT NULL CHECK (timeout_ms BETWEEN 1 AND 300000),
    audience text NOT NULL CHECK (audience IN ('passive', 'proactive_source', 'internal', 'admin')),
    parameter_terms text[] NOT NULL CHECK (cardinality(parameter_terms) BETWEEN 1 AND 32),
    examples text[] NOT NULL CHECK (cardinality(examples) BETWEEN 1 AND 8),
    output_kinds text[] NOT NULL CHECK (cardinality(output_kinds) BETWEEN 1 AND 5),
    input_schema jsonb NOT NULL CHECK (jsonb_typeof(input_schema) = 'object'),
    schema_digest text NOT NULL CHECK (schema_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, operation_id, version)
);

CREATE TABLE capability.snapshots (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    id text NOT NULL CHECK (id ~ '^capability-v1:[0-9a-f]{64}$'),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE capability.snapshot_tools (
    tenant_id uuid NOT NULL,
    snapshot_id text NOT NULL,
    tool_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (tenant_id, snapshot_id, tool_id),
    UNIQUE (tenant_id, snapshot_id, ordinal),
    FOREIGN KEY (tenant_id, snapshot_id) REFERENCES capability.snapshots(tenant_id, id),
    FOREIGN KEY (tenant_id, tool_id) REFERENCES capability.tool_descriptors(tenant_id, id)
);

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:enterprise.knowledge.search:1:' || id::text)::uuid, id,
       'enterprise.knowledge.search', '1', 'Enterprise knowledge search',
       'Search only enterprise knowledge authorized for the current member and purpose.',
       'core', 'platform-retrieval', 'read', ARRAY['knowledge:read'],
       'native', 'safe', 10000, 'passive',
       ARRAY['knowledge', 'policy', 'procedure', 'document', '知识', '制度', '流程', '文档'],
       ARRAY['查询公司的报销制度', '查找发布流程文档'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"limit":{"maximum":8,"minimum":1,"type":"integer"},"query":{"maxLength":2000,"minLength":1,"type":"string"}},"required":["query"],"type":"object"}'::jsonb,
       'sha256:0c4e49dd7d99b838c21ef44a64454a8154a6123edb27b7adfc625a582890c3ae'
FROM identity.tenants;

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:collaboration.ticket.create:1:' || id::text)::uuid, id,
       'collaboration.ticket.create', '1', 'Create collaboration ticket',
       'Create one digest-bound collaboration ticket after explicit enterprise approval.',
       'core', 'action-executor', 'write', ARRAY['ticket:create'],
       'keyed', 'reconcile_first', 10000, 'passive',
       ARRAY['ticket', 'issue', 'incident', '工单', '问题'],
       ARRAY['创建工单：复核迁移计划'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"title":{"maxLength":200,"minLength":1,"type":"string"}},"required":["title"],"type":"object"}'::jsonb,
       'sha256:2d6102b1b02a9f47ec9801476a8e4a2a1427acc542b3c0ab74eb4d97cc26e166'
FROM identity.tenants;

INSERT INTO capability.snapshots (tenant_id, id, schema_version, payload)
SELECT id,
       'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
       1,
       '{"schema_version":1,"tools":[{"operation_id":"collaboration.ticket.create","version":"1"},{"operation_id":"enterprise.knowledge.search","version":"1"}]}'::jsonb
FROM identity.tenants;

INSERT INTO capability.snapshot_tools (tenant_id, snapshot_id, tool_id, ordinal)
SELECT id, 'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
       md5('tool:collaboration.ticket.create:1:' || id::text)::uuid, 0
FROM identity.tenants;

INSERT INTO capability.snapshot_tools (tenant_id, snapshot_id, tool_id, ordinal)
SELECT id, 'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
       md5('tool:enterprise.knowledge.search:1:' || id::text)::uuid, 1
FROM identity.tenants;

ALTER TABLE agent.versions
    ADD COLUMN capability_snapshot_id text NOT NULL
        DEFAULT 'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
    ADD CONSTRAINT versions_capability_snapshot_fk
        FOREIGN KEY (tenant_id, capability_snapshot_id)
        REFERENCES capability.snapshots(tenant_id, id);

ALTER TABLE agent.runs
    ADD COLUMN capability_snapshot_id text NOT NULL
        DEFAULT 'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
    ADD CONSTRAINT runs_capability_snapshot_fk
        FOREIGN KEY (tenant_id, capability_snapshot_id)
        REFERENCES capability.snapshots(tenant_id, id);

CREATE OR REPLACE FUNCTION capability.reject_immutable_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'published capability records are immutable';
END;
$$;

CREATE TRIGGER tool_descriptors_immutable_update
BEFORE UPDATE OR DELETE ON capability.tool_descriptors
FOR EACH ROW EXECUTE FUNCTION capability.reject_immutable_mutation();

CREATE TRIGGER capability_snapshots_immutable_update
BEFORE UPDATE OR DELETE ON capability.snapshots
FOR EACH ROW EXECUTE FUNCTION capability.reject_immutable_mutation();

CREATE TRIGGER capability_snapshot_tools_immutable_update
BEFORE UPDATE OR DELETE ON capability.snapshot_tools
FOR EACH ROW EXECUTE FUNCTION capability.reject_immutable_mutation();
