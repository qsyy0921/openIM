CREATE TABLE memory.extraction_jobs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    run_id uuid NOT NULL,
    member_id uuid NOT NULL,
    state text NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'extracting', 'projection_pending', 'projecting', 'succeeded', 'failed')),
    model_attempts integer NOT NULL DEFAULT 0 CHECK (model_attempts >= 0),
    projection_attempts integer NOT NULL DEFAULT 0 CHECK (projection_attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    lease_until timestamptz,
    extracted_facts jsonb,
    provider_response_id text,
    result_checksum text,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, run_id),
    FOREIGN KEY (tenant_id, run_id) REFERENCES agent.runs(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity.members(tenant_id, id),
    CHECK (
        (state IN ('extracting', 'projecting') AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state NOT IN ('extracting', 'projecting') AND lease_token IS NULL AND lease_until IS NULL)
    ),
    CHECK (
        (state IN ('projection_pending', 'projecting', 'succeeded') AND extracted_facts IS NOT NULL
            AND jsonb_typeof(extracted_facts) = 'array'
            AND provider_response_id IS NOT NULL
            AND result_checksum ~ '^sha256:[0-9a-f]{64}$')
        OR (state NOT IN ('projection_pending', 'projecting', 'succeeded'))
    )
);

CREATE INDEX memory_extraction_jobs_ready_idx
    ON memory.extraction_jobs (available_at, created_at)
    WHERE state IN ('pending', 'extracting', 'projection_pending', 'projecting');

CREATE TABLE audit.memory_extraction_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    job_id uuid NOT NULL,
    run_id uuid NOT NULL,
    event_type text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, job_id) REFERENCES memory.extraction_jobs(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, run_id) REFERENCES agent.runs(tenant_id, id) ON DELETE CASCADE
);
