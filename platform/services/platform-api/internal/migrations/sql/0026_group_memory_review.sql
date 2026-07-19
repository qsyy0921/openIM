CREATE TABLE memory.group_fact_proposals (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    source_channel text NOT NULL CHECK (source_channel IN ('openim', 'telegram')),
    conversation_id text NOT NULL,
    source_run_id uuid NOT NULL,
    proposed_by_member_id uuid NOT NULL,
    fact_key text NOT NULL,
    category text NOT NULL CHECK (category IN ('preference', 'profile', 'procedure', 'context')),
    content text NOT NULL CHECK (length(content) BETWEEN 1 AND 4000),
    checksum text NOT NULL CHECK (checksum ~ '^sha256:[0-9a-f]{64}$'),
    confidence double precision NOT NULL CHECK (confidence BETWEEN 0.8 AND 1),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected')),
    reviewed_by_member_id uuid,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source_run_id, fact_key),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, source_run_id) REFERENCES agent.runs(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, proposed_by_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, reviewed_by_member_id) REFERENCES identity.members(tenant_id, id),
    CHECK ((state = 'pending' AND reviewed_by_member_id IS NULL AND reviewed_at IS NULL)
        OR (state IN ('approved', 'rejected') AND reviewed_by_member_id IS NOT NULL AND reviewed_at IS NOT NULL))
);

CREATE INDEX memory_group_fact_proposals_scope_idx
    ON memory.group_fact_proposals (tenant_id, source_channel, conversation_id, state, created_at DESC);

CREATE TABLE audit.group_memory_review_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    proposal_id uuid NOT NULL,
    actor_member_id uuid NOT NULL,
    decision text NOT NULL CHECK (decision IN ('approve', 'reject')),
    memory_event_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, proposal_id),
    FOREIGN KEY (tenant_id, proposal_id) REFERENCES memory.group_fact_proposals(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, actor_member_id) REFERENCES identity.members(tenant_id, id),
    FOREIGN KEY (tenant_id, memory_event_id) REFERENCES memory.events(tenant_id, id)
);
