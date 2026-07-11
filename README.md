# OpenIM Intelligent Collaboration Platform

This repository is the integration and product-development root for an enterprise intelligent collaboration platform built on OpenIM.

OpenIM remains the communication plane responsible for messages, conversation sequence numbers, WebSocket connectivity, asynchronous transfer, push, and SDK synchronization. New platform code owns enterprise identity, collaboration facts, governed Agent runs, retrieval, and approved business actions.

## Current milestone

The local-first milestone is implemented and source/runtime verified. It includes enterprise OIDC-to-OpenIM identity exchange, durable OpenIM Kafka ingress and Outbox, fenced Agent Runs, ACL-constrained versioned knowledge retrieval, structured citations, digest-bound human approval, and one idempotent verified `create_ticket` action in a separate Executor process.

Development proceeds under one root Codex Goal as bounded vertical slices. A slice stops when its acceptance checks pass; optional adjacent features are not added. Production code uses one intended implementation path and does not add silent fallback behavior.

The single production model path uses DeepSeek Chat Completions with `deepseek-v4-pro`. Real local verification covers cited ACL-RAG answers and an approved `create_ticket` action; the test contract service remains isolated from production code.

## Source layout

- `platform/`: product services, workers, and client applications developed in this repository.
- `contracts/`: versioned HTTP, gRPC, and event contracts.
- `docs/architecture/`: stable system architecture.
- `docs/adr/`: consequential architecture decisions.
- `docs/sdd/`: unit-scoped software design documents synchronized with code.
- `deploy/`: tracked deployment definitions only; runtime data and image archives are ignored.
- `dependencies/openim.lock.yaml`: authoritative OpenIM repository and commit pins.

The local directories `open-im-server`, `chat`, `openim-sdk-core`, and `openim-docker-v3.8` are upstream working checkouts and are intentionally not tracked by this integration repository. Existing source-learning clones under `sources/` are also excluded.

## Development policy

1. Preserve the OpenIM message hot path unless an approved ADR explicitly changes it.
2. Keep enterprise business facts outside OpenIM message storage.
3. Never expose an OpenIM Admin Token to a client or model context.
4. Update the affected unit SDD in the same slice as its code.
5. Fail explicitly when required configuration or dependencies are unavailable.
6. Do not commit secrets, container volumes, generated binaries, raw benchmark logs, or downloaded repository mirrors.

The detailed target design is maintained in `docs/architecture/openim-intelligent-collaboration-platform-architecture.md`.

Local startup, evidence, and host migration constraints are maintained in `platform/deploy/local/README.md` and `docs/runbooks/migrate-local-milestone.md`.

## Baseline checks

Before committing a slice, run:

```powershell
Push-Location platform/services/platform-api
gofmt -l .
go vet ./...
go test ./...
Pop-Location

Push-Location platform/services/intelligence-worker
python -m pytest -q
Pop-Location

python ops/validate-repository.py
```

Database integration tests additionally require `PLATFORM_TEST_DATABASE_URL` pointing at the real local PostgreSQL instance. Runtime acceptance that needs OpenIM, Kafka, Keycloak, or the model provider follows `platform/deploy/local/README.md`; CI does not replace those checks with production-reachable fakes.
