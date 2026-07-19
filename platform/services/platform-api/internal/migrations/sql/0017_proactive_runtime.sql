ALTER TABLE agent.triggers DROP CONSTRAINT triggers_trigger_type_check;
ALTER TABLE agent.triggers DROP CONSTRAINT triggers_trigger_value_check;
ALTER TABLE agent.triggers
    ADD CONSTRAINT triggers_trigger_type_check
        CHECK (trigger_type IN ('mention_alias', 'system_source')),
    ADD CONSTRAINT triggers_trigger_value_check
        CHECK (
            (trigger_type = 'mention_alias' AND trigger_value ~ '^@[a-z][a-z0-9_-]{0,62}$')
            OR (trigger_type = 'system_source' AND trigger_value ~ '^source:[a-z][a-z0-9_-]{0,62}$')
        );

INSERT INTO agent.triggers (id, tenant_id, agent_id, trigger_type, trigger_value, enabled)
SELECT md5('agent-trigger-arxiv:' || tenant_id::text)::uuid,
       tenant_id, id, 'system_source', 'source:arxiv', status = 'active'
FROM agent.definitions
WHERE slug = 'knowledge-agent'
ON CONFLICT (tenant_id, trigger_type, trigger_value) DO NOTHING;

CREATE SCHEMA proactive;

CREATE TABLE proactive.preferences (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    member_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    timezone text NOT NULL DEFAULT 'Asia/Shanghai',
    quiet_start time NOT NULL DEFAULT '22:00',
    quiet_end time NOT NULL DEFAULT '08:00',
    daily_budget integer NOT NULL DEFAULT 5 CHECK (daily_budget BETWEEN 0 AND 50),
    minimum_score double precision NOT NULL DEFAULT 0.55
        CHECK (minimum_score BETWEEN 0 AND 1),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, member_id),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE proactive.subscriptions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    member_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    source_type text NOT NULL CHECK (source_type = 'arxiv'),
    query text NOT NULL CHECK (length(query) BETWEEN 1 AND 500),
    categories text[] NOT NULL DEFAULT '{}',
    source_channel text NOT NULL CHECK (source_channel IN ('openim', 'telegram')),
    target_id text NOT NULL CHECK (length(target_id) BETWEEN 1 AND 256),
    enabled boolean NOT NULL DEFAULT true,
    poll_interval_seconds integer NOT NULL DEFAULT 1800
        CHECK (poll_interval_seconds BETWEEN 300 AND 86400),
    next_poll_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    baseline_complete boolean NOT NULL DEFAULT false,
    etag text,
    last_modified text,
    last_success_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, member_id, source_type, query, source_channel, target_id),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, agent_id) REFERENCES agent.definitions(tenant_id, id),
    CHECK ((lease_token IS NULL AND lease_until IS NULL) OR (lease_token IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE INDEX proactive_subscriptions_due_idx
    ON proactive.subscriptions (next_poll_at, created_at)
    WHERE enabled;

CREATE TABLE proactive.source_events (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    subscription_id uuid NOT NULL,
    external_id text NOT NULL CHECK (length(external_id) BETWEEN 1 AND 256),
    external_version integer NOT NULL CHECK (external_version >= 1),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 1000),
    summary text NOT NULL CHECK (length(summary) BETWEEN 1 AND 20000),
    source_url text NOT NULL CHECK (length(source_url) BETWEEN 1 AND 2000),
    published_at timestamptz NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    state text NOT NULL CHECK (state IN (
        'baseline', 'pending', 'ranking', 'ranked', 'dispatching', 'enqueued', 'delivered',
        'acknowledged', 'suppressed', 'failed'
    )),
    relevance_score double precision CHECK (relevance_score BETWEEN 0 AND 1),
    rank_reasons text[] NOT NULL DEFAULT '{}',
    suppression_reason text,
    run_id uuid,
    lease_token text,
    lease_until timestamptz,
    rank_attempts integer NOT NULL DEFAULT 0 CHECK (rank_attempts >= 0),
    dispatch_attempts integer NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (tenant_id, id),
    UNIQUE (subscription_id, external_id, external_version),
    FOREIGN KEY (tenant_id, subscription_id) REFERENCES proactive.subscriptions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, run_id) REFERENCES agent.runs(tenant_id, id),
    CHECK (
        (state IN ('ranking', 'dispatching') AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state NOT IN ('ranking', 'dispatching') AND lease_token IS NULL AND lease_until IS NULL)
    )
);

CREATE INDEX proactive_source_events_ready_idx
    ON proactive.source_events (available_at, created_at)
    WHERE state IN ('pending', 'ranking', 'ranked', 'dispatching', 'enqueued');

CREATE TABLE proactive.feedback (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    event_id uuid NOT NULL,
    member_id uuid NOT NULL,
    signal text NOT NULL CHECK (signal IN ('interesting', 'not_interesting', 'dismissed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, event_id),
    FOREIGN KEY (tenant_id, event_id) REFERENCES proactive.source_events(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE TABLE proactive.memory_exposures (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    event_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    fact_checksum text NOT NULL CHECK (fact_checksum ~ '^sha256:[0-9a-f]{64}$'),
    content_snapshot text NOT NULL CHECK (length(content_snapshot) BETWEEN 1 AND 4000),
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 7),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, event_id, fact_id),
    UNIQUE (event_id, ordinal),
    FOREIGN KEY (tenant_id, event_id) REFERENCES proactive.source_events(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, fact_id) REFERENCES memory.facts(tenant_id, id)
);

CREATE TABLE audit.proactive_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    source_event_id uuid NOT NULL,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, source_event_id)
        REFERENCES proactive.source_events(tenant_id, id) ON DELETE CASCADE
);
