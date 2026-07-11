-- Local-only knowledge fixture. This file is not part of the production migration chain.
BEGIN;

INSERT INTO knowledge.documents (
    id, tenant_id, title, source_uri, classification, status
) VALUES (
    'dddddddd-dddd-4ddd-8ddd-dddddddddddd',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'OpenIM 平台本地运行规范',
    'local://knowledge/openim-runtime-policy',
    'internal',
    'active'
) ON CONFLICT (id) DO NOTHING;

INSERT INTO knowledge.document_versions (
    id, tenant_id, document_id, version_number, checksum, status, published_at
) VALUES (
    'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'dddddddd-dddd-4ddd-8ddd-dddddddddddd',
    1,
    'sha256:local-openim-runtime-policy-v1',
    'published',
    now()
) ON CONFLICT (id) DO NOTHING;

INSERT INTO knowledge.chunks (
    id, tenant_id, document_id, version_id, ordinal, content, checksum
) VALUES (
    'ffffffff-ffff-4fff-8fff-ffffffffffff',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'dddddddd-dddd-4ddd-8ddd-dddddddddddd',
    'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee',
    0,
    'OpenIM 平台采用本机优先开发。PostgreSQL、Keycloak 和 OpenIM 依赖运行在 swe-docker WSL 中，应用进程运行在 Windows；迁移到服务器时使用版本化镜像、配置和数据库迁移，不复制本地数据卷。',
    'sha256:local-openim-runtime-policy-v1-chunk-0'
) ON CONFLICT (id) DO NOTHING;

UPDATE knowledge.documents
SET current_version_id = 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee'
WHERE id = 'dddddddd-dddd-4ddd-8ddd-dddddddddddd';

INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'dddddddd-dddd-4ddd-8ddd-dddddddddddd',
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'read'
) ON CONFLICT DO NOTHING;

COMMIT;
