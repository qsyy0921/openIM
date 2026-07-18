---
unit: im-client-message-search
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - im-client-media
  - im-client-message-actions
  - openim-adapter
---

# IM Client Message Search

## Scope

Add bounded keyword search over messages already synchronized into the locked OpenIM WASM SDK local database. The UI searches one currently selected conversation, pages through official SDK results, displays safe result metadata, and jumps to a context assembled from official forward and reverse history queries.

This slice does not add global enterprise search, server search, Elasticsearch, OCR, semantic/vector search, RAG, attachment-content indexing, browser traversal, or a custom index.

## Responsibilities and non-goals

`MessageSearchController` owns query validation, one current-conversation scope, request sequencing, 20-item pages, result flattening, loading/empty/error state, and stale-result rejection. The OpenIM search port owns `searchLocalMessages`. `ConversationController` owns bidirectional advanced-history navigation and active timeline projection. `ChatWorkspace` owns the search panel, pagination, result summaries, and target highlighting.

OpenIM SDK IndexedDB is the only search index and result authority. The browser does not scan loaded messages or persist another index.

## Contracts and dependencies

- `searchLocalMessages` accepts `conversationID`, `keywordList`, `keywordListMatchType`, `messageTypeList`, `pageIndex`, and `count`.
- Locked SDK core keyword indexing admits Text, AtText, and File; this product renders Text and File search hits.
- A non-empty `conversationID` is required in this slice because only conversation-scoped SDK search applies `pageIndex/count`.
- `keywordListMatchType=0` is official OR matching; this slice submits one trimmed keyword.
- Results are grouped as `SearchMessageResultItem[]`; the controller flattens only the exact requested conversation and preserves each `MessageItem`.
- The package declares `fetchSurroundingMessages`, but this locked runtime does not register its required global WASM function. The single supported jump path therefore combines `getAdvancedHistoryMessageList` and `getAdvancedHistoryMessageListReverse` around the exact result with `ViewType.Search` and a 20-message bound in each direction.

## Invariants

- Search requires an available active single/group conversation and a 1-100 character trimmed query.
- Every request captures conversation ID, query, and page; a later request invalidates every earlier result and error.
- Page is one-based, page size is fixed at 20, and next-page availability derives from official `totalCount`.
- Only official Text and File keyword results are displayed; unsupported content is never synthesized.
- Safe summaries use React text and file names only; message content is not interpreted as HTML.
- Jump requires a result still belonging to an available local conversation.
- A jump replaces the timeline only with official older/newer history results plus the authoritative search hit, preserves message identity, and highlights the exact `clientMsgID`.
- Closing search clears ephemeral result state but never deletes SDK search data.

## Runtime flow

1. The user selects a conversation and opens message search.
2. The controller validates the query, increments its request sequence, and calls official local search for page 1.
3. SDK queries its synchronized IndexedDB Text/AtText/File fields and returns grouped results and total count.
4. The controller ignores stale completions, flattens the requested group, and publishes results.
5. Previous/next submit the same captured query and conversation with an adjacent page.
6. Selecting a result calls both official advanced-history directions around its `clientMsgID`, merges the exact hit, switches to that conversation context, and scrolls/highlights the message.
7. Reload does not restore the ephemeral query; a new search reads the rebuilt SDK local index.

## Data ownership and state

OpenIM Server owns durable messages and OpenIM SDK IndexedDB owns the synchronized search index. React stores only the current query, scope, page, total, hits, request phase, and highlighted target. No search document is written to Platform PostgreSQL, `localStorage`, or a custom service.

## Failure handling

Missing conversation, invalid query, invalid page, SDK rejection, stale completion, missing conversation on jump, and either history-direction failure remain explicit. Failed search preserves no false results, and failed jump preserves the current timeline. No browser scan, remote search, cached-result success, or alternate index is permitted.

## Security

Search uses the current OpenIM User Token through the locked SDK. Results are limited to the current user's synchronized local database. Query/message text and tokens are not logged, placed in URLs, or persisted by this UI.

## Observability

The UI exposes loading, empty, exact SDK error, page, and total count. Existing OpenIM operation IDs and SDK logs remain authoritative. Tests use nonce messages without logging session tokens.

## Acceptance criteria

- A unique real Text message in the active single conversation is found through official local search after synchronization/reload.
- File-name keyword search is covered by deterministic tests; unsupported Picture content is excluded from keyword results.
- Page 1/2 parameters, next/previous bounds, stale request rejection, errors, and close/reset are deterministic.
- Selecting a hit fetches official older/newer context, activates the correct conversation, and highlights the exact message.
- Empty and invalid queries never invoke the SDK.
- Existing OIDC, contacts, text/media, conversation settings, group lifecycle, message actions, and Agent regressions pass.
- Desktop and mobile search panels have no overlap or horizontal overflow.

## Planned follow-up slices

1. Multi-device login management.

## Open questions

- Locked SDK global search does not apply page/count; exposing it requires a separately bounded UX and measured result limit.
- Quote keyword indexing and image OCR are not part of `SearchContentType` in this SDK and require upstream or external design.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/params.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `openim-sdk-core/internal/conversation_msg/conversation_msg.go`
- `openim-sdk-core/internal/conversation_msg/conversation.go`
- `openim-sdk-core/pkg/sdk_params_callback/conversation_msg_sdk_struct.go`
- `platform/apps/web/src/message-search.ts`
- `platform/apps/web/src/message-search.test.ts`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/scripts/patch-openim-worker.mjs`
- `platform/apps/web/e2e/node2-message-search.spec.ts`

## Verification evidence

- `npm run postinstall` passed twice, proving the pinned Worker compatibility patch applies and then remains idempotent. The patch fails closed on an unknown search registration signature.
- Vitest passed all 8 files and 64 tests, including pagination bounds, stale-search suppression, explicit errors, controller close, exact-context navigation, failed-context preservation, and unsupported result filtering.
- `npm run typecheck`, `npm run build`, `python ops/validate-repository.py`, strict unit-SDD validation, and `git diff --check` passed. The production build transformed 1,790 modules.
- The focused real Node2 E2E passed in 9.5 seconds: the Web SDK sent a unique Text message, reload rebuilt synchronized history, official local search found exactly one result, bidirectional official history navigation highlighted the exact `clientMsgID`, and a second reload found the same message again.
- Desktop `1280x720` and mobile `390x844` screenshots were inspected; the search panel remained readable without overlap or horizontal overflow. Browser checks found no failed HTTP responses, unexpected console errors, localStorage entries, or visible token text.
- The complete serial Node2 suite passed all 6 Agent, conversation, IM/media, group-lifecycle, message-action, and message-search scenarios in 91.7 seconds.
