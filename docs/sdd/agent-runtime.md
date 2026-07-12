---
unit: agent-runtime
status: verified
depends_on:
  - openim-adapter
  - intelligence-worker
  - adr-0001
  - adr-0003
---

# Agent Runtime

## Scope

Own durable execution of the read-only `@Agent` message flow with authoritative ACL retrieval and verifiable citations, but without tools, approval, or business writes.

## Responsibilities and non-goals

The unit consumes accepted IM events, recognizes an explicit text trigger, creates one durable Run per source event, requests authorized enterprise evidence, obtains a grounded answer/action candidate, and submits it to OpenIM. It does not decide document grants, approve or execute actions, create hidden memory, or claim user delivery/read semantics.

## Contracts and dependencies

- Kafka event `im.message.accepted.v1`
- PostgreSQL `agent.runs`, `agent.event_rejections`, and `agent.bot_identities`
- PostgreSQL `agent.run_citations` and the `acl-retrieval` contract
- `POST /v1/candidates` on `intelligence-worker`
- OpenIM `/user/user_register`, `/user/get_users_info`, and `/msg/send_msg`

## Invariants

- `source_event_id` is unique; Kafka redelivery cannot create another Run.
- A source offset is committed only after a matching Run or malformed-event rejection is durable.
- Only content type 101 containing a case-insensitive `@agent` marker creates a Run.
- The Run reaches `succeeded` only after OpenIM returns a non-empty reply `serverMsgID`.
- Candidate generation and reply submission are separate recoverable states.
- The triggering human member is resolved from the ready IdentityLink when the Run is inserted.
- Unknown or absent model citation IDs cannot be persisted or sent.
- Only a bounded `create_ticket` candidate can be materialized; the Go Runtime creates its immutable ID/digest and waits after sending approval evidence.
- Every execution update is fenced by a random lease token; a stale worker cannot complete a Run.
- A platform Bot has one deterministic OpenIM user per tenant and an authoritative tenant mapping.

## Runtime flow

1. Consume `im.message.accepted.v1` with consumer group `agent-runtime-v1`.
2. Validate and classify the event; commit non-trigger events and durably reject malformed events.
3. Insert `Run=queued` using unique `source_event_id`, then commit the Kafka offset.
4. Claim the Run with `FOR UPDATE SKIP LOCKED`, a deadline, and a fencing token.
5. Retrieve current authorized evidence using tenant, member, purpose, and query.
6. For evidence, call Python and atomically persist the validated citation provenance with the candidate; for zero results, persist an explicit abstention without calling Python.
7. Reclaim the reply phase, ensure the tenant Bot mapping and OpenIM Bot account, then submit a text reply.
8. Persist the returned `serverMsgID`; ordinary answers succeed, while action candidates enter `waiting_approval`.

## Data ownership and state

`agent-runtime` owns Run lifecycle, selected citation evidence, and Bot-to-tenant mapping in the `agent` schema. OpenIM owns Bot users and message acceptance. Knowledge and authorization modules own source facts and grants. The Python worker owns no durable business state.

## Failure handling

Dependency failures release the fenced lease and retry with bounded exponential delay up to the configured attempt count. Exhaustion records `failed`; no local answer, alternate provider, or fake OpenIM response is generated. An HTTP response lost after OpenIM acceptance can produce a duplicate reply because this upstream API does not accept a caller-selected idempotency key; `run_id` is included in message `ex` for reconciliation.

## Security

The Go process alone receives OpenIM Admin credentials. The Python worker receives minimized prompt context and no database, OpenIM, Kafka, tool, or business-system credentials. Message content is not written to process logs.

## Observability

Lifecycle logs use event ID, Run ID, model name, and OpenIM server message ID. Required metrics are queue age, state count, claim expiry, attempts, candidate latency, OpenIM submission latency, and terminal failure class.

## Acceptance criteria

- Duplicate source delivery creates one Run.
- A stale lease cannot persist a candidate or complete a Run.
- A model failure never returns a fallback answer.
- A real OpenIM `@Agent` input reaches `succeeded` and records a real reply `serverMsgID` through a contract-test model service.
- The Bot reply is attributed to the same tenant and does not recursively trigger another Run.

## Source evidence

- `platform/services/platform-api/internal/agent/`
- `platform/services/platform-api/internal/agent/workspace.go`
- `platform/services/platform-api/cmd/agent-runtime/main.go`
- `platform/services/platform-api/internal/openim/client.go`
- `platform/services/platform-api/internal/migrations/sql/0003_agent.sql`
- `platform/services/platform-api/internal/migrations/sql/0004_agent_bot_identity.sql`
- `platform/services/platform-api/internal/migrations/sql/0005_acl_retrieval.sql`

## Verification evidence

- Go unit tests cover trigger parsing, explicit provider failure, reply targeting, deterministic Bot identity, and OpenIM request shape.
- PostgreSQL integration tests cover source-event deduplication, candidate/reply phase recovery, and stale fencing-token rejection.
- A live OpenIM message `7e177577964b3990da22e988b452880e` created Run `1974bc03-c073-4ad6-b00e-f8597e3588d7` and OpenIM accepted reply `9abc54d6f2cca1921522a787dcef4ba8`.
- The input and Bot reply both entered durable ingress under the tenant; the reply contained no trigger and created no second Run.
- ACL-RAG smoke evidence is recorded in `acl-retrieval.md`; unknown citations fail validation and a zero-result query bypasses the model with an explicit abstention.
- The approved-action smoke is recorded in `action-executor.md`; no business row existed while the Run waited for approval.
- On node2 release `d663256`, authorized Run `00c88cd5-ec6f-4a9e-aacf-6fc4a0faf79b`, revoked-grant Run `24e91528-c5d1-4298-ba7b-cdf1d9953eff`, and no-match Run `a54ee93a-d1c3-4db3-aec5-ddd9cf144053` all reached a real OpenIM reply. Only the authorized Run invoked DeepSeek and persisted a citation.
- Web workbench acceptance on node2 release `b89a618` created cited Run `f0e2bc13-b33c-44d1-a761-70ccf03e3a26` through the unchanged OpenIM trigger path; the authenticated projection restored its exact `C1` provenance after browser reload.

## Open questions

- Production observability metrics enter with the observability slice.
- OpenIM reply uncertainty needs reconciliation before this path carries actions or externally visible exactly-once claims.
