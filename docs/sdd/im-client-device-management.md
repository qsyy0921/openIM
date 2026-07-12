---
unit: im-client-device-management
status: verified
depends_on:
  - identity-session
  - platform-api
  - web-client
  - openim-adapter
---

# IM Client Device Management

## Scope

Add a member-scoped device and platform-session workspace. An authenticated enterprise member can inspect the devices enrolled for that member, distinguish the current device, see OpenIM's platform-level online projection, and explicitly log out another enrolled platform. A kicked client leaves the active IM workspace with a distinct terminal state.

This slice does not add an administrator device console, device fingerprinting, MDM, password management, MFA, per-token inspection, same-platform device selection, or a second token authority.

## Responsibilities and non-goals

Platform API owns OIDC authorization, current-device enrollment checks, same-member ownership, safe device projection, and the server-side OpenIM Admin call. OpenIM remains authoritative for platform connectivity and Token invalidation. PostgreSQL remains authoritative only for enterprise device enrollment. The Web device controller owns request ordering, loading/error state, refresh, logout confirmation state, and duplicate-submit protection. It does not infer a device login from enrollment.

## Contracts and dependencies

- `GET /v1/im/devices?platform_id=<current>&device_id=<current>` returns only devices belonging to the authenticated member and OpenIM online platform IDs for that member.
- `POST /v1/im/platforms/{platform_id}/logout` accepts the current device context, rejects the current platform, validates that the target platform is enrolled for the same member, and invokes OpenIM `ForceLogout(userID, platformID)` server-side.
- The browser sends only the enterprise ID Token. OpenIM Admin Token and User Token values never enter either response.
- `identity.member_devices.status` is enrollment authorization, not online state. `updated_at` is shown only as enrollment metadata, never as last activity.
- OpenIM `GetUsersOnlineStatus` is reduced to unique platform IDs. Connection IDs, remote addresses, background state, and Token fields are discarded server-side.
- OpenIM `ForceLogout` invalidates every Token and connection for one user/platform. The current platform is rejected because OpenIM cannot select another device on the same platform.

## Invariants

- Every read/write first verifies the OIDC principal and exact current `device_id + platform_id` enrollment.
- The OpenIM user ID is read from the authenticated member's ready identity link; the client cannot submit a user ID.
- The target platform must be valid, different from the current platform, and present in the member's enrollment records.
- Logout is idempotent at the contract level: an already-offline target remains a successful logged-out state.
- A client displays success only after Platform API and OpenIM both return success.
- The UI exposes the response correlation ID for the last successful logout and blocks a duplicate in-flight command.
- `OnKickedOffline`, Token expiration/invalidity, and transport failure remain distinct connection states. Kicked/expired states leave the connected workspace.
- No device or online projection is copied to localStorage or a new database table.

## Runtime flow

1. Selecting the device module sends the enterprise ID Token and configured current device context to Platform API.
2. Platform API verifies OIDC, resolves the active member/device, reads that member's enrolled devices and ready OpenIM identity link, then queries OpenIM online status for only that user.
3. Platform API strips connection and Token details, merges platform-level online booleans into the enrollment projection, and marks the exact current device.
4. The user selects an online, non-current platform and confirms logout.
5. Platform API repeats identity/device/ownership checks and calls OpenIM `ForceLogout` for the authenticated member's OpenIM user and target platform.
6. The target gateway closes matching connections and marks target-platform Tokens kicked. The target SDK emits `OnKickedOffline` and leaves its active workspace.
7. The initiating client refreshes the authoritative projection and displays the request correlation ID.

## Data ownership and state

Keycloak owns the enterprise session. `identity.member_devices` owns enrollment records. `identity.identity_links` owns the member-to-OpenIM mapping. OpenIM gateway and Redis Token state own online/kicked truth. React stores only the current projection, loading/action state, explicit error, and last correlation ID in memory.

## Failure handling

Missing/invalid device context, inactive member/device, unready identity link, unsupported/current/unenrolled target platform, OpenIM dependency failure, stale read completion, and logout conflict remain explicit. A failed logout does not mutate the displayed enrollment or fabricate an offline state. Refresh is the only reconciliation path; there is no cached-success, direct OpenIM Admin request, or silent fallback.

## Security

The endpoint derives member, tenant, and OpenIM user from the verified OIDC Token and repository state. It never accepts these identifiers from the browser. Admin credentials remain inside Platform API. Responses omit all OpenIM Tokens, connection IDs, IP addresses, and internal ownership markers. The target is constrained to the same member and a valid non-current enrolled platform.

## Observability

Platform API preserves `X-Correlation-ID` on every response and logs failures without request Tokens. The client shows the last successful logout correlation ID. OpenIM operation IDs remain in backend-to-OpenIM calls. UI states distinguish loading, online/offline, current device, enrollment disabled, logout in progress, kicked, expired, and transport failure.

## Acceptance criteria

- Unit tests prove same-member projection, current-device marking, online platform reduction, stale-read suppression, duplicate logout protection, current/unenrolled target rejection, and explicit dependency errors.
- Handler tests prove authentication, unknown-field rejection, current-device authorization, target parsing, typed errors, and correlation IDs.
- OpenIM client tests prove Admin Token remains server-side, online responses discard Token details, and force logout sends exactly the resolved user/platform.
- Real Node2 E2E creates a temporary Windows enrollment, logs a second official SDK instance in as platform 3, observes it online from the Web device workspace, logs it out from platform 5, and observes `OnKickedOffline` on the target.
- The E2E removes the temporary enrollment and restores the primary Web session without exposing Tokens in logs, screenshots, localStorage, or visible UI.
- Desktop and mobile device views have no overlap or horizontal overflow.

## Planned follow-up slices

1. None in this Root Goal. Enterprise collaboration capabilities require a new Root Goal.

## Open questions

- OpenIM does not map Token/connection state to enterprise `device_id`; same-platform devices can only be managed as one platform and require an upstream device-aware Token model for individual logout.
- OpenIM exposes no trustworthy last-activity timestamp through the locked APIs. Adding one requires an explicit audited activity model rather than relabeling enrollment timestamps.

## Source evidence

- `platform/services/platform-api/internal/identity`
- `platform/services/platform-api/internal/openim/client.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/services/platform-api/internal/migrations/sql/0001_identity.sql`
- `contracts/openapi/platform-v1.yaml`
- `open-im-server/internal/rpc/auth/auth.go`
- `open-im-server/internal/msggateway/ws_server.go`
- `open-im-server/internal/api/user.go`
- `open-im-server/config/share.yml`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/src/openim.ts`

## Verification evidence

- Platform API tests verify member/device authorization, current-platform and unenrolled-target rejection, strict request decoding, correlation IDs, safe online-platform reduction, server-side Admin Token use, and OpenIM's successful `force_logout` response with `data: null`.
- Web Vitest passed 71 tests across 10 files. Device tests cover stale read suppression, duplicate logout protection, authoritative refresh after acknowledgement, explicit dependency failure, malformed response rejection, and completion after controller close.
- `npm run typecheck`, `npm run build`, `go test ./...`, `python ops/validate-repository.py`, strict unit-SDD validation, and `git diff --check` passed.
- Focused real Node2 E2E passed in 15.0 seconds: the Web platform and a temporary Windows platform 3 SDK session were both observed online; Platform API invoked OpenIM `ForceLogout`; the target official SDK emitted `OnKickedOffline`; the authoritative refresh then showed Windows offline.
- The complete serial Node2 suite passed all seven Agent, conversation, device, IM foundation, group lifecycle, message action, and message search scenarios in 107.3 seconds. The temporary Windows enrollment was removed after verification.
- Desktop 1280x720 and mobile 390x844 screenshots passed visual inspection. The E2E found no horizontal overflow, failed HTTP response, unexpected console error, localStorage entry, or Token in visible text.
