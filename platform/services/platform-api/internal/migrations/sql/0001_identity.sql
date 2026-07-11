CREATE SCHEMA IF NOT EXISTS identity;

CREATE TABLE identity.tenants (
    id uuid PRIMARY KEY,
    external_id text NOT NULL UNIQUE,
    display_name text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE identity.members (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    issuer text NOT NULL,
    subject text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, issuer, subject)
);

CREATE INDEX members_federated_identity_idx
    ON identity.members (issuer, subject);

CREATE TABLE identity.member_devices (
    member_id uuid NOT NULL REFERENCES identity.members(id) ON DELETE CASCADE,
    device_id text NOT NULL CHECK (length(device_id) BETWEEN 1 AND 128),
    platform_id integer NOT NULL CHECK (platform_id BETWEEN 1 AND 11 AND platform_id <> 10),
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (member_id, device_id, platform_id)
);

CREATE TABLE identity.identity_links (
    member_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    openim_user_id text NOT NULL UNIQUE,
    provisioning_state text NOT NULL CHECK (
        provisioning_state IN ('pending', 'provisioning', 'ready')
    ),
    lease_until timestamptz,
    lease_token text,
    provisioned_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, member_id)
        REFERENCES identity.members(tenant_id, id) ON DELETE CASCADE,
    CHECK (
        (provisioning_state = 'provisioning' AND lease_until IS NOT NULL AND lease_token IS NOT NULL)
        OR (provisioning_state <> 'provisioning' AND lease_until IS NULL AND lease_token IS NULL)
    )
);
