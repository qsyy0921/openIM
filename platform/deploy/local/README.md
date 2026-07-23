# Local development runtime

This runtime keeps application development on the Windows host while PostgreSQL, Keycloak, and the pinned OpenIM Compose stack run in the dedicated `swe-docker` WSL distribution stored under `H:\wsl\swe-docker`.

## Boundaries

- `platform-api` runs natively from `platform/services/platform-api`.
- PostgreSQL, Keycloak, Prometheus, and Grafana are defined by `compose.yaml` and use named volumes inside the WSL Docker data root.
- OpenIM continues to use the pinned upstream Compose file under `deploy/node1-openim-docker-v3.8`.
- `openim-wsl.override.yaml` replaces only runtime data mounts with clean WSL named volumes. Existing benchmark/runtime data under the root `deploy/` directory is not modified.
- The override exposes a dedicated Kafka `HOST` listener on `127.0.0.1:19094`; OpenIM containers retain their own `kafka:9094` listener.
- The override replaces compile-heavy `mage check` health probes with the image's expected process-count checks so HDD-backed startup does not spawn concurrent Go builds.
- The local Keycloak password grant exists only for automated smoke verification. Interactive clients use Authorization Code with PKCE.
- No in-memory database, fake Token issuer, or alternate OpenIM implementation is available in production code.

## Prerequisites

1. `wsl -d swe-docker -- systemctl start docker`
2. Docker context data root verified as `/var/lib/docker` inside the `swe-docker` distribution.
3. Pinned PostgreSQL and Keycloak images from `compose.yaml` are present.
4. The OpenIM amd64 images from `deploy/openim-images-amd64.tar` are present.
5. Create ignored `platform/deploy/local/.env` from `.env.example`.
6. Set a local-only Grafana administrator password in that ignored `.env`; anonymous access and self-registration are disabled.

The Docker daemon pull proxy is maintained in `ops/swe-docker-proxy.conf`. Runtime containers receive no proxy environment because `/root/.docker/config.json` is synchronized from `ops/swe-docker-client-config.json`.

## Start dependencies

From WSL:

```bash
cd /mnt/e/development/OPENIM/platform/deploy/local
docker compose --env-file .env -f compose.yaml up -d
```

Prometheus is bound to `127.0.0.1:19091` and Grafana to `127.0.0.1:13001`. Prometheus scrapes the native Platform API on `18080` and Intelligence Worker on `18082` through `host.docker.internal`. These endpoints must remain loopback-only unless a separate authenticated monitoring ingress is designed.

The provisioned `OpenIM Agent Platform` dashboard covers HTTP rate/latency, durable queue state and age, MCP/A2A lifecycle, and enterprise RAG outcomes/latency. Six local alert rules cover target loss, stalled queues, failed or uncertain work, metric collection errors, degraded MCP servers, and elevated RAG errors.

Validate configuration before startup:

```powershell
$env:PLATFORM_LOCAL_POSTGRES_PASSWORD = "check-only"
$env:PLATFORM_LOCAL_KEYCLOAK_ADMIN_PASSWORD = "check-only"
$env:PLATFORM_LOCAL_GRAFANA_ADMIN_PASSWORD = "check-only"
docker compose -f compose.yaml config --quiet
docker run --rm --entrypoint=/bin/promtool `
  --mount "type=bind,src=$((Resolve-Path .\observability).Path),dst=/etc/prometheus,readonly" `
  prom/prometheus@sha256:c6b27ea434f8389bfe233fbc7be381cf50587c286e871bc842008f5a1b1908a7 `
  check config /etc/prometheus/prometheus.yml
```

For a no-model-call Worker scrape smoke on Windows:

```powershell
cd platform/services/intelligence-worker
./scripts/monitoring-smoke.ps1
```

Start the clean local OpenIM data plane:

```bash
cd /mnt/e/development/OPENIM/deploy/node1-openim-docker-v3.8
docker compose \
  --env-file .env \
  -f docker-compose.yaml \
  -f /mnt/e/development/OPENIM/platform/deploy/local/openim-wsl.override.yaml \
  up -d --pull never etcd kafka minio mongo redis openim-server openim-chat
```

Do not treat mapped ports as readiness. Wait for the OpenIM API to return a successful `/auth/get_admin_token` envelope.

## Initialize identity state

From `platform/services/platform-api` in PowerShell:

```powershell
$env:PLATFORM_DATABASE_URL = 'postgres://platform:<local-password>@127.0.0.1:15432/platform?sslmode=disable'
$env:PLATFORM_DEPENDENCY_TIMEOUT = '15s'
go run ./cmd/platform-migrate
```

Apply `seed-local-identity.sql` only to the local database. It must not enter the production migration chain.
After migration `0005`, apply `seed-local-knowledge.sql` only when running the ACL-RAG smoke test; it creates one versioned internal document and one direct-member read grant.

## Verified flow

```text
Keycloak ID Token
  -> platform-api OIDC verification
  -> active tenant/member/device lookup in PostgreSQL
  -> fenced IdentityLink provisioning
  -> OpenIM Admin Token held server-side
  -> OpenIM user registration / ownership check
  -> OpenIM User Token
  -> WebSocket handshake on port 12001
```

The real smoke run returned `ready`, issued a non-empty User Token, and opened a WebSocket. Tokens were not printed or persisted.

The durable ingress smoke sent a real OpenIM text message, consumed its `toRedis` protobuf with an independent group, committed one ingress plus one Outbox row, recovered from a transient Kafka leader error, and published the same `event_id` on attempt 2. The payload passed `contracts/events/im.message.accepted.v1.schema.json`.

The historical read-only Agent smoke used the then-current DeepSeek route. Current acceptance instead uses the fixed Terra/high Responses route: Node2 completed authorized/revoked/no-match OpenIM runs and one cited Telegram delivery while retaining candidate-only, ACL, citation, and no-fallback controls.

The ACL-RAG smoke used the local versioned knowledge fixture and direct-member grant. The Runtime persisted exact `C1` document/version/chunk provenance before sending the cited reply. Integration tests proved cross-tenant, ungranted, `restricted`, and freshly revoked content returned zero chunks. A separate no-match message produced the explicit no-evidence response with zero citations and no model call.

For the approved-action smoke, create a dedicated login role outside migrations and grant only `USAGE` on `action`, `collaboration`, `audit`, `agent`, and `identity`; `SELECT/UPDATE` on intents/executions and Runs; `SELECT/INSERT` on tickets; and `INSERT` plus sequence usage on action audit events. Pass its URL only as `ACTION_DATABASE_URL` to `action-executor`. Do not give that process OpenIM or model credentials.

The real smoke proposed one `create_ticket` Intent from an OpenIM message and confirmed zero tickets before approval. The requesting member approved the exact digest through the OIDC-protected API. The dedicated Executor created one ticket by stable idempotency key, read it back, and converged Execution, Intent, and Run to `succeeded`. Repeating approval returned the same Execution and left one approval and one ticket.

The generation path has no alternate provider. Start the Windows Worker through `openim-intelligence-local`, which loads the CLIProxyAPI key directly from the user's local configuration into process memory. Never place that key in Compose YAML, `.env` samples, source, logs, release bundles, databases, or model context.
