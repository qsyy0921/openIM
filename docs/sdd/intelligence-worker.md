---
unit: intelligence-worker
status: proposed
depends_on:
  - adr-0003
  - adr-0010
---

# Intelligence Worker

## Scope

Generate governed structured candidates for Agent answers, intent analysis,
memory extraction, and Tool parameter planning through one fixed local Responses
provider. A separate retrieval-only process proxies the fixed Node2 embedding
contract and executes one fixed local multilingual reranker. Embeddings and
reranking remain separate model dependencies and are never generation or
retrieval fallbacks.

## Responsibilities and non-goals

The Worker owns bounded provider calls, strict structured-output parsing, typed provider failures, deterministic reranker input/output validation, and domain-level candidate validation. It does not own OpenIM facts, tenant authorization, retrieval candidate selection, durable Runs, final citations, Tool selection, approval, action execution, delivery, or model/provider fallback.

## Contracts and dependencies

- Candidate Worker API: `POST /v1/candidates`, `/v1/routes`,
  `/v1/memory-extractions`, and `/v1/tool-plans`.
- Retrieval Worker API: only `POST /v1/embeddings`, `POST /v1/rerank`,
  `GET /healthz`, and `GET /metrics`. Candidate, Tool, Memory, and routing
  endpoints are absent from this process.
- Generation provider: Windows-only `http://127.0.0.1:8317/v1`, `GET /v1/models`, and `POST /v1/responses`.
- Generation model: exactly `gpt-5.6-terra` with `reasoning={"effort":"high"}`, `stream=false`, `store=false`, and strict JSON schemas.
- Embedding provider: loopback Ollama `POST /v1/embeddings`, currently `qwen3-embedding:4b` with 2560 dimensions.
- Reranker: local-only `BAAI/bge-reranker-v2-m3` revision `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`, loaded from a manifest-verified directory with no runtime model download.
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
- A request for enterprise knowledge with an empty authorized evidence set may produce only an explicit `insufficient_evidence` candidate with no citations; `grounded` remains invalid and it cannot be recast as ordinary chat.
- The model cannot create an action. The explicit `创建工单：<标题>` protocol is parsed deterministically outside the model and still requires downstream approval.
- Memory output is candidate-only, excludes sensitive material, and remains subject to deterministic projection and review policy.
- Tool planning can only fill the already selected operation schema; it cannot choose or execute a Tool.
- Intent analysis receives explicit server-resolved identity, ACL, Tool-permission, and approval-policy context. Those authority-owned values cannot be requested from the user as missing business inputs.
- For enterprise knowledge queries, a user-provided document title, keyword, or question is already the complete query; IntentView must select retrieval rather than request a duplicate document target.
- Reranking accepts at most 32 unique candidate IDs, truncates every `(query, passage)` pair to the fixed 512-token model limit, and returns exactly one finite score for every supplied ID.
- The Worker does not authorize or retrieve reranker input. Agent Runtime may call it only after SQL ACL, current-version, publication, checksum, and active-index predicates have produced the bounded fusion set.
- A missing local model path, wrong revision marker, load error, timeout, malformed score, duplicate/missing ID, or non-finite result fails closed. The caller cannot silently keep RRF order or select another reranker.

## Runtime flow

1. Agent Runtime resolves a pinned Agent version, applies tenant/member ACL, and sends bounded evidence and context.
2. The Worker validates the logical route as `gpt-5.6-terra`.
3. The Responses client sends fixed instructions, untrusted input as data, and a strict per-operation JSON schema.
4. The client validates status, model, response ID, output cardinality, and JSON shape.
5. Domain validation checks citations, grounding state, memory sensitivity, or Tool arguments.
6. The Worker returns a candidate. Go Runtime independently enforces Catalog, citations, policy, approval, persistence, and delivery.

The separate retrieval path sends at most 32 already-authorized query/passage
pairs to `/v1/rerank`. The Worker returns model ID, immutable revision, and
candidate scores; Go validates cardinality and applies the stable final
tie-break. Reranking never calls Terra and never sees unauthorized candidates.

## Data ownership and state

The Worker is stateless. CLIProxyAPI owns its local authentication configuration; OpenIM owns IM facts; PostgreSQL and Agent Runtime own durable platform state; Ollama owns only local embedding model artifacts. The versioned local model directory owns reranker artifacts. The Worker receives bounded authorized inputs and returns untrusted candidates or ranking scores.

## Deployment topology

Windows runs CLIProxyAPI on `127.0.0.1:8317` and the Candidate Worker on
`127.0.0.1:18082`. Node2 must not receive the gateway key or expose either
port. `openim-intelligence-tunnel.service` creates:

- Node2 `127.0.0.1:18082` -> Windows `127.0.0.1:18082` for Agent Runtime calls;
- Windows `127.0.0.1:11435` -> Node2 `127.0.0.1:11434` for the Worker's embedding dependency. The dedicated Windows port avoids colliding with a developer-local Ollama listener on `11434` and does not select it as a fallback.

Both forwards are SSH authenticated and loopback-bound. If the tunnel or
Windows Candidate Worker is unavailable, generation operations fail explicitly.

Node2 runs the retrieval-only process on `127.0.0.1:18083`. It calls Node2
Ollama at `127.0.0.1:11434` with environment proxy inheritance disabled and
loads the manifest-verified BGE reranker from the M2 model directory. Neither
port is opened on the LAN. This keeps embedding/reranking CPU work on the
72-thread server while generation remains behind the Windows-only credential
boundary. The provisioned model and Python dependency archives are deployment
artifacts and are never committed or copied into the product release.

External model and Python artifacts are downloaded on Windows through the
loopback Clash proxy, verified against their pinned SHA-256 values, and then
copied to Node2 over the private LAN. Node2 does not download those artifacts
from the Internet. The production deployer requires an already provisioned
Python runtime and model manifest, installs the release Worker wheel with
`pip --no-index --no-deps`, and clears all proxy variables from the retrieval
service. A missing artifact or version mismatch is a deployment failure, not a
reason to fetch or select a substitute.

## Failure handling

Stable errors distinguish timeout, unavailable, rate-limited, authentication, rejected request, missing model route, invalid provider protocol, missing reranker model, reranker timeout, and invalid reranker output. Retryable provider classes return HTTP 503 after the bounded budget; non-retryable provider/output and reranker-contract failures return 502. No class activates a fallback.

## Security

The local bootstrap reads the CLIProxyAPI key into process memory and never puts
it in arguments, samples, logs, databases, or model context. Provider error
bodies are suppressed. The Candidate Worker and gateway bind Windows loopback;
the retrieval process binds Node2 loopback and has no generation credential or
generation endpoint. Model output is candidate-only and cannot authorize itself.
The reranker receives no tenant/member identifiers, object paths, Tokens,
Memory, Tool arguments, or candidates outside the already-authorized fusion
set.

## Observability

Worker HTTP metrics expose bounded request counts, latency, embedding batch size, and reranker batch size without prompt/evidence content. Runtime records the exact generation model/provider response and reranker model/revision with the Run or retrieval trace. Stable error codes permit alerting without leaking upstream payloads.

## Acceptance criteria

- Unit tests cover every generation entry point, fixed route, error class, retry budget, and no-fallback endpoint/model behavior.
- Real local `/models`, `/responses`, and Worker candidate calls succeed without exposing credentials.
- Catalog migration preserves old immutable versions and activates a new exact-route version with audit evidence.
- Node2 permanent deployment is accepted only after bidirectional loopback topology, full OpenIM ACL-RAG, and Telegram E2E pass on the new release.
- Unit tests cover fixed reranker path/revision, cardinality, finite scores, input bounds, timeout, startup failure, and no unreranked fallback.
- A real local model load and rerank call completes within the measured timeout, and the frozen 1,120-case retrieval report meets ADR-0010 thresholds.
- Retrieval-only OpenAPI inspection proves Candidate, Tool, Memory, and routing
  endpoints are absent; Node2 warm probes verify the locked 2,560-dimensional
  embedding and exact reranker revision.

## Source evidence

- `platform/services/intelligence-worker/src/intelligence_worker/`
- `platform/services/intelligence-worker/tests/`
- `platform/services/platform-api/internal/agent/`
- `platform/services/platform-api/internal/migrations/sql/0030_terra_responses_model.sql`
- `ops/provision-node2-retrieval-runtime.sh`
- `ops/deploy-node2-agent-runtime.sh`
- `ops/accept-node2-intelligence*.sh`
- `docs/sdd/enterprise-knowledge-rag.md`
- `docs/adr/0010-pgvector-hybrid-enterprise-retrieval.md`

## Verification evidence

- `uv run pytest -q`: 37 tests passed, including fixed route validation, high-reasoning Responses payload shape, model discovery, bounded retry, no retry on authentication failure, exact model/output parsing, empty-authorized-corpus semantics, citation validation, memory filtering, and Tool planning.
- `go test ./...` and `go vet ./...` passed with the Catalog route and migration embedded; Web typecheck, 94 tests, and the production build also passed.
- Node2 immutable release `akashic-node2-20260723-oidc-renew1` reports migration `0031`; runtime, loopback Worker/embedding topology, and monitoring acceptance pass. An initial high-reasoning request received upstream `server_is_overloaded` (`502` at the gateway and retryable `503` at the Worker); after the bounded retry window, real Terra candidate and route calls plus OpenIM ACL-RAG authorization/revocation/no-match acceptance passed. A later self-service Telegram run produced four authorized citations and one sent delivery with `1|1|1` ingress/Run/delivery cardinality. The transient provider error remained observable and never selected a fallback.
- On 2026-07-24, the isolated Node2 retrieval process started from a hash-locked
  Python 3.14 dependency archive and manifest-verified reranker. A warm real
  embedding returned 2,560 dimensions in `0.589s`; a real 32-passage rerank
  returned the exact model/revision in `3.378s`. Eight concurrent embedding
  requests processed 32 synthetic long passages in `6.034s`. This is runtime
  evidence for the retrieval process, not yet the production release or
  three-channel E2E.

## Legacy evidence

The earlier `0029` Luna release, its local probes, and its Node2 OpenIM/Telegram Runs remain historical evidence for the surrounding ACL, Run, citation, approval, and delivery architecture. They do not verify the current Terra/high route. Earlier DeepSeek evidence is historical for the same reason.

## Open questions

- The fixed reranker's measured CPU p95 determines only timeout and concurrency. It cannot authorize a different model, dynamic quantization, remote provider, or unreranked result.
