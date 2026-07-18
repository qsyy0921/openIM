---
unit: enterprise-knowledge-dataset
status: implemented
depends_on:
  - acl-retrieval
  - intelligence-worker
---

# Enterprise Knowledge Dataset

## Scope

Provide a deterministic, Chinese, single-enterprise knowledge corpus and grounded QA suite for exercising the OpenIM Agent RAG pipeline beyond the existing one-document smoke fixture. The company, people, systems, products, and events are fictional; the operating rules and document relationships are deliberately realistic and internally consistent.

## Responsibilities and non-goals

This unit owns a canonical company fact catalog, versioned source documents, chunks aligned with the current PostgreSQL schema, grounded QA cases, a PostgreSQL import artifact, generation, and offline validation. It does not claim to contain real proprietary company data, train an embedding model, choose a vector database, implement ingestion services, implement Memory, or replace production authorization tests.

## Contracts and dependencies

- `knowledge.documents`, `knowledge.document_versions`, and `knowledge.chunks` from migration `0005_acl_retrieval.sql`.
- The local fixture tenant `aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa` and member `bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb` for the generated SQL import.
- UTF-8 JSON Lines for documents, versions, chunks, and QA cases.
- Markdown source files retained beneath `datasets/enterprise-knowledge/v1/raw/`.
- Python 3.11 or later, using only the standard library.

## Invariants

- Generation is deterministic for generator version `1.0.0`, release date `2026-07-14`, and seed `20260714`.
- The corpus contains one fictional enterprise, thirteen business domains, eight topics per domain, and five document forms per topic.
- Canonical topic facts are defined once and reused across policy, SOP, FAQ, runbook, and decision-record documents.
- Each logical document has one current published version; every policy also has one superseded historical version with explicit changed values.
- Every answerable QA case references exact document, version, chunk, and verbatim evidence spans.
- Unanswerable cases contain no evidence and require explicit abstention.
- IDs are UUID5 values derived from stable semantic keys; reruns cannot create alternate identities.
- Generated content contains no credentials, real personal data, or claims that it was copied from an actual company.

## Runtime flow

1. Load the fixed company profile and thirteen domain blueprints from the generator.
2. Materialize 104 canonical topics and their owners, systems, SLAs, deadlines, retention periods, escalation roles, and control thresholds.
3. Render five complementary document forms per topic and one superseded policy version.
4. Split documents by semantic section, assign deterministic IDs, and calculate SHA-256 checksums.
5. Generate single-document, multi-document, numeric, version-awareness, procedure, and unanswerable QA cases.
6. Write JSONL, Markdown, PostgreSQL SQL, statistics, and a hash manifest.
7. Run the independent validator and persist its report.

## Data ownership and state

`ops/knowledge_dataset/generate.py` is the authoritative generator. Generated artifacts under `datasets/enterprise-knowledge/v1/` are a reproducible release. `documents.jsonl`, `document_versions.jsonl`, and `chunks.jsonl` map to PostgreSQL facts; `qa.jsonl` and `validation-report.json` are evaluation assets and are not production knowledge state.

## Failure handling

Generation writes to a temporary sibling directory and only replaces the release directory after all artifacts are complete. Validation fails non-zero for broken references, checksum mismatches, duplicate IDs or questions, missing evidence spans, invalid version state, insufficient domain coverage, unexpected counts, short documents, or a non-deterministic manifest hash. No reduced-size or alternate-format success path exists.

## Security

The dataset uses a named fictional company and role titles instead of real individuals. Source URIs use the `knowledge://xinglan/` namespace. The generated SQL grants read access only to the fixed local test member and is explicitly a development import, not a production migration. No model key or OpenIM token is read by generation or validation.

## Observability

`statistics.json` records counts by domain, form, version status, QA type, and split, along with character and chunk distributions. `validation-report.json` records every quality gate, corpus hashes, and the validator version. Commands print concise key-value summaries suitable for CI logs.

## Acceptance criteria

- Exactly 520 logical documents cover 13 domains, 104 topics, and five document forms.
- Exactly 624 versions exist: 520 published current versions and 104 superseded policy versions.
- At least 2,600 chunks and at least 1,100 QA cases are produced.
- Domain and document-form distributions are balanced.
- All answerable questions have valid exact evidence; all unanswerable questions have none.
- Current-version QA evidence never points at a superseded version.
- Every raw Markdown file, JSONL artifact, and SQL import is included in the manifest.
- Two consecutive generations produce identical release hashes.
- Repository validation and dataset validation both pass.

## Source evidence

- `ops/knowledge_dataset/generate.py`
- `ops/knowledge_dataset/validate.py`
- `datasets/enterprise-knowledge/v1/README.md`
- `datasets/enterprise-knowledge/v1/manifest.json`
- `platform/services/platform-api/internal/migrations/sql/0005_acl_retrieval.sql`
- `platform/services/platform-api/internal/retrieval/retrieval.go`

## Verification evidence

- Generator output contains 520 documents, 624 versions, 3,224 chunks, and 1,120 QA cases across all thirteen domains.
- Published source text totals 486,966 Chinese characters; current documents range from 854 to 1,118 characters.
- The QA suite contains 1,040 answerable cases and 80 explicit-abstention cases; every answerable evidence span resolves exactly to a current chunk.
- Exact document-body and question uniqueness are both 100%; maximum same-form character 5-gram Jaccard similarity is `0.719577` after scenario diversification.
- Two consecutive generations produced release hash `d9a31899246bfa9077be3d42d3dd0b54737a3fa50461ffd628ad4c5ae68059c9`.
- `python ops/knowledge_dataset/validate.py`, `python ops/validate-repository.py`, Python compilation, and `git diff --check` passed.

## Open questions

- A later Goal must implement document ingestion instead of relying on generated SQL.
- Embedding model revision, vector dimension, normalization, batching, and `pgvector` index parameters require a measured contract.
- Retrieval evaluation must establish Recall@K, MRR/nDCG, citation correctness, abstention quality, latency, and cost against a real running stack.
- Multi-turn conversational memory remains a separate dataset and runtime slice.
