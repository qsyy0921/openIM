CREATE SCHEMA memory;

ALTER TABLE agent.runs
    ADD CONSTRAINT runs_tenant_id_id_unique UNIQUE (tenant_id, id),
    ADD CONSTRAINT runs_tenant_principal_member_fk
        FOREIGN KEY (tenant_id, principal_member_id) REFERENCES identity.members(tenant_id, id);

CREATE TABLE memory.streams (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    scope_type text NOT NULL CHECK (scope_type IN ('personal', 'group')),
    owner_member_id uuid,
    source_channel text,
    conversation_id text,
    next_sequence bigint NOT NULL DEFAULT 1 CHECK (next_sequence >= 1),
    projected_sequence bigint NOT NULL DEFAULT 0 CHECK (projected_sequence >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, scope_type, owner_member_id),
    UNIQUE (tenant_id, scope_type, source_channel, conversation_id),
    FOREIGN KEY (tenant_id, owner_member_id) REFERENCES identity.members(tenant_id, id),
    CHECK (projected_sequence < next_sequence),
    CHECK (
        (scope_type = 'personal' AND owner_member_id IS NOT NULL AND source_channel IS NULL AND conversation_id IS NULL)
        OR (scope_type = 'group' AND owner_member_id IS NULL AND source_channel IN ('openim', 'telegram') AND conversation_id IS NOT NULL)
    )
);

CREATE TABLE memory.events (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    stream_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence >= 1),
    event_type text NOT NULL CHECK (event_type IN ('fact_upserted', 'fact_deleted', 'stream_erased')),
    fact_key text,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    source_run_id uuid,
    idempotency_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (stream_id, sequence),
    UNIQUE (tenant_id, idempotency_key),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, stream_id) REFERENCES memory.streams(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, source_run_id) REFERENCES agent.runs(tenant_id, id),
    CHECK ((event_type IN ('fact_upserted', 'fact_deleted') AND fact_key IS NOT NULL)
        OR (event_type = 'stream_erased' AND fact_key IS NULL))
);

CREATE TABLE memory.facts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    stream_id uuid NOT NULL,
    fact_key text NOT NULL,
    category text NOT NULL CHECK (category IN ('preference', 'profile', 'procedure', 'context')),
    content text NOT NULL CHECK (length(content) BETWEEN 1 AND 4000),
    source_event_id uuid NOT NULL,
    source_run_id uuid,
    checksum text NOT NULL CHECK (checksum ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN ('active', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (stream_id, fact_key),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, stream_id) REFERENCES memory.streams(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, source_event_id) REFERENCES memory.events(tenant_id, id),
    FOREIGN KEY (tenant_id, source_run_id) REFERENCES agent.runs(tenant_id, id)
);

CREATE INDEX memory_facts_active_idx ON memory.facts (tenant_id, stream_id, fact_key)
WHERE state = 'active';

CREATE TABLE memory.exposures (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    fact_checksum text NOT NULL CHECK (fact_checksum ~ '^sha256:[0-9a-f]{64}$'),
    content_snapshot text NOT NULL CHECK (length(content_snapshot) BETWEEN 1 AND 4000),
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    retrieval_reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, fact_id),
    UNIQUE (run_id, ordinal),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, run_id) REFERENCES agent.runs(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, fact_id) REFERENCES memory.facts(tenant_id, id)
);

CREATE TABLE memory.feedback (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    exposure_id uuid NOT NULL,
    actor_member_id uuid NOT NULL,
    signal text NOT NULL CHECK (signal IN ('helpful', 'not_helpful', 'incorrect')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, exposure_id),
    FOREIGN KEY (tenant_id, exposure_id) REFERENCES memory.exposures(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE audit.memory_projector_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    stream_id uuid NOT NULL,
    memory_event_id uuid,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, stream_id) REFERENCES memory.streams(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, memory_event_id) REFERENCES memory.events(tenant_id, id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION memory.reject_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'memory events are append-only';
END;
$$;

CREATE TRIGGER memory_events_immutable
BEFORE UPDATE OR DELETE ON memory.events
FOR EACH ROW EXECUTE FUNCTION memory.reject_event_mutation();
