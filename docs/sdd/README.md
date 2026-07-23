# Unit SDD index

| Unit | SDD | Status | Next required evidence |
| --- | --- | --- | --- |
| Platform API | [platform-api.md](platform-api.md) | verified | add OpenTelemetry only in a dedicated observability slice |
| Identity session | [identity-session.md](identity-session.md) | M2 verified | preserve refreshed-token server identity/device checks |
| OIDC session continuity | [oidc-session-continuity.md](oidc-session-continuity.md) | Node2 verified | preserve fail-closed renewal and identity pinning; cross-tab auth is a separate slice |
| OpenIM adapter | [openim-adapter.md](openim-adapter.md) | verified | run protocol compatibility tests before upstream upgrade |
| Agent Runtime | [agent-runtime.md](agent-runtime.md) | verified | preserve bounded read/intent scope during later tools work |
| Akashic integration | [akashic-openim-integration.md](akashic-openim-integration.md) | Node2 dual-channel verified | preserve fixed Terra routing, ACL/citation checks, and channel idempotency |
| Akashic Goal state | [goal-state.md](goal-state.md) | verified checkpoint | report completion until a new bounded Goal is explicitly admitted |
| Platform evolution Goal | [platform-evolution-goal.md](platform-evolution-goal.md) | M2 verified | admit no new slice without an explicit Goal |
| Telegram identity linking | [telegram-identity-linking.md](telegram-identity-linking.md) | Node2 verified | unlink, recovery, and multi-Bot work require a separate slice |
| Agent Catalog | [agent-catalog.md](agent-catalog.md) | verified | preserve immutable versions, atomic Run pinning, host-only mutation, and fail-closed execution |
| Intelligence worker | [intelligence-worker.md](intelligence-worker.md) | Node2 route verified | preserve fixed Responses route, typed failure, candidate-only policy, and citation validation |
| ACL retrieval | [acl-retrieval.md](acl-retrieval.md) | verified | group grants and vector search require separate measured slices |
| Action Executor | [action-executor.md](action-executor.md) | verified | preserve single-action boundary; external adapters require a new slice |
| Web client | [web-client.md](web-client.md) | M2 verified | preserve current-token adapters and OpenIM continuity |
| IM client foundation | [im-client-foundation.md](im-client-foundation.md) | verified | add image/file messages only in the next bounded client slice |
| IM client contacts | [im-client-contacts.md](im-client-contacts.md) | verified | preserve OpenIM ownership; enterprise directory requires a separate architecture slice |
| IM client media | [im-client-media.md](im-client-media.md) | verified | retain OpenIM SDK/MinIO ownership; governance and resumable upload are later slices |
| IM client conversations | [im-client-conversations.md](im-client-conversations.md) | verified | retain OpenIM ownership; archive, drafts, and scheduled mute are later slices |
| IM client group lifecycle | [im-client-group-lifecycle.md](im-client-group-lifecycle.md) | verified | preserve locked-SDK role rules; transfer ownership and role editing require a separate slice |
| IM client message actions | [im-client-message-actions.md](im-client-message-actions.md) | verified | preserve official message ownership; merged forwarding and group reader lists require separate slices |
| IM client message search | [im-client-message-search.md](im-client-message-search.md) | verified | preserve the pinned Worker ABI fix; global/server search requires a separate measured slice |
| IM client device management | [im-client-device-management.md](im-client-device-management.md) | verified | same-platform device selection requires an upstream device-aware Token model |

Future units are added only when they enter an admitted root-Goal slice. A unit is not marked implemented or verified until code and stated checks exist.
