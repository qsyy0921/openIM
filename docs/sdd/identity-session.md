---
unit: identity-session
status: verified
depends_on:
  - platform-api
  - openim-adapter
  - adr-0001
---

# Identity session

## Scope

Map a verified enterprise member identity to one OpenIM user and issue the OpenIM session material required by the client.

## Responsibilities and non-goals

The unit owns OIDC claim validation, tenant/member status checks, `IdentityLink`, device/platform validation, and `/v1/im/session`. It does not store user passwords, issue OpenIM Admin Tokens to clients, or implement a development authentication bypass.

The active browser-continuity delta is specified by `oidc-session-continuity.md`: a refreshed ID Token must still pass this unit's full server-side verification and current membership/device checks. Browser renewal does not weaken or cache these decisions.

## Contracts and dependencies

- `POST /v1/im/session` in `contracts/openapi/platform-v1.yaml`
- Enterprise OIDC issuer and audience configuration
- PostgreSQL identity schema
- OpenIM adapter user registration and token APIs

## Invariants

- Tenant and member identity come only from the verified bearer token and authoritative membership state.
- `(tenant_id, member_id)` and `(tenant_id, openim_user_id)` mappings are unique.
- OpenIM Admin credentials remain server-side.
- A disabled member cannot create a new IM session.

## Runtime flow

1. Verify the OIDC token issuer, audience, signature, expiry, and subject.
2. Load the active tenant member and IdentityLink.
3. Validate requested OpenIM platform and registered device policy.
4. Create the OpenIM user only when the authoritative mapping workflow requires it.
5. Request an OpenIM User Token through the adapter.
6. Return `ws_url`, mapped user ID, user token, and expiry.

## Data ownership and state

The identity module owns tenant/member identity and `IdentityLink` in PostgreSQL. OpenIM owns its user record and User Token state. No distributed transaction is claimed; incomplete provisioning is recorded and reconciled explicitly.

## Failure handling

Invalid identity, disabled membership, mapping conflicts, OpenIM rejection, and unavailable required dependencies return typed failures. The unit does not issue a local substitute token or reuse an expired cached token.

## Security

Use Authorization Code with PKCE for interactive clients. Never log bearer, Admin, or User Token values. Authorization checks use the current member state on every session issue.

## Observability

Record session issue latency and outcomes by error class without token values. Correlate enterprise subject, member ID, and OpenIM user ID using controlled identifiers.

## Acceptance criteria

- Valid identity produces a User Token that completes a real OpenIM WebSocket handshake.
- Disabled, expired, wrong-audience, and wrong-tenant identities are rejected.
- Concurrent first-session requests create one IdentityLink and one OpenIM user mapping.
- No response or log exposes an OpenIM Admin Token.

## Source evidence

- `chat/internal/api/chat/chat.go`
- `open-im-server/internal/rpc/auth/auth.go`
- `open-im-server/internal/msggateway/ws_server.go`
- `contracts/openapi/platform-v1.yaml`
- `platform/services/platform-api/internal/identity/`
- `platform/services/platform-api/internal/openim/client.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/services/platform-api/internal/migrations/sql/0001_identity.sql`
- `platform/deploy/local/`

## Verification evidence

- OIDC tests verify signature, issuer, audience, expiry, subject, and required `tenant_id` claim.
- HTTP tests reject missing bearer credentials, forged fields, invalid platform IDs, and map typed dependency failures.
- OpenIM client tests verify Admin Token isolation/caching, User Token requests, and ownership checks for idempotent registration.
- A live PostgreSQL test ran 16 concurrent link acquisitions: exactly one lease owner was admitted, a stale fencing token was rejected, and the owner committed `ready`.
- Local Keycloak 26.7.0 issued a token whose `iss`, `aud`, `sub`, and `tenant_id` matched the authoritative seed.
- A live request traversed Keycloak, PostgreSQL, OpenIM user provisioning and User Token issuance, then completed an OpenIM WebSocket handshake with state `Open`.
- A disabled member returned HTTP 403 before OpenIM was called.
- Node2 release `akashic-node2-20260723-oidc-renew1` completed a real 314-second browser run in which the refreshed ID Token advanced by 240 seconds, the same subject and tenant remained pinned, and a post-renewal member-scoped Platform API request returned HTTP 200 before the OpenIM round trip completed.

## Open questions

- The production OIDC issuer remains an environment decision and must expose the same verified issuer/audience/tenant contract.
- Local direct access grant is smoke-test-only; the future web client must use Authorization Code with PKCE.
