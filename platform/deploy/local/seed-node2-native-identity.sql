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
    '{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and abstain when evidence is absent.","model_route":"deepseek-v4-pro","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}'::jsonb,
    'sha256:27dcf2cfab60cb915d584127e1524bcda821ce29c8383346adb8f8d2b31bb8d8'
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
