CREATE SCHEMA IF NOT EXISTS channel;

CREATE TABLE channel.telegram_principals (
    telegram_user_id bigint PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    member_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id),
    UNIQUE (tenant_id, member_id)
);
CREATE TABLE channel.telegram_chats (
    telegram_chat_id bigint PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    session_type integer NOT NULL CHECK (session_type IN (1, 2)),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE channel.telegram_offsets (
    telegram_bot_id bigint PRIMARY KEY,
    next_update_id bigint NOT NULL DEFAULT 0 CHECK (next_update_id >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE integration.ingress_messages
    ADD COLUMN source_channel text NOT NULL DEFAULT 'openim'
        CHECK (source_channel IN ('openim', 'telegram')),
    ADD COLUMN principal_member_id uuid;

UPDATE integration.ingress_messages AS i
SET principal_member_id = l.member_id
FROM identity.identity_links AS l
WHERE i.tenant_id = l.tenant_id
  AND i.sender_id = l.openim_user_id
  AND l.provisioning_state = 'ready';

ALTER TABLE integration.ingress_messages
    ADD CONSTRAINT ingress_messages_principal_fk
    FOREIGN KEY (tenant_id, principal_member_id)
    REFERENCES identity.members(tenant_id, id);

ALTER TABLE agent.runs
    ADD COLUMN source_channel text NOT NULL DEFAULT 'openim'
        CHECK (source_channel IN ('openim', 'telegram'));

ALTER TABLE agent.runs DROP CONSTRAINT runs_state_check;
ALTER TABLE agent.runs ADD CONSTRAINT runs_state_check
    CHECK (state IN (
        'queued', 'running', 'reply_pending', 'delivery_pending', 'delivery_unknown',
        'waiting_approval', 'succeeded', 'failed'
    ));

CREATE TABLE agent.deliveries (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES agent.runs(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    channel text NOT NULL CHECK (channel IN ('openim', 'telegram')),
    target_id text NOT NULL CHECK (length(target_id) BETWEEN 1 AND 256),
    session_type integer NOT NULL CHECK (session_type IN (1, 2)),
    content text NOT NULL CHECK (length(content) BETWEEN 1 AND 20000),
    waiting_approval boolean NOT NULL DEFAULT false,
    state text NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'sending', 'sent', 'uncertain', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    external_message_id text,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state <> 'sending' AND lease_token IS NULL AND lease_until IS NULL)
    ),
    CHECK (state <> 'sent' OR external_message_id IS NOT NULL),
    UNIQUE (tenant_id, id)
);

CREATE INDEX agent_deliveries_ready_idx
    ON agent.deliveries (available_at, created_at)
    WHERE state IN ('pending', 'sending');

CREATE TABLE audit.delivery_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL REFERENCES agent.runs(id),
    delivery_id uuid NOT NULL REFERENCES agent.deliveries(id),
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
