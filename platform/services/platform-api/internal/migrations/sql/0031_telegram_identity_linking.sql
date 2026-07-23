CREATE TABLE channel.telegram_link_challenges (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    member_id uuid NOT NULL,
    code_digest text NOT NULL UNIQUE CHECK (code_digest ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    telegram_user_id bigint,
    telegram_chat_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id),
    CHECK (expires_at > created_at),
    CHECK (consumed_at IS NULL OR revoked_at IS NULL),
    CHECK (
        (consumed_at IS NULL AND telegram_user_id IS NULL AND telegram_chat_id IS NULL)
        OR (consumed_at IS NOT NULL AND telegram_user_id IS NOT NULL AND telegram_chat_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX telegram_link_challenges_one_live_member_idx
    ON channel.telegram_link_challenges (tenant_id, member_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX telegram_link_challenges_member_status_idx
    ON channel.telegram_link_challenges (tenant_id, member_id, created_at DESC);

CREATE TABLE audit.telegram_link_events (
    id bigserial PRIMARY KEY,
    challenge_id uuid NOT NULL REFERENCES channel.telegram_link_challenges(id),
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    member_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('issued', 'revoked', 'consumed')),
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id)
);

CREATE INDEX telegram_link_events_member_idx
    ON audit.telegram_link_events (tenant_id, member_id, created_at DESC);
