---
unit: intelligence-worker
status: implemented
depends_on:
  - adr-0003
---

# Intelligence Worker

## Scope

Generate governed structured candidates for Agent answers, intent analysis, memory extraction, and Tool parameter planning through one fixed local Responses provider. Embeddings remain a separate local Ollama dependency and are not a generation fallback.

## Responsibilities and non-goals

The Worker owns bounded provider calls, strict structured-output parsing, typed provider failures, and domain-level candidate validation. It does not own OpenIM facts, tenant authorization, durable Runs, final citations, Tool selection, approval, action execution, delivery, or model/provider fallback.

## Contracts and dependencies

- Worker API: `POST /v1/candidates`, `/v1/routes`, `/v1/memory-extractions`, `/v1/tool-plans`, `/v1/embeddings`.
- Generation provider: Windows-only `http://127.0.0.1:8317/v1`, `GET /v1/models`, and `POST /v1/responses`.
- Generation model: exactly `gpt-5.6-luna` with `stream=false`, `store=false`, low reasoning effort, and strict JSON schemas.
- Embedding provider: loopback Ollama `POST /v1/embeddings`, currently `qwen3-embedding:4b` with 2560 dimensions.
- Agent Runtime owns durable Runs, authoritative ACL retrieval, Catalog versions, citations, approval, Tool policy, and action execution.
- The Worker is stateless and never owns tenant facts or business side effects.

## Invariants

- Environment configuration must match the fixed generation base URL and model. Drift fails at startup.
- Startup verifies that `/v1/models` advertises the exact model before the Worker becomes available.
- Every generation method uses the same Responses client. `/v1/chat/completions`, alternate models, alternate providers, cached answers, and local echo are absent.
- Unknown request fields and malformed provider responses fail closed.
- Only one `completed` response with the exact model and one non-empty `output_text` is accepted.
- Retries are limited to timeout, transport failure, `429`, and `408/500/502/503/504`; authentication, route, request, and protocol failures are not retried.
- Provider error bodies, prompts, evidence, and credentials are not logged or returned.
- Enterprise answers can only cite evidence supplied by the trusted Runtime. Declared citations must exactly match citations in the answer text.
- The model cannot create an action. The explicit `创建工单：<标题>` protocol is parsed deterministically outside the model and still requires downstream approval.
- Memory output is candidate-only, excludes sensitive material, and remains subject to deterministic projection and review policy.
- Tool planning can only fill the already selected operation schema; it cannot choose or execute a Tool.
- Intent analysis receives explicit server-resolved identity, ACL, Tool-permission, and approval-policy context. Those authority-owned values cannot be requested from the user as missing business inputs.

## Runtime flow

1. Agent Runtime resolves a pinned Agent version, applies tenant/member ACL, and sends bounded evidence and context.
2. The Worker validates the logical route as `gpt-5.6-luna`.
3. The Responses client sends fixed instructions, untrusted input as data, and a strict per-operation JSON schema.
4. The client validates status, model, response ID, output cardinality, and JSON shape.
5. Domain validation checks citations, grounding state, memory sensitivity, or Tool arguments.
6. The Worker returns a candidate. Go Runtime independently enforces Catalog, citations, policy, approval, persistence, and delivery.

## Data ownership and state

The Worker is stateless. CLIProxyAPI owns its local authentication configuration; OpenIM owns IM facts; PostgreSQL and Agent Runtime own durable platform state; Ollama owns only local embedding model artifacts. The Worker receives bounded authorized inputs and returns untrusted candidates.

## Deployment topology

Windows runs CLIProxyAPI on `127.0.0.1:8317` and the Intelligence Worker on `127.0.0.1:18082`. Node2 must not receive the gateway key or expose either port. `openim-intelligence-tunnel.service` is designed to create:

- Node2 `127.0.0.1:18082` -> Windows `127.0.0.1:18082` for Agent Runtime calls;
- Windows `127.0.0.1:11435` -> Node2 `127.0.0.1:11434` for the Worker's embedding dependency. The dedicated Windows port avoids colliding with a developer-local Ollama listener on `11434` and does not select it as a fallback.

Both forwards are SSH authenticated and loopback-bound. If the tunnel or Windows Worker is unavailable, Node2 Agent operations fail explicitly.

## Failure handling

Stable errors distinguish timeout, unavailable, rate-limited, authentication, rejected request, missing model route, and invalid provider protocol. Retryable classes return HTTP 503 after the bounded budget; non-retryable provider and output-validation failures return 502. No class activates a fallback.

## Security

The local bootstrap reads the CLIProxyAPI key into process memory and never puts it in arguments, samples, logs, databases, or model context. Provider error bodies are suppressed. Both Worker and gateway bind Windows loopback; Node2 forwards only the Worker and embedding ports over authenticated SSH. Model output is candidate-only and cannot authorize itself.

## Observability

Worker HTTP metrics expose bounded request counts, latency, and embedding batch size without prompt/evidence content. Runtime records the exact model and provider response ID with the Run. Stable error codes permit alerting without leaking upstream payloads.

## Acceptance criteria

- Unit tests cover every generation entry point, fixed route, error class, retry budget, and no-fallback endpoint/model behavior.
- Real local `/models`, `/responses`, and Worker candidate calls succeed without exposing credentials.
- Catalog migration preserves old immutable versions and activates a new exact-route version with audit evidence.
- Node2 permanent deployment is accepted only after bidirectional loopback topology, full OpenIM ACL-RAG, and Telegram E2E pass on the new release.

## Source evidence

- `platform/services/intelligence-worker/src/intelligence_worker/`
- `platform/services/intelligence-worker/tests/`
- `platform/services/platform-api/internal/agent/`
- `platform/services/platform-api/internal/migrations/sql/0029_local_responses_model.sql`
- `ops/deploy-node2-agent-runtime.sh`
- `ops/accept-node2-intelligence*.sh`

## Verification evidence

- `uv run pytest -q`: 33 tests passed, including fixed route validation, Responses payload shape, model discovery, bounded retry, no retry on authentication failure, exact model/output parsing, citation validation, memory filtering, and Tool planning.
- `go test ./internal/agent ./internal/migrations`: passed with the new Catalog route and migration embedded.
- Real local `/v1/models`: `gpt-5.6-luna` present.
- Real local `/v1/responses`: exact model, `completed`, response ID present, and one non-empty output text.
- Real local Worker candidate: exact model, grounded `C1`, matching `[C1]` text, provider response ID, and no action intent.
- A temporary Node2 `127.0.0.1:28082` forward reached the Windows Worker and passed the real candidate/citation contract. The paired reverse loopback forward also let that Worker obtain a real 2560-dimensional `qwen3-embedding:4b` vector from Node2 Ollama. Temporary forwards and files were removed afterward.
- Node2 release `akashic-node2-20260721-responses3` passed migration `0029`, the permanent bidirectional loopback tunnel, authorized/revoked/no-match OpenIM acceptance, and a real Telegram round trip. The Telegram Run used exact model `gpt-5.6-luna`, persisted four citations, produced a sent delivery with a non-empty external message ID, satisfied `1|1|1` cardinality, and left no test binding.

## Legacy evidence

Earlier DeepSeek/OpenIM/Telegram Runs remain historical evidence for the surrounding ACL, Run, citation, approval, and delivery architecture. They do not verify the current `gpt-5.6-luna` route.

## Open questions

- No unresolved deployment question remains for the fixed Responses route. Production admission thresholds remain owned by the separate evaluation policy.
