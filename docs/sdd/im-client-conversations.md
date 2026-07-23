---
unit: im-client-conversations
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - im-client-media
  - openim-adapter
---

# IM Client Conversation Management

## Scope

Add the bounded conversation-management slice to the verified Web IM client: filter the currently synchronized conversation projection, pin or unpin one conversation, and enable or disable per-conversation do-not-disturb while preserving real message synchronization.

This slice uses the pinned `@openim/wasm-client-sdk@3.8.3-patch.13` `setConversation` path and `OnConversationChanged`/`OnNewConversation` events. It does not add a Platform API, browser settings store, conversation database, server search endpoint, message-content search, archive, deletion, drafts, or scheduled do-not-disturb.

## Responsibilities and non-goals

`ConversationController` owns the ephemeral search query, per-conversation mutation lock, explicit failure state, and projection ordering. The OpenIM port owns the one `setConversation` invocation. `ChatWorkspace` owns search input, menu visibility, status icons, and accessible commands.

OpenIM owns `isPinned`, `recvMsgOpt`, conversation persistence, callback synchronization, unread state, and message delivery. The browser must not persist or reconcile a second copy.

## Contracts and dependencies

The locked SDK and current source establish these facts:

- `ConversationItem.isPinned` is the authoritative pin projection;
- `ConversationItem.recvMsgOpt` uses `MessageReceiveOptType.Normal=0`, `NotReceive=1`, and `NotNotify=2`;
- `setConversation({ conversationID, isPinned })` is the current non-deprecated pin mutation;
- `setConversation({ conversationID, recvMsgOpt })` is the current non-deprecated receive-option mutation;
- SDK `SetConversation` serializes synchronization with `conversationSyncMutex`, writes through Conversation RPC, then runs incremental conversation synchronization;
- `OnConversationChanged` returns authoritative changed conversation rows;
- OpenIM Server `ReceiveNotNotifyMessage` keeps message delivery/persistence but disables offline push;
- `NotReceiveMessage` is a different semantic and is explicitly not used as do-not-disturb.

If an existing conversation has `NotReceive`, the UI reports that unsupported receive state and disables this slice's mute command. It does not silently convert it to `Normal` or `NotNotify`.

## Invariants

- The authoritative conversation collection is never replaced by filtered search results.
- A settings write changes only the selected conversation and one accepted field.
- Pin and mute success is displayed only after `sdk.setConversation` resolves.
- Do-not-disturb always maps to `NotNotify`; `NotReceive` is never used or silently normalized.
- Same-conversation writes are serialized while different conversations remain independent.
- Reconnect and reload rebuild settings from OpenIM rather than browser persistence.

## State and concurrency model

React state adds only:

- `conversationQuery`: the current presentation filter;
- `conversationActionByID`: the active `pin` or `mute` mutation for each conversation.

Search normalization trims and lowercases the query. It matches `showName`, direct `userID`, and group `groupID` by substring. Filtering never mutates or replaces the synchronized `conversations` collection.

At most one settings write may run per conversation. Different conversations may update concurrently. A duplicate command for the same conversation fails explicitly. SDK success permits the controller to project the accepted field immediately; later callbacks merge idempotently. SDK failure preserves the prior field and removes the busy marker.

## Runtime flow

1. The SDK restores the conversation list into the existing controller.
2. Search input updates only `conversationQuery`; the visible list is derived from the authoritative collection.
3. The user opens one conversation menu and requests pin/unpin or do-not-disturb.
4. The controller verifies the conversation and receive-option state, installs a per-conversation mutation marker, and calls the port.
5. The port calls `sdk.setConversation` with exactly one accepted field.
6. On success the controller applies the accepted projection and clears the marker; `OnConversationChanged` later merges the synchronized row.
7. On failure the previous projection remains, the marker clears, and the UI exposes the error.
8. Reconnect and reload rebuild settings from OpenIM through the normal conversation restore path.

## Data ownership and state

OpenIM Conversation RPC and MongoDB own persistent settings. The SDK local database owns the synchronized browser projection. React owns only the current query, menu, and in-flight state. No conversation setting is written to Platform PostgreSQL, localStorage, sessionStorage, IndexedDB outside the SDK, or URL parameters.

## Failure handling

Unknown conversation IDs, duplicate per-conversation mutation, unsupported `NotReceive` state, SDK rejection, reconnect failure, and missing callbacks remain explicit. There is no alternate API, local success, delayed background write, or catch-and-continue path.

## Security

Only the current OpenIM User Token reaches `setConversation`. The browser never receives an Admin Token. Search text remains in memory and is not added to the URL or logs. Menu commands are derived from synchronized conversation state, not hidden authorization claims.

## Observability

The UI shows current pin/mute state, a stable per-conversation busy state, and explicit failure. Existing OpenIM operation IDs and server/SDK logs remain authoritative; no synthetic setting-sync metric is introduced.

## Acceptance criteria

- Search filters by name, user ID, and group ID without changing the authoritative list.
- Pinned conversations remain before unpinned conversations with stable latest-message ordering inside each group.
- Pin and mute writes use only `sdk.setConversation` and show success only after the SDK resolves.
- `NotNotify` messages still arrive, render, persist, and survive reload in the real Node2 environment.
- Reload/reconnect restores pin and mute state from OpenIM.
- Duplicate same-conversation writes fail explicitly while different conversations can proceed independently.
- SDK callbacks merge idempotently and controller teardown removes listeners.
- Desktop and mobile controls remain accessible without overlap or horizontal overflow.
- Existing OIDC, contacts, single/group text, media, and Agent E2E continue to pass.

## Planned follow-up slices

1. Group membership and lifecycle management.
2. Message quote, forward, revoke, and receipt actions.
3. Official SDK message search.
4. Multi-device login management.

## Open questions

- Production notification policy may also include operating-system and mobile push settings; this slice owns only OpenIM per-conversation `NotNotify`.
- Conversation archive and draft semantics require separate product decisions.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/enum.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/params.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/index.es.js`
- `openim-sdk-core/internal/conversation_msg/api.go`
- `openim-sdk-core/internal/conversation_msg/conversation.go`
- `openim-sdk-core/pkg/constant/constant.go`
- `open-im-server/internal/rpc/msg/verify.go`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/chat.test.ts`
- `platform/apps/web/e2e/node2-conversation-management.spec.ts`

## Verification evidence

- `npm test` passed 44 tests across seven files. The 23 conversation-controller tests cover name/user/group filtering, stable pin sorting, exact `NotNotify` mapping, unsupported `NotReceive`, failure preservation, duplicate same-conversation rejection, independent conversation writes, callbacks, reconnect, media, and listener cleanup.
- `npm run typecheck` and `npm run build` passed with the locked SDK unchanged.
- The focused Node2 E2E passed in 11.2 seconds. It normalized existing `imAdmin` settings, filtered the real projection, persisted pin across reload, persisted `NotNotify` across reload, received and restored a real Node2 message while muted, and reset both settings.
- The full serial `npm run test:e2e:node2` suite passed all three Agent, conversation-management, and IM foundation scenarios in 48.4 seconds.
- Desktop `1280x720` and mobile `390x844` menu screenshots passed visual inspection. Search, status icons, menu commands, unread badges, and mobile controls did not overlap or create horizontal overflow.
- The real browser checks found no HTTP failures, unexpected console errors, localStorage entries, or visible token material.
