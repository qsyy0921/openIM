---
unit: im-client-foundation
status: verified
depends_on:
  - web-client
  - identity-session
  - openim-adapter
  - adr-0001
  - adr-0003
---

# IM Client Foundation

## Scope

Evolve the verified Web single-chat slice into a reusable IM client foundation before expanding the Agent platform. The first admitted slice supports one unified single/group conversation list, group text history and real-time messages, creating a working group from explicit OpenIM user IDs, and a read-only group-member view.

It preserves enterprise OIDC, `/v1/im/session`, the official OpenIM WASM SDK, SDK-owned local synchronization, in-memory credentials, explicit failures, and the existing Agent workspace. It does not implement contacts, friend applications, organization directory, media/file messages, search, pin/mute controls, group administration, calls, documents, or another transport.

## Responsibilities and non-goals

The Web client owns presentation state, conversation selection, optimistic text-send state, bounded group-member presentation, and mapping user commands to the official SDK. OpenIM owns group membership, conversation/message state, local SDK storage, synchronization, and server authorization. Platform API continues to own enterprise identity exchange. This unit does not duplicate OpenIM group or message facts in PostgreSQL and does not treat a client-side optimistic message as server acceptance. The contacts/member-picker extension is specified in `im-client-contacts.md`, and the admitted image/file extension is specified in `im-client-media.md`, so the verified conversation foundation remains stable.

## Open-source reuse boundary

- The application continues to depend on the pinned `@openim/wasm-client-sdk`; OpenIM remains the source of truth for conversations, groups, members, messages, unread state, and local synchronization.
- The pinned Worker compatibility script fail-closed patches two upstream dynamic-history-table defects: `getMessageList` and `getMessagesByClientMsgIDs` must initialize the conversation table just as neighboring read functions already do. The script requires one exact minified signature per defect in each Worker and aborts when upstream changes.
- `openimsdk/openim-electron-demo` and `openimsdk/openim-flutter-demo` are read-only references for SDK call sequences and feature coverage. Their AGPL/additional license terms prohibit copying their implementation into this repository without a separate legal decision.
- Mattermost and Element are product-structure references for modular navigation, conversation layouts, settings, accessibility, and large-list behavior. Their code is not imported.
- The existing React 19 client remains the product codebase. There is no replacement client or hidden compatibility transport.

## Contracts and dependencies

The client-side OpenIM port exposes:

- list all supported `SessionType.Single` and `SessionType.Group` conversations;
- resolve one single or group conversation by source ID;
- load text history by conversation ID;
- send text using `recvID` for single chat or `groupID` for group chat;
- create `GroupType.WorkingGroup` with a name and explicit member user IDs;
- list group members with bounded pagination;
- mark the selected conversation read and subscribe to existing conversation/message/unread callbacks.

No new Platform API or database table is required. OpenIM owns all new durable facts in this slice.

## Invariants

- The controller does not infer a default destination; a current conversation is required before send.
- Single messages match by sender/receiver pair; group messages match by exact `groupID`.
- Group conversations marked `isNotInGroup` by OpenIM after exit or dismissal are excluded from the active conversation list; history remains owned by OpenIM rather than rewritten locally.
- Only text messages enter the current timeline in this slice. Unsupported message types remain in OpenIM and are not misrendered as text.
- Conversation and message callbacks remain deduplicated by stable IDs.
- Group creation success is reported only after OpenIM returns a group and its conversation can be resolved.
- Client errors are visible. Group creation, history, member loading, and send failures do not synthesize local success.
- OpenIM User Token remains memory-only; no new state is persisted in `localStorage`.

## Runtime flow

1. Existing OIDC and `/v1/im/session` establish the OpenIM SDK session.
2. The unified controller restores single and group conversations plus total unread count from the SDK local database.
3. Selecting a conversation loads text history and marks that conversation read.
4. Selecting a group additionally loads a bounded member page from OpenIM.
5. Sending text creates an SDK message, renders an optimistic state, and routes by conversation type.
6. OpenIM callbacks merge conversation, unread, and message changes by stable IDs.
7. Creating a group calls OpenIM, waits for the SDK conversation event, selects the empty group without an unnecessary history query, loads members, and exposes failures without fallback. Later selection and reload use the normal history path.

## Data ownership and state

OpenIM Server and SDK local storage own conversations, groups, members, unread counters, and messages. Keycloak owns the enterprise login session; Platform API owns the short-lived identity-to-OpenIM session exchange. React state contains only the active projection, group-dialog input, connection status, and optimistic message status. Reload and reconnect rebuild from the SDK rather than a second client store.

## Failure handling

Unsupported conversation types are excluded explicitly. History, read-state, send, group creation, conversation resolution, and member-list failures remain visible in `ChatState.error`. The failed operation may be retried against the same SDK path; no local group, fake member, alternate transport, or silent success is generated.

The pinned SDK emits one exact `updateColumnsConversation no record updated` diagnostic while a newly created group notification races the later `OnNewConversation` insert. Returning false success from the database adapter would prevent the SDK's convergence path, so the application does not suppress or rewrite it. Real E2E permits at most this exact diagnostic and still requires final conversation, history, member, and message state; every other console error fails the suite.

## Security

The change introduces no new credential or server API. OIDC state remains in session storage and the OpenIM User Token remains memory-only. Group operations execute as the authenticated OpenIM user and rely on OpenIM authorization. User-entered IDs are trimmed, deduplicated, bounded by the SDK request, and never interpreted as SQL, HTML, or an endpoint.

## Observability

The UI exposes connection, restore, loading, optimistic send, and explicit error states. Existing OpenIM operation IDs and server logs remain the runtime authority. Client telemetry, group-operation latency metrics, and error aggregation require a later observability slice and are not simulated locally.

## Acceptance criteria

- Existing single-chat unit and real Node2 behavior remain unchanged.
- Single and group conversations are restored and sorted together.
- Group history and real-time group text are scoped by exact group ID.
- Group send uses an empty receiver ID and the selected group ID.
- Creating a group opens its OpenIM conversation and exposes a read-only member list.
- Failed group creation and member loading are visible and do not report success.
- Unit tests, TypeScript checks, production build, repository validation, and focused browser layout checks pass.
- Real E2E dismisses the group it creates through a server-side admin helper so repeated verification does not accumulate product data.
- Agent workspace behavior and navigation remain available but receive no new capability in this slice.

## Planned follow-up slices

1. Image and file messages with MinIO-backed upload progress and failure recovery.
2. Conversation pin, mute, drafts, local/global message search, and message actions.
3. Group administration, mentions, receipts, and notification rendering.
4. Account, device, privacy, appearance, and notification settings.
5. One-to-one call evaluation; multi-party meetings remain a separate architecture decision.
6. Only after the client foundation is stable: Agent Catalog, Tool/Skill Registry, MCP, Memory, and business Agents.

## Verification evidence

- TypeScript typecheck, 23 Vitest tests, and the production Vite build passed with the pinned SDK, including stale group-member result rejection.
- Repository contract/SDD validation and `git diff --check` passed.
- The full serial Node2 Playwright suite passed both the governed Agent workspace and IM foundation scenarios in 49.4 seconds.
- The final focused Node2 IM rerun passed in 28.5 seconds after adding desktop member-panel and mobile group-chat screenshot checks.
- The real IM scenario preserved single-chat unread, bidirectional text, reconnect, and reload behavior; it also created an OpenIM working group, sent group text, loaded members, restored group history after reload, and dismissed the test group through the server-side cleanup helper.
- The compatibility script ran twice successfully, proving idempotence and exact-signature checks for both Worker variants.
- Desktop group chat plus mobile single-chat/list screenshots passed visual inspection and horizontal-overflow assertions.
- The only admitted console diagnostic is the exact, bounded OpenIM new-group conversation update race documented under Failure handling; no HTTP failures or other console errors occurred.

## Open questions

- Whether enterprise contacts should be sourced from OpenIM friendship, the future organization domain, or a composed read model.
- Whether desktop packaging should reuse the official Electron SDK or continue with browser WASM inside a thin desktop shell.
- Enterprise media retention, malware scanning, DLP, and download audit policy remain open; the verified transport path is the pinned OpenIM SDK through third RPC to Node2 MinIO as specified in `im-client-media.md`.
- Whether one-to-one calling uses the OpenIM commercial/LiveKit path or an independently governed meeting service.

## Source evidence

- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/openim.ts`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/src/chat.test.ts`
- `docs/sdd/im-client-contacts.md`
- `docs/sdd/im-client-media.md`
- `platform/apps/web/scripts/patch-openim-worker.mjs`
- `ops/cleanup-node2-web-e2e-group.ps1`
- `ops/cleanup-node2-web-e2e-group.sh`
- `dependencies/openim.lock.yaml`
- <https://github.com/openimsdk/openim-electron-demo>
- <https://github.com/openimsdk/open-im-flutter-demo>
- <https://github.com/mattermost/mattermost/tree/master/webapp>
- <https://github.com/element-hq/element-web>
