---
unit: akashic-openim-goal-state
status: implemented
branch: codex/local-responses-model-current
worktree: E:\development\OPENIM-akashic
updated: 2026-07-21
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
| Migrations 0009-0029 | node2_verified | Node2 reports `29|sql/0029_local_responses_model.sql`; the migration preserved the historical version and activated the immutable `gpt-5.6-luna` version | preserve the one-time migration and audit evidence |
| Fixed Responses generation | node2_openim_verified_telegram_pending | permanent loopback tunnel, real Node2 candidate, authorized ACL-RAG, revoked ACL, explicit no-match abstention, citation persistence, and OpenIM reply passed on `responses3` | complete one explicitly confirmed Telegram user round trip and clean the temporary binding |
| Routing 36/190 | local_verified | `platform/services/intelligence-worker/eval/routing-gate-report.json` | rerun deterministic evaluator in final gate |
| Enterprise RAG | node2_verified | schema-v3 local report plus Node2 `2704/2704` current-chunk index and real authorized/revoked/no-match OpenIM Runs | preserve pinned embedding revision, strict Candidate schema, ACL-first retrieval, and no-fallback routing |
| Group Memory review | local_verified | migration 0026, API, Web panel, tests | include in full Go/Web gates |
| Catalog Web administration | local_verified | migration 0027, admin API/UI/tests | include in full Go/Web gates |
| Remote A2A | local_verified | migration 0028, bounded client/store/API/UI/tests | include in full Go/Web gates; no broad federation claim |
| Prometheus/Grafana | node2_verified | Node2 reported three healthy targets, six loaded rules, the `Prometheus` datasource, and the `openim-agent-platform` dashboard | preserve the Node2 host-network override and repeat after monitoring changes |
| Full repository gates | local_verified | Go vet/all-package tests, Python 33, Web typecheck/94 tests/build, repository validation, shell syntax, compileall, diff, and credential-shape scan pass | repeat after any further source change |
| Node2 runtime and OpenIM ingress | node2_verified | release `akashic-node2-20260721-responses3`, database/index `29|...|2704|2704`, permanent bidirectional tunnel, OpenIM ingress/Outbox, real fixed-model ACL-RAG, revoked target isolation, no-match abstention, and observability passed | preserve exact release hashes and repeat after runtime changes |
| Telegram channel E2E | stale_after_model_change | ingress and Delivery are active; snapshot baseline is `96338389`, while the prior `deepseek-v4-pro` round trip remains historical evidence only | after action-time confirmation, send one new user message, bind the isolated identity, verify `gpt-5.6-luna`, then remove and verify the binding |

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
- A fresh database reaches migration `0029` exactly once.
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

- Which production generation model and release thresholds will be admitted after the harness rejects the local `qwen2.5:3b` baseline?

## Automation

The Codex task may use the 15-minute `OpenIM Akashic Goal 心跳` to re-enter this resume protocol while a slice remains pending. It must not create a second Goal, force a migration, retry an uncertain side effect, or modify `main`; after every row is verified it may only report completion and must not extend the scope.

## Latest local evidence

- Release `akashic-node2-20260721-responses3` was built from clean commit `003dee6`, contains 695 verified manifest entries, and has archive SHA-256 `2518a1a1054e3c07c39e1f8a3dd2a61f91be172795a5b49f08341f06d006a5b3`. Node2 applied migration `0029`, retained `2704/2704` valid current embeddings, and passed runtime, permanent bidirectional topology, real fixed-model Candidate, OpenIM ingress/Outbox, authorized ACL-RAG (five citations), revoked target isolation (zero target Tool results and citations), explicit no-match abstention, and observability acceptance. Telegram ingress is healthy at baseline `96338389`, but a fresh user-originated Telegram round trip remains pending action-time confirmation and must not be inferred from service health.
- The fixed generation migration passed real local model discovery and Responses calls, a real cited Worker candidate, a temporary Node2 candidate forward, and a bidirectional tunnel check that returned a 2560-dimensional Node2 Ollama embedding to the Windows Worker. Empty-database migration reached `0029`; a simulated Node2 upgrade preserved one historical DeepSeek version, activated one `gpt-5.6-luna` version, and wrote one deployment audit event. Go vet/all-package tests, Python `33/33`, Web typecheck/`94/94`/build, repository validation, shell syntax, Python compilation, diff checks, and credential-shape scan passed. Temporary tunnels, files, and PostgreSQL containers were removed.
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
- The locally accepted implementation is committed as `9366636` and pushed to `origin/codex/akashic-openim-integration`. Draft PR `#14` remains explicitly gated on the final Node2 OpenIM/Telegram acceptance; its Go, Python/docs, and Web CI jobs all passed. The branch is not merged into `main`.
- The next Goal continuation after Node2's brief recovery found neither `172.31.50.2` nor `192.168.0.38` reachable by SSH, and a complete scan of both `/24` networks found no alternate Node2 SSH or HTTPS address. This is the second consecutive Goal audit after the newest outage; no archive transfer, migration, credential action, or business message was attempted.
- The third consecutive Goal continuation after that recovery again found both known Node2 addresses unreachable by SSH. The local implementation, immutable release, full local gates, pushed branch, Draft PR, and CI were already complete, so no remaining work can progress without physical/network recovery and a separately provisioned Telegram Bot Token. The strict repeated-external-blocker threshold is met; no transfer, migration, credential write, or channel message was attempted, and the heartbeat must be paused until the user resumes the Goal after recovery.
- Node2 recovered as `qsyy0921-Default-string`. OpenIM delivery was separated from optional Telegram delivery: `openim-agent-delivery` runs with the OpenIM adapter when Telegram is explicitly disabled, while Telegram intents fail permanently on their own channel and are never rerouted.
- The deployment built all 2,704 missing `qwen3-embedding:4b` vectors. Runtime acceptance now requires `28|sql/0028_remote_a2a.sql|520|624|3224|520|2704|2704`; endpoint health alone is rejected. The initial index completed as one process and the deployment restored the stopped consumers only after the final count was reached.
- Real OpenIM acceptance exposed two fail-closed defects rather than hiding them: a failed read ToolCall had previously been decoded as an empty result, and a nil Go `tool_results` slice had been sent as JSON `null` to the strict Python Candidate contract. Safe read retries now preserve the same durable call and add `retry_prepared`; `unknown` and side-effect calls remain terminal. Candidate collection fields are normalized to JSON arrays without weakening the Python schema.
- Release `akashic-node2-20260720-candidatefix1` has 695 verified manifest entries; archive SHA-256 is `f5d0558610c74c95a01c884da416100e893502df92a0e9d092dbc6f9e4cd5a01`, and the platform archive contains no `.env`. A no-migration narrow deployment switched Platform, Agent workers, Delivery, and Web; runtime and observability acceptance passed afterward.
- Final OpenIM ACL-RAG acceptance produced authorized grounded Run `c136aae2-474c-4e89-8681-3016c2b5f130` with five persisted citations, revoked Run `f22ac73e-2157-415f-9bd1-abe9c63365d7` with zero target-document Tool results and zero target-document citations, and explicit-abstention Run `961499af-0a1c-4530-9694-1cfffb21d461` with zero citations. The removed document grant was restored before completion.
- A disposable PostgreSQL 18.4 database reached migration `0028`, received the deterministic Node2 identity/catalog seed, and passed `TestStoreRetriesOnlyFailedSafeReadWithSameDurableCall` on 2026-07-20. The temporary container was removed after the test.
- Node2's Telegram dependency was restored without exposing the subscription or Bot Token. The imported Mihomo candidate was SHA-locked, sanitized, constrained to loopback, switched atomically with rollback, and accepted only after Telegram and Google HTTPS probes passed. The active configuration uses TCP resolvers for proxy endpoint bootstrap because Node2's Wi-Fi path showed intermittent UDP DNS loss.
- The Telegram Bot Token passed the official `getMe` call through the Node2 loopback proxy and was installed as a root-only systemd credential. `openim-telegram-ingress`, `openim-agent-delivery`, and Mihomo are active; Delivery advertises explicit `openim` and `telegram` channels. A later five-request Telegram API probe succeeded, while transient transport failures remain visible in the ingress journal rather than being hidden.
- The active release still matched all 695 recorded file hashes after `telegram-admin` received the executable mode required by the host-admin binding flow. The initial `ops/accept-node2-telegram.sh snapshot` returned Bot offset `0`, so the acceptance started without a fabricated inbound message or binding.
- On 2026-07-21, a real Telegram bootstrap message advanced the Bot offset and was durably rejected as an unbound identity. After an explicit isolated member/chat binding, source offset `96338388` produced published event `d6563d40-c2c0-482a-bd16-7d067086460d`, successful `deepseek-v4-pro` Run `81b5f00f-1d01-4d51-a495-335151905efb`, five persisted citations, and sent delivery `de09f2a5-36fa-481a-82fd-995cd5b5e4ac` with external message ID `238`. The Telegram client received the cited answer, idempotency was `1|1|1`, and the temporary principal/chat binding was removed and verified as `0|0`. The acceptance script's final cardinality query was corrected to compare the text `integration.ingress_messages.event_id` without an invalid UUID cast.
- The 2026-07-21 final local gate passed Go formatting, vet, and all-package tests; Python `26/26`; deterministic routing `190/190`; Web typecheck, `94/94` tests, and the production build with explicit Node2 URLs; dataset validation (`520|624|3224|1120`); repository validation; shell syntax; Python compilation; credential-shape scanning; and `git diff --check`.
