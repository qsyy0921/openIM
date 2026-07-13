---
unit: platform-api
status: verified
depends_on:
  - adr-0001
  - adr-0002
  - adr-0003
---

# Platform API

## Scope

Own the first public HTTP boundary and host the initial modular Go implementation for identity, OpenIM integration, collaboration, and governed Agent control.

## Responsibilities and non-goals

The unit owns HTTP routing, request identity propagation, typed errors, service health, module composition, authenticated intent approval, and the read-only tenant Agent Catalog projection. It does not own OpenIM message delivery, model inference, write execution, Catalog mutation, or a generic plugin framework.

## Contracts and dependencies

- `contracts/openapi/platform-v1.yaml`
- ADR-0001 through ADR-0003
- PostgreSQL for authoritative platform state once stateful modules are enabled
- OpenIM only through the `openim-adapter` module
- Authenticated `GET /v1/agents` returning only active tenant definitions and their current production version

## Invariants

- A successful health response proves process readiness only, not downstream health.
- Authenticated routes use verified enterprise identity; request bodies cannot select a tenant or subject identity.
- Missing required configuration prevents startup.
- Internal modules do not access another module's tables directly.
- Intent approval resolves the active member/device from the bearer identity and binds the exact payload digest; request bodies cannot choose an approver or tenant.
- Catalog reads resolve tenant/member/device from the bearer identity and expose no mutation endpoint.

## Runtime flow

1. Load and validate configuration.
2. Construct required module dependencies.
3. Register versioned HTTP routes.
4. Start the HTTP server and expose readiness.
5. On shutdown, stop accepting requests and drain within the configured deadline.

## Data ownership and state

The process itself owns no business tables. Hosted modules own separate PostgreSQL schemas and transactions. Process configuration is immutable after startup.

## Failure handling

Invalid configuration or dependency construction fails startup. Request validation, authorization, conflict, and dependency failures return typed non-success responses. The service does not substitute an in-memory store or no-op module.

## Security

Public authenticated routes require OIDC bearer tokens. Secrets are read from process configuration or an external secret mechanism and are never returned by health endpoints or logs.

## Observability

The service emits structured process lifecycle logs and request logs with correlation ID, method, route, status, and duration. Session failures include an internal error without bearer, Admin, or User Token values. Health endpoints exclude tenant and credential data. OpenTelemetry propagation enters with the next admitted cross-process observability slice.

## Acceptance criteria

- The service builds and its unit tests pass.
- `/healthz` matches the OpenAPI response schema.
- Missing required configuration causes a non-zero process exit.
- Graceful shutdown is covered by a test or deterministic process-level check.

## Source evidence

- `platform/services/platform-api/cmd/platform-api/main.go`
- `platform/services/platform-api/internal/app/app.go`
- `platform/services/platform-api/internal/app/app_test.go`
- `platform/services/platform-api/internal/config/config.go`
- `platform/services/platform-api/internal/config/config_test.go`
- `platform/services/platform-api/internal/httpserver/handler.go`
- `platform/services/platform-api/internal/httpserver/handler_test.go`
- `contracts/openapi/platform-v1.yaml`
- `platform/services/platform-api/internal/action/service.go`
- `platform/services/platform-api/internal/agent/catalog_service.go`
- `platform/services/platform-api/internal/agent/catalog_store.go`
- `docs/architecture/openim-intelligent-collaboration-platform-architecture.md`

## Verification evidence

- `go test ./...`: passed on Windows.
- `go vet ./...`: passed on Windows.
- `go build -o platform-api.exe ./cmd/platform-api`: passed on Windows.
- Process smoke test: a configured binary returned `{"status":"ready","service":"platform-api","version":"dev-smoke"}` from `/healthz` and was then stopped.
- The real approval route authenticated a local Keycloak ID Token, resolved the seeded active device, and queued one digest-bound execution; duplicate approval returned the same execution.
- Migrations `0001` through `0008` applied successfully; Node2 Catalog migration backfilled every historical Run with non-null version provenance, and the authenticated `GET /v1/agents` returned the active tenant v1 projection.
- `go test -race ./...`: not executed because the current Windows Go environment has `CGO_ENABLED=0`; this is a recorded validation gap, not a passing check.

## Open questions

None for the initial health and composition slice.
