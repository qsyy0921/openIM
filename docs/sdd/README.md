# Unit SDD index

| Unit | SDD | Status | Next required evidence |
| --- | --- | --- | --- |
| Platform API | [platform-api.md](platform-api.md) | verified | add OpenTelemetry only in a dedicated observability slice |
| Identity session | [identity-session.md](identity-session.md) | verified | preserve identity/device invariants during client work |
| OpenIM adapter | [openim-adapter.md](openim-adapter.md) | verified | run protocol compatibility tests before upstream upgrade |
| Agent Runtime | [agent-runtime.md](agent-runtime.md) | verified | preserve bounded read/intent scope during later tools work |
| Intelligence worker | [intelligence-worker.md](intelligence-worker.md) | verified | preserve DeepSeek JSON/action protocol during future model changes |
| ACL retrieval | [acl-retrieval.md](acl-retrieval.md) | verified | group grants and vector search require separate measured slices |
| Action Executor | [action-executor.md](action-executor.md) | verified | preserve single-action boundary; external adapters require a new slice |
| Web client | [web-client.md](web-client.md) | verified | add conversations/messages only in a dedicated IM UI slice |

Future units are added only when they enter an admitted root-Goal slice. A unit is not marked implemented or verified until code and stated checks exist.
