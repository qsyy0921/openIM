# Node2 enterprise RAG production acceptance

Status: prepared acceptance procedure; production roots remain intentionally
unset until evaluation passes. No remote pass is claimed until every result
named below exists and the final cleanup checks return zero.

## Scope

This runbook accepts the bounded enterprise-knowledge RAG slice on Node2 through
the real Web, OpenIM, and Telegram channels. It covers four source formats,
ACL isolation, immediate revocation, immutable version replacement, parser,
embedding, reranker, and Terra failure injection, and exact fixture cleanup.

It does not accept OCR, SaaS connectors, cloud-document synchronization,
department-derived ACL, or Agent Memory. OpenIM remains authoritative for
messages and identities; PostgreSQL remains authoritative for enterprise
documents, grants, Runs, citations, and audit state; MinIO remains authoritative
for immutable source objects.

## Safety invariants

- Run only after `retrieval-report.json`, `generation-report.json`, and
  `final-report.json` pass the locked evaluation gate.
- Use a release whose application binary is byte-identical to the evaluated
  application commit. Acceptance-only scripts may come from a later tooling
  commit, but application changes require a new full evaluation.
- Keep the temporary identity credential file under ignored `.runtime` storage.
  It may enter only the local Playwright process and the one root-only Node2
  identity preparation call.
- Do not put OIDC passwords, OpenIM secrets, Bot Tokens, or model credentials in
  command arguments, logs, screenshots, result JSON, or Git.
- Every external send persists a prepared intent first. An uncertain result is
  reconciled; it is never sent again blindly.
- Preparing deterministic A/B identities fails before password reset or
  database writes if either identity retains a Run, document grant, Telegram
  binding/challenge, Memory state, unexpected authorization, or foreign OpenIM
  link.
- Stop the Memory extractor for the isolated acceptance window. RAG evidence
  must not become Memory facts.
- A failed dependency must produce a failed Run with no Candidate, Citation, or
  Delivery. There is no provider, model, endpoint, parser, retriever, or
  reranker fallback.
- Cleanup acts only on the generated batch, two isolated identities, exact
  document IDs, exact Telegram message IDs, and their exact OpenIM histories.

## Fixed paths

Controller:

```powershell
$Repo = 'E:\development\OPENIM-akashic'
$Runtime = Join-Path $Repo '.runtime'
$Batch = 'enterprise-rag-e2e-' + (Get-Date -Format 'yyyyMMdd-HHmmss')
$Fixtures = Join-Path $Runtime "$Batch-fixtures"
$IdentityCredentials = Join-Path $Runtime "$Batch-identities.json"
$WebState = Join-Path $Runtime "$Batch-web-state.json"
$WebOutput = Join-Path $Runtime "$Batch-web-output"
```

Node2:

```bash
deploy_root=<activated-deploy-root-built-from-evaluated-commit>
release_root=<immutable-release-built-from-evaluated-commit>
evaluated_application_commit=393cf18a5459c38ebef87a6dfc5672451f561510
work_root=/home/qsyy0921/MFL/staging/enterprise-rag-e2e
batch_id=<generated-batch-id>
tenant_id=<tenant-uuid>
member_a=<authorized-member-uuid>
member_b=<denied-member-uuid>
```

Set `deploy_root` and `release_root` only to the production deployment and
immutable release built from `evaluated_application_commit` after evaluation
passes. Never substitute the preserved pre-remediation deployment or modify
release contents in place.

## Gate 1: evaluation

Read the user services and reports. Do not restart an active stage:

```bash
systemctl --user show \
  openim-rag-enterprise-evaluation.service \
  -p Id -p ActiveState -p SubState -p Result -p ExecMainStatus

test -f /home/qsyy0921/MFL/eval/enterprise-rag/retrieval-report.json
test -f /home/qsyy0921/MFL/eval/enterprise-rag/generation-report.json
test -f /home/qsyy0921/MFL/eval/enterprise-rag/final-report.json
```

The final report must identify application commit
`393cf18a5459c38ebef87a6dfc5672451f561510`, projection
`document-title-content-v1`, embedding
`qwen3-embedding:4b`, reranker revision
`953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`, generation model
`gpt-5.6-terra`, and `passed=true`. A report file existing is not sufficient;
`ops/run-node2-enterprise-rag-evaluation.sh` must accept the immutable
retrieval, generation, and final reports together. Tooling-only commits may be
newer, but any application change after the evaluated commit requires a new
index and full evaluation.

## Gate 2: generate isolated fixtures

From the controller:

```powershell
Set-Location $Repo
Push-Location "$Repo\platform\services\platform-api"
try {
    go run ./cmd/knowledge-e2e-fixtures `
      -batch-id $Batch `
      -output $Fixtures
} finally {
    Pop-Location
}
```

The output directory must be new and empty.

Create two deterministic isolated OIDC/member/device contracts:

```powershell
python ops/manage-node2-enterprise-rag-identities.py `
  contract $Batch $IdentityCredentials
```

The output file contains temporary test passwords. Do not print or transfer it
anywhere except the Node2 staging directory used by the next step.

## Gate 3: prepare Node2 identities and isolate Memory

Create one owner-only remote directory and transfer the fixtures and credential
file over the authenticated LAN SSH connection. On Node2, run:

```bash
sudo python3 "$deploy_root/ops/manage-node2-enterprise-rag-identities.py" \
  prepare "$work_root/$batch_id/identities.json" \
  "$work_root/$batch_id/identity-state.json"

sudo rm -f -- "$work_root/$batch_id/identities.json"

sudo python3 "$deploy_root/ops/manage-node2-enterprise-rag-identities.py" \
  isolate-begin "$work_root/$batch_id/identity-state.json"
```

Keep the sanitized `identity-state.json`. It contains IDs and ownership markers,
not passwords. Read the tenant, authorized member A, and denied member B IDs
from the local credential contract without printing passwords:

```powershell
$Identity = Get-Content $IdentityCredentials -Raw | ConvertFrom-Json
$Tenant = $Identity.tenant_id
$MemberA = $Identity.members.authorized.member_id
$MemberB = $Identity.members.denied.member_id
```

## Gate 4: Web bootstrap

Run the password-safe wrapper. It injects credentials only into the Playwright
process and restores the caller's process environment in `finally`:

```powershell
& "$Repo\ops\run-enterprise-rag-web-phase.ps1" `
  -Phase bootstrap `
  -CredentialPath $IdentityCredentials `
  -FixtureDirectory $Fixtures `
  -StatePath $WebState `
  -OutputDirectory $WebOutput
```

The phase must:

1. Upload Markdown, TXT, text-layer PDF, and DOCX through the real Web/API path.
2. Reach real MinIO, parser, Chunk, embedding, pgvector, publication, and grant
   states for all four documents.
3. Return one Terra answer with all four control markers and durable citations.
4. Reject the malformed PDF as `PDF_STRUCTURE_INVALID` with no publication.
5. Produce desktop/mobile screenshots with no horizontal overflow, console
   errors, failed network responses, or localStorage credentials.

Copy the resulting `WebState` to the Node2 batch directory after every Web
phase. Never edit it manually.

## Gate 5: authorized OpenIM and Telegram

OpenIM sends one real message as isolated member A and reconciles the exact
durable Run:

```bash
bash "$deploy_root/ops/accept-node2-enterprise-rag-openim.sh" \
  authorized "$work_root/$batch_id/web-state.json" \
  "$work_root/$batch_id/fixtures/manifest.json" \
  "$tenant_id" "$member_a" "$member_b" "$deploy_root" \
  "$work_root/$batch_id/openim-result.json"
```

For Telegram, repeat this bounded physical-client flow for each phase:

1. Read the current Bot update baseline with
   `accept-node2-telegram.sh snapshot`.
2. Send one bootstrap message from the real private Telegram chat.
3. As root, bind only the latest unbound update to the intended isolated member.
4. Record the emitted bootstrap chat/message IDs with
   `accept-node2-enterprise-rag-telegram.sh record-bootstrap`.
5. Generate the exact phase prompt with
   `accept-node2-enterprise-rag-telegram.sh prompt`.
6. Send that exact prompt from the real client once.
7. Run `accept-node2-enterprise-rag-telegram.sh verify` with the binding's
   emitted verification baseline.
8. Remove the exact temporary binding with `accept-node2-telegram.sh cleanup`.

The authorized Telegram result must contain one ingress, one published Outbox
event, one successful Terra Run, one sent Delivery, a real external message ID,
and citations covering all four initial versions.

## Gate 6: dependency fault injection

Run these three phases after Web bootstrap and before any grant revocation:

```bash
for phase in embedding reranker model; do
  sudo bash "$deploy_root/ops/accept-node2-enterprise-rag-fault.sh" \
    "$phase" "$work_root/$batch_id/web-state.json" \
    "$work_root/$batch_id/fixtures/manifest.json" \
    "$tenant_id" "$member_a" "$deploy_root" \
    "$work_root/$batch_id/fault-result.json"
done
```

The proxy binds only Node2 loopback and rejects only the phase's endpoint when
the request body contains the unique batch marker. All unrelated traffic is
forwarded once. Expected evidence:

| Phase | Failed path | Forwarded requests | Rejected requests | Terminal error |
| --- | --- | ---: | ---: | --- |
| embedding | `/v1/embeddings` | 0 | 3 | embedding HTTP 503 |
| reranker | `/v1/rerank` | 3 embeddings | 3 | reranker HTTP 503 |
| model | `/v1/candidates` | 1 route | 3 | intelligence HTTP 503 |

Each exact Run must be `failed`, route to
`enterprise.knowledge.search`, record two bounded retries plus one terminal
failure, and have zero Candidate text/model/provider ID, citations, reply ID,
and Delivery rows. The script restores direct `18082`/`18083` topology only
after the Run is terminal. If an external send is uncertain, the marker-scoped
proxy remains active and the next invocation reconciles instead of resending.

The malformed PDF in Gate 4 is the parser fail-closed injection.

## Gate 7: denied, revoked, and immutable-version phases

Run the Web denied phase as member B, copy the state to Node2, then run the
OpenIM and Telegram `denied` phases using member B.

Run the Web `revoke` phase as member A. It removes all four grants before the
immediately following query. Copy the state and run OpenIM and Telegram
`revoked` as member A.

Run the Web `version` phase. It regrants only the Markdown document, creates
immutable version 2, publishes it, and asks the old-version control question.
Copy the state and run OpenIM and Telegram `version`.

Use the same safe wrapper for each Web phase:

```powershell
& "$Repo\ops\run-enterprise-rag-web-phase.ps1" `
  -Phase <denied|revoke|version> `
  -CredentialPath $IdentityCredentials `
  -FixtureDirectory $Fixtures `
  -StatePath $WebState `
  -OutputDirectory $WebOutput
```

After member B has logged in once, verify both isolated OpenIM links:

```bash
sudo python3 "$deploy_root/ops/manage-node2-enterprise-rag-identities.py" \
  verify "$work_root/$batch_id/identity-state.json" --require-openim
```

Final channel assertions:

- B has zero target Tool results and zero target citations in every channel.
- A has zero target evidence immediately after revocation.
- Version queries cite version 2, contain the new marker, and contain neither
  version 1 citations nor its old marker.
- OpenIM and Telegram use the same locked model and post-generation Citation
  validation path; neither regenerates at Delivery time.

## Gate 8: final verification

After all four Web phases and all four OpenIM/Telegram phases:

```bash
bash "$deploy_root/ops/accept-node2-enterprise-rag.sh" \
  verify-web "$work_root/$batch_id/web-state.json" \
  "$work_root/$batch_id/fixtures/manifest.json" \
  "$tenant_id" "$member_a" "$member_b"
```

Inspect the Playwright desktop/mobile screenshots, browser console/network
results, and real Telegram client replies. Database rows alone do not prove
visible delivery.

## Gate 9: exact cleanup

Cleanup order is intentional:

1. Run Web `cleanup` to revoke every grant and unpublish every document.
2. Remove the final Telegram binding.
3. Delete only the recorded acceptance Telegram messages through the official
   `deleteMessages` call.
4. Delete exact source objects and PostgreSQL fixture Runs/documents.
5. Disable the two isolated identities and clear only their OpenIM histories.
6. Restore the Memory extractor.

Commands:

```powershell
& "$Repo\ops\run-enterprise-rag-web-phase.ps1" `
  -Phase cleanup `
  -CredentialPath $IdentityCredentials `
  -FixtureDirectory $Fixtures `
  -StatePath $WebState `
  -OutputDirectory $WebOutput
```

```bash
bash "$deploy_root/ops/accept-node2-enterprise-rag-telegram.sh" \
  cleanup-contract "$work_root/$batch_id/telegram-result.json"

sudo bash "$deploy_root/ops/accept-node2-enterprise-rag-telegram.sh" \
  cleanup-messages "$work_root/$batch_id/telegram-result.json"

bash "$deploy_root/ops/accept-node2-enterprise-rag.sh" \
  cleanup "$work_root/$batch_id/web-state.json" \
  "$work_root/$batch_id/fixtures/manifest.json" \
  "$tenant_id" "$member_a" "$member_b" "$batch_id"

sudo python3 "$deploy_root/ops/manage-node2-enterprise-rag-identities.py" \
  disable "$work_root/$batch_id/identity-state.json"

sudo python3 "$deploy_root/ops/manage-node2-enterprise-rag-identities.py" \
  isolate-end "$work_root/$batch_id/identity-state.json"
```

Delete the local temporary credential only after all Web phases and cleanup
complete. Remove the Node2 batch directory only after every zero-residual check
passes. A cleanup failure remains explicit and must be reconciled against the
same IDs; do not create another batch to hide it.

## Required evidence

- Locked retrieval, generation, and final evaluation reports.
- Four-format Web state and parser failure state.
- Desktop/mobile screenshots and clean console/network checks.
- OpenIM result JSON for authorized/denied/revoked/version.
- Telegram result JSON for authorized/denied/revoked/version plus confirmed
  message cleanup.
- Fault result JSON for embedding/reranker/model with exact failed Run IDs.
- Zero-residual platform, object, grant, Telegram binding/challenge, Memory, and
  isolated identity preflight checks.
- Final repository/test gates, immutable release manifest, branch commit, push,
  and Draft PR.
