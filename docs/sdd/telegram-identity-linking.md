---
unit: telegram-identity-linking
status: verified
depends_on:
  - identity-session
  - akashic-openim-integration
  - adr-0006
  - adr-0008
---

# Telegram enterprise identity linking

## Scope

Provide a production-real self-service path that links an authenticated enterprise member to one Telegram private-chat identity. The existing `channel.telegram_principals` and `channel.telegram_chats` records remain the binding authority; a link challenge is only a short-lived authorization ceremony.

## Responsibilities and non-goals

This unit owns challenge issuance, challenge consumption, member-visible binding status, link-command interception, and binding audit evidence.

Non-goals for this slice:

- unlinking, reassignment, account recovery, or administrator override;
- Telegram group/supergroup binding;
- multiple Telegram principals per member or multiple tenants per principal;
- Bot provisioning, webhook transport, or multi-Bot selection;
- sending link acknowledgements directly from the poller;
- changing Agent routing, model prompts, OpenIM identity, or delivery semantics.

## Contracts and dependencies

### Member HTTP API

Both endpoints require a valid bearer Token plus the existing `device_id` and Web `platform_id` query context. Tenant and member are resolved server-side through OIDC and active-device enrollment.

- `GET /v1/agent/channels/telegram/link`
  - returns `unbound`, `pending`, or `bound`;
  - a pending response may expose `expires_at`, but never the challenge code;
  - a bound response does not expose the raw Telegram user or chat identifier.
- `POST /v1/agent/channels/telegram/link-challenges`
  - creates one challenge and invalidates any prior unconsumed challenge for the same member;
  - returns the one-time code, exact `/link <code>` command, and `expires_at` once;
  - returns an explicit conflict if the member is already bound;
  - rate limiting is explicit and never returns a previous plaintext code.

### Telegram command

- Only `/link <code>` and `/link@<this_bot> <code>` in a Telegram `private` chat are recognized.
- A recognized link command is intercepted before normal binding resolution and never becomes an ingress message or model context.
- Invalid, expired, replayed, group-chat, and conflicting commands become durable ingress rejections with stable reason codes.
- Successful consumption is recorded transactionally and advances the polling offset normally. The Web client observes success by refreshing status.

## State model

Challenge states are derived from timestamps:

- `pending`: not consumed, not revoked, and `expires_at > now()`;
- `expired`: not consumed or revoked and `expires_at <= now()`;
- `revoked`: replaced by a newer challenge;
- `consumed`: bound exactly once and stamped with consumption evidence.

Member status precedence is `bound`, then live `pending`, otherwise `unbound`.

## Data ownership and state

Migration `0031_telegram_identity_linking.sql` adds:

- `channel.telegram_link_challenges`: challenge ID, tenant/member ownership, SHA-256 digest, expiry, consumed/revoked timestamps, and non-secret Telegram consumption evidence;
- a partial unique index allowing at most one non-consumed, non-revoked challenge per member;
- `audit.telegram_link_events`: append-only `issued`, `revoked`, and `consumed` events without plaintext challenge material.

The challenge code contains at least 128 bits of cryptographic entropy. Only its digest is stored. Plaintext exists only in the issuing process response and browser memory.

## Runtime flow

1. The Web client requests a challenge using its OIDC Token and active device context.
2. Platform API resolves the enterprise member and rejects disabled tenants, members, or devices.
3. The channel store atomically rejects an existing binding, revokes the previous active challenge, persists the new digest/expiry, and appends audit evidence.
4. The member sends the returned command in a private chat with the Bot.
5. Telegram ingress recognizes the command before Agent normalization.
6. The store hashes the supplied code, locks the live challenge, validates active tenant/member state, and inserts the existing principal/chat binding in one serializable transaction.
7. The same transaction stamps the challenge consumed and appends audit evidence.
8. The Web client refreshes status and displays `bound`.
9. Subsequent normal Telegram messages resolve the binding and enter the existing idempotent Agent pipeline.

## Invariants

- The browser never supplies tenant ID, member ID, Telegram user ID, Telegram chat ID, or session type.
- A code is one-time, short-lived, high entropy, and persisted only as a digest.
- Consumption and principal/chat binding commit atomically.
- An existing principal or member binding is never reassigned by this flow.
- Only a private chat can establish a v1 binding.
- Link commands never reach RAG, Memory, Tool, Agent Run, or delivery generation paths.
- Missing identity, database, or channel dependencies fail closed; there is no host-admin or model fallback.
- Logs, audit evidence, tests, screenshots, and repository files contain no plaintext challenge after the response boundary.

## Failure handling

| Condition | HTTP / ingress result |
| --- | --- |
| Missing or invalid OIDC Token | `401 AUTHENTICATION_REQUIRED` |
| Inactive member/device | `403 MEMBER_OR_DEVICE_FORBIDDEN` |
| Existing member binding | `409 TELEGRAM_ALREADY_BOUND` |
| Challenge issuance cooldown | `429 TELEGRAM_LINK_RATE_LIMITED` with retryable true |
| Database unavailable | `503 TELEGRAM_LINK_UNAVAILABLE` with retryable true |
| Malformed/unknown/expired/replayed code | durable `telegram_link_challenge_invalid` rejection |
| Link command outside private chat | durable `telegram_link_private_chat_required` rejection |
| Principal/chat/member ownership conflict | durable `telegram_link_binding_conflict` rejection |

Failures do not create a Run, send a candidate, or silently use the host-admin binding command.

## Security

- The HTTP path reuses OIDC verification and active device enrollment.
- Code comparison is digest-based; the stored value cannot be used as a command.
- Rotation revokes the prior challenge and preserves its audit history.
- Conflict errors do not reveal the owner of an existing Telegram identity.
- The API does not return raw bound Telegram identifiers.
- The poller does not log command text or challenge material.

## Observability

- Audit rows record challenge lifecycle, tenant/member, correlation/challenge identity, and non-secret outcome metadata.
- HTTP metrics expose route status and latency through the existing middleware.
- Durable ingress rejection reason codes expose malformed, expired, group, and conflict attempts.
- No metric label contains challenge, member, user, or chat identifiers.

## Tests

- Service tests: authorization, code entropy/shape, digest-only repository input, already-bound, cooldown, dependency failure.
- Poller tests: valid private command, Bot-qualified command, malformed code, group rejection, replay/expiry rejection, conflict, and proof that a link command never calls `IngestBound`.
- Store integration tests: atomic consume/bind, one live challenge, replacement revocation, replay failure, tenant/member activity, principal/member/chat conflict, and rollback on failure.
- HTTP tests: authentication/device validation, success payload, stable errors, body limits, and no identity fields accepted from a body.
- Web tests: stale request protection, repeated-submit guard, pending refresh, expiry rendering, explicit failure, and unmount cleanup.

## Acceptance criteria

Local acceptance:

- migration `0031` applies once on empty and upgraded databases;
- targeted Go and Web tests pass;
- all Go tests/vet, Web typecheck/tests/build, repository validation, formatting, and diff checks pass;
- a source and artifact scan finds no challenge or credential fixture.

Node2 real acceptance:

1. deploy an immutable release after local gates;
2. sign in as an isolated active Web member and create a challenge;
3. send the exact command to the real Bot in a private Telegram chat;
4. verify the Web status becomes `bound` without host-admin binding;
5. send one enterprise knowledge query and receive a cited Terra answer through Telegram;
6. verify one ingress, one Run, one delivery, and immutable model/version evidence;
7. remove the isolated principal/chat/challenge/audit fixture and verify no residual binding.

Interface probes, unit tests, and historical Luna/DeepSeek channel evidence do not satisfy this real acceptance.

## Source evidence

- `platform/services/platform-api/internal/telegram/link.go`
- `platform/services/platform-api/internal/telegram/store.go`
- `platform/services/platform-api/internal/telegram/poller.go`
- `platform/services/platform-api/internal/httpserver/telegram_link.go`
- `platform/services/platform-api/internal/identity/repository.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/services/platform-api/internal/migrations/sql/0009_agent_channels.sql`
- `platform/services/platform-api/internal/migrations/sql/0031_telegram_identity_linking.sql`
- `platform/apps/web/src/telegram-link-api.ts`
- `platform/apps/web/src/telegram-link.ts`
- `platform/apps/web/src/ChannelWorkspace.tsx`
- `ops/accept-node2-telegram.sh`
- `platform/apps/web/src/App.tsx`

## Current checkpoint

- 2026-07-23: migration `0031`, digest-only challenge service, authenticated member HTTP API, private-chat command interception, atomic PostgreSQL consumption, durable rejection semantics, and the Web channel module are implemented.
- A disposable PostgreSQL 18.4 database reached `0031` twice with exactly one migration record. The real store integration test passed challenge rotation, cooldown, consumption, binding, audit cardinality, replay rejection, ownership conflict, and rollback.
- Go all-package tests, vet, and formatting passed. Web typecheck, 105 tests, and the explicit Node2-topology production build passed. Intelligence Worker 37 tests, repository validation, shell syntax, and `git diff --check` passed.
- Node2 runs release `akashic-node2-20260723-telegram-link1` at migration `0031`; immutable release hashes, service health, the `520|624|3224|520|2704|2704` data/index contract, and the public HTTPS endpoint passed acceptance.
- A real Web member issued the challenge, the real private Bot chat consumed it atomically, and a separate fast convergence run displayed `connected` in the Web channel module. No host-admin binding path was used.
- Telegram update `96338395` produced one accepted ingress, one published Outbox event, one successful `gpt-5.6-terra` Run with four authorized citations, one sent delivery with a non-empty external message ID, visible client receipt, and `1|1|1` idempotency.
- The isolated principal, private-chat binding, challenge rows, and link-audit rows were removed by exact transaction and verified as zero. `ops/accept-node2-telegram.sh cleanup` now applies the same fail-closed four-table cleanup and is synchronized to Node2.

## Open questions

- Whether a later slice should add a durable Bot acknowledgement after binding, without weakening polling-offset idempotency.
- OIDC silent renewal is verified separately in `oidc-session-continuity.md`; Telegram binding keeps using the current validated member Token and does not own browser session renewal.
