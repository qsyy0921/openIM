---
unit: platform-evolution-goal
status: verified
branch: codex/local-responses-model-current
updated: 2026-07-23
---

# OpenIM intelligent collaboration platform evolution Goal

## Scope

Govern continued development of the existing OpenIM intelligent collaboration platform through a single admitted vertical slice at a time. M1 member-driven Telegram identity linking and M2 OIDC browser session continuity are verified. No later backlog slice is admitted by this Goal.

## Outcome

Evolve the existing OpenIM-backed collaboration product through one production-real vertical slice at a time. Every admitted slice must keep OpenIM and enterprise-domain ownership intact, update its SDD before implementation, pass affected automated gates, and finish with an explicit real-environment acceptance result or an evidence-backed blocker.

This is a bounded evolution loop, not permission to expand one module indefinitely. Exactly one slice may be `active`; future work remains backlog until the active slice reaches an acceptance boundary.

## Responsibilities and non-goals

This Goal owns slice admission, SDD lifecycle, cross-unit verification, real-environment evidence, and the stop boundary. It does not create a second runtime, replace an owning unit SDD, authorize silent scope growth, or turn partial evidence into acceptance.

## Contracts and dependencies

- OpenIM owns users, contacts, groups, conversations, messages, sequence state, and client synchronization.
- PostgreSQL owns enterprise identity, Agent catalog, policy, Run, Memory, knowledge, approval, audit, channel binding, and durable delivery state.
- The intelligence worker is stateless and candidate-only.
- The only active generation route is `POST /v1/responses` with `gpt-5.6-terra`, `reasoning.effort=high`, `stream=false`, and no provider, model, endpoint, or semantic fallback.
- The Terra migration and Telegram/OpenIM round-trip evidence remain in `goal-state.md`; historical blocked checkpoints stay distinguishable from the later verified results.

## Invariants

- At most one implementation slice is active, and admission is explicit.
- Source facts, design decisions, local verification, and remote E2E evidence remain distinguishable.
- Production dependencies fail closed without provider, model, channel, storage, or semantic fallback.
- No slice may duplicate an upstream or bounded-context source of truth.
- A slice stops when its stated acceptance boundary is reached or an evidence-backed blocker prevents further progress.

## Runtime flow

1. Inspect current source, Git state, deployment state, tests, and owning SDDs.
2. Admit one slice with explicit outcome, invariants, non-goals, and acceptance criteria.
3. Move the owning SDD from `proposed` to `implemented`, then to `verified` only as evidence is produced.
4. Keep source changes inside the named bounded contexts and preserve dependency direction.
5. Run targeted tests first, then affected repository gates.
6. Run a real business round trip when the slice crosses an external channel or dependency.
7. Record partial or blocked evidence without fallback or fabricated success.
8. Stop at the slice boundary before selecting the next slice.

## Completed slices

### M1: self-service Telegram enterprise identity linking

An authenticated, active enterprise member can create a short-lived one-time link challenge in the Web client and consume it from a private chat with the configured Telegram Bot. Consumption atomically establishes the existing member/principal/chat binding. The command never reaches the Agent model path.

Owning SDD: `telegram-identity-linking.md`.

Acceptance requires:

- OIDC and active-device authorization for challenge creation and status reads;
- high-entropy, one-time, expiring challenge material stored only as a digest;
- private-chat-only atomic consumption with conflict, expiry, replay, and malformed-command rejection;
- no tenant/member/Telegram identity accepted from browser request bodies;
- Web loading, pending, bound, expired, conflict, and retry states;
- unit and database integration tests plus Web typecheck/tests/build;
- a real Node2 Web-to-Telegram binding followed by one cited Agent answer and deterministic cleanup.

M1 met this contract on Node2 release `akashic-node2-20260723-telegram-link1`: the Web client converged to `connected`, the Telegram Agent answer carried four authorized citations, durable ingress/Run/delivery cardinality was `1|1|1`, and the isolated four-table binding fixture was removed.

### M2: OIDC browser session continuity

A PKCE-authenticated Web workspace must remain usable across the configured short Keycloak Token lifetime. `oidc-client-ts` uses only its refresh-token path, renewed identity remains pinned to the original enterprise subject and tenant, and every long-lived Platform API adapter resolves the current ID Token at invocation time. A healthy OpenIM session is not reconnected just because OIDC rotated.

Owning SDD: `oidc-session-continuity.md`. Security decision: ADR-0009.

Acceptance requires:

- automatic refresh-token renewal with one bounded timeout retry and no iframe, password-grant, issuer, or Token fallback;
- immutable `(sub, tenant_id)` pinning and a current ID Token after every refresh;
- idempotent terminal shutdown plus complete listener/controller cleanup;
- focused lifecycle tests and all affected Web/repository gates;
- a real Node2 PKCE session that crosses the access-token lifetime, then completes an authenticated Platform API read and a real OpenIM message round trip without renewal-triggered reconnect;
- no Token in URL, localStorage, visible UI, console output, logs, screenshots, or repository files.

M2 stops at this continuity boundary. Cross-tab sessions, device enrollment, new business modules, Keycloak administration UI, and alternative grants are non-goals.

M2 met this contract on Node2 release `akashic-node2-20260723-oidc-renew1`: the same PKCE browser workspace remained online for 314 seconds, one refresh advanced access/ID expiry by 240 seconds, a post-renewal member-scoped API read returned 200, and real outbound/inbound OpenIM messages completed without a renewal-triggered reconnect or authentication artifact.

## Backlog, not admitted

- production generation-quality admission thresholds;
- broader remote A2A interoperability and lifecycle governance;
- Telegram unlink, account transfer, group-chat binding, and multi-Bot management;
- knowledge administration and ingestion UI;
- meeting, document, calendar, and task collaboration domains.

These items must not be implemented during M2.

## Data ownership and state

The owning unit SDD records domain state and acceptance evidence. `goal-state.md` preserves the prior Terra migration checkpoint, while `telegram-identity-linking.md` owns M1 state. Git and deployed release metadata remain operational facts and are checked before each resumed action.

## Failure handling

Failed checks remain failed and are repaired within the active slice. External blockers are recorded with the last safe idempotent step. Uncertain writes are never repeated as if they had failed, and an unrelated backlog item is not used to bypass a blocked acceptance criterion.

## Security

Local implementation and verification only unless the user explicitly authorizes commit, push, PR, or mainline integration. Node2 deployment is allowed only after local gates pass and must use the existing immutable-release procedure.

Secrets, Tokens, model credentials, Telegram credentials, data volumes, generated releases, and upstream source mirrors must not be committed or copied into SDD evidence.

## Observability

Progress is visible through owning SDD status, exact test output, migration cardinality, immutable release metadata, service health, durable Run/channel records, and real client observations. Heartbeat execution, an API probe, or a healthy process alone is not business E2E evidence.

## Acceptance criteria

- The active slice has an approved SDD and ADR where ownership or security changes.
- Implementation and contracts match the SDD and all affected automated gates pass.
- External-channel work has a real business round trip or an explicit evidence-backed blocker.
- The owning SDD is reconciled with exact evidence and non-goals before another slice is admitted.
- No secret, fallback, duplicate authority, or unrelated feature enters the change set.

## Source evidence

- `docs/sdd/goal-state.md`
- `docs/sdd/akashic-openim-integration.md`
- `docs/sdd/telegram-identity-linking.md`
- `docs/adr/0007-agent-platform-ddd-bounded-contexts.md`
- `ops/validate-repository.py`

## Open questions

- Which bounded backlog item should be admitted after M2 reaches its real Node2 acceptance boundary?

## Restart checkpoint

M1 is verified in `telegram-identity-linking.md`, and M2 is verified in `oidc-session-continuity.md`. A resumed session reads this file, `goal-state.md`, and Git status, then reports the closed Goal unless the user explicitly admits a new bounded slice; it must not infer permission to start a backlog item.
