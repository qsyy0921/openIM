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
