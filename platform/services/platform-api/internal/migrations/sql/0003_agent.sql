CREATE SCHEMA IF NOT EXISTS agent;

CREATE TABLE agent.runs (
    id uuid PRIMARY KEY,
    source_event_id text NOT NULL UNIQUE,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    conversation_id text NOT NULL,
    sender_id text NOT NULL,
    session_type integer NOT NULL CHECK (session_type IN (1, 2)),
    prompt text NOT NULL,
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'reply_pending', 'succeeded', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    candidate_text text,
    model text,
    provider_response_id text,
    reply_server_msg_id text,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (state = 'running' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state <> 'running' AND lease_token IS NULL AND lease_until IS NULL)
    ),
    CHECK (state <> 'reply_pending' OR candidate_text IS NOT NULL),
    CHECK (state <> 'succeeded' OR reply_server_msg_id IS NOT NULL)
);

CREATE INDEX agent_runs_ready_idx
    ON agent.runs (available_at, created_at)
    WHERE state IN ('queued', 'running', 'reply_pending');

CREATE TABLE agent.event_rejections (
    source_topic text NOT NULL,
    source_partition integer NOT NULL,
    source_offset bigint NOT NULL,
    event_id text,
    reason text NOT NULL,
    rejected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_topic, source_partition, source_offset)
);
