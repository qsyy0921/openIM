\if :{?platform_oidc_issuer}
\else
\echo 'platform_oidc_issuer is required'
\quit
\endif

INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'tenant-local',
    'Xinglan Development Tenant',
    'active'
)
ON CONFLICT (id) DO UPDATE
SET display_name = EXCLUDED.display_name,
    status = EXCLUDED.status;

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    source_operation, risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:enterprise.knowledge.search:1:' || id::text)::uuid, id,
       'enterprise.knowledge.search', '1', 'Enterprise knowledge search',
       'Search only enterprise knowledge authorized for the current member and purpose.',
       'core', 'platform-retrieval', 'enterprise.knowledge.search', 'read', ARRAY['knowledge:read'],
       'native', 'safe', 10000, 'passive',
       ARRAY['knowledge', 'policy', 'procedure', 'document', '知识', '制度', '流程', '文档'],
       ARRAY['查询公司的报销制度', '查找发布流程文档'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"limit":{"maximum":8,"minimum":1,"type":"integer"},"query":{"maxLength":2000,"minLength":1,"type":"string"}},"required":["query"],"type":"object"}'::jsonb,
       'sha256:0c4e49dd7d99b838c21ef44a64454a8154a6123edb27b7adfc625a582890c3ae'
FROM identity.tenants
WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
ON CONFLICT (tenant_id, operation_id, version) DO NOTHING;

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    source_operation, risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
    parameter_terms, examples, output_kinds, input_schema, schema_digest
)
SELECT md5('tool:collaboration.ticket.create:1:' || id::text)::uuid, id,
       'collaboration.ticket.create', '1', 'Create collaboration ticket',
       'Create one digest-bound collaboration ticket after explicit enterprise approval.',
       'core', 'action-executor', 'collaboration.ticket.create', 'write', ARRAY['ticket:create'],
       'keyed', 'reconcile_first', 10000, 'passive',
       ARRAY['ticket', 'issue', 'incident', '工单', '问题'],
       ARRAY['创建工单：复核迁移计划'], ARRAY['data', 'text'],
       '{"additionalProperties":false,"properties":{"title":{"maxLength":200,"minLength":1,"type":"string"}},"required":["title"],"type":"object"}'::jsonb,
       'sha256:2d6102b1b02a9f47ec9801476a8e4a2a1427acc542b3c0ab74eb4d97cc26e166'
FROM identity.tenants
WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
ON CONFLICT (tenant_id, operation_id, version) DO NOTHING;

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
FROM identity.tenants
WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
ON CONFLICT (tenant_id, operation_id, version) DO NOTHING;

INSERT INTO capability.tool_descriptors (
    id, tenant_id, operation_id, version, name, summary, source_type, source_id,
    source_operation, risk, permissions, idempotency, retry_semantics, timeout_ms, audience,
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
FROM identity.tenants
WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
ON CONFLICT (tenant_id, operation_id, version) DO NOTHING;

INSERT INTO capability.snapshots (tenant_id, id, schema_version, payload)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
    1,
    '{"schema_version":1,"tools":[{"operation_id":"collaboration.ticket.create","version":"1"},{"operation_id":"enterprise.knowledge.search","version":"1"}]}'::jsonb
)
ON CONFLICT (tenant_id, id) DO NOTHING;

INSERT INTO capability.snapshot_tools (tenant_id, snapshot_id, tool_id, ordinal)
VALUES
    (
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
        md5('tool:collaboration.ticket.create:1:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
        0
    ),
    (
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        'capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b',
        md5('tool:enterprise.knowledge.search:1:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
        1
    )
ON CONFLICT DO NOTHING;

INSERT INTO identity.members (
    id, tenant_id, issuer, subject, display_name, status
)
VALUES (
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    :'platform_oidc_issuer',
    '11111111-1111-4111-8111-111111111111',
    'Xinglan Local Member',
    'active'
)
ON CONFLICT (id) DO UPDATE
SET issuer = EXCLUDED.issuer,
    display_name = EXCLUDED.display_name,
    status = EXCLUDED.status;

INSERT INTO identity.member_devices (
    member_id, device_id, platform_id, status
)
VALUES (
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'ubuntu-web',
    5,
    'active'
)
ON CONFLICT (member_id, device_id, platform_id) DO UPDATE
SET status = EXCLUDED.status;

INSERT INTO capability.member_grants (tenant_id, member_id, permission)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'knowledge:read'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'ticket:create')
ON CONFLICT DO NOTHING;

INSERT INTO identity.member_roles (tenant_id, member_id, role, granted_by_member_id)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'platform_admin',
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'
)
ON CONFLICT DO NOTHING;

INSERT INTO agent.definitions (
    id, tenant_id, slug, display_name, description, status
)
VALUES (
    md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'knowledge-agent',
    'Enterprise Agent',
    'Enterprise knowledge and governed ticket actions',
    'active'
)
ON CONFLICT (tenant_id, slug) DO NOTHING;

INSERT INTO agent.versions (
    id, tenant_id, agent_id, version_number, spec_schema_version, spec, spec_checksum
)
VALUES (
    md5('agent-version-v1:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    1,
    1,
    '{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and abstain when evidence is absent.","model_route":"gpt-5.6-terra","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}'::jsonb,
    'sha256:471923cdf65b51bc84007b761fea1488990405a4f1a1a03fa62dced6afdd5cfe'
)
ON CONFLICT (agent_id, version_number) DO NOTHING;

INSERT INTO agent.deployments (
    id, tenant_id, agent_id, slot, active_version_id
)
VALUES (
    md5('agent-deployment-production:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'production',
    md5('agent-version-v1:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid
)
ON CONFLICT (tenant_id, agent_id, slot) DO NOTHING;

INSERT INTO agent.triggers (
    id, tenant_id, agent_id, trigger_type, trigger_value, enabled
)
VALUES (
    md5('agent-trigger-at-agent:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
    'mention_alias',
    '@agent',
    true
)
ON CONFLICT (tenant_id, trigger_type, trigger_value) DO UPDATE
SET enabled = true;

INSERT INTO agent.triggers (id, tenant_id, agent_id, trigger_type, trigger_value, enabled)
VALUES
    (
        md5('agent-trigger-arxiv:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
        'system_source',
        'source:arxiv',
        true
    ),
    (
        md5('agent-trigger-delegate:' || md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid::text)::uuid,
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        md5('agent-definition:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')::uuid,
        'internal_delegate',
        'delegate:knowledge-agent',
        true
    )
ON CONFLICT (tenant_id, trigger_type, trigger_value) DO UPDATE
SET enabled = true;
