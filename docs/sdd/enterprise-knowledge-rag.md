---
unit: enterprise-knowledge-rag
status: implemented
depends_on:
  - identity-session
  - acl-retrieval
  - agent-runtime
  - intelligence-worker
  - adr-0001
  - adr-0003
  - adr-0007
  - adr-0010
  - adr-0011
---

# Enterprise Knowledge RAG

## Scope

Provide one production path for an enterprise knowledge administrator to upload,
process, grant, publish, and revoke Markdown, TXT, text-layer PDF, and DOCX
documents, and for an authenticated member to receive only authorized,
version-current, reranked evidence and a checksum-verifiable cited answer through
the existing Web/OpenIM and Telegram Agent channels.

## Responsibilities and non-goals

This unit owns immutable knowledge-source upload, versioning, ingestion jobs,
derived Chunk/vector/FTS projections, explicit publication, direct-member grants,
ACL-first retrieval, deterministic fusion, fixed reranking, Citation
reauthorization, the Knowledge Web module, and RAG evaluation records. It does
not own OpenIM facts, Agent Memory, cloud-document CRDT state, arbitrary
connectors, OCR, or dynamic provider/parser/retriever selection.

## Contracts and dependencies

- OIDC and active device/member resolution from `identity-session`.
- PostgreSQL Knowledge/Authorization facts and pgvector 0.8.5 from ADR-0010.
- Dedicated loopback-only MinIO process and private Bucket. Its per-instance
  credentials and data root are separate from the upstream OpenIM MinIO.
- `qwen3-embedding:4b`, 2,560-dimensional normalized embedding contract.
- Local `BAAI/bge-reranker-v2-m3@953dc6f6...` through the loopback Intelligence
  Worker.
- Existing Terra Candidate, Agent Run, Outbox, OpenIM, and Telegram delivery
  contracts.
- API shapes declared in `contracts/openapi/platform-v1.yaml`.

## Invariants

- ACL, current publication, tenant, checksum, and active index predicates execute
  before content leaves PostgreSQL.
- MinIO is the raw-object source; PostgreSQL is the document/version/job/grant
  fact source; projections are rebuildable derivatives.
- A version is immutable and cannot be published before all required projections
  exist.
- RAG, Agent Memory, OpenIM history, and cloud-document state remain separate.
- Enterprise-knowledge generation receives only the current authorized Evidence
  envelope; personal or group Memory is not loaded into that model request.
- No parser, provider, retriever, reranker, model, public-corpus, or historical
  `real[]` production fallback exists.
- No model or parser call occurs inside a database transaction.
- Every delivered knowledge answer is a durable candidate whose citations were
  reauthorized and checksum-validated after generation.

## Runtime flow

The write path is bounded upload validation, UUID object reservation, MinIO put,
leased parse/Chunk/embed/index processing, explicit grant, and explicit publish.
The read path is authenticated member/device resolution, ACL/current-version
relation, lexical and dense Top-N, RRF, fixed rerank, Evidence budget, Terra
Candidate, Citation reauthorization/checksum/support validation, durable save,
and existing idempotent channel delivery.

## Data ownership and state

PostgreSQL owns document, immutable version, ingestion job, publication, direct
grant, index generation, evaluation, and audit state. MinIO owns immutable raw
objects. pgvector and FTS rows are PostgreSQL-derived projections. The local
model directory owns only the fixed reranker artifact. OpenIM owns all IM
messages and membership; the Agent runtime owns Runs, Candidates, citations, and
delivery intents.

## Domain boundaries

### KnowledgeDocument aggregate

Owns the stable document identity, tenant, title, classification, lifecycle, and
pointer to the current published version. It does not own OpenIM messages,
members, group membership, or the raw bytes.

### DocumentVersion aggregate

Owns an immutable source checksum, object reference, version number, ingestion
state, and publication history. Source bytes, checksum, and version number never
change after the upload reservation is finalized.

### IngestionJob aggregate

Owns asynchronous processing state, lease, attempts, bounded retry schedule,
terminal error code, parser contract, embedding contract, and projection counts.
It never reports success before all Chunk and vector projection rows commit.

### Chunk and Embedding projections

Derived, replaceable projections keyed by immutable version and Chunk checksum.
They are not document facts. A projection is production-ready only under the
active index generation and exact model revision.

### DocumentGrant authorization fact

`authz.document_grants` is the authoritative direct-member read grant. Grant
deletion affects the next query. No grant cache, UI-only hiding, public fallback,
or PostgreSQL copy of OpenIM group membership is permitted.

### RetrievalEvidence value object

Contains Citation ID, document/version/Chunk IDs, title, source URI, Chunk
checksum, authorized excerpt, retrieval scores, and active index revision. It
exists only for one authenticated query.

### Citation value object

Resolves a model Citation ID to one item in that query's authorized Evidence
envelope. Persistence stores immutable provenance, not model-provided document
metadata.

### EvaluationRun aggregate

Owns dataset revision, application commit, index/model revisions, thresholds,
metrics, bounded failure records, and terminal pass/fail. Reports contain IDs and
metric evidence, not private document bodies.

## Explicit separation

| Domain | Authoritative data | This unit may do | This unit must not do |
| --- | --- | --- | --- |
| Enterprise RAG | PostgreSQL Knowledge/ACL + MinIO source | Import, index, authorize, retrieve, cite | Store personal preferences or chat summaries |
| Agent Memory | PostgreSQL `memory` schema | Serve independent non-RAG conversation paths | Load Memory into enterprise-knowledge generation or turn it into documents |
| OpenIM history | OpenIM server/SDK | Receive a prompt and deliver one validated answer | Copy messages, users, groups, members, seq |
| Cloud documents | Future collaboration domain | Import an explicit immutable export later | Pretend RAG source versions are live CRDT state |

## State models

### DocumentVersion

```text
draft/uploading
  -> draft/queued
  -> draft/processing
  -> draft/indexed
  -> published
  -> superseded

draft/* -> draft/failed
published -> draft/indexed        (explicit unpublish)
```

The existing `status` remains `draft|published|superseded`; `ingestion_state`
records `uploading|queued|processing|indexed|failed`. A failed version is
immutable and may be superseded by another upload, not overwritten.

### IngestionJob

```text
queued -> leased -> succeeded
                  -> retryable -> leased
                  -> failed
```

- A lease has an owner and expiry.
- Only expired `leased` and due `retryable` jobs can be reclaimed.
- Maximum attempts are fixed by deployment configuration.
- Parser, embedding, database, object-store, and checksum errors use distinct
  sanitized codes.
- An uncertain external operation is reconciled against the same object key and
  checksum; it does not create a second version.

## API contracts

All endpoints require OIDC Bearer plus the existing `device_id` and
`platform_id` context. Administration requires `knowledge_admin` or
`platform_admin`; member query behavior still enforces document grants.

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/v1/knowledge/documents` | List safe document/version/job projections |
| `POST` | `/v1/knowledge/documents` | Multipart initial upload with required idempotency key |
| `POST` | `/v1/knowledge/documents/{id}/versions` | Upload immutable next version |
| `POST` | `/v1/knowledge/documents/{id}/versions/{version}/publish` | Publish only a fully indexed version |
| `POST` | `/v1/knowledge/documents/{id}/unpublish` | Remove current version from retrieval |
| `PUT` | `/v1/knowledge/documents/{id}/grants/{member}` | Add direct read grant idempotently |
| `DELETE` | `/v1/knowledge/documents/{id}/grants/{member}` | Revoke direct read grant immediately |
| `GET` | `/v1/knowledge/documents/{id}/versions` | List immutable versions and failures |
| `GET` | `/v1/knowledge/runs/{run}/citations` | Resolve citations only for the owning member or administrator |

The Web test-question action sends a real message to the published Knowledge
Agent over the existing OpenIM transport. It does not create a second direct
generation endpoint. Structured citation details are read from the durable Agent
workspace/run projection after delivery.

## Upload and object storage

1. The HTTP layer applies a byte limit before multipart parsing.
2. Extension, declared MIME, detected leading signature, and filename safety
   must agree before persistence. Full PDF/DOCX structure validation is deferred
   to the leased parser job so the HTTP request does not perform long parsing.
3. The server streams to a private temporary file while computing SHA-256; raw
   bytes are not loaded into PostgreSQL.
4. A short transaction reserves UUIDs, version number, object key, idempotency
   digest, and `uploading` state.
5. MinIO receives the object under a UUID-only key. The Bucket is private.
6. A short transaction records size/checksum/format and queues the job.
7. A failed upload leaves an explicit failed job/version and moves the exact
   reserved object from `stored` to `delete_pending`. A cleanup worker claims it
   with `FOR UPDATE SKIP LOCKED`, a UUID lease token, and a bounded lease.
8. Cleanup deletes only the persisted bucket/object key. Success becomes
   `deleted`; failure clears the lease, records a bounded safe detail, and uses
   exponential delay for at most three attempts before terminal
   `delete_failed`. No handler or worker reports success while the object
   outcome is unknown.

The leased worker downloads the immutable object, verifies its byte count and
SHA-256 against PostgreSQL, and only then performs the complete format-structure
validation described below. That parser call is outside every database
transaction.

The original filename is presentation metadata only. It never forms a path,
Bucket, URL, SQL identifier, log field, or authorization decision.

## Parser contracts

- Markdown/TXT: strict UTF-8, no NUL, normalized newlines, bounded line and
  document size.
- PDF: `pdfcpu v0.13.0` strict validation followed by
  `ledongthuc/pdf@5959a402...` text extraction. Encrypted, malformed, text-empty,
  or image-only files fail with no OCR.
- DOCX: standard-library ZIP/XML parser; required OOXML parts only; bounded
  entries, compressed/uncompressed sizes and ratio; reject macros, external
  relationships, `altChunk`, encryption, entities, and missing text.
- There is one parser per accepted format. A parser error is terminal or
  retryable according to its typed cause; another parser is never tried.

## Chunking contract

- Preserve heading and paragraph boundaries.
- Normalize Unicode whitespace without changing visible text semantics.
- Target at most 1,200 Unicode code points and 8,000 UTF-8 bytes per Chunk.
- Split oversized paragraphs deterministically by sentence, then code point.
- Keep at most 120 code points overlap only between adjacent chunks in the same
  section.
- `chunk.checksum = sha256(normalized_content)`.
- IDs are UUIDv5-derived from `(version_id, ordinal, checksum)` so a replay of the
  same job produces the same projection.
- Empty and duplicate adjacent chunks are rejected.

## Indexing contract

- Embedding model: `qwen3-embedding:4b`, exact deployed revision, dimension
  2,560, normalized.
- Dense storage: pgvector 0.8.5 `halfvec(2560)`.
- Dense index: cosine HNSW with fixed parameters recorded in ADR-0010.
- Lexical projection: application-generated deterministic Chinese/English
  lexemes stored as `tsvector` with GIN.
- The historical `real[]` table from migration `0025` remains migration history
  but no production query reads it.
- A model/index generation becomes active only when every current published
  Chunk has exactly one matching checksum/dimension projection.
- Publishing a new version requires every Chunk in that version to have the
  active projection before the pointer changes.
- Index generation may issue 1..8 concurrent requests to the same locked
  embedding endpoint. The concurrency is explicit, bounded, and used only
  outside database transactions; all completed batches are validated before
  deterministic persistence. Any request failure fails the generation and
  never selects another model or endpoint.
- Node2 uses four texts per request and two concurrent requests by default.
  A 2026-07-25 benchmark on real title-plus-content projections measured a warm
  four-text batch at `56.920s`, eight texts at `121.823s`, and a sixteen-text
  batch exceeded the Worker's fixed `180s` dependency timeout. Higher
  concurrency remains an explicit operator override, not an automatic retry or
  fallback.
- Production deployment enumerates the PostgreSQL tenants that own a current
  published knowledge version and invokes the index builder once per tenant.
  It does not use the evaluation tenant as a production default. Every returned
  generation must be active, use `document-title-content-v1`, and report a
  positive exact `indexed_chunks == expected_chunks` count before Agent and
  ingestion services restart.

## Retrieval-quality remediation (implemented locally; activation proposed)

The first complete schema-v4 Node2 retrieval evaluation finished all 1,120
cases but failed the release gate: Recall@5 was `0.768269`, Recall@10 was
`0.848077`, MRR was `0.480470`, and 158 answerable cases did not retrieve their
expected evidence. ACL leakage and stale-version leakage remained `0`, while
provenance and checksum integrity remained `1.0`. The gated generation service
therefore stopped before making any Terra request.

The failure is concentrated in `single_document`, `version_awareness`, and
`numeric` questions. A read-only replay of the lexical candidate query found the
expected evidence in the lexical Top-32 for only 39 of the 158 failed cases; the
other 119 had a lexical match below Top-32. Source inspection of the evaluated
implementation explains this candidate-recall loss:

- ingestion and bulk reindex embed only `chunk.content`;
- the FTS projection is derived only from `chunk.content`;
- the reranker receives only `chunk.content`;
- the authorized SQL already loads `document.title`, but the title is not part
  of any retrieval-model input;
- dataset questions often identify the governing document or topic by title
  while the relevant Chunk contains only the section body.

ADR-0011 defines one deterministic
`document-title-content-v1` retrieval projection. It concatenates the
authorized current document title and original Chunk content for embedding,
lexical indexing, and reranking. The original Chunk body, checksum, Citation
excerpt, and authorization predicates do not change.

Migration 0033 and the application now make the projection contract explicit in
ingestion jobs, index generations, search rows, EvaluationRuns, configuration,
and reports. A new application fails closed when only the historical
`chunk-content-v1` generation is active; it must build, verify, and atomically
activate the new projection instead of silently reusing the old index.
Disposable PostgreSQL 17/pgvector 0.8.5 upgrade, fresh-install, repeat-run,
historical-backfill, and Knowledge/Retrieval integration checks pass.

Production activation remains **proposed** until a bounded failed-case
regression passes. Only then may the full 1,120-case evaluation be started
again. Thresholds, candidate limits, models, ACL order, and no-fallback
behavior remain unchanged.

The Node2 evaluation runner owns all three ordered stages. It creates the full
retrieval report atomically only when that report is absent; an existing report
is immutable evidence and must pass the same schema, projection, security, and
quality gates. Generation and finalization are unreachable until retrieval
passes, so a failed report cannot be replaced by an automatic retry.
The runner rejects missing, non-finite, or out-of-range required metrics,
recomputes every generation release threshold from measured fields, locks the
final threshold object, and requires the final report's embedded retrieval and
generation objects to equal their immutable source reports. A top-level
`passed` flag is never accepted as standalone evidence.

`ops/prepare_node2_enterprise_rag_evaluation.py` is the only supported handoff
from the bounded projection regression to the full evaluation. It requires the
active `2704/2704` index report and passing 158-case regression, verifies the
application binary, evaluation runner, QA dataset and database wrapper against
operator-pinned SHA-256 values, preserves the original working-tree regression
manifest as evidence, and atomically creates a new owner-only evaluation root.
A repeated call validates the existing root byte-for-byte and never repairs or
overwrites it. The generated user-service file is inert until explicitly
installed and started after the regression gate; preparation itself does not
send a request, mutate the evaluation database, or start an evaluation.

## Retrieval flow

```text
OIDC principal
  -> active member/device
  -> tenant + member
  -> current direct DocumentGrant
  -> active document + current published version
  -> authorized Chunk relation
  -> lexical Top-32
  -> dense Top-32
  -> RRF Top-32
  -> fixed local reranker Top-8
  -> document diversity + evidence byte budget
  -> Citation envelope
```

### Hard bounds

- Query: 1..2,000 UTF-8 bytes.
- Lexical and dense candidates: at most 32 each.
- Fusion set: at most 64 unique Chunk IDs.
- Reranker input: at most 32 after deterministic pre-truncation.
- Final Evidence: at most 8, at most 24 KiB total content.
- At most 3 chunks per document unless fewer than requested are available.

Both lexical and dense SQL branches include tenant, member grant, active
document, current published version, active index revision, classification, and
checksum predicates. An unauthorized Chunk cannot enter the RRF set, reranker
request, cache, prompt, log, Trace, or evaluation report.

### Ordering

- Lexical order: `ts_rank_cd DESC, chunk_id ASC`.
- Dense order: cosine distance ASC, `chunk_id ASC`.
- Fusion: RRF score DESC, best component rank ASC, `chunk_id ASC`.
- Rerank: score DESC, RRF score DESC, `chunk_id ASC`.
- Citation IDs are reassigned `C1..Cn` only after final ordering.

## Reranker contract

- Model ID: `BAAI/bge-reranker-v2-m3`.
- Revision:
  `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`.
- Local model directory is provisioned and manifest-verified before Worker
  startup.
- Endpoint: `POST /v1/rerank`, loopback-only Intelligence Worker.
- Maximum 32 pairs, 512 tokens per pair, non-streaming, one bounded timeout.
- Startup rejects a missing path or revision marker.
- A timeout, malformed response, model mismatch, duplicate/missing candidate ID,
  or non-finite score fails the retrieval. It never returns the RRF order as a
  fallback.

## Trusted generation and citation validation

1. Only final authorized Evidence enters the bounded Terra prompt.
2. Terra remains `gpt-5.6-terra`, Responses API, high reasoning, non-streaming,
   candidate-only.
3. The strict Candidate schema accepts only declared Citation IDs.
4. The validator reloads cited Chunk identity, current-version status, grant,
   checksum, and content in one post-generation authorization query.
5. It recomputes `sha256(content)` and requires exact equality.
6. Every factual answer sentence must declare at least one Citation ID. A
   deterministic support gate requires lexical anchor overlap and the fixed
   reranker score against at least one cited excerpt.
7. Any missing, stale, revoked, forged, duplicated, unsupported, or checksum-
   mismatched Citation fails the entire Candidate.
8. A no-evidence query uses the explicit runtime-policy refusal and never calls
   Terra.
9. The Candidate save transaction reloads and row-locks the active member,
   direct grant, current version, Chunk checksum, and publish/classification
   state. A revocation between post-generation validation and persistence fails
   the whole save.
10. The validated Candidate and its citations commit durably before the existing
    Outbox delivery path. OpenIM and Telegram never regenerate the answer.

No model, retrieval, parsing, or validation call is made while a database
transaction is open.

## Web behavior

The top-level Knowledge module includes:

- document list and upload;
- processing, indexing, publication, and failure states;
- immutable version list;
- direct-member grant summary and controls;
- publish/unpublish;
- test question routed to the existing Knowledge Agent conversation;
- citation details resolved from durable Run provenance.

The module is absent from navigation unless the authenticated API projection
contains `knowledge_admin` or `platform_admin`; every API independently enforces
the same role. It never renders object keys, credentials, raw Tokens, internal
model paths, or unauthorized excerpts.

Controls have explicit loading, empty, failure, conflict, and duplicate-submit
states. Desktop and mobile layouts use the existing workspace design language.

## Data consistency

- PostgreSQL is the fact source for document/version/job/grant/publication state.
- MinIO is the raw-object source; its checksum must match the version fact.
- Vector and FTS rows are derived projections and may be rebuilt from an
  immutable object/version.
- Publication is one short serializable transaction that checks the active
  projection and changes current-version state.
- This first direct-grant slice publishes only `public` and `internal`
  classifications. `confidential` and `restricted` fail closed until an
  explicit clearance model exists.
- Revocation and unpublish are authoritative writes; the next retrieval SQL must
  observe them.
- A cleanup failure is visible and retried against the same object key under an
  exclusive lease. Three failed attempts produce terminal `delete_failed`;
  operators must reconcile that exact object, and the state never changes a
  failed upload into success.

## Failure handling

| Failure | State/result |
| --- | --- |
| Authentication/device | 401/403, no object or job |
| Role check | 403, no object or job |
| MIME/signature/format | 422 terminal validation failure |
| Size/ZIP expansion | 413/422 terminal failure |
| MinIO put/get/checksum | job retryable, then failed |
| Parser deterministic rejection | job failed |
| Embedding transient error | job retryable with same version |
| Embedding contract mismatch | job failed |
| Projection commit | retryable with deterministic IDs |
| Incomplete projection | publish 409 |
| pgvector/FTS query | explicit retrieval failure |
| Reranker | explicit retrieval failure |
| Terra | existing bounded retry, then failed Run |
| Citation reauthorization/checksum/support | failed Run, no delivery |

### Node2 fault-injection contract

Production acceptance uses a temporary loopback-only conditional proxy between
Agent Runtime and the locked retrieval or generation Worker. The proxy rejects
only the selected endpoint when the request body contains a unique acceptance
batch marker. It never binds a LAN address, changes an upstream model, retries a
request, or forwards a failed request to another endpoint.

- Embedding injection rejects `/v1/embeddings` before any evidence query.
- Reranker injection forwards the required embedding request and rejects only
  `/v1/rerank`.
- Terra injection forwards the persisted `/v1/routes` decision and rejects only
  `/v1/candidates`.
- Each phase persists its send intent immediately before the OpenIM side effect,
  after local token and request preparation has completed; it reconciles
  uncertain sends and requires exactly three bounded model-phase attempts.
- A passing fault phase has a terminal failed Run, the exact typed 503 error,
  no Candidate/model/provider response, no Citation, no reply message, and no
  Delivery row.
- The runtime URL override lives only under `/run/systemd/system` and is removed
  after the exact Run reaches a terminal state. Direct loopback `18082` and
  `18083` health and process environment are rechecked after restoration.

The proxy is not a production fallback or chaos layer. It is an acceptance-only
fault boundary documented in
`docs/runbooks/node2-enterprise-rag-e2e.md`.

Error messages returned to members are stable codes with safe summaries. Raw
source content, query text, object key, model output, and credentials are not
logged.

## Security

- All content is untrusted data and cannot alter system instructions, ToolPolicy,
  roles, grants, approval, or delivery policy.
- Knowledge object storage is a dedicated MinIO process bound to
  `127.0.0.1:12015`; its generated credentials and M2-backed data directory are
  not shared with the LAN-exposed upstream OpenIM object store.
- Temporary files use owner-only permissions and are removed after processing.
- ZIP/PDF parsing is bounded by bytes, entries, ratio, pages, time, and output
  characters.
- ACL is applied before reranking and generation and rechecked after generation.
- API idempotency keys are tenant-scoped, payload-bound, and never reused across
  documents.
- Tokens, MinIO secrets, model credentials, source bytes, data volumes, model
  files, and generated releases are excluded from Git.

## Observability

Record bounded metadata only:

- upload/job counts and durations by format/state/error code;
- object bytes and parser output size histograms;
- Chunk and projection counts;
- lexical/dense/fusion/rerank latency and candidate counts;
- zero-evidence, ACL deny, citation reject, checksum mismatch, and stale-version
  counters;
- active model/index revisions;
- evaluation run ID, dataset digest, metrics, and failure QA IDs.

The finalizer accepts only schema-v5 retrieval and locked Terra generation
reports, recomputes the QA file digest, applies the frozen thresholds, derives a
deterministic EvaluationRun ID, and records one idempotent terminal row in
`knowledge.evaluation_runs`.

Never log document body, authorized excerpt, raw query, object key, Token, API
key, or model prompt.

## Acceptance criteria

- The four accepted formats traverse real MinIO, parsing, Chunk, embedding,
  pgvector, publish, ACL, retrieval, reranking, Terra, citation validation, and
  durable delivery.
- Duplicate upload/job execution produces one immutable version/projection.
- Failed parsing, embedding, reranking, and generation remain explicit failures.
- Member B never receives member A's target evidence in Web/OpenIM/Telegram.
- Grant deletion and unpublish affect the immediately following query.
- Publishing a new version makes old-version Citation IDs invalid.
- Retrieval full-set thresholds and generation sample thresholds in the Goal
  pass with ACL leakage 0 and checksum integrity 1.
- Desktop/mobile visual, console, network, repository, migration, secret, and
  large-file gates pass.

## Source evidence

Existing:

- `platform/services/platform-api/internal/migrations/sql/0005_acl_retrieval.sql`
- `platform/services/platform-api/internal/migrations/sql/0025_knowledge_embeddings.sql`
- `platform/services/platform-api/internal/retrieval`
- `platform/services/platform-api/internal/agent/worker.go`
- `platform/services/intelligence-worker`
- `datasets/enterprise-knowledge/v1`

Implemented write set:

- `platform/services/platform-api/internal/knowledge`
- `platform/services/platform-api/internal/migrations/sql/0032_*.sql` and later
- `platform/services/platform-api/cmd/knowledge-ingestion`
- `platform/apps/web/src/knowledge-*`
- `contracts/openapi/platform-v1.yaml`
- `eval/enterprise-rag-*.json`

## Verification plan

- Parser unit and adversarial fixtures.
- Object-store contract tests against real MinIO.
- Fresh and upgrade PostgreSQL migration tests with pgvector 0.8.5.
- Lease/retry/idempotency and publication integration tests.
- ACL isolation, stable ordering, RRF, rerank no-fallback, stale-version, forged
  Citation, checksum mutation, and support-gate tests.
- All 1,120 retrieval cases and at least 120 stratified generation cases.
- Node2 Web/OpenIM/Telegram authorized, denied, revoked, updated-version, and
  fault-injection E2E.

## Non-goals

- OCR, image understanding, Excel, PowerPoint, web crawling, or SaaS connectors.
- Department/group-derived grants or a generic policy language.
- Cloud-document CRDT ingestion.
- A second vector database or complete RAG framework.
- Dynamic provider/model/parser selection.
- Knowledge editing beyond immutable upload/version/publish controls.

## Open questions

- The measured CPU p95 of the fixed reranker determines only the final timeout
  value and batch concurrency; it cannot authorize a different model or skip.
- Group/department ACL inheritance remains a later explicitly designed slice.
