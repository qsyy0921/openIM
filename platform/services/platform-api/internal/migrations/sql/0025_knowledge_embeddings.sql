CREATE TABLE knowledge.chunk_embeddings (
    chunk_id uuid NOT NULL REFERENCES knowledge.chunks(id) ON DELETE CASCADE,
    model_revision text NOT NULL CHECK (length(model_revision) BETWEEN 1 AND 256),
    dimension integer NOT NULL CHECK (dimension BETWEEN 8 AND 8192),
    content_checksum text NOT NULL CHECK (length(content_checksum) BETWEEN 1 AND 256),
    embedding real[] NOT NULL,
    normalized boolean NOT NULL,
    indexed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chunk_id, model_revision),
    CHECK (cardinality(embedding) = dimension)
);

CREATE INDEX knowledge_chunk_embeddings_model_idx
    ON knowledge.chunk_embeddings (model_revision, chunk_id);
