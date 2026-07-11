---
unit: web-client
status: verified
depends_on:
  - identity-session
  - platform-api
---

# Web Client Foundation

## Scope

Provide one browser vertical slice from enterprise OIDC Authorization Code with PKCE through Platform API session exchange to a real OpenIM WASM SDK WebSocket connection. This slice does not implement conversations, message rendering, contacts, approval UI, documents, search, or administration.

## Responsibilities and non-goals

The unit owns interactive sign-in/out, use of an explicitly configured enrolled device identity, in-memory OpenIM session material, WASM SDK initialization, connection-state presentation, and explicit client errors. It does not own enterprise credentials, OpenIM Admin Tokens, device enrollment, identity provisioning rules, server authorization, or an alternate IM transport.

## Contracts and dependencies

- Keycloak/OIDC Authorization Code with PKCE through `oidc-client-ts`.
- `POST /v1/im/session` from `contracts/openapi/platform-v1.yaml`.
- Official `@openim/wasm-client-sdk@3.8.3-patch.13` and its pinned WASM assets.
- A fail-closed install-time compatibility patch normalizes the pinned official SDK Worker's five nullable batch payloads to empty arrays; it refuses to run if the upstream signature changes.
- Same-origin `/platform-api` and `/openim-api` routes; Vite proxies them to node2 only in local development.

## Invariants

- Interactive login uses `response_type=code`; the local password grant is never used by the Web client.
- The enterprise ID Token is sent only to Platform API. The returned OpenIM User Token is sent only to the OpenIM SDK.
- Tokens are not rendered, logged, placed in URLs, or stored in localStorage. OIDC transaction/user state uses sessionStorage.
- The device ID is required configuration and must already be active for the member; the client cannot self-enroll or bypass this check.
- Missing or invalid configuration, malformed session responses, endpoint mismatch, and SDK failure are explicit errors; no alternate provider or transport exists.

## Runtime flow

1. Redirect the browser to the configured enterprise OIDC issuer with PKCE.
2. Process the authorization callback and retain OIDC state in sessionStorage.
3. Read the required pre-enrolled device ID from explicit deployment configuration.
4. Send the ID Token and device tuple to `/v1/im/session`.
5. Validate the exact response and require the returned WebSocket endpoint to match configured OpenIM topology.
6. Login through the official WASM SDK and observe connecting, connected, failed, kicked, and token-invalid events.
7. Render the connected identity and endpoint status without exposing tokens.

## Data ownership and state

Keycloak owns the enterprise session, Platform API owns identity exchange policy, OpenIM owns its User Token and connection, and IndexedDB/WASM SDK own future IM client state. This first UI keeps only presentation phase, user metadata, and session material in memory; the application writes nothing to localStorage.

## Failure handling

OIDC callback failure, expired identity, typed Platform API failure, malformed success response, unexpected endpoint, and OpenIM SDK failure enter an explicit error state. Retry repeats the authoritative session exchange and SDK login; it does not reuse a failed response or switch transports.

## Security

The browser receives no OpenIM Admin Token or model credential. PKCE protects the authorization code. Same-origin API paths avoid permissive CORS. Token values are excluded from UI and logs, and generated WASM binaries plus local environment files are ignored by Git.

## Observability

The UI exposes coarse connection phase and endpoint readiness. Platform and OpenIM server logs remain authoritative for correlation IDs and connection failures; client telemetry is deferred to a dedicated observability slice.

## Acceptance criteria

- Missing required environment configuration fails the build/startup explicitly.
- Unit tests verify configuration, ID Token exchange, typed errors, and malformed success rejection.
- TypeScript typecheck and production build pass with pinned dependencies.
- A real browser completes PKCE login, `/v1/im/session`, WASM SDK login, and node2 WebSocket connection.
- Browser inspection confirms no token in visible UI, URL, localStorage, or console output.

## Source evidence

- `platform/apps/web/src/auth.ts`
- `platform/apps/web/src/platform-api.ts`
- `platform/apps/web/src/openim.ts`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/vite.config.ts`
- `platform/apps/web/scripts/patch-openim-worker.mjs`
- `platform/apps/web/src/config.test.ts`
- `platform/apps/web/src/platform-api.test.ts`
- `platform/apps/web/e2e/node2-foundation.spec.ts`

## Verification evidence

- TypeScript project references passed strict typecheck; Vitest passed 6 configuration and Platform API tests; Vite produced the pinned production bundle.
- A Playwright test in isolated system Chrome completed real node2 Keycloak Authorization Code with PKCE, exchanged the ID Token at `/v1/im/session`, initialized the official WASM SDK, reached `OpenIM 已连接`, and returned to the signed-out screen through OIDC logout.
- The same test observed zero HTTP failures and zero console errors after the deterministic nullable-batch compatibility patch, found no token-shaped value in localStorage or visible text, and confirmed that the callback URL no longer contained the authorization code.
- Desktop `1280x720` and mobile `390x844` screenshots passed horizontal-overflow checks and visual inspection without overlapping controls or text.

## Open questions

- Conversation and message state enter only in a later IM UI slice.
- Production reverse-proxy and CSP headers enter the deployment slice before public exposure.
- Group/department authorization and vector retrieval remain independent backend slices.
- Secure browser device enrollment requires a separate identity slice; this local slice uses the pre-enrolled `local-browser` fixture.
- Remove the Worker compatibility patch when an accepted upstream SDK release handles nullable batch payloads itself; `3.8.5-hotfix.0` still contains the same unsafe batch operations.
