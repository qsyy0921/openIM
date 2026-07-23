# ADR 0007: Organize the Agent platform by DDD bounded contexts

## Status

Accepted

## Context

The platform extends OpenIM with enterprise identity, Agent runtime, RAG, Memory, capability governance, remote A2A, and operations. These domains have different invariants and authorities, but they currently share a small deployment footprint and do not have evidence that independent services would improve scale or reliability.

Splitting by database table or feature screen would introduce distributed transactions and operational coupling before the domain contracts are stable. Keeping all code in one undifferentiated package would instead let browser, channel, retrieval, and execution concerns mutate each other's state.

## Decision

Use DDD bounded contexts inside the existing deployable units:

- identity and tenancy;
- channel ingress and delivery;
- Agent runtime;
- knowledge retrieval;
- Agent Memory;
- capability governance;
- remote Agent collaboration;
- operations and assurance.

Each context owns its aggregate invariants, PostgreSQL schema, application services, and outbound ports. Cross-context access uses typed Go interfaces, HTTP/event contracts, or explicit transactional application services. Adapters for OpenIM, Telegram, model/embedding providers, MCP, A2A, and PostgreSQL remain outside domain policy.

OpenIM remains the sole authority for IM messages, conversations, groups, membership, seq, and client synchronization. PostgreSQL owns only enterprise and Agent-platform state. The browser calls authenticated application APIs and never writes domain tables directly.

Retain the Platform API as a modular service plus bounded workers and a stateless intelligence worker. Split a context into an independently deployed service only after measured scaling, isolation, ownership, or release constraints justify the distributed-systems cost.

## Consequences

- Domain changes must identify an owning context and update its SDD, contracts, tests, and migration together.
- Database joins across contexts are permitted only in explicit read-side operational projections; they do not transfer write ownership.
- Queue consumers and external adapters must be idempotent and fail closed because they cross process or authority boundaries.
- The Web administration surface cannot bypass catalog, role, approval, or audit application services.
- Package boundaries can be extracted later without redesigning domain language or authority.

## Rejected alternatives

- One microservice per feature: rejected until independent scale or release evidence exists.
- One generic CRUD service over all schemas: rejected because it erases aggregate invariants and authority.
- Copying OpenIM messages or group membership into PostgreSQL: rejected because dual authority creates synchronization and authorization defects.
- Reusing Akashic SQLite, chat frontend, or channel host: rejected because OpenIM and the current platform already own those responsibilities.
