---
unit: akashic-openim-integration
status: implemented
depends_on:
  - agent-runtime
  - agent-catalog
  - identity-session
  - acl-retrieval
  - action-executor
  - adr-0001
  - adr-0003
  - adr-0006
---

# Akashic capability integration

## DDD architecture ruling

This integration uses domain boundaries inside the existing deployable services. DDD here defines ownership, language, invariants, and dependency direction; it does not require one microservice per domain.

| Bounded context | Aggregate / authority | Inbound contracts | Outbound contracts |
| --- | --- | --- | --- |
| Identity and tenancy | tenant, member, enrolled device, role binding | OIDC subject and active-device proof | authenticated tenant/member context |
| Channel ingress and delivery | normalized ingress, delivery intent, channel binding | OpenIM event or bound Telegram update | idempotent Run request and durable channel delivery |
| Agent runtime | Agent Run, execution context, lifecycle, delegation | pinned catalog/capability snapshot and authorized context | candidate, approval suspension, delivery intent, replay evidence |
| Knowledge retrieval | versioned document, chunk embedding, ACL grant | member-scoped query and embedding contract | cited authorized evidence only |
| Agent Memory | personal events/projections and reviewed group proposals | extraction proposal and explicit review decision | member-authorized derived facts and exposure records |
| Capability governance | Agent, Skill, Tool, MCP descriptor, immutable snapshot | administrator publication commands | execution-plane-visible pinned capabilities |
| Remote Agent collaboration | remote Agent registration, Agent Card revision, A2A job | administrator registration/verification and governed delegation | bounded A2A 1.0 REST message result |
| Operations and assurance | runtime controls, audit, metrics, evaluation artifacts | queue/catalog/runtime observations | Prometheus metrics, Grafana dashboards, deterministic gate reports |

Dependencies point inward through Go interfaces and typed HTTP/event contracts. OpenIM remains an upstream authority rather than a repository inside these contexts. PostgreSQL schemas are separated by domain (`identity`, `integration`, `agent`, `knowledge`, `memory`, `capability`, `proactive`, `audit`) and are not treated as a shared bag of tables. Cross-context writes are performed by explicit application services and transactional domain operations; browser components never write domain tables directly.

The current deployment remains a modular platform API, bounded workers, and the stateless intelligence worker. A context is split into another process only after independently measured scaling, isolation, or release requirements justify the operational cost.

## Scope

Reimplement the production capabilities verified in the Akashic Runtime3 reference inside the existing OpenIM intelligent-collaboration architecture. The resulting platform has one Agent control plane and supports both OpenIM and Telegram channels. It does not vendor a second Agent runtime or treat the reference repository's unimplemented proposals as delivered behavior.

## Responsibilities and non-goals

This unit owns the cross-cutting migration contract and delivery order. Individual packages continue to own identity, ingress, Agent Runs, retrieval, actions, Web UI, and intelligence generation.

In scope:

- normalized OpenIM and Telegram channel ingress;
- durable channel delivery, reconciliation, and dead-letter state;
- execution identity, per-conversation ordering, bounded leases, traces, and replay evidence;
- capability and immutable tool snapshots;
- Tool/Skill/MCP registry, execution-plane visibility, policy, approvals, and ledger;
- RAG and personal/group Memory separation with event-sourced derived memory;
- passive routing with lexical/dense/IntentView evidence and deterministic fusion;
- proactive source polling, candidate envelopes, ranking, disturbance policy, terminal ACK, and feedback;
- arXiv passive search/subscription and proactive event source;
- authenticated Web administration, observability, evaluation, and real OpenIM/Telegram acceptance.

Non-goals:

- copying Akashic's SQLite MessageLog, channel host, direct-send tool, or Web chat application;
- changing OpenIM seq, message storage, WebSocket, SDK synchronization, or group authority;
- allowing Telegram identities without an explicit enterprise member binding;
- exposing arbitrary shell/filesystem tools to production Agents;
- implementing provider fallback, fake success, unbounded ReAct, or automatic write tools;
- claiming exactly-once remote delivery when the channel API cannot prove it.

## Capability migration matrix

| Akashic capability | Existing platform equivalent | Required delta |
| --- | --- | --- |
| Durable Inbox | OpenIM Kafka ingress plus `integration.ingress_messages` | Generalize accepted messages with channel/member identity and add Telegram polling ingress |
| Turn/Run repository and leases | `agent.runs` and fenced worker leases | Add channel reply target, execution trace, per-conversation scheduling, and replay projection |
| Transactional Outbox | ingress event Outbox; direct Agent reply remains | Add durable channel delivery intents and supervisor; remove direct reply from candidate worker |
| ExecutionContext/config snapshot | Run-pinned Agent version/checksum | Add channel, execution plane, capability snapshot, budget, and trace identity |
| Capability snapshot | Agent Catalog version | Add immutable Tool/Skill/MCP capability snapshot referenced by AgentVersion/Run |
| ToolPolicy/approval/ledger | bounded `create_ticket` approval/executor | Generalize read/tool policy while retaining digest-bound approval for side effects |
| MemoryEvent/projectors | no Agent Memory | Add ACL-bound derived memory events, projector cursors, delete/rebuild, and keep RAG separate |
| Intent Routing V3 | fixed knowledge/action protocol | Add bounded operation routing after registry exists; preserve pinned policy and fail-closed schema |
| Proactive v2 | no scheduler/source gateway | Add scheduled Runs, source gateway, candidate state, delivery policy, ACK, and feedback |
| arXiv plugin | none | Implement passive search/subscription and proactive poll/get/ack through MCP/tool contracts |
| Telegram channel | none | Implement Bot API long polling and durable delivery through enterprise bindings |
| Dashboard/evaluation/replay | Agent Workspace and repository checks | Extend authenticated workspace and add routing/runtime/proactive evaluation artifacts |

## Contracts and dependencies

- OpenIM message, conversation, group membership, and user-token contracts remain upstream contracts and are consumed through the existing SDK and server APIs.
- PostgreSQL migrations `0009` through `0031` define the Agent channel, runtime, capability, Tool, Skill, MCP, Memory, proactive, approval-continuation, bounded delegation, administrative-role state, tenant-scoped runtime incident controls, knowledge embeddings, group-Memory review, catalog audit, remote A2A state, immutable fixed Responses model-route transitions, and member-issued Telegram link challenges.
- Intelligence requests and responses use strict typed JSON contracts; invalid model output fails the Run phase and is never interpreted as a successful answer or ToolCall.
- OpenIM and Telegram delivery adapters return an external message identifier before an intent can enter `sent` state.
- OpenIM delivery remains available when Telegram is explicitly disabled. The unified Delivery Worker always starts with the OpenIM adapter; the Telegram adapter is registered only when a validated systemd credential is present. A Telegram intent encountered while that adapter is disabled fails permanently on its own channel and is never rerouted to OpenIM.
- The Web member API depends on enterprise OIDC verification and active device enrollment. It does not accept tenant, member, reply-target, or administrator identity from request payloads.

## Invariants

- OpenIM is the only authority for its messages and membership; PostgreSQL is authoritative for enterprise identity, Agent, policy, memory, delivery, and audit state.
- Telegram input cannot enter model context before tenant/member/chat mapping succeeds.
- A source channel event is idempotent and creates at most one Run.
- Every Run pins immutable Agent and capability versions before execution.
- Passive, proactive-source, internal, and admin execution planes have separate tool visibility and executor enforcement.
- Memory and RAG use different records, ACL, retention, deletion, and citations.
- The intelligence worker receives only authorized evidence and capability schemas; it receives no channel, database, Kafka, or business credentials.
- Candidate persistence and remote delivery are separate recoverable states.
- Missing model, embedding, MCP, parser, policy, or channel dependencies fail explicitly. No alternate provider or semantics are selected.
- Deployment indexes every active, published knowledge chunk with the pinned embedding revision before Agent Runtime starts. Runtime acceptance requires the valid current-chunk embedding count to equal the current published chunk count; an embedding endpoint health probe alone is insufficient.
- Side effects require policy, approval when configured, an idempotency key, execution audit, and target read-back where supported.

## Delivery slices

1. **Channel runtime foundation**: normalized channel identity, Telegram bindings and polling, durable delivery intents, OpenIM/Telegram send adapters, reconciliation states.
2. **Runtime kernel**: execution context, per-conversation mailbox, bounded scheduler, lifecycle trace, replay bundle, config/capability pinning.
3. **Capability platform**: Tool/Skill/MCP descriptors, snapshots, process supervision, execution-plane visibility, ToolPolicy, approval, ledger, budgets.
4. **Passive intelligence**: bounded operation plan, lexical/dense/IntentView routing, deterministic fusion, clarification, tool result grounding.
5. **Memory**: MemoryEvent, projectors, retrieval, deletion/rebuild, ACL, feedback attribution; enterprise RAG remains a separate evidence source.
6. **Proactive intelligence**: source gateway, candidate envelope, schedule, ranking, disturbance policy, delivery, ACK and explicit feedback.
7. **arXiv dual-chain plugin**: passive search/subscription plus proactive poll/get/ack, cache and rate contract.
8. **Product and operations**: Web administration, health/metrics/traces, evaluation gates, fault/replay tests and dual-channel real acceptance.

Each slice must update this document and its owning SDDs, pass targeted and affected tests, and stop at its acceptance boundary before the next slice starts.

## Runtime flow

1. A channel adapter normalizes an OpenIM event or a bound Telegram update and persists an idempotent ingress record.
2. The ingress transaction creates or finds a Run pinned to an Agent version and immutable capability snapshot.
3. The scheduler claims the Run using a fenced lease and serializes work by conversation lane.
4. Retrieval, Memory, routing, and Tool planning provide authorized evidence and schema-only capabilities to the intelligence worker.
5. Read tools execute under the pinned execution-plane policy. Side-effect tools suspend the Run until an exact digest-bound approval is decided.
6. The candidate answer is persisted before a durable channel delivery intent is created.
7. A channel-specific delivery worker sends the intent, records the external message ID, and reconciles retryable, terminal, and unknown outcomes.
8. Lifecycle, Tool, retrieval, Memory exposure, and delivery evidence can be projected into a deterministic member-owned replay bundle.

## Data ownership and state

- OpenIM owns IM messages, conversations, users, groups, membership, seq, and multi-device synchronization.
- PostgreSQL owns enterprise identity bindings, Agent catalog and versions, immutable capabilities, Runs, Tool ledger and approvals, delivery intents, Memory events/projections, proactive subscriptions/events, and audit records.
- The intelligence worker is stateless with respect to authority. It receives only the bounded evidence and schemas needed for one phase.
- Personal Memory is a derived event-sourced store and is not the enterprise RAG corpus.
- Group Memory writes are explicit host-admin event appends through `group-memory-admin`; passive conversations are not automatically promoted into shared memory. OpenIM reads revalidate current group membership, while Telegram reads require an active member/chat binding.
- Redis, Kafka, MongoDB, and OpenIM SDK local storage retain their existing upstream responsibilities and are not duplicated by this integration.

## Failure handling

- Missing or invalid identity binding rejects ingress before model execution.
- Expired worker leases, phase retries, delivery retries, and approval expiry are explicit durable states with bounded attempts.
- Invalid model schemas, unavailable MCP processes, authorization failures, and storage failures fail closed; there is no provider or semantic fallback.
- A failed ToolCall is never decoded as an empty successful result. Only a currently authorized `read` Tool whose pinned descriptor declares `retry_semantics=safe` may move from `failed` back to `prepared`; the durable Run/call ID, operation, argument digest, attempt count, and audit trail are preserved. `unknown` outcomes and side-effect Tools are never automatically reset.
- Enterprise knowledge retrieval accepts only a `succeeded` ToolCall with a non-null result. A current ACL denial maps to an explicitly empty authorized corpus, while transport, embedding, storage, and Tool lifecycle failures remain errors and consume the Run's bounded retry budget rather than producing the no-evidence answer.
- A side-effect timeout after dispatch is recorded as `unknown` and is not automatically retried into a success state.
- Candidate persistence and remote delivery are separate, so a channel outage does not require rerunning model reasoning.

## Security

- OIDC subject, active device, tenant, member, Telegram binding, and OpenIM reply target are resolved server-side.
- Capability visibility and execution are both enforced by execution plane; UI hiding is not an authorization mechanism.
- Side effects require policy evaluation, digest-bound approval when configured, idempotency, audit, and outcome recording.
- Replay and control APIs redact raw Tool arguments, credentials, tokens, MCP environments, and unauthorized Memory content.
- Secrets are supplied through runtime environment or deployment secret stores and must never enter logs, snapshots, migrations, or repository files.

## Observability

- Run lifecycle events record phase, attempt, lease, route, and terminal state.
- ToolCall, approval, delivery, retrieval citation, Memory exposure, proactive dispatch, and feedback records provide cross-plane evidence.
- Replay bundles provide a deterministic redacted view for a member-owned Run and include a checksum.
- Platform API and intelligence-worker Prometheus endpoints, queue/MCP/A2A/RAG metrics, local Prometheus rules, and a provisioned Grafana dashboard are part of the current acceptance slice.
- Metrics and dashboards are operational projections, not business authorities. A scrape failure increments an explicit collection error and never changes queue state.
- Node2 monitoring uses a host-network override because the native platform services bind loopback. Remote acceptance verified three scrape targets, six rules, the provisioned datasource, and the Agent dashboard; this does not substitute for channel E2E.

## Implemented baseline on this branch

The `codex/akashic-openim-integration` branch currently contains the following source-backed implementation. This list is implementation status, not a production-acceptance claim:

- normalized OpenIM and Telegram ingress identities with explicit member bindings;
- member-issued, digest-only Telegram link challenges with private-chat atomic consumption and a Web connection module;
- durable OpenIM/Telegram delivery intents and channel-specific send adapters;
- conversation lanes, execution contexts, bounded phase attempts, and lifecycle audit events;
- immutable capability snapshots, tool descriptors, execution-plane policy, approvals, and tool-call ledger;
- supervised stdio MCP servers with restricted environment, catalog-digest drift detection, health persistence, and read-tool execution;
- digest-bound MCP side-effect approval, Run suspension/resumption, rejection, expiry, and unknown-outcome handling;
- immutable Skill versions bound to Agent versions and constrained to pinned capability operations;
- bounded lexical/dense/IntentView routing and schema-only tool planning;
- event-sourced personal Memory, extraction jobs, projector, retrieval exposures, feedback, deletion events, and rebuild support;
- explicitly curated group Memory streams with personal-plus-group retrieval and source-channel authorization;
- arXiv collection, baseline suppression, relevance ranking, quiet hours, daily budgets, durable dispatch, ACK, and feedback;
- authenticated member control APIs and Web views for Memory and proactive subscriptions.
- deterministic member-owned Replay Bundles with pinned versions, lifecycle, redacted ToolCall metadata, delivery, citations, and Memory exposure provenance.
- member-owned Replay Bundle download from the Web workspace; the exported JSON is the same redacted, checksummed server artifact shown in the evidence dialog.
- bounded background Agent delegation through the governed `agent.delegate` Tool: the child pins the target production Agent version, executes on the `internal` plane, cannot recursively delegate, and sends a separately audited result into the origin conversation.
- immutable capability-snapshot publication through the host-admin `capability-admin` command.
- transactional creation of additional Agent definitions, version 1, production deployment, mention trigger, and non-recursive internal delegation trigger through `agent-catalog-admin`;
- tenant-scoped `platform_admin`, `agent_admin`, and `knowledge_admin` roles, plus audited host-admin role and capability-grant commands. The migration assigns the first existing active member in each tenant as the bootstrap `platform_admin`.
- tenant-scoped, revision-checked incident controls for Agent execution, delivery, and proactive dispatch; every change requires an active `platform_admin`, a reason, and an audit event, and paused components stop claiming new work without rewriting queued state;
- an authenticated administrator operations API and Web view for tenant queue-state counts, oldest ready-work age, MCP health, and incident controls. `agent_admin` is read-only and `platform_admin` mutations use optimistic revisions;
- a deterministic offline routing regression gate with strict JSONL cases for selection, clarification, and no-tool behavior. The gate runs as part of the intelligence-worker test suite and can also be invoked with `openim-agent-routing-eval`.

Not yet accepted as complete:

- production-model generation quality and an admitted release threshold. The local `qwen2.5:3b` run validates fail-closed harness behavior only and is not a production quality gate;
- production-federated A2A interoperability beyond the bounded A2A 1.0 REST Agent Card and `message:send` profile;

Implemented and locally verified in the current acceptance slice:

- group Memory proposals are review-only for group conversations; an authorized member can approve or reject them in the Web workspace, and approval appends the event-sourced group fact only after live OpenIM membership revalidation;
- the Web administrator console inventories and governs Agents, Skills, MCP-backed Tools, immutable capability snapshots, MCP state, tenant roles, and remote A2A registrations through authenticated audited APIs;
- remote A2A uses pinned Agent Card digests, exact host/private-CIDR allowlists, bounded response sizes, environment-resolved credentials, idempotent message IDs, and explicit `unknown` outcomes without automatic retry;
- the frozen routing gate contains 36 Tool descriptors and 190 deterministic cases;
- enterprise RAG uses real model-revisioned embeddings, ACL-first current-version filtering, bounded hybrid lexical/dense retrieval, reciprocal-rank fusion, and provenance-integrity evaluation;
- the Candidate contract exposes `grounded`, `insufficient_evidence`, and `not_applicable` grounding states; grounded output must declare exactly the authorized citation IDs present in the answer text, while insufficient evidence fails closed without inventing a citation;
- retrieval and generation are evaluated separately. The schema-v3 retrieval report covers all 1,120 frozen QA cases; the deterministic 40-case local generation baseline records provider/schema rejection, fact coverage, abstention, and generated-citation metrics without changing production provider routing;
- Prometheus instrumentation and the local Grafana provisioning are source-controlled; local container and full repository verification status is recorded in `goal-state.md`.
- the native Ubuntu release contract packages every long-running Agent process and host-admin command. Node2 runs Telegram ingress, channel delivery, Memory extraction/projection, and proactive dispatch as separate hardened systemd units. Telegram remains a root-only systemd credential; generation credentials stay exclusively in the Windows CLIProxyAPI process boundary.
- Node2 has executed migrations `0009` through `0028`, loaded the enterprise fixture, and indexed all 2,704 active current-version chunks with checksum-matched normalized 2560-dimensional `qwen3-embedding:4b` vectors from a loopback-only pinned Ollama runtime. Release `akashic-node2-20260720-candidatefix1` passed runtime, OpenIM ingress, real ACL-RAG, DeepSeek candidate, durable citation, OpenIM delivery, and observability acceptance. The accepted cross-language contract serializes every empty Candidate collection as `[]`; Python keeps `extra=forbid` and does not accept JSON `null` as a list fallback.
- The same release passed a real Telegram round trip after an explicit enterprise member/chat binding: unbound bootstrap rejection, bound ingress, published Outbox event, one successful DeepSeek Run with five authorized citations, one sent delivery with an external Telegram message ID, `1|1|1` idempotency, visible client receipt, and cleanup of the temporary binding.

Current model-route transition: generation source is fixed to `gpt-5.6-terra` through Windows-loopback `POST /v1/responses` with `reasoning.effort=high`, `stream=false`, and no fallback. Migration `0030` preserves the immutable Luna version, creates an immutable Terra version, copies pinned Skills, audits activation, and changes only canonical Luna production deployments. Node2 release `akashic-node2-20260722-terra2` passed runtime, embedding-topology, and observability acceptance. A transient upstream `server_is_overloaded` response was exposed as `502`/`503`; a bounded retry then passed real Terra candidate, route, and OpenIM ACL-RAG authorization/revocation/no-match E2E. At that checkpoint, Telegram still awaited current-model channel acceptance. Release `responses3` and earlier DeepSeek Runs remain historical architecture evidence only and are not substituted for Terra acceptance.

Migration `0031` and Node2 release `akashic-node2-20260723-telegram-link1` add the member-issued Telegram identity ceremony without changing that model route. A real OIDC Web member completed private-chat binding, the Web module converged to `connected`, and the resulting knowledge query produced one successful Terra Run with four authorized citations, one sent Telegram delivery, and `1|1|1` ingress/Run/delivery cardinality. The isolated principal/chat/challenge/audit fixture was removed after verification. This closes the current-model dual-channel acceptance; earlier DeepSeek and Luna evidence remains historical only.

## Goal recovery and heartbeat

`docs/sdd/goal-state.md` is the durable recovery checkpoint for this root Goal. A heartbeat or resumed session must inspect that file, Git status, current migration state, and generated reports before acting. Steps are idempotent and may only transition from `pending` to `verified`, `blocked`, or `failed`; a heartbeat must not infer success from source presence alone.

The heartbeat is a scheduler wake-up, not process checkpointing. Long-running commands must persist their output to declared artifacts or be rerun from their idempotent entry point. Node2 work is attempted only after SSH reachability succeeds, and no local result is substituted for remote dual-channel acceptance.

## Authenticated member control API

All routes below require an enterprise OIDC bearer token plus an active enrolled `device_id` and `platform_id`. Tenant and member identifiers are resolved on the server and are never accepted from the browser.

| Route | Purpose | Authority and failure behavior |
| --- | --- | --- |
| `GET /v1/agent/memory` | List active personal facts and recent retrieval exposures | Reads only the authenticated member's personal stream and Runs |
| `DELETE /v1/agent/memory/facts/{fact_id}` | Append a personal fact-deletion event | Requires `Idempotency-Key`; returns `202 projection_pending`; projector performs the derived-state deletion |
| `POST /v1/agent/memory/exposures/{exposure_id}/feedback` | Attribute memory usefulness feedback | Exposure must belong to a Run owned by the authenticated member |
| `GET /v1/agent/proactive` | Read preferences, subscriptions, and recent events | Filters by authenticated tenant/member |
| `POST /v1/agent/proactive/subscriptions` | Create or re-enable an arXiv subscription | OpenIM/Telegram target is derived from the member's bound identity; `target_id` is rejected |
| `PATCH /v1/agent/proactive/subscriptions/{id}` | Enable or pause a subscription | Subscription must belong to the authenticated member |
| `PUT /v1/agent/proactive/preferences` | Set quiet hours, timezone, daily budget, and relevance floor | Invalid timezones and ranges fail explicitly |
| `POST /v1/agent/proactive/events/{id}/acknowledge` | Record terminal recommendation feedback | Event must be delivered to the authenticated member; same-signal retries are idempotent |
| `GET /v1/agent/tool-approvals` | List the member's pending generic Tool approvals | Returns operation/risk/argument digest only; raw arguments are not exposed |
| `POST /v1/agent/tool-approvals/{id}/decision` | Approve or reject a suspended ToolCall | Decision is bound to requester, Run, ToolCall, argument digest, state, and expiry in one transaction |
| `GET /v1/agent/runs/{run_id}/replay` | Read a deterministic Run evidence bundle | Run ownership is checked; raw tool arguments, credentials, MCP environment, and tokens are excluded |
| `GET /v1/agent/delegations` | List recent background Agent jobs | Returns only jobs requested by the authenticated member; task execution and terminal delivery states are durable |

Administrator routes require the same OIDC and active-device context. `GET /v1/admin/agent/operations` additionally requires `platform_admin` or `agent_admin`; `PUT /v1/admin/agent/runtime-controls/{component}` requires `platform_admin`, a non-empty audit reason, and the exact current revision.

MCP registration, Skill publishing, capability snapshots, and Agent deployment remain host-admin CLI operations. Tenant-scoped administrator roles now exist, but a browser administration API must verify OIDC identity, enrolled device, role, optimistic revision, and audit in one contract before these writes are exposed. No header-based or hidden administrator fallback is provided.

## Web control-plane state model

The existing Agent workspace now has four views:

1. Assistant: OpenIM-backed Agent conversation, citations, and digest-bound action approval.
2. Personal Memory: active facts, projection-pending deletion state, retrieval exposures, and explicit feedback.
3. Proactive subscriptions: arXiv topics, OpenIM/Telegram channel selection, notification preferences, subscription state, recommendation ACK, and source links.
4. Runtime operations: administrator-only queue state, waiting age, MCP health, and revision-checked incident controls.

The Web controller loads Memory and proactive state together, rejects overlapping mutations, ignores stale refresh results after stop/restart, and refreshes only after a real API success. It does not cache an authoritative copy in `localStorage` and does not optimistically report success.

## Acceptance criteria

- One explicitly bound Telegram user can send `@agent` text and create the same catalog-pinned Run used by OpenIM.
- Unknown Telegram users/chats are durably rejected and never call the model.
- OpenIM and Telegram replies are represented by durable delivery intents and reach `sent` only with a non-empty external message ID.
- Duplicate Telegram updates and duplicate OpenIM events do not create duplicate Runs or delivery intents.
- Deterministic send failures and uncertain outcomes are distinguishable; neither produces a success-shaped response.
- Tokens and message content do not enter logs, migrations, fixtures, snapshots, or repository files.

## Source evidence

- Current platform: `platform/services/platform-api/internal/ingress`, `internal/agent`, `internal/action`, `internal/retrieval`.
- Intelligence plane: `platform/services/intelligence-worker`.
- Reference implementation: Akashic Runtime3 SDD and source under the separately checked-out MIT-licensed repository; it is design evidence, not a runtime dependency.

## Open questions

- Telegram unlinking, account recovery, reassignment, group binding, and multi-Bot management remain separate slices; v1 self-service private-chat linking is verified.
- OIDC silent renewal is verified in its dedicated identity-session slice; it preserves server-side member/device checks and does not change Agent, Telegram, or OpenIM ownership.
- Public Telegram webhook mode is deferred until the deployment has an approved public TLS endpoint.
- Distributed scheduler partitioning is deferred until single-node correctness and workload measurements exist.
