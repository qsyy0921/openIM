CREATE SCHEMA IF NOT EXISTS action;
CREATE SCHEMA IF NOT EXISTS collaboration;
CREATE SCHEMA IF NOT EXISTS audit;

ALTER TABLE agent.runs
    ADD COLUMN action_type text,
    ADD COLUMN action_title text;

ALTER TABLE agent.runs DROP CONSTRAINT runs_state_check;
ALTER TABLE agent.runs ADD CONSTRAINT runs_state_check
    CHECK (state IN ('queued', 'running', 'reply_pending', 'waiting_approval', 'succeeded', 'failed'));

CREATE TABLE action.intents (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES agent.runs(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    requested_by uuid NOT NULL REFERENCES identity.members(id),
    action_type text NOT NULL CHECK (action_type = 'create_ticket'),
    payload jsonb NOT NULL,
    payload_digest text NOT NULL,
    state text NOT NULL DEFAULT 'pending_approval'
        CHECK (state IN ('pending_approval', 'approved', 'executing', 'succeeded', 'failed', 'expired')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, requested_by) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE action.approvals (
    id uuid PRIMARY KEY,
    intent_id uuid NOT NULL UNIQUE REFERENCES action.intents(id),
    tenant_id uuid NOT NULL,
    approved_by uuid NOT NULL,
    payload_digest text NOT NULL,
    decision text NOT NULL CHECK (decision = 'approved'),
    decided_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, approved_by) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, intent_id) REFERENCES action.intents(tenant_id, id)
);

CREATE TABLE action.executions (
    id uuid PRIMARY KEY,
    intent_id uuid NOT NULL UNIQUE REFERENCES action.intents(id),
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    idempotency_key text NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'unknown', 'succeeded', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    ticket_id uuid,
    receipt jsonb,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((state = 'running' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state <> 'running' AND lease_token IS NULL AND lease_until IS NULL))
);

CREATE INDEX action_executions_ready_idx ON action.executions (available_at, created_at)
    WHERE state IN ('queued', 'running', 'unknown');

CREATE TABLE collaboration.tickets (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_by uuid NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, created_by) REFERENCES identity.members(tenant_id, id)
);

ALTER TABLE action.executions ADD CONSTRAINT executions_ticket_fk
    FOREIGN KEY (ticket_id) REFERENCES collaboration.tickets(id);

CREATE TABLE audit.action_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    intent_id uuid NOT NULL REFERENCES action.intents(id),
    execution_id uuid REFERENCES action.executions(id),
    actor_member_id uuid,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
