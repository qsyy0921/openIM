# ADR-0004: Pin upstream OpenIM without importing its repositories

- Status: Accepted
- Date: 2026-07-11

## Context

The workspace contains independent OpenIM repositories, many source-learning clones, runtime databases, image archives, and user-owned benchmark modifications. Importing them into the product repository would mix histories and generated data.

## Decision

The product repository tracks exact upstream URLs, tags, and commits in `dependencies/openim.lock.yaml`. Upstream working checkouts remain untracked local directories. Product code references OpenIM only through documented adapter contracts.

If a production patch to an upstream repository becomes necessary, create an explicitly owned fork and patch branch in a separate Goal; do not hide the patch in an untracked checkout.

## Consequences

- The integration repository remains small and reviewable.
- Bootstrap and CI must verify dependency commits before building integration tests.
- Existing dirty SDK benchmark work remains preserved locally until extracted by a dedicated slice.
