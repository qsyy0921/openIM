# Unit SDD index

| Unit | SDD | Status | Next required evidence |
| --- | --- | --- | --- |
| Platform API | [platform-api.md](platform-api.md) | verified | add OpenTelemetry only in a dedicated observability slice |
| Identity session | [identity-session.md](identity-session.md) | verified | preserve identity/device invariants during client work |
| OpenIM adapter | [openim-adapter.md](openim-adapter.md) | verified | run protocol compatibility tests before upstream upgrade |
| Agent Runtime | [agent-runtime.md](agent-runtime.md) | verified | preserve bounded read/intent scope during later tools work |
| Agent Catalog | [agent-catalog.md](agent-catalog.md) | implemented | verify real node2 version switch/rollback and Agent regressions before marking verified |
| Intelligence worker | [intelligence-worker.md](intelligence-worker.md) | verified | preserve DeepSeek JSON/action protocol during future model changes |
| ACL retrieval | [acl-retrieval.md](acl-retrieval.md) | verified | group grants and vector search require separate measured slices |
| Action Executor | [action-executor.md](action-executor.md) | verified | preserve single-action boundary; external adapters require a new slice |
| Web client | [web-client.md](web-client.md) | verified | add conversations/messages only in a dedicated IM UI slice |
| IM client foundation | [im-client-foundation.md](im-client-foundation.md) | verified | add image/file messages only in the next bounded client slice |
| IM client contacts | [im-client-contacts.md](im-client-contacts.md) | verified | preserve OpenIM ownership; enterprise directory requires a separate architecture slice |
| IM client media | [im-client-media.md](im-client-media.md) | verified | retain OpenIM SDK/MinIO ownership; governance and resumable upload are later slices |
| IM client conversations | [im-client-conversations.md](im-client-conversations.md) | verified | retain OpenIM ownership; archive, drafts, and scheduled mute are later slices |
| IM client group lifecycle | [im-client-group-lifecycle.md](im-client-group-lifecycle.md) | verified | preserve locked-SDK role rules; transfer ownership and role editing require a separate slice |
| IM client message actions | [im-client-message-actions.md](im-client-message-actions.md) | verified | preserve official message ownership; merged forwarding and group reader lists require separate slices |
| IM client message search | [im-client-message-search.md](im-client-message-search.md) | verified | preserve the pinned Worker ABI fix; global/server search requires a separate measured slice |
| IM client device management | [im-client-device-management.md](im-client-device-management.md) | verified | same-platform device selection requires an upstream device-aware Token model |

Future units are added only when they enter an admitted root-Goal slice. A unit is not marked implemented or verified until code and stated checks exist.
