ALTER TABLE capability.skills
    ADD CONSTRAINT skills_tool_operations_bound
        CHECK (cardinality(tool_operations) BETWEEN 0 AND 32);

CREATE TABLE agent.version_skills (
    tenant_id uuid NOT NULL REFERENCES identity.tenants(id),
    agent_id uuid NOT NULL,
    agent_version_id uuid NOT NULL,
    skill_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 31),
    PRIMARY KEY (tenant_id, agent_version_id, skill_id),
    UNIQUE (tenant_id, agent_version_id, ordinal),
    FOREIGN KEY (tenant_id, agent_id, agent_version_id)
        REFERENCES agent.versions(tenant_id, agent_id, id),
    FOREIGN KEY (tenant_id, skill_id)
        REFERENCES capability.skills(tenant_id, id)
);

CREATE TRIGGER agent_version_skills_immutable
BEFORE UPDATE OR DELETE ON agent.version_skills
FOR EACH ROW EXECUTE FUNCTION capability.reject_immutable_mutation();
