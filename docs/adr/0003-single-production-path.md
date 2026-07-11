# ADR-0003: Use one intended production path

- Status: Accepted
- Date: 2026-07-11

## Context

Temporary fallback providers, silent degradation, and placeholder success paths make an incomplete distributed system appear functional and obscure the semantics needed for testing and operations.

## Decision

Each production capability has one configured implementation path. Missing required dependencies, credentials, configuration, or contracts produce typed observable failures. Production code must not select fake providers, alternate stores, no-op success, or silent compatibility paths.

Retries, reconciliation, and rollback are allowed only when they preserve the same authoritative operation and are required by its contract. Deterministic fakes are confined to test-owned code and cannot be selected by production configuration.

## Consequences

- Local setup must run the real dependency needed by the tested slice.
- Partial environments fail clearly instead of appearing healthy.
- Optional provider portability is deferred until a separate Goal proves it is required.
