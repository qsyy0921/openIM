ALTER TABLE knowledge.ingestion_jobs
    ADD COLUMN projection_revision text;

UPDATE knowledge.ingestion_jobs
SET projection_revision = 'chunk-content-v1';

ALTER TABLE knowledge.ingestion_jobs
    ALTER COLUMN projection_revision SET NOT NULL,
    ADD CONSTRAINT ingestion_jobs_projection_revision_check
        CHECK (projection_revision ~ '^[a-z0-9][a-z0-9._-]{0,127}$');

ALTER TABLE knowledge.index_generations
    ADD COLUMN projection_revision text;

UPDATE knowledge.index_generations
SET projection_revision = 'chunk-content-v1';

ALTER TABLE knowledge.index_generations
    ALTER COLUMN projection_revision SET NOT NULL,
    ADD CONSTRAINT index_generations_projection_revision_check
        CHECK (projection_revision ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    ADD CONSTRAINT index_generations_projection_identity_key
        UNIQUE (tenant_id, model_revision, projection_revision, id);

DROP INDEX knowledge.index_generations_model_state_idx;

CREATE INDEX index_generations_model_state_idx
    ON knowledge.index_generations
       (tenant_id, model_revision, projection_revision, state);

ALTER TABLE knowledge.chunk_search_indexes
    ADD COLUMN projection_revision text;

UPDATE knowledge.chunk_search_indexes
SET projection_revision = 'chunk-content-v1';

ALTER TABLE knowledge.chunk_search_indexes
    ALTER COLUMN projection_revision SET NOT NULL,
    ADD CONSTRAINT chunk_search_indexes_projection_revision_check
        CHECK (projection_revision ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    ADD CONSTRAINT chunk_search_indexes_projection_generation_fkey
        FOREIGN KEY (tenant_id, model_revision, projection_revision, generation_id)
        REFERENCES knowledge.index_generations
            (tenant_id, model_revision, projection_revision, id)
        ON DELETE CASCADE;

CREATE INDEX chunk_search_indexes_projection_lookup_idx
    ON knowledge.chunk_search_indexes
       (tenant_id, generation_id, projection_revision, chunk_id);

ALTER TABLE knowledge.evaluation_runs
    ADD COLUMN projection_revision text;

UPDATE knowledge.evaluation_runs
SET projection_revision = 'chunk-content-v1';

ALTER TABLE knowledge.evaluation_runs
    ALTER COLUMN projection_revision SET NOT NULL,
    ADD CONSTRAINT evaluation_runs_projection_revision_check
        CHECK (projection_revision ~ '^[a-z0-9][a-z0-9._-]{0,127}$');
