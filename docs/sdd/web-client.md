---
unit: web-client
status: verified
depends_on:
  - identity-session
  - platform-api
---

# Web Client Foundation

## Scope

Provide an extensible browser collaboration shell with verified OpenIM single/group text conversation foundations and an independently owned Agent workspace module. The group extension is specified in `im-client-foundation.md`; the Agent module is specified in `agent-workspace.md`. This unit continues to own shell, identity/session bootstrap, and IM presentation rather than Agent business state. It does not yet implement files, images, audio, video, search, contacts, documents, or administration.

## Responsibilities and non-goals

The unit owns interactive sign-in/out, the extensible workspace shell, use of an explicitly configured enrolled device identity, in-memory OpenIM session material, WASM SDK initialization, connection state, single/group conversation presentation, bounded history and group-member loading, text composition, optimistic send state, real-time receive handling, active-conversation read state, and explicit client errors. It does not own enterprise credentials, OpenIM Admin Tokens, device enrollment, identity provisioning rules, server authorization, authoritative group state, or an alternate IM transport.

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
- Conversation and message identity use OpenIM `conversationID` and `clientMsgID`; event replay cannot duplicate rendered messages.
- Only `SessionType.Single`, `SessionType.Group`, and text messages enter this UI. Unsupported content is not synthesized into text.
- A send is shown as `sending` before the SDK call, `succeeded` only from the returned authoritative message, and `failed` on an explicit SDK error.
- Global modules are registered through `WorkspaceModule` descriptors and rendered by `WorkspaceShell`; feature modules own their inner list/detail workflow and do not duplicate global navigation or account controls.
- Desktop uses global-module, conversation-list, and work-panel columns. At mobile width, the current module keeps one work panel visible and provides an explicit list/detail transition.

## Runtime flow

1. Redirect the browser to the configured enterprise OIDC issuer with PKCE.
2. Process the authorization callback and retain OIDC state in sessionStorage.
3. Read the required pre-enrolled device ID from explicit deployment configuration.
4. Send the ID Token and device tuple to `/v1/im/session`.
5. Validate the exact response and require the returned WebSocket endpoint to match configured OpenIM topology.
6. Login through the official WASM SDK, wait for initial `OnSyncServerFinish`, and observe connecting, connected, sync, failed, kicked, and token-invalid events.
7. Load supported single/group conversations and total unread state, then load bounded history and group members for the selected conversation.
8. Create and optimistically render a text message; replace it with the SDK result or mark it failed.
9. Merge real-time message and conversation events by stable IDs and mark the visible conversation read.
10. On successful reconnection, reload conversations, unread state, and active history before declaring the chat state restored.

## Data ownership and state

Keycloak owns the enterprise session, Platform API owns identity exchange policy, OpenIM owns its User Token and connection, and IndexedDB/WASM SDK own future IM client state. This first UI keeps only presentation phase, user metadata, and session material in memory; the application writes nothing to localStorage.

## Failure handling

OIDC callback failure, expired identity, typed Platform API failure, malformed success response, unexpected endpoint, OpenIM SDK failure, history failure, send failure, and read-state failure are explicit. Retry repeats the authoritative operation; it does not generate a local success, switch transports, or silently discard failed sends.

## Security

The browser receives no OpenIM Admin Token or model credential. PKCE protects the authorization code. Same-origin API paths avoid permissive CORS. Token values are excluded from UI and logs, and generated WASM binaries plus local environment files are ignored by Git.

## Observability

The UI exposes coarse connection phase and endpoint readiness. Platform and OpenIM server logs remain authoritative for correlation IDs and connection failures; client telemetry is deferred to a dedicated observability slice.

## Acceptance criteria

- Missing required environment configuration fails the build/startup explicitly.
- Unit tests verify configuration, ID Token exchange, typed errors, and malformed success rejection.
- TypeScript typecheck and production build pass with pinned dependencies.
- A real browser completes PKCE login, `/v1/im/session`, WASM SDK login, and node2 WebSocket connection.
- Real node2 members exchange single and group text; unread state clears when selected, history survives reload/reconnect, group members resolve, and sent messages converge from sending to succeeded.
- Browser inspection confirms no token in visible UI, URL, localStorage, or console output.

## Source evidence

- `platform/apps/web/src/auth.ts`
- `platform/apps/web/src/platform-api.ts`
- `platform/apps/web/src/openim.ts`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/src/WorkspaceShell.tsx`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/AgentWorkspace.tsx`
- `platform/apps/web/vite.config.ts`
- `platform/apps/web/scripts/patch-openim-worker.mjs`
- `platform/apps/web/src/config.test.ts`
- `platform/apps/web/src/platform-api.test.ts`
- `platform/apps/web/e2e/node2-foundation.spec.ts`

## Verification evidence

- `npm run typecheck` passed and Vitest passed 23 configuration, Platform API, connection, single/group-chat, Agent API, and Agent controller tests.
- `npm run test:e2e:node2` passed against the real `.2` runtime: system Chrome completed Keycloak Authorization Code with PKCE, exchanged the ID Token at `/v1/im/session`, initialized and synchronized the official WASM SDK, and connected to node2 OpenIM.
- The E2E injected real `imAdmin -> Web` messages through node2, observed unread increment and read clearing, sent `Web -> imAdmin` text, recovered from browser offline/online, and found the messages after reload. It also created a real group, sent group text, loaded members, restored group history, and dismissed the test group.
- The same test observed zero HTTP failures and no unexpected console errors, found no localStorage entries or token-shaped visible text, and confirmed the callback authorization code was removed from the URL. One exact bounded OpenIM new-group update diagnostic is documented in `im-client-foundation.md`.
- Desktop `1280x720` group chat and mobile `390x844` single-chat/list screenshots passed horizontal-overflow checks and visual inspection without overlapping controls or text; the mobile E2E exercised explicit conversation-list and chat-detail navigation.
- The node2 Playwright configuration uses one worker because the real single-chat and Agent scenarios intentionally share one authenticated account and global OpenIM unread state.

## Open questions

- Production reverse-proxy and CSP headers enter the deployment slice before public exposure.
- Organization/department authorization and vector retrieval remain independent backend slices; OpenIM currently owns IM group authorization.
- Secure browser device enrollment requires a separate identity slice; this local slice uses the pre-enrolled `local-browser` fixture.
- Remove the Worker compatibility patch when an accepted upstream SDK release handles nullable batch payloads and dynamic history-table initialization itself; the patch refuses unknown upstream signatures.
