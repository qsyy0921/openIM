ALTER TABLE knowledge.documents
    ADD CONSTRAINT documents_tenant_id_id_unique UNIQUE (tenant_id, id);

ALTER TABLE knowledge.document_versions
    ADD CONSTRAINT document_versions_tenant_document_id_unique UNIQUE (tenant_id, document_id, id),
    ADD CONSTRAINT document_versions_tenant_document_fk
        FOREIGN KEY (tenant_id, document_id)
        REFERENCES knowledge.documents(tenant_id, id);

ALTER TABLE knowledge.chunks
    ADD CONSTRAINT chunks_tenant_document_version_fk
        FOREIGN KEY (tenant_id, document_id, version_id)
        REFERENCES knowledge.document_versions(tenant_id, document_id, id);

ALTER TABLE authz.document_grants
    ADD CONSTRAINT document_grants_tenant_document_fk
        FOREIGN KEY (tenant_id, document_id)
        REFERENCES knowledge.documents(tenant_id, id),
    ADD CONSTRAINT document_grants_tenant_member_fk
        FOREIGN KEY (tenant_id, member_id)
        REFERENCES identity.members(tenant_id, id);
