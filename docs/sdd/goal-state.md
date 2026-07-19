---
unit: akashic-openim-goal-state
status: draft
branch: codex/akashic-openim-integration
worktree: E:\development\OPENIM-akashic
updated: 2026-07-19
---

# Akashic integration Goal state

This file is the authoritative restart checkpoint for the active Codex Goal. It records evidence, not intent. A resumed session must verify Git and external state before changing an entry.

## Scope

Track restart-safe implementation and acceptance evidence for the bounded Akashic integration Goal on `codex/akashic-openim-integration`. It does not define a second runtime or replace the owning unit SDD.

## Responsibilities and non-goals

This unit records slice state, durable evidence, and the next idempotent action. It does not execute business actions automatically, store credentials, duplicate source-of-truth data, or turn a local result into Node2 acceptance.

## Contracts and dependencies

- The owning design is `akashic-openim-integration.md`.
- Git status, migration metadata, test output, evaluation reports, service health, and real channel round trips are the accepted evidence classes.
- The active Codex Goal and heartbeat consume this file; they do not override it without new evidence.

## Invariants

- OpenIM owns messages, conversations, users, groups, membership, seq, and client synchronization.
- PostgreSQL owns enterprise identity and Agent-platform domain state; it must not duplicate OpenIM facts.
- Production dependencies fail closed. There is no hidden provider, retrieval, Tool, or delivery fallback.
- Side effects require pinned policy, idempotency, audit, and approval where configured.
- Node2 completion requires real host reachability and real OpenIM plus Telegram round trips.

## Data ownership and state

| Slice | State | Durable evidence | Resume action |
| --- | --- | --- | --- |
| DDD and SDD boundary | verified | `akashic-openim-integration.md` | preserve bounded-context ownership |
| Migrations 0009-0028 | node2_verified | Node2 reached `28|sql/0028_remote_a2a.sql`; fresh and restored-0021 upgrade chains also reached 0028 idempotently | preserve the 0022 compatibility fix and rerun the fresh-chain gate after migration edits |
| Routing 36/190 | local_verified | `platform/services/intelligence-worker/eval/routing-gate-report.json` | rerun deterministic evaluator in final gate |
| Enterprise RAG | local_verified | schema-v3 retrieval report plus deterministic generation-harness report; strict Candidate grounding contract and tests | admit a production model and thresholds in a later release gate; preserve no-fallback routing |
| Group Memory review | local_verified | migration 0026, API, Web panel, tests | include in full Go/Web gates |
| Catalog Web administration | local_verified | migration 0027, admin API/UI/tests | include in full Go/Web gates |
| Remote A2A | local_verified | migration 0028, bounded client/store/API/UI/tests | include in full Go/Web gates; no broad federation claim |
| Prometheus/Grafana | node2_verified | Node2 reported three healthy targets, six loaded rules, the `Prometheus` datasource, and the `openim-agent-platform` dashboard | preserve the Node2 host-network override and repeat after monitoring changes |
| Full repository gates | local_verified | Go `./...`, Python 26, Web typecheck/94 tests/build, dataset and repository validators, formatting, diff, and credential-shape checks passed | preserve on subsequent changes |
| Node2 runtime and OpenIM ingress | remote_partial | release runtime, real Ollama embedding, OpenIM ingress, Outbox publication, and a direct DeepSeek/citation turn were accepted; the full Agent ACL E2E exposed a pgx retry-parameter bug that is fixed and tested locally but not yet redeployed | when SSH returns, stage the rebuilt release once, deploy it, and rerun runtime plus OpenIM Agent ACL acceptance |
| Telegram channel E2E | pending_external | Telegram units correctly stayed disabled because no validated root-only Bot Token was present; no Telegram round trip is claimed | after Node2 is reachable, provision a validated Token through stdin, bind one test identity, run inbound/outbound E2E, and clean the fixture |

## Resume protocol

1. Read this file and `akashic-openim-integration.md`.
2. Run `git status --short` and confirm branch/worktree before editing.
3. Inspect the durable evidence named by the first non-verified row.
4. Execute only that bounded slice and its tests.
5. Update this checkpoint with exact commands/results; never mark remote acceptance from local evidence.
6. Stop when the Goal is complete or when the same external blocker satisfies the Goal blocked policy.

## Runtime flow

The heartbeat invokes the resume protocol, selects the first non-verified row, runs one bounded implementation or verification step, persists its evidence, and updates the row. Remote actions are gated by reachability and are never retried after an uncertain side effect.

## Failure handling

An implementation or validation failure remains visible as `failed` or `pending` with its exact evidence. External reachability remains `blocked_external`. A session interruption is recovered by rerunning the idempotent step, not by assuming the interrupted command succeeded.

## Security

The checkpoint contains no Token, API key, password, message body, raw Tool arguments, or private model context. Recovery scripts use environment or explicit local-only parameters and must redact credentials from output.

## Observability

Progress is observable through this table, generated evaluation reports, test output, Prometheus health, and Node2 probes. Heartbeat execution does not itself prove application health.

## Acceptance criteria

- Every local slice has current full-gate evidence.
- A fresh database reaches migration `0028` exactly once.
- Routing and RAG reports are regenerated with declared metric semantics.
- Prometheus rules and Grafana provisioning are loaded and healthy.
- Node2 migrations and OpenIM plus Telegram E2E are accepted only after real remote round trips.

## Source evidence

- `docs/sdd/akashic-openim-integration.md`
- `platform/services/platform-api/internal/migrations/sql`
- `platform/services/intelligence-worker/eval`
- `platform/services/platform-api/eval`
- `platform/deploy/local/observability`

## Open questions

- When will Node2 become reachable for the final remote acceptance?
- Which production generation model and release thresholds will be admitted after the harness rejects the local `qwen2.5:3b` baseline?

## Automation

The current Codex task has a 15-minute heartbeat named `OpenIM Akashic Goal 心跳`. It re-enters this resume protocol. The heartbeat may continue a pending idempotent step; it must not create a second Goal, force a migration, retry an uncertain side effect, or modify `main`.

## Latest local evidence

- Fresh empty PostgreSQL migration and self-contained dataset-import audit: `28|sql/0028_remote_a2a.sql|520 documents|3224 chunks|520 ACL grants`; the one-time container was removed after the check.
- Routing regression: 36 operations, 190 cases, 190 passed, Recall@1 1.0.
- Enterprise retrieval schema v3: 1120 cases, Recall@8 `0.928846`, MRR `0.594903`, retrieval Precision@8 `0.166947`, and provenance integrity `1.0`. Retrieval returned evidence for all 80 unanswerable questions; that zero-result metric is not generation abstention.
- Deterministic generation harness: local evaluation model `qwen2.5:3b`, 20 answerable plus 20 unanswerable cases, sample digest `sha256:ecf59964fba3bf6f672e90fd1deafb3fff27f4c1f9be3eb903ec6105bd0d7b89`. All 40 responses failed the strict production Candidate schema, so contract success and end-to-end success are `0`; this is a fail-closed negative baseline and `production_gate_evaluated=false`.
- Enterprise dataset generator `1.0.1` produces a self-contained development import that idempotently seeds its fixed synthetic tenant/member before ACL grants. The reproducible release hash is `7a8cd3e3a979865104ae128256e0f61703bec5f703bbbf235f22e3e4e0db9432`.
- Prometheus configuration and six rules passed `promtool`; Prometheus, Grafana, datasource, and `openim-agent-platform` dashboard loaded; the real Worker metrics target was scraped.
- Go all-package tests, Python 26 tests, the standalone 190/190 routing gate, Web typecheck/94 tests/production build with the exact Node2 public URLs, dataset validation, and repository validation passed again on 2026-07-19. The dataset remains `520 documents|624 versions|3224 chunks|1120 QA` with release hash `7a8cd3e3a979865104ae128256e0f61703bec5f703bbbf235f22e3e4e0db9432`.
- `gofmt -l` returned no changed Go files, `git diff --check` passed, and the credential-shaped scan found no credential (its only lexical match was the public NIST `risk-management` URL). Test-only Prometheus, Grafana, and PostgreSQL containers are removed after local acceptance.
- After Node2 recovered, the pinned Ollama `v0.32.1` runtime served `qwen3-embedding:4b` on loopback and returned a real 2560-dimensional embedding. Model-dependent workers were started only after that probe passed.
- The first remote migration attempt stopped at `0022` because its `agent.delegate` descriptor omitted the non-null `source_operation` introduced by `0018`. The migration was corrected, the pre-fix database was backed up, and both a fresh database and a restored Node2-at-0021 database reached `0028` twice without duplicate effects. Node2 then reached `28|sql/0028_remote_a2a.sql|520 documents|624 versions|3224 chunks|520 grants`.
- Node2 runtime acceptance verified the immutable release executables, OpenIM/Chat/PostgreSQL health, public HTTPS endpoints, and the Worker-to-Ollama embedding contract. OpenIM ingress/Outbox publication and a real DeepSeek response with citation validation also passed.
- Node2 observability acceptance verified exactly three healthy scrape targets, six rules, the provisioned Prometheus datasource, and the `openim-agent-platform` dashboard through the host-network-only monitoring override.
- The full OpenIM Agent ACL test found an actual pgx parameter-inference failure in `FailOrRetry`. Explicit integer casts and an integration regression test now pass locally. A fresh-seed dependency-order defect was also fixed by seeding final descriptor, snapshot, grant, role, and trigger fixtures after tenant creation.
- Node2 became unreachable during transfer of the rebuilt pgx-fixed release. On the current 2026-07-19 audit, the controller still owns `172.31.50.1/24` with a negotiated 1 Gbps wired link, but both `172.31.50.2` and `192.168.0.38` have `Incomplete` neighbors and fail SSH. A full subnet scan found only a different SSH identity at `172.31.50.6`; it was not treated as Node2. No redeploy, Telegram credential action, or dual-channel success is claimed.
- The next heartbeat audit found the same two Node2 addresses unreachable. The final ops, platform, and release archives remain present with their locked SHA256 values, and the release archive was built after the pgx fix. A non-disclosing search of the Akashic workspaces, process environment, and Windows credential targets found no reusable Telegram Bot Token, so Telegram acceptance still requires an explicitly provisioned credential rather than a fallback.
- Node2 briefly returned on both known addresses and identified itself as `qsyy0921-Default-string`; Docker, Platform API, Agent Runtime, OpenIM, PostgreSQL, and the Node2 monitoring containers were active on the prior `akashic-node2-20260719` release. To preserve release immutability, the pgx-fixed source was rebuilt as `akashic-node2-20260719-pgxfix1`; all 695 manifest entries pass SHA verification, the platform archive contains no `.env`, and the release archive SHA256 is `c14a16e4f24e6dcaeb396f25c927b7738a02159c1cc4f7a5b753c8c2ccea881b`. Node2 lost both wired and Wi-Fi SSH before staging began, so no remote file or service was changed by this attempt.
