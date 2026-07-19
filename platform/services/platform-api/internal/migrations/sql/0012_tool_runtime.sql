CREATE TABLE capability.member_grants (
    tenant_id uuid NOT NULL,
    member_id uuid NOT NULL,
    permission text NOT NULL CHECK (permission ~ '^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$'),
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, member_id, permission),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id) ON DELETE CASCADE
);

INSERT INTO capability.member_grants (tenant_id, member_id, permission)
SELECT tenant_id, id, permission
FROM identity.members
CROSS JOIN (VALUES ('knowledge:read'), ('ticket:create')) AS defaults(permission)
WHERE status = 'active';

CREATE TABLE agent.tool_calls (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL REFERENCES agent.runs(id) ON DELETE CASCADE,
    call_id text NOT NULL CHECK (call_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    tool_id uuid NOT NULL,
    capability_snapshot_id text NOT NULL,
    arguments jsonb NOT NULL CHECK (jsonb_typeof(arguments) = 'object'),
    arguments_digest text NOT NULL CHECK (arguments_digest ~ '^sha256:[0-9a-f]{64}$'),
    idempotency_key text NOT NULL,
    policy_decision text NOT NULL CHECK (policy_decision IN ('allow', 'deny', 'require_approval')),
    policy_reason text NOT NULL,
    state text NOT NULL CHECK (state IN (
        'prepared', 'denied', 'waiting_approval', 'executing', 'unknown', 'succeeded', 'failed'
    )),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    result jsonb,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (run_id, call_id),
    UNIQUE (idempotency_key),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, tool_id) REFERENCES capability.tool_descriptors(tenant_id, id),
    FOREIGN KEY (tenant_id, capability_snapshot_id) REFERENCES capability.snapshots(tenant_id, id),
    CHECK (state <> 'succeeded' OR result IS NOT NULL)
);

CREATE TABLE agent.tool_approvals (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    tool_call_id uuid NOT NULL UNIQUE REFERENCES agent.tool_calls(id) ON DELETE CASCADE,
    arguments_digest text NOT NULL CHECK (arguments_digest ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN ('requested', 'approved', 'rejected', 'expired')),
    requested_by uuid NOT NULL,
    decided_by uuid,
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, requested_by) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, decided_by) REFERENCES identity.members(tenant_id, id),
    CHECK ((state = 'requested' AND decided_by IS NULL AND decided_at IS NULL)
        OR (state <> 'requested' AND decided_by IS NOT NULL AND decided_at IS NOT NULL))
);

CREATE TABLE audit.tool_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL REFERENCES agent.runs(id) ON DELETE CASCADE,
    tool_call_id uuid NOT NULL REFERENCES agent.tool_calls(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    actor_member_id uuid,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE INDEX tool_events_call_idx ON audit.tool_events (tool_call_id, id);
