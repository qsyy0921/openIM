---
unit: web-client
status: verified
depends_on:
  - identity-session
  - platform-api
---

# Web Client Foundation

## Scope

Provide an extensible browser collaboration shell with verified OpenIM single/group text conversation foundations and an independently owned Agent workspace module. The group foundation is specified in `im-client-foundation.md`, contacts/member-picker in `im-client-contacts.md`, image/file in `im-client-media.md`, search/pin/mute in `im-client-conversations.md`, group lifecycle in `im-client-group-lifecycle.md`, message actions in `im-client-message-actions.md`, local message search in `im-client-message-search.md`, the admitted device slice in `im-client-device-management.md`, and the Agent module in `agent-workspace.md`. This unit continues to own shell, identity/session bootstrap, and IM presentation rather than Agent business state. It does not yet implement audio, video, enterprise-directory search, documents, or administration.

## Responsibilities and non-goals

The unit owns interactive sign-in/out, the extensible workspace shell, use of an explicitly configured enrolled device identity, in-memory OpenIM session material, WASM SDK initialization, connection state, single/group conversation presentation, OpenIM contacts presentation, bounded history and group-member loading, text composition, optimistic send state, real-time receive handling, active-conversation read state, and explicit client errors. It does not own enterprise credentials, OpenIM Admin Tokens, device enrollment, identity provisioning rules, server authorization, authoritative relationship/group state, or an alternate IM transport.

## Contracts and dependencies

- Keycloak/OIDC Authorization Code with PKCE through `oidc-client-ts`.
- `POST /v1/im/session` from `contracts/openapi/platform-v1.yaml`.
- Official `@openim/wasm-client-sdk@3.8.3-patch.13` and its pinned WASM assets.
- A fail-closed install-time compatibility patch normalizes the pinned official SDK Worker's nullable batch payloads, dynamic history-table initialization, and the keyword-search ABI mismatch between the 8-argument WASM caller and 9-argument Worker implementation; it refuses unknown upstream signatures.
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
8. Load the SDK-synchronized friend/application projection, subscribe to relationship callbacks, and provide exact user-ID lookup plus authoritative friend mutations.
9. Create and optimistically render a text message; replace it with the SDK result or mark it failed.
10. Resolve the active Agent and trigger from the authenticated Catalog, require Bot identity agreement, and display exact Run version provenance in the Agent workspace.
11. Merge real-time message, conversation, friend, and application events by stable IDs and mark the visible conversation read.
12. On successful reconnection, reload conversations, contacts, unread state, and active history before declaring the client state restored.

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
- A real node2 member finds another user by exact OpenIM user ID, submits and processes a friend application, opens direct chat from the friend list, and creates a group through the reusable member picker.
- Real node2 single and group conversations upload, send, receive, preview, download/open, and restore image/file messages through the pinned SDK and OpenIM MinIO path described by `im-client-media.md`.
- Real node2 conversation search filters only the synchronized projection; pin and per-conversation `NotNotify` persist across reload while muted messages continue to synchronize.
- Real node2 message search uses the official synchronized local index, bounded pages, official bidirectional history context, and exact `clientMsgID` highlighting after reload.
- A member can inspect the safe device-enrollment/platform-online projection, distinguish the current device, and log out another enrolled OpenIM platform without receiving an Admin Token; kicked and expired SDK states leave the active workspace.
- Browser inspection confirms no token in visible UI, URL, localStorage, or console output.

## Source evidence

- `platform/apps/web/src/auth.ts`
- `platform/apps/web/src/platform-api.ts`
- `platform/apps/web/src/openim.ts`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/src/WorkspaceShell.tsx`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/contact.ts`
- `platform/apps/web/src/ContactsWorkspace.tsx`
- `platform/apps/web/src/MemberPicker.tsx`
- `platform/apps/web/src/AgentWorkspace.tsx`
- `platform/apps/web/src/agent.ts`
- `platform/apps/web/src/agent-api.ts`
- `platform/apps/web/src/device.ts`
- `platform/apps/web/src/device-api.ts`
- `platform/apps/web/src/DeviceWorkspace.tsx`
- `platform/apps/web/vite.config.ts`
- `platform/apps/web/scripts/patch-openim-worker.mjs`
- `platform/apps/web/src/config.test.ts`
- `platform/apps/web/src/platform-api.test.ts`
- `platform/apps/web/src/contact.test.ts`
- `platform/apps/web/src/MemberPicker.test.ts`
- `platform/apps/web/e2e/node2-foundation.spec.ts`
- `platform/apps/web/e2e/node2-device-management.spec.ts`

## Verification evidence

- `npm run typecheck` passed and Vitest passed 38 configuration, Platform API, connection, single/group/media-chat, contacts, Agent API, and Agent controller tests.
- `npm run test:e2e:node2` passed against the real `.2` runtime: system Chrome completed Keycloak Authorization Code with PKCE, exchanged the ID Token at `/v1/im/session`, initialized and synchronized the official WASM SDK, and connected to node2 OpenIM.
- The E2E injected real `imAdmin -> Web` messages through node2, observed unread increment and read clearing, sent `Web -> imAdmin` text, recovered from browser offline/online, and found the messages after reload. It also created a real group, sent group text, loaded members, restored group history, and dismissed the test group.
- The same test observed zero HTTP failures and no unexpected console errors, found no localStorage entries or token-shaped visible text, and confirmed the callback authorization code was removed from the URL. One exact bounded OpenIM new-group update diagnostic is documented in `im-client-foundation.md`.
- Desktop `1280x720` group chat and mobile `390x844` single-chat/list screenshots passed horizontal-overflow checks and visual inspection without overlapping controls or text; the mobile E2E exercised explicit conversation-list and chat-detail navigation.
- The node2 Playwright configuration uses one worker because the real single-chat and Agent scenarios intentionally share one authenticated account and global OpenIM unread state.
- The verified contacts extension adds exact user-ID discovery, real friend request/acceptance, callback-driven friend state, direct-chat entry, and MemberPicker-based group creation without a second relationship store or browser Admin Token. The final full Agent plus IM/contact suite passed serially in 37.5 seconds.
- The verified media extension adds official-SDK image/file creation and upload, exact `clientMsgID` progress state, explicit validation/failure, safe HTTP(S) rendering, preview/download, real Node2 inbound media, and single/group history restoration. The final full Agent plus IM/contact/media suite passed serially in 42.1 seconds.
- The verified conversation-management extension adds projection-only search, real OpenIM pinning, and real `NotNotify` do-not-disturb with per-conversation concurrency protection. The final Agent plus conversation-management plus IM/contact/media suite passed serially in 48.4 seconds.
- The verified message-search extension repairs the pinned SDK Worker/WASM keyword-search ABI at install time, queries only the official synchronized local database, and navigates through official forward/reverse history APIs because the locked runtime does not register its declared `fetchSurroundingMessages` global. The complete six-scenario Node2 suite passed serially in 91.7 seconds.
- The verified device extension adds a member-scoped Platform API projection, exact current-device authorization, server-side OpenIM Admin calls, other-platform confirmation, request-order and duplicate-action protection, and distinct kicked/expired terminal states. A real Web plus Windows-platform run observed `OnKickedOffline`, then the full seven-scenario Node2 suite passed serially in 107.3 seconds.

## Open questions

- Production reverse-proxy and CSP headers enter the deployment slice before public exposure.
- Organization/department authorization and vector retrieval remain independent backend slices; OpenIM currently owns IM group authorization.
- Secure browser device enrollment remains a separate identity slice; device management reads existing enrollment and never self-enrolls a browser.
- Remove the Worker compatibility patch when an accepted upstream SDK release handles nullable batch payloads and dynamic history-table initialization itself; the patch refuses unknown upstream signatures.
