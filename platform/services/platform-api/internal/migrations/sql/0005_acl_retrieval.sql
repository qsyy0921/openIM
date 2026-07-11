CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE SCHEMA IF NOT EXISTS authz;

CREATE TABLE knowledge.documents (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    title text NOT NULL,
    source_uri text NOT NULL,
    classification text NOT NULL CHECK (classification IN ('public', 'internal', 'confidential', 'restricted')),
    current_version_id uuid,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source_uri)
);

CREATE TABLE knowledge.document_versions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    version_number integer NOT NULL CHECK (version_number > 0),
    checksum text NOT NULL,
    status text NOT NULL CHECK (status IN ('draft', 'published', 'superseded')),
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, version_number),
    UNIQUE (document_id, id),
    CHECK ((status = 'published' AND published_at IS NOT NULL) OR status <> 'published')
);

ALTER TABLE knowledge.documents
    ADD CONSTRAINT documents_current_version_fk
    FOREIGN KEY (id, current_version_id)
    REFERENCES knowledge.document_versions(document_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge.chunks (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    document_id uuid NOT NULL,
    version_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    content text NOT NULL CHECK (length(content) BETWEEN 1 AND 16000),
    checksum text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (document_id, version_id)
        REFERENCES knowledge.document_versions(document_id, id) ON DELETE CASCADE,
    UNIQUE (version_id, ordinal)
);

CREATE INDEX knowledge_chunks_document_idx ON knowledge.chunks (tenant_id, document_id, version_id);

CREATE TABLE authz.document_grants (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    member_id uuid NOT NULL REFERENCES identity.members(id) ON DELETE CASCADE,
    permission text NOT NULL CHECK (permission = 'read'),
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, document_id, member_id, permission)
);

ALTER TABLE agent.runs ADD COLUMN principal_member_id uuid REFERENCES identity.members(id);

UPDATE agent.runs AS r
SET principal_member_id = l.member_id
FROM identity.identity_links AS l
WHERE l.tenant_id = r.tenant_id
  AND l.openim_user_id = r.sender_id
  AND l.provisioning_state = 'ready'
  AND r.principal_member_id IS NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM agent.runs WHERE principal_member_id IS NULL) THEN
        RAISE EXCEPTION 'cannot migrate Agent Run without a ready human IdentityLink';
    END IF;
END $$;

ALTER TABLE agent.runs ALTER COLUMN principal_member_id SET NOT NULL;

CREATE TABLE agent.run_citations (
    run_id uuid NOT NULL REFERENCES agent.runs(id) ON DELETE CASCADE,
    citation_id text NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    document_id uuid NOT NULL REFERENCES knowledge.documents(id),
    version_id uuid NOT NULL REFERENCES knowledge.document_versions(id),
    chunk_id uuid NOT NULL REFERENCES knowledge.chunks(id),
    title text NOT NULL,
    source_uri text NOT NULL,
    checksum text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, citation_id),
    UNIQUE (run_id, ordinal)
);
