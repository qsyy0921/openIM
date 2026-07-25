# ADR-0012: Fuse retrieval and reranker ranks

- Status: Proposed
- Date: 2026-07-26

## Context

The immutable `document-title-content-v1` regression processed 158 previously
failed answerable cases. Recall@5 improved to `0.582278`, but remained below the
unchanged `0.60` gate; Recall@10 was `0.797468`.

A separate read-only Node2 diagnostic completed all 158 cases without database
writes or content fields. Its report SHA-256 is
`a7b330024e4522a45e4c4b99eefe2cf30a2f4fc08f6ae00904bb9ca1ca550e1d`.
The report attributes the remaining ranking loss as follows:

- 4 cases have no expected Chunk in the bounded candidate pool;
- 23 cases have no expected Chunk in fusion Top-10, but the reranker rescues 13;
- 131 cases have expected evidence in fusion Top-10;
- the reranker then pushes 18 of those 131 cases below Top-10;
- final reranker ranks contain 92 Top-5, 34 ranks 6-10, and 32 Top-10 misses.

The fixed cross-encoder adds useful signal, but treating its raw score as the
sole final ordering discards the independent lexical/dense evidence already
captured by RRF. Raw cross-encoder scores are not calibrated against RRF scores,
so directly adding the two score values would create a model- and
distribution-specific weight.

## Proposed decision

1. Keep ACL-first lexical Top-32, dense Top-32, initial RRF, the 32-candidate
   bound, the fixed BGE reranker, and every release threshold unchanged.
2. Require a complete, valid reranker response exactly as before. A reranker
   error still fails the request; initial RRF is not a fallback.
3. Convert the initial fusion order and raw reranker order to ranks.
4. Compute the final ordering with equal-weight reciprocal-rank fusion:

   `1 / (60 + fusion_rank) + 1 / (60 + reranker_rank)`.

5. Resolve equal combined scores by initial fusion rank, then raw reranker rank,
   then Chunk ID. Preserve the raw reranker score on Evidence for audit and
   observability.
6. Do not change candidate generation, authorization, model inputs, Citation
   validation, generation, or channel delivery.

## Alternatives considered

### Use only the reranker score

Rejected by the measured diagnostic: it demoted 18 expected fusion Top-10
results below Top-10 and failed the bounded Recall@5 gate.

### Add normalized model and RRF scores

Rejected because the normalization and weight would be fitted to one evaluation
distribution and raw cross-encoder scores have no stable calibration contract.

### Preserve a fixed number of RRF anchors

Rejected because interleaving fixed prefixes creates discontinuities around the
cutoff and gives no common ordering to all 32 candidates.

### Increase candidate count or change a model

Rejected for this remediation. It changes CPU cost or confounds the measured
ranking-stage diagnosis.

## Consequences

- Final ordering uses both independent retriever evidence and the required
  cross-encoder judgment.
- Ordering remains bounded, deterministic, scale-independent, and explainable.
- Reranker latency and failure behavior do not change.
- A new immutable bounded regression is required. This proposal does not claim
  improvement until that regression passes the original thresholds.

## Verification required before acceptance

- Unit tests prove equal-weight rank fusion, deterministic ties, raw score
  preservation, and fail-closed response validation.
- Full Go tests, vet, repository validation, SDD validation, and diff checks
  pass from a clean worktree.
- Two clean Linux amd64 builds produce the same digest.
- A new independent Node2 root and one-shot service run the same immutable
  158-case regression exactly once.
- Only a regression that passes the original quality, ACL, stale-version,
  provenance, and checksum gates may unlock a new full 1,120-case evaluation.
