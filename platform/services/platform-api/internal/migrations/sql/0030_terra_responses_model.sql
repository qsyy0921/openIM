DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM agent.deployments AS deployment
        JOIN agent.versions AS version
          ON version.tenant_id = deployment.tenant_id
         AND version.agent_id = deployment.agent_id
         AND version.id = deployment.active_version_id
        WHERE deployment.slot = 'production'
          AND version.spec ->> 'model_route' = 'gpt-5.6-luna'
          AND version.spec_checksum <> 'sha256:3264d3cfe96c5d3aada40ccf8be8440b478d8c72c883fac7b78d09df9c1e8cc4'
    ) THEN
        RAISE EXCEPTION 'custom active Luna Agent version requires explicit publication before model migration';
    END IF;
END $$;

INSERT INTO agent.versions (
    id, tenant_id, agent_id, version_number, spec_schema_version, spec,
    spec_checksum, created_by_member_id, capability_snapshot_id
)
SELECT md5('agent-version-gpt-5.6-terra:' || active.agent_id::text)::uuid,
       active.tenant_id,
       active.agent_id,
       (SELECT max(existing.version_number) + 1
        FROM agent.versions AS existing
        WHERE existing.tenant_id = active.tenant_id AND existing.agent_id = active.agent_id),
       1,
       '{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and abstain when evidence is absent.","model_route":"gpt-5.6-terra","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}'::jsonb,
       'sha256:471923cdf65b51bc84007b761fea1488990405a4f1a1a03fa62dced6afdd5cfe',
       active.created_by_member_id,
       active.capability_snapshot_id
FROM agent.deployments AS deployment
JOIN agent.versions AS active
  ON active.tenant_id = deployment.tenant_id
 AND active.agent_id = deployment.agent_id
 AND active.id = deployment.active_version_id
WHERE deployment.slot = 'production'
  AND active.spec_checksum = 'sha256:3264d3cfe96c5d3aada40ccf8be8440b478d8c72c883fac7b78d09df9c1e8cc4'
ON CONFLICT (agent_id, spec_checksum) DO NOTHING;

INSERT INTO agent.version_skills (tenant_id, agent_id, agent_version_id, skill_id, ordinal)
SELECT deployment.tenant_id,
       deployment.agent_id,
       replacement.id,
       link.skill_id,
       link.ordinal
FROM agent.deployments AS deployment
JOIN agent.versions AS active
  ON active.tenant_id = deployment.tenant_id
 AND active.agent_id = deployment.agent_id
 AND active.id = deployment.active_version_id
JOIN agent.versions AS replacement
  ON replacement.tenant_id = deployment.tenant_id
 AND replacement.agent_id = deployment.agent_id
 AND replacement.spec_checksum = 'sha256:471923cdf65b51bc84007b761fea1488990405a4f1a1a03fa62dced6afdd5cfe'
JOIN agent.version_skills AS link
  ON link.tenant_id = active.tenant_id
 AND link.agent_version_id = active.id
WHERE deployment.slot = 'production'
  AND active.spec_checksum = 'sha256:3264d3cfe96c5d3aada40ccf8be8440b478d8c72c883fac7b78d09df9c1e8cc4'
ON CONFLICT DO NOTHING;

INSERT INTO audit.agent_catalog_events (
    tenant_id, agent_id, deployment_id, actor_member_id, event_type, old_version_id, new_version_id
)
SELECT deployment.tenant_id,
       deployment.agent_id,
       deployment.id,
       NULL,
       'deployment_activated',
       deployment.active_version_id,
       replacement.id
FROM agent.deployments AS deployment
JOIN agent.versions AS active
  ON active.tenant_id = deployment.tenant_id
 AND active.agent_id = deployment.agent_id
 AND active.id = deployment.active_version_id
JOIN agent.versions AS replacement
  ON replacement.tenant_id = deployment.tenant_id
 AND replacement.agent_id = deployment.agent_id
 AND replacement.spec_checksum = 'sha256:471923cdf65b51bc84007b761fea1488990405a4f1a1a03fa62dced6afdd5cfe'
WHERE deployment.slot = 'production'
  AND active.spec_checksum = 'sha256:3264d3cfe96c5d3aada40ccf8be8440b478d8c72c883fac7b78d09df9c1e8cc4';

UPDATE agent.deployments AS deployment
SET active_version_id = replacement.id,
    activated_at = now(),
    revision = deployment.revision + 1,
    updated_at = now()
FROM agent.versions AS active,
     agent.versions AS replacement
WHERE deployment.slot = 'production'
  AND active.tenant_id = deployment.tenant_id
  AND active.agent_id = deployment.agent_id
  AND active.id = deployment.active_version_id
  AND active.spec_checksum = 'sha256:3264d3cfe96c5d3aada40ccf8be8440b478d8c72c883fac7b78d09df9c1e8cc4'
  AND replacement.tenant_id = deployment.tenant_id
  AND replacement.agent_id = deployment.agent_id
  AND replacement.spec_checksum = 'sha256:471923cdf65b51bc84007b761fea1488990405a4f1a1a03fa62dced6afdd5cfe';
