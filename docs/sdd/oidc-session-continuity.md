---
unit: oidc-session-continuity
status: verified
depends_on:
  - identity-session
  - web-client
  - adr-0009
---

# OIDC browser session continuity

## Scope

Keep one authenticated Web workspace usable across normal short OIDC Token rotations while preserving the existing Keycloak, Platform API, active-device, and OpenIM trust boundaries.

## Responsibilities and non-goals

This unit owns browser-side OIDC user restoration, refresh-token renewal orchestration, immutable enterprise identity pinning, a current ID Token accessor for Platform API adapters, listener cleanup, and terminal session shutdown. It does not issue Tokens, change Keycloak membership or device policy, refresh OpenIM User Tokens on every OIDC rotation, synchronize authentication across tabs, or introduce another authentication grant.

## Contracts and dependencies

- ADR-0009 defines the only renewal mechanism and terminal behavior.
- `oidc-client-ts@3.5.0` owns Authorization Code + PKCE, refresh-token exchange, OIDC response validation, `sessionStorage`, and lifecycle events.
- Keycloak owns the OIDC session and returns a refresh token plus a current ID Token on refresh.
- Platform API continues to validate issuer, audience, signature, expiry, subject, `tenant_id`, active membership, and enrolled-device authorization on every request.
- OpenIM independently owns its User Token and WebSocket session.

## Invariants

- Automatic renewal uses the same configured issuer/client and refresh-token grant only; there is no hidden-iframe, password-grant, provider, or cached-token fallback.
- The mounted workspace pins non-empty `sub` and `tenant_id`; renewal cannot change either value and must rotate to a different ID Token with a later `exp`.
- An admitted user has a refresh token, a non-expired access token, and an ID Token whose `exp` is in the future.
- Platform API adapters resolve the current ID Token at invocation time and never retain the initial Token value.
- A successful OIDC rotation does not reconnect a healthy OpenIM WebSocket.
- Terminal events are idempotent, stale events after teardown are ignored, and all OIDC listeners are removed on teardown.
- Token values never enter logs, URLs, rendered text, localStorage, screenshots, tests, or repository files.

## Runtime flow

1. Process the Authorization Code callback or read the OIDC user from `sessionStorage`.
2. Validate renewal material and pin `(sub, tenant_id)`.
3. If the stored user is expired but has valid identity claims and a refresh token, perform one same-provider `signinSilent` refresh before mounting the workspace.
4. Start a lifecycle controller before Platform API/OpenIM initialization, register `UserLoaded`, `SilentRenewError`, `AccessTokenExpired`, and `UserUnloaded` callbacks, then start the library's automatic silent-renew service.
5. Exchange the current ID Token for an OpenIM session, connect the pinned SDK, and construct all Platform API adapters with the current-token accessor.
6. On `UserLoaded`, validate the renewed user against the pin, atomically replace the in-memory current user, and update presentation identity without touching OpenIM.
7. If OpenIM independently expires, retry obtains a new IM session with the current enterprise Token.
8. On renewal failure, expiry, unload, or identity mismatch, admit the terminal transition once, invalidate in-flight connection work, stop controllers/listeners, disconnect OpenIM, remove the OIDC user, and render an explicit login-required state.

## Data ownership and state

Keycloak owns refresh-token and SSO state. `oidc-client-ts` keeps its user and transaction state in `sessionStorage`. The continuity controller keeps only the pinned identity and current `User` reference in process memory. Platform API and OpenIM ownership do not change, and no database schema is added.

## Failure handling

Boot restoration refreshes at most once. Automatic renewal permits one library-managed timeout retry against the same token endpoint. A missing refresh token, null refresh result, stale ID Token, claim mismatch, provider error, terminal lifecycle event, or storage-clear failure leaves the workspace closed and requires interactive login. Raw provider errors and Token material are not rendered. No request is converted into success and no expired Token is reused.

## Security

Authorization Code + PKCE remains the only interactive flow. The client validates the OIDC library result again for the application-specific `tenant_id` pin and current ID Token requirement. Server-side verification and active-device authorization remain mandatory after refresh. The browser never receives an OpenIM Admin Token or model credential.

## Observability

The UI exposes only coarse states: restoring, connected, renewal failed/login required, OpenIM expired, or OpenIM kicked. Tests and acceptance record lifetimes, request outcomes, and connection continuity without recording Token values. A real acceptance must distinguish OIDC renewal from OpenIM reconnect.

## Acceptance criteria

- Unit tests cover valid boot, expired boot refresh, missing refresh token, null refresh, expired renewed ID Token, subject/tenant mismatch, current-token rotation, duplicate terminal events, stale callbacks, and listener cleanup.
- Auth configuration defers automatic renewal until identity restoration, enables subject validation and a bounded timeout retry, and uses `sessionStorage` while leaving iframe renewal unconfigured.
- Web tests, typecheck, production build, repository validation, and secret/fallback scans pass.
- On Node2, a real PKCE browser session remains in the workspace beyond the configured access-token lifetime.
- After that boundary, a real authenticated Platform API read succeeds with the renewed ID Token and the existing OpenIM connection can still send/receive a real message without a renewal-triggered reconnect.
- Browser inspection finds no Token in URL, localStorage, visible UI, console output, or captured acceptance artifacts.

## Source evidence

- `platform/apps/web/src/auth.ts`
- `platform/apps/web/src/main.tsx`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/src/enterprise-session.ts`
- `platform/apps/web/src/enterprise-session.test.ts`
- `platform/apps/web/e2e/node2-oidc-continuity.spec.ts`
- `ops/send-node2-web-e2e-message.ps1`
- `platform/deploy/local/keycloak/realm-platform.json`
- `platform/services/platform-api/internal/identity/verifier.go`
- `docs/adr/0009-refresh-browser-identity-with-oidc-refresh-token.md`

## Verification evidence

- Vitest passes 14 focused auth/lifecycle cases and the complete Web suite passes 119 cases across 17 files.
- `npm run typecheck` and the production Node2-topology build pass with 1,801 transformed modules.
- The dedicated no-artifact Playwright configuration discovers `node2-oidc-continuity.spec.ts`; the scenario derives its wait from non-secret OIDC expiry metadata and requires refresh, Platform API, OpenIM send/receive, connection-state, URL, localStorage, console, and HTTP checks.
- Repository validation, strict SDD validation, `git diff --check`, changed-file credential-shape scanning, and the production no-fallback scan pass.
- Immutable release `akashic-node2-20260723-oidc-renew1` contains 696 SHA-verified files and no detected credential shape. Node2 verified all three transport archive hashes, preserved the existing `platform/.env` hash, activated that release, and passed `accept-node2-runtime.sh` with migration `0031`, dataset cardinality `520|624|3224|520`, embedding cardinality `2704|2704`, and the pinned `qwen3-embedding:4b|2560` contract.
- The real Node2 Playwright run stayed in one PKCE-authenticated browser workspace for 314 seconds, observed exactly one successful refresh-token exchange, advanced both access-token and ID-Token expiry by 240 seconds, retained the same enterprise subject and tenant, and never observed an OpenIM state other than `online`.
- After renewal, `GET /v1/agent/channels/telegram/link` returned HTTP 200, the browser sent a real OpenIM message, and a Node2-native helper delivered a real inbound OpenIM message that the same browser received. URL, hash, localStorage, visible-text, console-error, failed-response, screenshot, trace, and video checks remained clean. The first attempt reached this post-renewal stage but exposed an obsolete Windows/WSL path in the inbound helper; the helper was changed to the current Ubuntu-native topology and the complete scenario then passed in 5.3 minutes.

## Open questions

- Cross-tab authentication synchronization requires a separate product and security decision because this slice intentionally uses per-tab `sessionStorage`.
