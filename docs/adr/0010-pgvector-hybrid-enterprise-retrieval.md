# ADR-0010: Use pgvector halfvec HNSW for ACL-first hybrid retrieval

- Status: Accepted
- Date: 2026-07-23

## Context

The current enterprise retriever correctly applies tenant, direct-member grant,
current-version, publication, and classification predicates before returning
content. It then loads every authorized `real[]` embedding into the Go process
and computes lexical and dense scores in memory. That preserves ACL safety for
the current 3,224-Chunk dataset but does not provide a production index, bounded
database work at larger scale, or a fixed reranking stage.

The deployed embedding contract is `qwen3-embedding:4b`, 2,560 dimensions,
normalized. pgvector 0.8.5 can store vectors with more dimensions, but its HNSW
and IVFFlat index classes support at most 2,000 dimensions for `vector` and 4,000
for `halfvec`. Therefore `vector(2560)` would store data but could not satisfy
the required ANN-index migration.

Enterprise retrieval also has exact-token requirements such as process IDs,
department names, and policy numbers, while natural-language questions require
semantic recall. Neither dense-only nor lexical-only is sufficient. ACL must be
enforced before any text reaches the reranker or model.

## Decision

1. Pin the PostgreSQL extension to pgvector `0.8.5`. Migration fails if that
   exact extension version is unavailable.
2. Add a new derived projection table using `halfvec(2560)`, fixed model
   revision, Chunk checksum, normalized flag, and application-produced
   `tsvector`.
3. Create a cosine HNSW index with `m=16` and `ef_construction=64`, plus a GIN
   index for the lexical projection. Queries use fixed `hnsw.ef_search` and
   `hnsw.iterative_scan = strict_order`.
4. Keep migration `0025` and its `real[]` table unchanged as historical upgrade
   input. The new production application never reads it. A one-time migration
   command may verify and convert matching 2,560-dimensional normalized rows;
   otherwise it recomputes the new projection from immutable Chunk content.
5. Represent an index build as a tenant-scoped generation with model revision,
   dimension, expected/current counts, state, and activation audit. A generation
   becomes active only after all current published Chunk checksums match.
6. Execute lexical and dense Top-32 queries against the same authorized relation:
   tenant, authenticated member grant, active document, current published
   version, allowed classification, active generation, and checksum.
7. Fuse unique authorized candidates with deterministic Reciprocal Rank Fusion,
   then call the fixed local
   `BAAI/bge-reranker-v2-m3@953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`
   for Top-8 selection and evidence budgeting.
8. Treat pgvector, FTS, embedding, and reranker failure as retrieval failure.
   There is no `real[]`, lexical-only, dense-only, unreranked, alternate-model,
   or external-provider production fallback.

## Why halfvec is not a model fallback

`halfvec` is a pgvector extension type and uses the original 2,560 coordinates.
It changes storage precision from 32-bit to 16-bit solely to fit the extension's
documented ANN index limit. It does not change the embedding model, dimension,
provider, endpoint, or retrieval policy.

The full frozen dataset must demonstrate that this precision change does not
lower Recall@5 or MRR below the accepted baseline. A failed comparison blocks
activation; it does not trigger dimensionality reduction or the old scanner.

## ACL and HNSW

Approximate indexes apply filters while scanning and can return fewer rows under
selective predicates. The implementation therefore:

- carries ACL/version predicates in both lexical and dense SQL, never in Go
  post-filtering;
- uses pgvector iterative scans with strict ordering;
- fixes candidate and `ef_search` bounds;
- adds B-tree indexes for tenant/document/member grant joins;
- evaluates department/permission strata and records empty-result failures;
- reauthorizes selected citations after generation.

Increasing a bounded scan parameter after measured evidence is tuning the same
implementation, not fallback. Removing ACL, querying a global index first, or
returning fewer unchecked candidates is forbidden.

## Alternatives considered

### `vector(2560)`

Rejected because pgvector cannot build the required HNSW/IVFFlat index above
2,000 dimensions.

### Dimensionality reduction or another embedding model

Rejected because it changes the locked embedding contract and creates an
unmeasured semantic migration. It would also violate strict no fallback.

### IVFFlat

Rejected for the first production slice. At the current data size, HNSW gives a
better speed/recall trade-off without training lists and is more stable during
incremental document publication. IVFFlat may be reconsidered only from measured
scale evidence in another ADR.

### External vector database

Rejected because PostgreSQL already owns document, version, ACL, audit, and
index metadata. A second fact/index service would add synchronization and
authorization failure modes without evidence that the current scale requires
it.

### Full RAGFlow, Haystack, or LlamaIndex runtime

Rejected because the project already has DDD, task, Agent, ACL, delivery, and
evaluation boundaries. Importing a general runtime would duplicate those
boundaries and encourage provider/parser fallback.

### Post-retrieval ACL filtering

Rejected because unauthorized text or scores could reach application memory,
the reranker, traces, or the model.

## Consequences

- PostgreSQL image/runtime must provide pgvector 0.8.5 before migration 0032.
- Vector storage is approximately half the payload of single-precision vectors,
  with a measurable precision trade-off guarded by the frozen QA suite.
- Retrieval no longer has a process-memory vector cache or a full authorized
  scan.
- Publication and index activation gain explicit readiness checks.
- Reranker availability becomes a required production dependency.
- The existing 0025 rows remain until a later cleanup migration, but are not an
  executable production path.

## Rollout

1. Back up Node2 PostgreSQL and verify the immutable release manifest.
2. Replace the PostgreSQL runtime with the digest-pinned pgvector 0.8.5
   PostgreSQL 17 image while preserving the data volume.
3. Apply migrations through 0032+ exactly once.
4. Build the new projection for the locked model and verify counts, dimensions,
   normalization, checksum, finite values, and published-Chunk coverage.
5. Run the frozen retrieval gate and compare with the stored baseline.
6. Activate the index generation in one audited transaction.
7. Start the new Agent Runtime only after embedding and reranker probes pass.
8. Run authorized, denied, revoked, stale-version, Web, OpenIM, and Telegram
   acceptance.

## Rollback

Rollback means restoring the previous immutable application release and, when
schema/data restoration is required, the pre-migration PostgreSQL backup. New
publication is paused first. The previous application may be enabled only after
an audit proves that every then-current published Chunk has its matching
historical 0025 projection; otherwise enterprise knowledge execution remains
paused and reports dependency failure.

The new application never switches to 0025 at runtime, and a health endpoint or
presence of historical rows is not evidence that rollback retrieval is valid.

## Verification

- Fresh PostgreSQL 17 + pgvector 0.8.5 migration.
- Upgrade copy from migration 0031 with existing 0025 rows.
- Extension-version, dimension, finite-value, normalization, checksum, and
  coverage assertions.
- `EXPLAIN (ANALYZE, BUFFERS)` evidence that dense retrieval uses HNSW and
  lexical retrieval uses GIN under ACL predicates.
- Full 1,120-case metrics and no-regression comparison.
- Reranker model/revision, malformed response, timeout, and unavailable tests.
- ACL leakage 0 and citation checksum integrity 1.0.
