---
unit: action-executor
status: verified
depends_on:
  - agent-runtime
  - identity-session
  - adr-0003
---

# Action Executor

## Scope

Execute one governed action, `create_ticket`, after the requesting member approves the exact immutable intent digest.

## Responsibilities and non-goals

The slice owns intent persistence, approval evidence, queued execution, idempotent internal ticket creation, read-back verification, and UNKNOWN reconciliation. It does not create a generic plugin framework, execute arbitrary model tools, support external ticket systems, or approve on behalf of users.

## Contracts and dependencies

- Candidate `action_intent` type `create_ticket`
- Authenticated `POST /v1/agent/intents/{intent_id}/approve`
- PostgreSQL `action`, `collaboration`, and `audit` schemas
- Dedicated `action-executor` process configuration

## Invariants

- The Intelligence Worker only returns an explicit-command intent candidate; it never approves or executes it, and unsolicited model actions are rejected.
- Approval binds tenant, requesting member, intent ID, payload digest, and expiry.
- One intent has one stable idempotency key and at most one ticket business effect.
- `succeeded` requires a read-back match of tenant, creator, title, status, and idempotency key.
- Ambiguous commit/expired execution enters `unknown`; reconciliation checks the authoritative target before retry.
- The Executor has no model API key or OpenIM Admin Token.

## Runtime flow

1. Persist a bounded `create_ticket` candidate with the Run.
2. Materialize one immutable intent and send its ID/digest to the conversation; Run waits for approval.
3. Authenticate the member/device and approve only an unexpired matching digest owned by that member.
4. Transactionally create one queued execution.
5. Executor claims with a fenced lease and inserts the ticket by unique idempotency key.
6. Read the ticket back and mark execution/intent/Run succeeded.
7. Reconcile `unknown` by idempotency key; verify an existing ticket or safely requeue when absence is authoritative.

## Data ownership and state

`action` owns intents, approvals, and executions. `collaboration` owns tickets. `audit` owns immutable decision/execution records. Agent Runtime owns Run state and candidate proposal.

## Failure handling

Invalid identity, digest mismatch, expiry, duplicate/conflicting approval, lease loss, target failure, and verification mismatch are explicit states/errors. Retries preserve the same idempotency key. No alternate executor or success-shaped no-op exists.

## Security

Only the original active member/device can approve in this slice. Payload title length and action type are allow-listed. The Executor uses a separate process and required database URL; production grants are limited to approved-action reads, execution/audit updates, and ticket creation/read-back.

## Observability

Record intent/execution/Run IDs, digest, approval actor, state transitions, lease expiry, reconciliation outcome, and ticket receipt without bearer tokens or prompt content.

## Acceptance criteria

- No ticket exists before matching approval.
- Another member, stale digest, or expired intent cannot approve.
- Duplicate approval/execution creates one ticket.
- Read-back mismatch cannot produce succeeded.
- UNKNOWN with an existing ticket reconciles to succeeded; UNKNOWN without a ticket requeues the same idempotency key.

## Source evidence

- `contracts/openapi/platform-v1.yaml`
- `platform/services/platform-api/internal/action/`
- `platform/services/platform-api/cmd/action-executor/main.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/services/platform-api/internal/agent/workspace.go`
- `platform/services/platform-api/internal/migrations/sql/0007_approved_ticket_action.sql`

## Verification evidence

- PostgreSQL integration tests proved zero tickets before approval, wrong member/digest rejection, duplicate approval idempotency, one verified ticket, and terminal state convergence.
- UNKNOWN with an existing ticket reconciled to succeeded; UNKNOWN with no ticket requeued the unchanged idempotency key and retained zero tickets.
- Real OpenIM Run `1e85f3f8-8352-46b8-a0c9-034ed8dcbd3d` produced Intent `50557b0d-f427-4f1f-b6ef-2027eb2ba906` and remained `waiting_approval` with zero tickets.
- A real Keycloak ID Token for the requesting member approved the exact digest and queued Execution `3c247ee7-3a4c-42f9-943b-3fedc83f8344`.
- A dedicated PostgreSQL role with only required schema/table grants created ticket `b54c3421-36aa-4edf-aa6b-0c53ea64093a`; read-back moved Execution, Intent, and Run to succeeded.
- Repeating the same approval returned the same Execution and database counts remained one approval and one ticket.
- The final real DeepSeek protocol run `99216135-a79a-406f-b0c2-ca51b8baa55b` accepted only the `创建工单：<标题>` prefix, held zero tickets before OIDC approval, and completed ticket `e87c89b9-200b-4e0e-9c68-e6c9e3518e77` with one approval and one effect.
- Node2 Run `1e635b15-76b9-4654-bb53-e6945ae4a9a9` produced Intent `25514bf8-b6f1-4839-be07-c15127834963` with zero tickets before approval. A wrong digest was rejected, duplicate exact-digest OIDC approval returned Execution `0a9101aa-c78d-4943-ae60-73212f472f40`, and the restricted Executor produced one verified ticket. Forced `UNKNOWN` states then proved both existing-ticket read-back and authoritative-absence safe retry; the latter retained the same idempotency key and converged to ticket `c8091ade-8dc5-45f1-a7c0-42df10f716cd`.
- Web workbench acceptance approved Intent `c0dcba7c-5720-4779-89db-e31a0c87fdfd` with its server-projected digest; restricted Execution `bac0faa0-014b-4ce0-a0a6-8dc5d75ad789` produced exactly one verified ticket `dccc657d-62db-479e-bafc-4600abf26e74`, and the terminal receipt survived browser reload.
- The first live attempt exposed that `ON CONFLICT DO UPDATE` required excess UPDATE permission; implementation was narrowed to `INSERT DO NOTHING` plus authoritative SELECT instead of widening role grants.

## Open questions

- External target adapters require separate per-system contracts and credentials.
