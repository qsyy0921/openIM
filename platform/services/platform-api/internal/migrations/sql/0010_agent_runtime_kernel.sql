CREATE TABLE agent.conversation_lanes (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    source_channel text NOT NULL CHECK (source_channel IN ('openim', 'telegram')),
    conversation_id text NOT NULL,
    next_enqueue_sequence bigint NOT NULL DEFAULT 1 CHECK (next_enqueue_sequence >= 1),
    next_dispatch_sequence bigint NOT NULL DEFAULT 1 CHECK (next_dispatch_sequence >= 1),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, source_channel, conversation_id),
    CHECK (next_dispatch_sequence <= next_enqueue_sequence)
);

ALTER TABLE agent.runs
    ADD COLUMN conversation_sequence bigint,
    ADD COLUMN execution_plane text NOT NULL DEFAULT 'passive'
        CHECK (execution_plane IN ('passive', 'proactive_source', 'internal', 'admin')),
    ADD COLUMN trace_id text;

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY tenant_id, source_channel, conversation_id
               ORDER BY created_at, id
           ) AS sequence_number
    FROM agent.runs
)
UPDATE agent.runs AS r
SET conversation_sequence = ranked.sequence_number,
    trace_id = 'agent-run:' || r.id::text
FROM ranked
WHERE ranked.id = r.id;

INSERT INTO agent.conversation_lanes (
    tenant_id, source_channel, conversation_id, next_enqueue_sequence, next_dispatch_sequence
)
SELECT tenant_id, source_channel, conversation_id,
       max(conversation_sequence) + 1,
       COALESCE(
           min(conversation_sequence) FILTER (
               WHERE state NOT IN ('succeeded', 'failed', 'waiting_approval', 'delivery_unknown')
           ),
           max(conversation_sequence) + 1
       )
FROM agent.runs
GROUP BY tenant_id, source_channel, conversation_id;

ALTER TABLE agent.runs
    ALTER COLUMN conversation_sequence SET NOT NULL,
    ALTER COLUMN trace_id SET NOT NULL,
    ADD CONSTRAINT runs_conversation_sequence_unique
        UNIQUE (tenant_id, source_channel, conversation_id, conversation_sequence),
    ADD CONSTRAINT runs_conversation_sequence_positive
        CHECK (conversation_sequence >= 1),
    ADD CONSTRAINT runs_trace_id_nonempty
        CHECK (length(trace_id) BETWEEN 1 AND 256);

CREATE INDEX agent_runs_conversation_dispatch_idx
    ON agent.runs (tenant_id, source_channel, conversation_id, conversation_sequence);

CREATE TABLE audit.agent_run_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL REFERENCES agent.runs(id) ON DELETE CASCADE,
    trace_id text NOT NULL,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX agent_run_events_trace_idx
    ON audit.agent_run_events (trace_id, id);
