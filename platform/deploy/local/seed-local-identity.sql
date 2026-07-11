-- Local-only seed matching keycloak/realm-platform.json.
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'tenant-local',
    'Local Development Tenant',
    'active'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO identity.members (
    id, tenant_id, issuer, subject, display_name, status
)
VALUES (
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'http://127.0.0.1:18081/realms/platform',
    '11111111-1111-4111-8111-111111111111',
    'Local Member',
    'active'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO identity.member_devices (
    member_id, device_id, platform_id, status
)
VALUES (
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    'local-browser',
    5,
    'active'
)
ON CONFLICT (member_id, device_id, platform_id) DO NOTHING;
