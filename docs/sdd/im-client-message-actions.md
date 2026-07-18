---
unit: im-client-message-actions
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - im-client-media
  - im-client-conversations
  - openim-adapter
---

# IM Client Message Actions

## Scope

Add the bounded message actions supported by the locked OpenIM WASM SDK: quote one text, image, or file message in a text reply; forward one succeeded text, image, or file message to one explicit single/group conversation; revoke one message through OpenIM; and project authoritative single-chat read receipts.

This slice does not add batch or merged forwarding, editing, reactions, favorites, reports, local deletion, custom revoke policy, group reader lists, read-by-member panels, or another message store.

## Responsibilities and non-goals

`ConversationController` owns active-message validation, one in-flight message action, official message creation/sending, callback reconciliation, and explicit failure. The OpenIM port owns official SDK calls and subscriptions. `ChatWorkspace` owns a message-scoped action menu, quote composer context, target-conversation selection, revoke confirmation, and read/revoked rendering.

OpenIM owns message content, status, revoke authorization/window, local SDK persistence, and read receipts. Browser state is an ephemeral projection and never invents a receipt or mutates the authoritative SDK database directly.

## Contracts and dependencies

- `createQuoteMessage({ text, message: JSON.stringify(source) })` creates an official `QuoteMessage` with `quoteElem` and prevents nested quote chains in SDK core.
- `createForwardMessage(source)` accepts only a succeeded Text, Picture, or File message and recreates the same official content type before normal `sendMessage` delivery to one target conversation.
- `revokeMessage({ conversationID, clientMsgID })` delegates sender/admin authorization and the server time window to OpenIM.
- A successful `revokeMessage` permits an immediate React-only revoked projection; `OnNewRecvMessageRevoked` identifies later replacements by `clientMsgID`, and history represents the source and nested quote as `MessageType.RevokeMessage`.
- `OnRecvC2CReadReceipt` supplies authoritative `clientMsgID` values in `ReceiptInfo.msgIDList`; only matching outgoing single-chat messages become read.
- Existing history and receive paths accept Text, Picture, File, Quote, and Revoke message types and deduplicate by `clientMsgID`.

## Invariants

- Quote source, revoke source, and forward source must exist in the active loaded timeline when the action begins.
- Quote replies use the SDK quote structure and never concatenate quoted text into a normal text message.
- Forward requires one explicit, currently available target conversation distinct from no target; it never silently sends to the active conversation.
- Only succeeded non-revoked messages are forward candidates.
- Revoke success is shown only after the SDK call resolves; failed revoke keeps the original message and displays the SDK error.
- Revoke callbacks are idempotent and update exactly one matching message; unknown IDs do not change the timeline.
- Read UI changes only from history `isRead` or `OnRecvC2CReadReceipt`, never from focus, elapsed time, or local send completion.
- A receipt for another peer, group, unknown message, or incoming message cannot mark an outgoing single-chat message read.
- Selecting another conversation clears quote/action UI state so a source cannot cross conversation scope.

## Runtime flow

1. The user opens the menu on one loaded message.
2. Reply records the source only in React state; submitting text asks the controller to create an official quote draft and send it through the active conversation.
3. Forward opens the current OpenIM conversation list, requires one target, creates an official forward draft, and sends it through that target.
4. Revoke opens a destructive confirmation and calls the official SDK revoke API for the active conversation and source `clientMsgID`.
5. After the SDK write resolves, the controller replaces the matching React projection with a revoke notification while preserving identity/order fields. Detailed callbacks apply the same replacement idempotently, and history remains authoritative after reload.
6. When a peer reads outgoing single-chat messages, the SDK persists `isRead` and emits C2C receipts; the controller marks only listed outgoing messages read.
7. Reload restores quote, revoke, and `isRead` state from SDK history.

## Data ownership and state

OpenIM Server and SDK IndexedDB remain authoritative. React adds only an in-memory quote source, action target/dialog state, one in-flight message-action marker, and a post-success revoked projection. No message, receipt, or revoke state is written to Platform PostgreSQL, `localStorage`, or a custom API.

## Failure handling

Missing active conversation, stale source, unsupported type/status, empty quote text, missing/invalid forward target, concurrent action, SDK create/send/revoke rejection, unknown callback IDs, and stale receipt scope remain explicit. Failed writes never close the relevant dialog or display success. No alternate production REST path, direct SDK-database rewrite, retry-as-success, or synthetic receipt exists.

## Security

The browser uses only the current OpenIM User Token. Message text, URLs, file metadata, and tokens are not logged. Revoke confirmation names the target message without rendering untrusted HTML. OpenIM remains authoritative for sender/admin permission and time-window enforcement.

## Observability

The UI exposes the active action and exact SDK error. Existing OpenIM operation IDs, message RPC logs, callback events, and persisted history remain authoritative. E2E helpers may operate a second real identity only inside Node2 and must not return its token to the browser or logs.

## Acceptance criteria

- A text reply to a real text, image, or file source is stored and restored as `QuoteMessage` with official `quoteElem` data.
- One succeeded message can be forwarded to one explicit single or group target through the official create/send path.
- Allowed revoke replaces the source with a stable revoked presentation; denied revoke preserves the source and exposes the OpenIM error.
- Duplicate revoke and receipt callbacks are idempotent; unknown or out-of-scope callbacks do not mutate messages.
- Outgoing single-chat messages display sent/read from authoritative history or C2C receipts.
- Existing text/media/conversation/group/Agent behavior continues to pass.
- Desktop and mobile action menu, quote context, forward target dialog, and revoke confirmation have no overlap or horizontal overflow.

## Planned follow-up slices

1. Official SDK local message search.
2. Multi-device login management.

## Open questions

- Group reader counts and reader lists require a separate product slice around `sendGroupMessageReadReceipt` and `getGroupMessageReaderList`.
- Merged forwarding requires an explicit summary/privacy design and is outside this Goal.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/eventData.d.ts`
- `openim-sdk-core/internal/conversation_msg/create_message.go`
- `openim-sdk-core/internal/conversation_msg/revoke.go`
- `openim-sdk-core/internal/conversation_msg/read_drawing.go`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/chat.test.ts`
- `platform/apps/web/e2e/node2-message-actions.spec.ts`
- `platform/apps/web/e2e/peer-openim.ts`
- `ops/issue-node2-web-e2e-peer-token.ps1`
- `ops/issue-node2-web-e2e-peer-token.sh`

## Verification evidence

- `npm test`: 7 files and 57 tests passed, including quote/forward creation, explicit target routing, unsupported/stale/concurrent rejection, failed-revoke preservation, post-success revoke projection, callback idempotency, nested quote revocation, and C2C receipt scope.
- `npm run typecheck` and `npm run build`: passed against locked SDK `3.8.3-patch.13`; the production bundle emitted both SDK workers and the Web assets.
- `python ops/validate-repository.py`: passed. Strict unit-SDD validation passed with zero warnings.
- PowerShell parser and `bash -n` passed for the isolated Node2 peer-token helper. The Admin Token remained inside Node2 WSL; the ordinary test User Token was held only in the isolated Playwright process and second browser context.
- Focused real Node2 E2E passed in 23.4 seconds. Two isolated WASM SDK identities created/restored bidirectional official Quote messages; the normal peer identity used the official exact-seq read API to produce a real C2C receipt; sender and denied-peer revoke paths converged; and Text messages forwarded single-to-group and group-to-single.
- Full serial Node2 regression passed 5 tests in 80.7 seconds, covering Agent, conversation settings, contacts/text/media/group foundations, group lifecycle, and message actions.
- Reload proved authoritative Quote/Revoke/read persistence, including OpenIM rewriting a quote source to `RevokeMessage` after the source was revoked.
- Desktop forward/revoke dialogs and mobile reply context screenshots passed visual inspection without overlap or horizontal overflow. Browser request and console assertions remained active.
- Source inspection and real runtime behavior showed that forwarding a nested Quote does not complete in the locked SDK; production policy therefore admits only succeeded Text, Picture, and File sources, matching this slice's stated scope.
