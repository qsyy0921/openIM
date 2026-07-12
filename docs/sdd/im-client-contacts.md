---
unit: im-client-contacts
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - identity-session
  - openim-adapter
---

# IM Client Contacts

## Scope

Add the first OpenIM-owned contacts slice to the Web collaboration client: friend-list and friend-application projections, exact OpenIM user-ID lookup, friend request submission and processing, direct-chat entry from a friend, and one reusable member picker used by working-group creation.

This slice treats OpenIM friendship and friend-application state as authoritative. It does not introduce an organization directory, fuzzy enterprise people search, a second relationship store, friend deletion, blacklist management, remarks, group administration, media messages, or Agent capabilities.

## Responsibilities and non-goals

The contacts controller owns the in-memory presentation projection, request concurrency, stale-result rejection, operation state, explicit errors, and subscription cleanup. The official OpenIM WASM SDK owns synchronized friend/application data and every durable mutation. React owns the contacts workspace and member-picker interaction state. The existing conversation controller continues to own direct-chat and group creation.

`getUsersInfo([userID])` is the only admitted non-friend lookup. It is an exact user-ID lookup, not fuzzy search by name, email, or department. A future organization-domain slice must define enterprise discovery and authorization before broader search is added.

## Contracts and dependencies

The client-side contacts port uses the pinned `@openim/wasm-client-sdk@3.8.3-patch.13`:

- `getFriendList(true)` for the synchronized non-blacklisted friend projection;
- `getFriendApplicationListAsRecipient()` and `getFriendApplicationListAsApplicant()`;
- `getUsersInfo([userID])` for exact user-ID lookup;
- `addFriend({ toUserID, reqMsg })`;
- `acceptFriendApplication({ toUserID, handleMsg })` and `refuseFriendApplication(...)`;
- `OnFriendAdded`, `OnFriendDeleted`, `OnFriendInfoChanged`, and all friend-application callbacks.

No Platform API route, database table, token, or server patch is added. The member picker receives controller state and commands through component props and returns selected OpenIM user IDs to the existing group-creation path.

## Invariants

- OpenIM remains the only durable source for friendship, applications, users returned by exact lookup, and group membership.
- The current user is excluded from lookup results and member selection.
- Friends, lookup results, and selected members are deduplicated by `userID`.
- A stale lookup response cannot overwrite a newer query or a cleared result.
- A repeated submit while the same mutation is in flight is rejected by the UI and controller.
- Mutations report success only after the SDK call resolves; errors stay visible and never become empty-success state.
- SDK callbacks trigger an idempotent authoritative refresh; controller stop invalidates in-flight reads and removes every listener.
- Tokens and contact projections remain out of URLs, logs, `localStorage`, PostgreSQL, and application-owned durable storage.
- The member picker replaces manual member-ID text input; no hidden compatibility input or production fallback remains.

## Runtime flow

1. Existing OIDC, `/v1/im/session`, SDK login, and initial OpenIM synchronization complete.
2. The application starts the contacts controller for the authenticated OpenIM user and loads friends plus incoming/outgoing applications.
3. The controller subscribes to friend and application callbacks; each callback coalesces an authoritative refresh rather than trusting event ordering.
4. The contacts module renders friends, incoming requests, outgoing requests, and exact user-ID lookup as separate views.
5. Lookup trims and validates one OpenIM user ID, calls `getUsersInfo`, rejects stale results, excludes self, and annotates existing friendship/application state from the current projection.
6. Add, accept, and reject call the SDK once and then refresh the authoritative projection.
7. Starting a chat delegates the selected friend user ID to `ConversationController.openDirect` and switches to the messages module.
8. Group creation opens the reusable member picker, combines friends and exact lookup results, and passes unique selected IDs to `ConversationController.createGroup`.

## Data ownership and state

OpenIM Server owns relationship facts and application decisions. The OpenIM SDK local database owns the synchronized client copy. React state contains only the current projection, selected contacts tab, current exact lookup result, mutation/loading flags, dialog selection, and visible errors. Reload and reconnect reconstruct these values from the SDK.

## Failure handling

Initial load, refresh, lookup, request submission, acceptance, rejection, direct-chat opening, and group creation expose explicit errors. Empty lookup means the exact user ID was not returned, not that a fake user was created. Event refreshes are serialized/coalesced and final failure remains observable. There is no alternate transport, REST bypass, local relationship record, optimistic friendship, or catch-and-continue success.

## Security

All operations execute as the authenticated OpenIM user and rely on OpenIM authorization. User IDs and messages are trimmed and length-bounded before SDK calls. Rendered names, request text, and IDs use React text nodes. No Admin Token enters the browser and no secret-bearing payload is logged or snapshotted.

## Observability

The UI exposes initial loading, refresh, exact lookup, per-application mutation, group creation, empty, and error states. Existing SDK operation IDs and server logs remain authoritative. Contact telemetry and enterprise-directory audit events are future dedicated slices and are not simulated.

## Acceptance criteria

- Friends and incoming/outgoing applications load from the pinned SDK and update after callbacks without duplicates.
- Exact OpenIM user-ID lookup rejects self, stale responses, blank input, and missing users explicitly.
- Add, accept, and reject operations disable duplicate submission and expose SDK failures.
- A friend row opens the existing single-chat flow.
- The reusable member picker supports friend browsing, exact lookup, multi-select, deselect, de-duplication, and a visible selected summary.
- Working-group creation has no manual user-ID input and succeeds only from selected OpenIM users.
- Unit tests cover stale lookup, callback refresh coalescing/idempotence, duplicate operations, duplicate member selection, group failure, and listener cleanup.
- Typecheck, production build, repository validation, SDD validation, real Node2 IM/Agent regression, contact E2E, and desktop/mobile visual checks pass.
- Real E2E removes created groups and any friend/request fixture state it changes.

## Planned follow-up slices

1. Image and file messages with authoritative upload progress and explicit failure recovery.
2. Conversation pin, mute, drafts, and message search.
3. Group administration and member mutation.
4. Enterprise organization directory and governed people search after its ownership and authorization model are designed.

## Open questions

- The enterprise directory source, tenant boundary, and fuzzy-search contract remain intentionally undecided.
- Whether a later contacts product composes OpenIM friends with organization members requires a separate architecture decision.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/params.d.ts`
- `openim-sdk-core/internal/relation/api.go`
- `openim-sdk-core/internal/relation/relation.go`
- `platform/apps/web/src/App.tsx`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/ChatWorkspace.tsx`

## Verification evidence

- The pinned SDK type definitions and `openim-sdk-core/internal/relation` implementation confirmed the exact lookup, local synchronized friend/application reads, mutation parameters, and seven subscribed callback events; no Platform API or schema change was required.
- `npm run typecheck`, 32 Vitest tests, and `npm run build` passed. The focused tests cover stale exact lookup, callback refresh de-duplication, duplicate mutation rejection, listener cleanup, accumulated lookup candidates, unique member selection, and explicit group-creation failure.
- `python ops/validate-repository.py`, strict unit-SDD validation, `git diff --check`, and the no-fallback/secret scope audit passed.
- The final full serial Node2 Playwright suite passed both the governed Agent scenario and the IM/contact scenario in 37.5 seconds. The contact scenario used the real WASM SDK to find `imAdmin`, submit an application, observe the outgoing projection, accept it through the real OpenIM relation API, observe the friend callback/list, open direct chat, create a group through MemberPicker, send group text, reload history, and retain the prior reconnect behavior.
- `ops/manage-node2-web-e2e-friend.ps1` and `.sh` normalize the fixture through OpenIM relation APIs and remove the friendship after each run; the existing group helper dismisses the created group. The E2E leaves no active test friendship or group from the run.
- Desktop contacts and MemberPicker plus mobile contacts and MemberPicker screenshots were inspected at `1280x720` and `390x844`. Navigation labels, candidate rows, selected-member chips, dialogs, and action controls fit without overlap or horizontal overflow.
- Browser assertions found no token-shaped visible text, no `localStorage` entries, no failed HTTP responses, and no unexpected console errors. The previously documented exact OpenIM group-conversation race remains bounded to at most one diagnostic.
