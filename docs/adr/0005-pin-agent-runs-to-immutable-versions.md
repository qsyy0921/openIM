# ADR-0005: Pin every Agent Run to an immutable Agent version

- Status: Proposed
- Date: 2026-07-12

## Context

The verified Agent path currently has one hard-coded `@agent` trigger and one effective configuration. A Run records the source event, prompt, model result, citations, action state, and OpenIM reply, but it does not identify an Agent definition or immutable configuration version. Reading the latest configuration when a queued Run starts would make replay, rollback, audit, and incident analysis ambiguous.

## Decision

Introduce three separate concepts:

1. `AgentDefinition` is a tenant-owned stable identity and lifecycle record.
2. `AgentVersion` is an immutable, complete, validated execution specification.
3. `AgentDeployment` is a mutable named pointer, initially only `production`, to one published version.

Trigger resolution and Run insertion occur in one PostgreSQL transaction. The new Run stores `agent_id`, `agent_version_id`, `deployment_id`, `trigger_id`, and the version checksum. Workers load the exact stored version and never resolve the current deployment again.

Publishing creates a new version; it never updates an existing version. Rollback changes only the deployment pointer and affects only Runs inserted after that transaction. No missing definition, deployment, version, or checksum may fall back to a default Agent.

The first admitted runtime kind is the existing bounded `knowledge_ticket_v1` path. Generic tools, skills, memory, arbitrary workflows, per-Agent Bot users, and administration UI are not part of this decision.

## Consequences

- Historical Runs remain explainable and replayable after configuration changes.
- A deployment rollback is fast and does not mutate or delete history.
- Database migration must seed and backfill the current implicit Agent before adding non-null constraints.
- The Agent Runtime gains a catalog lookup and spec validation dependency.
- Published versions consume additional rows and cannot be edited in place.
- Emergency disabling of new invocations is separate from execution versioning; already pinned Runs keep their original semantics unless a platform-wide kill switch stops execution.
