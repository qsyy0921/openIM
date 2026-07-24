# ADR-0011: Version the structure-aware retrieval projection

- Status: Proposed
- Date: 2026-07-25

## Context

The first complete Node2 schema-v4 evaluation of the production pgvector
retriever processed all 1,120 QA cases. Security and integrity gates held, but
quality did not:

- Recall@5: `0.7682692307692308`;
- Recall@10: `0.8480769230769231`;
- MRR: `0.480470085470084`;
- answerable failures: `158`;
- ACL leakage: `0`;
- stale-version leakage: `0`;
- provenance and checksum integrity: `1.0`.

The generation stage correctly failed its prerequisite gate and made no Terra
request.

A read-only candidate diagnosis found the expected Chunk within lexical Top-32
for 39 failed cases and below Top-32 for 119. There were no failed cases with no
lexical match at all. Failures were concentrated in questions whose identifying
terms occur in the document title or topic metadata rather than in the section
body.

The current ingestion worker and bulk reindexer embed and index only
`chunk.content`. The reranker also receives only `chunk.content`, even though
the ACL-first SQL has already joined the authorized current document title.
Consequently, document identity is lost at every ranking stage.

Changing the indexed text without recording its semantics would make an active
index irreproducible. The current generation identity records model revision
and dimension but cannot distinguish the historical content-only projection
from a title-aware projection.

## Proposed decision

1. Define one shared deterministic projection revision:
   `document-title-content-v1`.
2. The canonical retrieval text is the normalized authorized current document
   title, two newline characters, and the immutable normalized Chunk content.
3. Use that canonical text for embedding and lexical indexing in both leased
   ingestion and bulk reindexing.
4. Use the same title-first text for reranking, with deterministic UTF-8-safe
   truncation to the existing byte limit.
5. Keep `chunk.content`, `chunk.checksum`, evidence excerpts, and Citation
   checksums unchanged. Answers still expose only original authorized Chunk
   content.
6. Add `projection_revision` to index-generation, search-index, and evaluation
   metadata through a forward migration. Existing rows are labeled
   `chunk-content-v1`.
7. Require the application configuration to match the compiled projection
   revision exactly. A missing or incompatible active generation is a hard
   dependency failure.
8. Build and verify the new generation before atomic activation. Do not mutate
   or reinterpret the historical generation.
9. Preserve the existing ACL/current-publication predicates, Top-32 bounds,
   RRF, fixed reranker, embedding model, and release thresholds.
10. Run a bounded regression set containing the 158 observed failures plus
    stratified passing controls before repeating the full 1,120-case evaluation.

## Alternatives considered

### Increase Top-N

Rejected as the first fix. It increases reranker CPU cost while retaining a
projection that omits known document identity. Candidate limits remain fixed
until a corrected projection is measured.

### Add title only to lexical search

Rejected because dense retrieval and reranking would still operate on a
different semantic representation. All ranking stages must share one
reproducible projection contract.

### Change embedding or reranker model

Rejected. The failure is attributable to missing structure in model inputs, and
changing a locked model would confound the measurement and violate the current
Goal.

### Query all authorized chunks

Rejected because it removes bounded database/model work and does not provide a
production retrieval design.

### Reinterpret the existing active generation

Rejected because it would make historical evaluation and rollback evidence
false. Projection semantics require their own immutable revision.

## Consequences

- One forward migration is required after 0032.
- Existing production generations remain valid historical evidence but are
  incompatible with the new application projection contract.
- Reindexing requires new embeddings and lexical rows for the current published
  Chunks.
- Title changes require a new derived generation; they cannot silently mutate
  an active projection.
- Ingestion, bulk reindex, retrieval, reranking, evaluation, deployment, and
  acceptance scripts must all pin the same revision.
- The added title may improve document identity recall, but only measured
  regression and full-set results can accept this proposal.

## Verification required before acceptance

- Fresh and upgrade migration tests label historical rows without changing
  their vectors or lexemes.
- Unit tests prove deterministic projection text and UTF-8-safe truncation.
- Ingestion and reindex tests prove identical title/content inputs.
- Retrieval tests reject a missing or mismatched projection revision.
- ACL, stale-version, checksum, Citation, and no-fallback tests remain green.
- A bounded Node2 failed-case regression shows no security regression and enough
  recall improvement to justify the full rerun.
- The full 1,120-case retrieval report meets every unchanged release threshold.

This ADR remains proposed until the migration, implementation, focused
regression, and full evaluation evidence are complete.
