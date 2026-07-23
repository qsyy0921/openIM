CREATE EXTENSION IF NOT EXISTS vector WITH VERSION '0.8.5';

DO $$
DECLARE
    installed_version text;
BEGIN
    SELECT extversion INTO installed_version
    FROM pg_extension
    WHERE extname = 'vector';

    IF installed_version IS DISTINCT FROM '0.8.5' THEN
        RAISE EXCEPTION 'pgvector 0.8.5 is required, found %', COALESCE(installed_version, 'missing');
    END IF;
END
$$;

ALTER TABLE knowledge.document_versions
    ADD COLUMN ingestion_state text NOT NULL DEFAULT 'legacy_indexed'
        CHECK (ingestion_state IN (
            'legacy_indexed',
            'uploading',
            'queued',
            'processing',
            'indexed',
            'failed'
        )),
    ADD COLUMN ingestion_failure_code text,
    ADD COLUMN ingestion_failure_detail text,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT document_versions_ingestion_failure_check CHECK (
        (ingestion_state = 'failed'
            AND length(ingestion_failure_code) BETWEEN 1 AND 64
            AND length(ingestion_failure_detail) BETWEEN 1 AND 512)
        OR
        (ingestion_state <> 'failed'
            AND ingestion_failure_code IS NULL
            AND ingestion_failure_detail IS NULL)
    );

ALTER TABLE knowledge.document_versions
    ALTER COLUMN ingestion_state SET DEFAULT 'uploading';

ALTER TABLE knowledge.chunks
    ADD CONSTRAINT chunks_tenant_id_id_unique UNIQUE (tenant_id, id);

ALTER TABLE agent.run_citations
    ADD COLUMN authorized_excerpt text,
    ADD CONSTRAINT run_citations_authorized_excerpt_check CHECK (
        authorized_excerpt IS NULL
        OR char_length(authorized_excerpt) BETWEEN 1 AND 1200
    );

CREATE TABLE knowledge.document_source_objects (
    version_id uuid PRIMARY KEY REFERENCES knowledge.document_versions(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    document_id uuid NOT NULL,
    bucket text NOT NULL CHECK (length(bucket) BETWEEN 3 AND 63),
    object_key text NOT NULL CHECK (length(object_key) BETWEEN 1 AND 512),
    original_filename text NOT NULL CHECK (length(original_filename) BETWEEN 1 AND 255),
    media_type text NOT NULL CHECK (length(media_type) BETWEEN 1 AND 160),
    source_format text NOT NULL CHECK (source_format IN ('markdown', 'text', 'pdf', 'docx')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 33554432),
    checksum text NOT NULL CHECK (checksum ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN (
        'reserved',
        'stored',
        'delete_pending',
        'deleted',
        'delete_failed'
    )),
    stored_at timestamptz,
    cleanup_attempts integer NOT NULL DEFAULT 0 CHECK (cleanup_attempts BETWEEN 0 AND 8),
    cleanup_max_attempts integer NOT NULL DEFAULT 3 CHECK (cleanup_max_attempts BETWEEN 1 AND 8),
    cleanup_available_at timestamptz,
    cleanup_lease_owner text,
    cleanup_lease_token uuid,
    cleanup_lease_expires_at timestamptz,
    cleanup_failure_detail text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bucket, object_key),
    FOREIGN KEY (tenant_id, document_id, version_id)
        REFERENCES knowledge.document_versions(tenant_id, document_id, id) ON DELETE CASCADE,
    CHECK (
        (state = 'reserved' AND stored_at IS NULL)
        OR (state IN ('stored', 'delete_pending', 'delete_failed') AND stored_at IS NOT NULL)
        OR (state = 'deleted' AND stored_at IS NULL)
    ),
    CHECK (
        (state = 'delete_pending'
            AND cleanup_available_at IS NOT NULL
            AND (cleanup_failure_detail IS NULL
                OR length(cleanup_failure_detail) BETWEEN 1 AND 512)
            AND (
                (cleanup_lease_owner IS NULL
                    AND cleanup_lease_token IS NULL
                    AND cleanup_lease_expires_at IS NULL)
                OR
                (length(cleanup_lease_owner) BETWEEN 1 AND 128
                    AND cleanup_lease_token IS NOT NULL
                    AND cleanup_lease_expires_at IS NOT NULL)
            ))
        OR
        (state = 'delete_failed'
            AND cleanup_available_at IS NULL
            AND cleanup_lease_owner IS NULL
            AND cleanup_lease_token IS NULL
            AND cleanup_lease_expires_at IS NULL
            AND length(cleanup_failure_detail) BETWEEN 1 AND 512)
        OR
        (state IN ('reserved', 'stored', 'deleted')
            AND cleanup_available_at IS NULL
            AND cleanup_lease_owner IS NULL
            AND cleanup_lease_token IS NULL
            AND cleanup_lease_expires_at IS NULL
            AND cleanup_failure_detail IS NULL)
    )
);

CREATE INDEX document_source_objects_tenant_document_idx
    ON knowledge.document_source_objects (tenant_id, document_id, version_id);

CREATE INDEX document_source_objects_cleanup_claim_idx
    ON knowledge.document_source_objects (cleanup_available_at, updated_at, version_id)
    WHERE state = 'delete_pending';

CREATE TABLE knowledge.upload_idempotency (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    key_digest text NOT NULL CHECK (key_digest ~ '^sha256:[0-9a-f]{64}$'),
    request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    document_id uuid NOT NULL,
    version_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key_digest),
    FOREIGN KEY (tenant_id, document_id, version_id)
        REFERENCES knowledge.document_versions(tenant_id, document_id, id) ON DELETE CASCADE
);

CREATE TABLE knowledge.ingestion_jobs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    document_id uuid NOT NULL,
    version_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('queued', 'leased', 'retryable', 'succeeded', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 8),
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_owner text,
    lease_token uuid,
    lease_expires_at timestamptz,
    parser_revision text NOT NULL CHECK (length(parser_revision) BETWEEN 1 AND 128),
    embedding_revision text NOT NULL CHECK (length(embedding_revision) BETWEEN 1 AND 256),
    embedding_dimension integer NOT NULL CHECK (embedding_dimension = 2560),
    chunk_count integer CHECK (chunk_count > 0),
    vector_count integer CHECK (vector_count > 0),
    failure_code text,
    failure_detail text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, version_id),
    FOREIGN KEY (tenant_id, document_id, version_id)
        REFERENCES knowledge.document_versions(tenant_id, document_id, id) ON DELETE CASCADE,
    CHECK (
        (state = 'leased'
            AND length(lease_owner) BETWEEN 1 AND 128
            AND lease_token IS NOT NULL
            AND lease_expires_at IS NOT NULL)
        OR
        (state <> 'leased'
            AND lease_owner IS NULL
            AND lease_token IS NULL
            AND lease_expires_at IS NULL)
    ),
    CHECK (
        (state = 'succeeded'
            AND chunk_count > 0
            AND vector_count = chunk_count
            AND failure_code IS NULL
            AND failure_detail IS NULL)
        OR
        (state = 'failed'
            AND length(failure_code) BETWEEN 1 AND 64
            AND length(failure_detail) BETWEEN 1 AND 512)
        OR state IN ('queued', 'leased', 'retryable')
    )
);

CREATE INDEX ingestion_jobs_claim_idx
    ON knowledge.ingestion_jobs (available_at, created_at, id)
    WHERE state IN ('queued', 'retryable', 'leased');

CREATE TABLE knowledge.index_generations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    model_revision text NOT NULL CHECK (length(model_revision) BETWEEN 1 AND 256),
    dimension integer NOT NULL CHECK (dimension = 2560),
    storage_type text NOT NULL CHECK (storage_type = 'halfvec'),
    distance_metric text NOT NULL CHECK (distance_metric = 'cosine'),
    state text NOT NULL CHECK (state IN ('building', 'ready', 'active', 'retired', 'failed')),
    expected_chunk_count integer NOT NULL DEFAULT 0 CHECK (expected_chunk_count >= 0),
    indexed_chunk_count integer NOT NULL DEFAULT 0 CHECK (indexed_chunk_count >= 0),
    activated_at timestamptz,
    failure_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, model_revision, id),
    CHECK (
        (state = 'active'
            AND activated_at IS NOT NULL
            AND indexed_chunk_count = expected_chunk_count)
        OR state <> 'active'
    ),
    CHECK (
        (state = 'failed' AND length(failure_code) BETWEEN 1 AND 64)
        OR (state <> 'failed' AND failure_code IS NULL)
    )
);

CREATE UNIQUE INDEX index_generations_one_active_per_tenant_idx
    ON knowledge.index_generations (tenant_id)
    WHERE state = 'active';

CREATE INDEX index_generations_model_state_idx
    ON knowledge.index_generations (tenant_id, model_revision, state);

CREATE TABLE knowledge.chunk_search_indexes (
    generation_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    chunk_id uuid NOT NULL,
    model_revision text NOT NULL,
    dimension integer NOT NULL CHECK (dimension = 2560),
    content_checksum text NOT NULL CHECK (content_checksum ~ '^sha256:[0-9a-f]{64}$'),
    embedding halfvec(2560) NOT NULL,
    search_vector tsvector NOT NULL,
    normalized boolean NOT NULL CHECK (normalized),
    indexed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (generation_id, chunk_id),
    FOREIGN KEY (tenant_id, generation_id)
        REFERENCES knowledge.index_generations(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, chunk_id)
        REFERENCES knowledge.chunks(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, model_revision, generation_id)
        REFERENCES knowledge.index_generations(tenant_id, model_revision, id) ON DELETE CASCADE,
    CHECK (vector_dims(embedding) = dimension),
    CHECK (l2_norm(embedding) > 0)
);

CREATE INDEX chunk_search_indexes_lookup_idx
    ON knowledge.chunk_search_indexes (tenant_id, generation_id, chunk_id);

CREATE INDEX chunk_search_indexes_fts_idx
    ON knowledge.chunk_search_indexes USING gin (search_vector);

CREATE INDEX chunk_search_indexes_embedding_hnsw_idx
    ON knowledge.chunk_search_indexes
    USING hnsw (embedding halfvec_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX document_grants_member_document_idx
    ON authz.document_grants (tenant_id, member_id, document_id)
    WHERE permission = 'read';

CREATE INDEX documents_current_published_idx
    ON knowledge.documents (tenant_id, current_version_id, id)
    WHERE status = 'active' AND current_version_id IS NOT NULL;

CREATE TABLE knowledge.evaluation_runs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    dataset_revision text NOT NULL CHECK (length(dataset_revision) BETWEEN 1 AND 256),
    dataset_digest text NOT NULL CHECK (dataset_digest ~ '^sha256:[0-9a-f]{64}$'),
    application_commit text NOT NULL CHECK (application_commit ~ '^[0-9a-f]{7,64}$'),
    embedding_revision text NOT NULL CHECK (length(embedding_revision) BETWEEN 1 AND 256),
    reranker_revision text NOT NULL CHECK (length(reranker_revision) BETWEEN 1 AND 256),
    generation_model text NOT NULL CHECK (length(generation_model) BETWEEN 1 AND 128),
    state text NOT NULL CHECK (state IN ('running', 'passed', 'failed')),
    thresholds jsonb NOT NULL CHECK (jsonb_typeof(thresholds) = 'object'),
    metrics jsonb,
    failure_case_ids text[] NOT NULL DEFAULT '{}',
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (state = 'running' AND metrics IS NULL AND completed_at IS NULL)
        OR (state IN ('passed', 'failed') AND jsonb_typeof(metrics) = 'object' AND completed_at IS NOT NULL)
    )
);

CREATE INDEX evaluation_runs_started_idx
    ON knowledge.evaluation_runs (started_at DESC, id);

CREATE TABLE audit.knowledge_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    actor_member_id uuid REFERENCES identity.members(id),
    document_id uuid,
    version_id uuid,
    job_id uuid,
    event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 64),
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_events_tenant_time_idx
    ON audit.knowledge_events (tenant_id, occurred_at DESC, id DESC);
