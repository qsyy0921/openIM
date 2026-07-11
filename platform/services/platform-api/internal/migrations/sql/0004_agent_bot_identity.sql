CREATE TABLE agent.bot_identities (
    tenant_id uuid PRIMARY KEY REFERENCES identity.tenants(id),
    openim_user_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
