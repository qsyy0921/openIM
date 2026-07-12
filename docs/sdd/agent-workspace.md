---
unit: agent-workspace
status: implemented
depends_on:
  - agent-runtime
  - acl-retrieval
  - action-executor
  - identity-session
  - web-client
---

# Agent Workspace

## Scope

Provide one authenticated Web workbench that submits enterprise questions through the existing OpenIM Agent trigger, restores the requesting member's durable Runs, displays authorized citation provenance, and approves the existing digest-bound `create_ticket` action. It does not add another Runtime ingress, model provider, tool type, generic workflow builder, knowledge administration, memory, or production fallback.

## Responsibilities and non-goals

The backend projection owns member/device-scoped reads of up to 30 recent Runs, citations, immutable Intent data, Execution state, and ticket receipt. It ensures the deterministic tenant Agent Bot exists before the browser sends. The Web module owns module navigation, prompt submission, bounded status polling, citation presentation, approval interaction, and refresh restoration. OpenIM remains the only prompt ingress; Agent Runtime, retrieval, Action Executor, and collaboration ticket tables retain their existing ownership.

## Contracts and dependencies

- Official `@openim/wasm-client-sdk` text send to the deterministic tenant Bot using `<prompt> @Agent`.
- Authenticated `GET /v1/agent/workspace?platform_id=5&device_id=...`.
- Existing authenticated `POST /v1/agent/intents/{intent_id}/approve` with the exact `payload_digest`.
- PostgreSQL `agent.runs`, `agent.run_citations`, `action.intents`, `action.executions`, and `collaboration.tickets` as authoritative state.
- Existing Kafka ingress, Agent Runtime, ACL retrieval, DeepSeek worker, OpenIM reply, and restricted Action Executor processes.

## Invariants

- The browser cannot provide tenant/member identity; OIDC subject, tenant claim, and active device resolve the authoritative member.
- Workspace reads include only Runs where both `tenant_id` and `principal_member_id` match that member.
- The workspace returns citation provenance but never chunk content, model credentials, OpenIM Admin Token, or another member's Runs.
- Prompt submission uses the official OpenIM SDK and the existing `@Agent` event contract; there is no direct model or alternate Run-creation endpoint.
- The explicit `创建工单：<title>` prefix remains before the appended trigger so the Intelligence Worker action protocol is unchanged.
- Approval sends the exact server-returned Intent ID and digest. The client does not calculate, edit, or infer either value from Bot text.
- Polling is bounded to two minutes and fails visibly; it does not synthesize completion or switch providers.
- A ticket is displayed only when the authoritative Execution projection returns its `ticket_id`.

## Runtime flow

1. The signed-in browser selects the registered `agent` workspace module.
2. Platform API verifies the ID Token, resolves the active member/device, derives the tenant Bot ID, ensures its mapping and OpenIM account, and returns the member-scoped projection.
3. The Web controller creates a text message and sends `<prompt> @Agent` to that Bot through the official SDK.
4. Existing OpenIM ingress, Kafka, Agent Runtime, ACL retrieval, and Intelligence Worker create and process the durable Run.
5. The Web controller polls the projection while a prompt or Run is active and renders answer state and citation provenance.
6. For `waiting_approval`, the UI displays the immutable ticket title and submits the returned ID/digest only after the member clicks approve.
7. Existing Action Executor applies its stable idempotency key, verifies the ticket by read-back, and updates Run/Intent/Execution.
8. The projection returns the terminal state and ticket ID; a browser reload reconstructs the same workbench from durable data.

## Data ownership and state

Agent Runtime owns Runs and selected citations. Action Executor owns Intent, approval, Execution, and idempotency state. Collaboration owns the ticket. Identity owns member/device authorization. OpenIM owns message delivery. The browser stores only React presentation state in memory and writes no Token or Agent state to localStorage.

## Failure handling

Invalid bearer identity, inactive device, Bot provisioning failure, database read failure, malformed API data, OpenIM send failure, approval conflict/expiry, and polling timeout are explicit. Failed prompts remain failed rather than receiving local answers. `unknown` execution is displayed as reconciliation in progress and is never presented as success.

## Security

Every projection read and approval re-verifies OIDC and active device state. SQL predicates bind tenant and member. Citation content is not returned. Approval remains bound to requesting member, immutable digest, and expiry. Browser traffic contains no Admin Token, database credential, model key, or action-executor credential.

## Observability

The projection exposes durable Run, Intent, Execution, and ticket identifiers for support correlation. HTTP failures retain typed code and correlation ID. The UI displays state and explicit errors without logging Tokens or prompts to application logs.

## Acceptance criteria

- Selecting the Agent module restores the authenticated member's recent Runs and deterministic Bot.
- A real `.1` browser prompt traverses `.2` OpenIM, ingress, ACL retrieval, DeepSeek, Agent Runtime, and returns an answer with persisted citation provenance.
- A real explicit ticket prompt remains at zero business effects before approval.
- Browser approval uses the exact server digest; duplicate approval converges on one Execution and one ticket.
- Terminal ticket ID and state survive browser reload.
- Cross-member data, secret material, fallback answers, and localStorage Tokens are absent.
- Desktop and mobile layouts have no horizontal overflow or incoherent overlap.

## Source evidence

- `contracts/openapi/platform-v1.yaml`
- `platform/services/platform-api/internal/agent/workspace.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/apps/web/src/agent-api.ts`
- `platform/apps/web/src/agent.ts`
- `platform/apps/web/src/AgentWorkspace.tsx`
- `platform/apps/web/e2e/node2-agent-workspace.spec.ts`

## Verification evidence

- Go unit tests cover identity/device resolution, deterministic Bot setup, member-scoped projection routing, and invalid request rejection.
- Web unit tests cover workspace restore, official OpenIM trigger placement, exact-digest approval, and typed API errors.
- Real node2 and visual evidence are pending deployment acceptance; this document remains `implemented` until that evidence passes.

## Open questions

- Streaming transport requires a separately designed Run event contract; this slice uses bounded polling over authoritative state.
- Reject/cancel, multiple Agent definitions, memory, and generic tools require separate bounded slices.
