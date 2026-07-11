CREATE SCHEMA IF NOT EXISTS integration;

CREATE TABLE integration.ingress_messages (
    source_key text PRIMARY KEY,
    event_id text NOT NULL UNIQUE,
    source_topic text NOT NULL,
    source_partition integer NOT NULL,
    source_offset bigint NOT NULL,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    conversation_id text NOT NULL,
    server_msg_id text NOT NULL UNIQUE,
    client_msg_id text NOT NULL,
    sender_id text NOT NULL,
    session_type integer NOT NULL,
    content_type integer NOT NULL,
    content text NOT NULL,
    event_payload jsonb NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_topic, source_partition, source_offset)
);

CREATE TABLE integration.ingress_rejections (
    source_topic text NOT NULL,
    source_partition integer NOT NULL,
    source_offset bigint NOT NULL,
    server_msg_id text,
    sender_id text,
    reason text NOT NULL,
    rejected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_topic, source_partition, source_offset)
);

CREATE TABLE integration.outbox_events (
    event_id text PRIMARY KEY REFERENCES integration.ingress_messages(event_id) ON DELETE CASCADE,
    event_type text NOT NULL,
    event_key text NOT NULL,
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'publishing', 'published')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    published_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (state = 'publishing' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state <> 'publishing' AND lease_token IS NULL AND lease_until IS NULL)
    )
);

CREATE INDEX outbox_ready_idx
    ON integration.outbox_events (available_at, created_at)
    WHERE state IN ('pending', 'publishing');
