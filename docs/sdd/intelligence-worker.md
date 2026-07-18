---
unit: intelligence-worker
status: verified
depends_on:
  - adr-0003
---

# Intelligence Worker

## Scope

Generate a structured, citation-bearing candidate from bounded authorized evidence through the configured DeepSeek Chat Completions API.

## Responsibilities and non-goals

The unit validates a strict question/evidence request, treats evidence as untrusted data, calls the single required DeepSeek provider in JSON mode, and returns answer text, citation IDs, an optional explicitly requested `create_ticket` candidate, and provider evidence. It does not own Run state, authorization, approval, execution, business writes, OpenIM credentials, or provider fallback.

## Contracts and dependencies

- `POST /v1/candidates`
- DeepSeek `POST /chat/completions`
- Required environment configuration for base URL, API key, model, and timeout
- Required immutable Catalog provenance, instructions, logical model route, and action allow-list from Agent Runtime

## Invariants

- Unknown request fields fail validation.
- The model name is required configuration and has no code default.
- The request's logical model route must exactly match the configured model; an unknown route fails before provider egress.
- Published Agent instructions are trusted configuration, while evidence and user input remain untrusted data.
- An action candidate is rejected unless its type appears in the pinned Agent version allow-list.
- Exactly one normally completed Chat Completion choice with non-empty JSON content is accepted.
- DeepSeek JSON output is revalidated by Pydantic; the Go Runtime independently validates citations and action type/length.
- The only action candidate schema is `create_ticket` with a title of at most 200 characters.
- An action candidate exists only when the request starts with the explicit `创建工单：<标题>` protocol. Negated/embedded phrases do not match, and unsolicited model action output is rejected.
- Missing output, provider errors, and malformed responses return failure; no synthetic candidate is produced.
- The worker never receives a tenant database connection or OpenIM credential.

## Runtime flow

1. Validate Run, tenant, Agent/version/checksum, instructions, logical route, action allow-list, conversation, sender, and bounded prompt fields.
2. Require the logical route to match the configured DeepSeek model and serialize only authorized evidence supplied by the trusted Runtime.
3. Combine fixed governance instructions with the immutable published instructions, then call DeepSeek in JSON mode with thinking disabled.
4. Extract and validate non-empty answer text, citation IDs, optional explicit-command action candidate, and provider response ID.
5. Return the candidate to the trusted Go Runtime, which performs the authoritative citation allow-list check.

## Data ownership and state

The worker is stateless. The Go Runtime persists all final state and provider evidence.

## Failure handling

HTTP, timeout, invalid response, and missing-output failures return 502 to the Runtime. There is no alternate model, local echo, cached answer, or success-shaped fallback.

## Security

The API key is process configuration and is never returned. Prompt instructions prohibit tools, system-prompt disclosure, and claims of side effects. The Worker receives only evidence already filtered by authoritative tenant/member ACL; `restricted` evidence is excluded before model egress.

## Observability

Record request outcome, provider latency, model name, provider response ID, and bounded token/cost metadata without prompt text, API keys, or tenant content. Correlate with Run ID; provider failures expose a stable internal class rather than returning the upstream body to callers.

## Acceptance criteria

- Strict request validation passes unit tests.
- Chat Completions JSON request and response parsing pass against an HTTP transport mock.
- Provider failure returns 502 and no candidate.
- A real provider call succeeds with a valid deployment credential.

## Source evidence

- `platform/services/intelligence-worker/src/intelligence_worker/`
- `platform/services/intelligence-worker/tests/`
- `platform/services/intelligence-worker/pyproject.toml`

## Verification evidence

- `python -m pytest -q`: 9 tests passed, including missing configuration, exact model-route enforcement, explicit-prefix action extraction, disallowed actions, negated phrase handling, and unsolicited action rejection.
- The pinned setuptools build produced `openim_intelligence_worker-0.1.0-py3-none-any.whl`; installation into an isolated virtual environment returned HTTP 200 from `/healthz`.
- A real DeepSeek `deepseek-v4-pro` request returned a validated `C1` answer and provider response ID.
- Real OpenIM Run `7a68255b-395e-4cb2-a0aa-6f993f317442` completed Kafka ingress, ACL-RAG, DeepSeek generation, citation persistence, and OpenIM reply `72e0612b54bf873a854d01430e5310d4`.
- Real explicit-prefix action Run `99216135-a79a-406f-b0c2-ca51b8baa55b` produced a digest-bound Intent, stayed at zero tickets before approval, then completed one verified ticket through the separate Executor.
- Node2 release `d663256` loaded the DeepSeek credential through systemd `LoadCredential` and completed a real `deepseek-v4-pro` request with a validated `C1` citation. The credential remained absent from tracked files, unit command lines, and environment files.
- Node2 release `9a6ffee` force-reinstalled the same-version wheel, verified the new Agent/version/checksum/instructions/model-route/action-policy request contract in the installed environment, and completed real v2 then rollback-v1 DeepSeek Runs without alternate-provider behavior.

## Open questions

- Model egress remains limited to authorized `public`/`internal` evidence in this local slice; broader classification policy requires explicit approval.
