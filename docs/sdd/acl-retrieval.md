---
unit: acl-retrieval
status: verified
depends_on:
  - platform-api
  - identity-session
  - adr-0002
  - adr-0003
---

# ACL Retrieval

## Scope

Return versioned enterprise-knowledge chunks only when the triggering member currently has direct read permission in the same tenant.

## Responsibilities and non-goals

The first slice owns authoritative document/version/chunk metadata, direct-member read grants, bounded lexical retrieval, immediate revocation, and citation provenance. It does not own file upload, OCR, connectors, embeddings, group-derived grants, a generic policy language, or Memory.

## Contracts and dependencies

- PostgreSQL `knowledge` and `authz` schemas
- Enterprise member and tenant identity from `identity.identity_links`
- Agent Runtime retrieval request carrying tenant, member, purpose, and query

## Invariants

- Tenant and current direct-member read grant are predicates in the content-returning SQL query.
- Unauthorized chunks never enter the Python request, logs, cache, or Run citation table.
- Only a document's current published version is searchable.
- Every result carries document, version, chunk, source URI, and checksum provenance.
- Grant deletion affects the next query; no stale authorization cache exists in this slice.
- Query failure is explicit; there is no unfiltered database scan or alternate retrieval path.

## Runtime flow

1. Resolve the Run's human member from its ready OpenIM IdentityLink.
2. Derive bounded lexical terms from the query.
3. Query current published chunks while joining tenant and direct-member read grants.
4. Rank by matched terms, return a bounded result set, and persist selected provenance with the Run.
5. Supply only those authorized chunks to the intelligence worker.

## Data ownership and state

`knowledge` owns document/version/chunk facts. `authz` owns direct-member document grants. Both are authoritative PostgreSQL state for this first slice; any later search/vector index is derived and must still pass an authoritative check.

## Failure handling

Invalid identity, empty query terms, database failure, and malformed provenance fail the Run. A valid zero-result query produces an explicit no-evidence answer and does not call the model; this is an intended authorization-safe outcome, not a provider fallback.

## Security

The effective read permission is the intersection of Run tenant, triggering member, fixed read-only Agent capability, current document tenant, current version state, and current direct grant. Content is treated as untrusted data and cannot grant tools or alter system instructions.

## Observability

Record Run ID, tenant, member, purpose, candidate count, authorized result count, document/version IDs, duration, and deny/zero-result counts without logging query or chunk content.

Retrieval evaluation and generation evaluation are separate contracts:

- Retrieval runs all frozen QA cases and reports Recall@K, MRR, retrieval precision@K, provenance integrity, and the unanswerable-query empty-result rate.
- Generation evaluation uses a deterministic frozen answerable/unanswerable sample, the same authorized retrieval path, and the production Candidate schema. It reports explicit abstention accuracy, required-fact coverage, generated citation precision/recall, citation syntax integrity, and provider/schema failures.
- The report records the exact generation model. A local evaluation model is evidence for the harness and that model only; it is not a production provider fallback or a DeepSeek quality claim.

## Acceptance criteria

- Cross-tenant documents are never returned even if IDs or text match.
- A member sees only directly granted current-version chunks.
- Deleting a grant prevents access on the immediately following query.
- Run citations exactly reference the authorized chunks supplied to the model.
- Empty or failed retrieval never sends unfiltered context to Python.
- A retrieved but insufficient context can produce an explicit `insufficient_evidence` candidate without an invented citation.
- Generated citation correctness is calculated from cited IDs mapped back to frozen gold chunk IDs; retrieval candidate precision is not mislabeled as generated citation correctness.

## Source evidence

- `platform/services/platform-api/internal/retrieval/retrieval.go`
- `platform/services/platform-api/internal/retrieval/retrieval_integration_test.go`
- `platform/services/platform-api/internal/migrations/sql/0005_acl_retrieval.sql`
- `platform/services/platform-api/internal/migrations/sql/0006_acl_tenant_constraints.sql`
- `platform/deploy/local/seed-local-knowledge.sql`

## Verification evidence

- PostgreSQL integration tests proved one allowed result while same-tenant ungranted, cross-tenant, and `restricted` documents remained absent.
- A mismatched tenant/member grant was rejected by a composite foreign key.
- Deleting the read grant made the next query return zero results.
- Real Run `fb7644b0-4746-45c9-a0d2-f6000bfac470` persisted citation `C1` with document/version/chunk/source provenance and OpenIM accepted reply `122174aef2c448a2a895c585a89be606`.
- Real zero-result Run `b380efd5-9734-4589-9ff3-a5ba745dc41a` called no model, persisted no citations, and returned an explicit no-evidence reply through OpenIM.
- Node2 Run `00c88cd5-ec6f-4a9e-aacf-6fc4a0faf79b` traversed real OpenIM ingress, authoritative ACL retrieval, DeepSeek, citation persistence, and OpenIM reply with one citation. After deleting the member grant, the immediately following Run `24e91528-c5d1-4298-ba7b-cdf1d9953eff` persisted zero citations and used the explicit no-evidence policy; the grant was then restored. Independent no-match Run `a54ee93a-d1c3-4db3-aec5-ddd9cf144053` also persisted zero citations without model egress.

## Open questions

- Group/department grants and relation inheritance require a separate authorization slice.
- Chinese production retrieval and vector/hybrid indexing require measured backend selection; lexical PostgreSQL is the single first implementation, not a fallback mode.
- The admitted vector-retrieval slice should reuse the locally deployed embedding model only after its endpoint, model revision, vector dimension, normalization, batching, and latency are recorded as an explicit index contract. Model unavailability must fail indexing/query work explicitly rather than silently switching embedding models or lexical semantics.
