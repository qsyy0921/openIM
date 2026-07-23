CREATE TABLE capability.skills (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    skill_id text NOT NULL CHECK (skill_id ~ '^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$'),
    version text NOT NULL CHECK (version ~ '^[1-9][0-9]{0,8}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    summary text NOT NULL CHECK (length(summary) BETWEEN 1 AND 512),
    instructions text NOT NULL CHECK (length(instructions) BETWEEN 1 AND 20000),
    tool_operations text[] NOT NULL,
    audience text NOT NULL CHECK (audience IN ('passive', 'proactive_source', 'internal', 'admin')),
    content_digest text NOT NULL CHECK (content_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, skill_id, version)
);

CREATE TABLE capability.mcp_servers (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    slug text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,62}$'),
    transport text NOT NULL CHECK (transport = 'stdio'),
    command text NOT NULL CHECK (length(command) BETWEEN 1 AND 1024),
    arguments jsonb NOT NULL CHECK (jsonb_typeof(arguments) = 'array'),
    working_directory text,
    environment_keys text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT false,
    config_digest text NOT NULL CHECK (config_digest ~ '^sha256:[0-9a-f]{64}$'),
    expected_tool_catalog_digest text NOT NULL CHECK (expected_tool_catalog_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, slug)
);

CREATE TABLE capability.mcp_health (
    tenant_id uuid NOT NULL,
    server_id uuid NOT NULL,
    instance_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('starting', 'healthy', 'degraded', 'stopped')),
    tool_catalog_digest text,
    restart_count integer NOT NULL DEFAULT 0 CHECK (restart_count >= 0),
    last_error_code text,
    checked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, server_id),
    FOREIGN KEY (tenant_id, server_id) REFERENCES capability.mcp_servers(tenant_id, id) ON DELETE CASCADE
);

CREATE TRIGGER skills_immutable_update
BEFORE UPDATE OR DELETE ON capability.skills
FOR EACH ROW EXECUTE FUNCTION capability.reject_immutable_mutation();
