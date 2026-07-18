---
unit: im-client-group-lifecycle
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - im-client-contacts
  - im-client-conversations
  - openim-adapter
---

# IM Client Group Lifecycle

## Scope

Complete the bounded lifecycle of an existing OpenIM working group in the Web client: invite non-members through the reusable member picker, remove a member according to synchronized role constraints, let a non-owner leave, and let the owner dismiss the group.

The slice uses only the locked SDK `inviteUserToGroup`, `kickGroupMember`, `quitGroup`, `dismissGroup`, group-member queries, and group callbacks. It does not add a Platform API, relationship store, department group, transfer-owner UI, role editor, group mute, announcement, application workflow, group file space, or server permission extension.

## Responsibilities and non-goals

`ConversationController` owns active-group validation, one active lifecycle mutation, member refresh, role-policy projection, explicit failure, and post-leave/dismiss cleanup. The OpenIM port owns official SDK calls and subscriptions. `MemberPicker` may exclude current members but does not own group state. `ChatWorkspace` owns invite and destructive-action dialogs.

OpenIM owns group existence, membership, role level, authorization, synchronization, and notification messages. Browser role checks only hide impossible commands early; server authorization remains final.

## Contracts and dependencies

- `GroupMemberRole.Normal=20`, `Admin=60`, and `Owner=100` are synchronized in each `GroupMemberItem`;
- `inviteUserToGroup({ groupID, reason, userIDList })` performs the official invite path and incremental group/member sync;
- `kickGroupMember(...)` rejects self-removal and owner removal; an admin cannot remove the owner or another admin;
- `quitGroup(groupID)` rejects the owner and synchronizes joined groups after success;
- `dismissGroup(groupID)` is owner-only and synchronizes joined groups after success;
- member add/delete/info callbacks identify the affected group and trigger member refresh;
- joined-group deletion and group-dismissed callbacks remove unavailable group detail and conversation projection;
- the existing `MemberPicker` keeps OpenIM contacts/search as its only candidate source.

## Invariants

- Every action targets the currently active working group and revalidates it immediately before the SDK call.
- Existing members, the current user, empty IDs, and duplicates never reach the invite call.
- Only owner/admin UI roles can invite in this product slice.
- Owner can remove non-owner members; admin can remove only normal members; normal members cannot remove anyone.
- Self-removal always uses `quitGroup`, never `kickGroupMember`.
- Owner exit is represented only as dismiss; transfer ownership is outside this UI slice.
- A lifecycle success is displayed only after the official SDK call resolves.
- Failed operations preserve the current group and member projection.
- Leave/dismiss clears active group state only after SDK success or an authoritative unavailable-group callback.

## Runtime flow

1. Selecting a group loads the member list including the current user's role.
2. Owner/admin opens invite, and `MemberPicker` excludes current group members and self.
3. The controller deduplicates candidates, installs a group mutation marker, and calls the SDK invite path.
4. SDK incremental synchronization and member callbacks converge the local member list; the controller refreshes it explicitly after success.
5. A permitted remove command uses one explicit confirmation and `kickGroupMember` for exactly one target.
6. A normal/admin member confirms leave and calls `quitGroup`; an owner confirms dismiss and calls `dismissGroup`.
7. Successful leave/dismiss or an authoritative group-unavailable callback removes the conversation projection, messages, members, progress state, and active selection.
8. Reconnect restores surviving groups and members from OpenIM.

## Data ownership and state

OpenIM Server and MongoDB own group and member facts; SDK IndexedDB owns the synchronized projection. React adds only one active group mutation and dialog input. Existing members are passed as ephemeral exclusions to `MemberPicker`; no member list is copied to Platform PostgreSQL or browser storage.

## Failure handling

Missing active group, missing self member row, stale member target, invalid role, duplicate mutation, empty invite, SDK rejection, callback race, and unavailable group remain explicit. A failed dismiss/leave never closes the group locally. No admin-token browser path, direct RPC, local membership edit, retry-as-success, or alternate store is permitted.

## Security

The browser uses only the current OpenIM User Token. Destructive actions require explicit confirmation and display the concrete group/member target. The reusable picker renders names and IDs as React text. Server role checks remain authoritative even when UI policy permits an action.

## Observability

The UI exposes the active group action and exact SDK failure. Existing OpenIM operation IDs, group RPC logs, notifications, and SDK callbacks remain authoritative. Test helpers may use a Node2 Admin Token only inside Node2 to prepare/clean fixtures; it never reaches browser state or logs.

## Acceptance criteria

- Owner/admin can invite one real non-member selected by the reusable picker; current members are excluded.
- Owner can remove a normal member, and admin/normal restrictions are covered by deterministic tests.
- Non-owner can leave and owner can dismiss through distinct confirmed commands.
- Failed operations keep the prior projection and expose an error.
- Member callbacks refresh only the matching active group and remain idempotent.
- Leave/dismiss callbacks and successful writes clear unavailable active state.
- Node2 E2E verifies invite, remove, owner dismiss, transferred-owner/non-owner leave, reload, and cleanup without mock success.
- Existing conversation, contact, text, media, and Agent regressions pass.
- Desktop/mobile member panel, picker, and confirmations have no overlap or overflow.

## Planned follow-up slices

1. Message quote, forward, revoke, and receipts.
2. Official SDK message search.
3. Multi-device login management.

## Open questions

- Transfer-owner and role editing require a separate product/authorization slice.
- Group verification applications and enterprise department groups require independent workflows.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/enum.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/params.d.ts`
- `openim-sdk-core/internal/group/api.go`
- `open-im-server/internal/rpc/group/group.go`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/MemberPicker.tsx`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/src/chat.test.ts`
- `platform/apps/web/src/MemberPicker.test.ts`
- `platform/apps/web/e2e/node2-group-lifecycle.spec.ts`
- `ops/manage-node2-web-e2e-group-lifecycle.ps1`
- `ops/manage-node2-web-e2e-group-lifecycle.sh`

## Verification evidence

- `npm test`: 7 files and 51 tests passed, including role policy, invite deduplication/exclusion, concurrent mutation rejection, failed-operation preservation, callback scoping/idempotency, listener cleanup, and stale-conversation tombstone coverage.
- `npm run typecheck` and `npm run build`: passed against the locked WASM SDK; the production build emitted the SDK workers and Web bundle.
- `python ops/validate-repository.py`: passed.
- Strict unit-SDD validation passed with zero warnings.
- `npm run test:e2e:node2`: 4 real Node2 tests passed in 58.9 seconds. The lifecycle test used two real identities to invite/remove a member, dismiss an owner group, transfer ownership through an isolated Node2 fixture helper, and leave as a non-owner; after-test cleanup removed owned fixtures.
- Playwright screenshots verified desktop invite/removal dialogs and the mobile member panel without overlap or overflow. Browser console and request failure assertions remained active in the Node2 suite.
