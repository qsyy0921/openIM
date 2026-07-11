# ADR-0002: Start with a modular platform API

- Status: Accepted
- Date: 2026-07-11

## Context

The target architecture contains identity, collaboration, document, retrieval, OpenIM integration, and Agent-control domains. Deploying each domain independently during local-first development would create more operational boundaries than current evidence justifies.

## Decision

Implement the first platform milestones as these deployment units:

1. `platform-api` in Go, internally separated into identity, OpenIM adapter, collaboration, document metadata, retrieval gateway, and Agent runtime modules.
2. `intelligence-worker` in Python for model and retrieval candidate generation.
3. `action-executor` in Go, introduced only when approved write actions are implemented.
4. `web-app` as the first product client.

OpenIM remains a separate existing deployment. A module may split into another service only after measured release, scaling, security, compliance, or failure-isolation pressure.

## Consequences

- Local development and integration require fewer processes.
- Module ownership, schemas, APIs, and events must remain explicit so later extraction does not change public contracts.
- In-process calls cannot bypass authorization or transaction boundaries documented by the owning module.
