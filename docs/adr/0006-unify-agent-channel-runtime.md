# ADR-0006: Unify OpenIM and Telegram behind the Agent channel runtime

- Status: Accepted
- Date: 2026-07-18

## Context

The platform already accepts OpenIM events, creates durable Agent Runs, retrieves authorized evidence, calls the Python intelligence worker, and replies through OpenIM. The Akashic reference runtime also has Telegram, durable inbox/outbox, tool policy, memory, proactive sources, and reliable delivery concepts.

Copying Akashic's channel host, message log, session database, and runtime into this repository would create a second authority for messages, identities, approvals, and Agent execution. Keeping Telegram as a separate Agent deployment would also make policy, memory, tool approvals, and audit differ by channel.

## Decision

Use one platform Agent Runtime and expose channels through explicit inbound and outbound ports.

- OpenIM remains the authority for OpenIM messages, conversations, groups, contacts, and delivery semantics.
- Telegram is an external channel adapter. It owns no Agent, knowledge, memory, approval, or business state.
- Every accepted channel message is normalized into a versioned platform event with a stable channel, external message identity, tenant, member, conversation, sender, and content contract.
- Tenant and member identity are resolved before a Telegram message can create a Run. Unknown or disabled mappings fail closed.
- One source event creates at most one Run. A Run pins the same immutable Agent version regardless of channel.
- Candidate generation never sends directly to a channel. It creates a durable delivery intent; a delivery supervisor invokes the selected channel adapter and records the external message ID.
- Channel credentials are process secrets and are never stored in Agent versions, prompts, logs, browser storage, or repository files.
- Akashic capabilities are reimplemented at the existing ownership boundaries. Its MessageLog, channel host, SQLite runtime database, and direct-send paths are not copied.

The initial Telegram transport uses Bot API long polling because the current private Node2 deployment has no public webhook endpoint. Long polling is the single production path for this deployment mode; webhook support requires a separate decision and migration.

## Consequences

- OpenIM and Telegram share Run, Catalog, ACL, ToolPolicy, Memory, proactive, audit, and evaluation semantics.
- Telegram polling offsets, accepted updates, identity mappings, delivery intents, attempts, and terminal outcomes require PostgreSQL state.
- OpenIM direct reply code moves behind the same durable delivery contract.
- A channel send with an uncertain remote outcome is not retried as if it definitely failed; it enters an observable reconciliation state.
- Telegram is not a replacement for OpenIM and does not become an enterprise directory or authorization source.
