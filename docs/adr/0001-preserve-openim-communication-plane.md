# ADR-0001: Preserve OpenIM as the communication plane

- Status: Accepted
- Date: 2026-07-11

## Context

The current OpenIM server and SDK already own message acceptance, conversation sequence allocation, asynchronous transfer, online/offline push, local persistence, and sequence-gap repair. Enterprise collaboration and Agent state have different transaction, authorization, and audit requirements.

## Decision

Keep OpenIM as an independently operable communication plane. New platform modules integrate through a versioned `openim-adapter`; they do not place task, approval, document, Agent Run, or action state in OpenIM messages or message storage.

No model call, retrieval request, enterprise database transaction, or Agent workflow may enter the OpenIM message hot path synchronously.

## Consequences

- OpenIM can continue operating when collaboration or Agent modules fail.
- Cross-plane behavior is eventually consistent and requires explicit event semantics, idempotency, and reconciliation.
- OpenIM-specific APIs and payloads are isolated behind the adapter and compatibility tests.
- Changes to OpenIM seq, message persistence, or SDK synchronization require a separate architecture decision.
