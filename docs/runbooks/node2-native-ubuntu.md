# Node2 native Ubuntu deployment

Status: executable deployment path for Ubuntu 26.04 on `qsyy0921@172.31.50.2` or its Wi-Fi management address.

## Boundaries

- Runtime and data stay on the Ubuntu M.2 filesystem.
- `/home/qsyy0921/MFL/releases/<version>` is immutable release content.
- `/home/qsyy0921/MFL/deploy/node2-native` contains host-local Compose definitions and ignored environment files.
- `/etc/openim-platform` contains root-owned service configuration and the DeepSeek systemd credential.
- OpenIM remains the authoritative IM system. PostgreSQL stores platform identity, Agent, ACL, approval, and collaboration projections.
- Missing secrets or dependencies fail closed; no alternate model provider or fake success path is enabled.

## Public addresses

The Wi-Fi management address is `192.168.0.38`. The static wired address is `172.31.50.2` and is the required application origin for `.1` to `.2` development because the Web client loads the large OpenIM WASM runtime during startup. Set `PLATFORM_NODE2_PUBLIC_HOST=172.31.50.2` before starting Keycloak and building Web; the HTTPS origin, OIDC issuer, and Web build must use the same host. Keep Wi-Fi for management access only.

Published ports:

| Port | Service |
| --- | --- |
| `3000` | HTTP-to-HTTPS redirect only |
| `3443` | HTTPS Web, OIDC, Platform/OpenIM API proxy, and secure WebSocket |
| `12001` / `12002` | OpenIM WebSocket / API aliases |
| `12005` | OpenIM MinIO alias |
| `12008` / `12009` | OpenIM Chat / Admin API aliases |

Platform API `18080`, Keycloak `18081`, PostgreSQL `15432`, Kafka `19094`, and intelligence worker `18082` remain loopback-only. Browser traffic must use the `3443` ingress.

The installer creates a Node2-local lab CA and server certificate under root-owned `/etc/openim-platform/tls`. It exports only the public CA certificate to `$DEPLOY_ROOT/native-ubuntu/openim-node2-lab-ca.crt`. Import that public certificate into the controller user's trust store before opening the Web client:

```powershell
certutil.exe -user -addstore Root .\openim-node2-lab-ca.crt
```

Never copy `lab-ca.key` or `node2.key` from Node2. Set `OPENIM_PLATFORM_TLS_ROTATE=true` only for an explicit lab certificate rotation, then replace the controller's old CA trust entry.

Changing the canonical host does not require CA rotation when the existing server certificate already contains the new host in its SAN list. The installer verifies that coverage before updating the host marker and fails closed when the certificate does not cover the requested host.

## Release

From a clean Windows worktree:

```powershell
npm --prefix platform/apps/web ci
./ops/build-release.ps1 `
  -Version <version> `
  -WebPublicOrigin https://<public-host>:3443 `
  -WebOIDCAuthority https://<public-host>:3443/auth/realms/platform `
  -WebOpenIMWSURL wss://<public-host>:3443/openim-ws
```

Verify every entry in `SHA256SUMS` after transfer. Do not transfer `.env`, API keys, tokens, database volumes, or upstream source mirrors.

The release builder emits precompressed `.wasm.gz` files. Nginx serves them with `gzip_static`; the native installer fails if `/openIM.wasm` does not return `Content-Encoding: gzip` for a gzip-capable client. The signed-out entry point must remain independent of the OpenIM SDK so an OIDC redirect cannot abort an unnecessary WASM initialization.

## Infrastructure

Copy `platform/deploy/local` into `$DEPLOY_ROOT/platform`. Generate `platform/.env` on node2 with independent PostgreSQL and Keycloak passwords plus `PLATFORM_NODE2_PUBLIC_HOST`.

Apply `node2-native-ubuntu.override.yaml` when starting PostgreSQL and Keycloak. Keycloak listens only on Node2 loopback under `/auth`; Nginx terminates TLS and publishes its canonical issuer. Apply `openim-native-ubuntu.override.yaml` to the pinned OpenIM Compose project to publish the stable aliases and host-only Kafka listener.

Run migrations `0001` through `0028`, then apply `seed-node2-native-identity.sql` with the exact public issuer:

```bash
docker exec -i openim-platform-local-postgres-1 \
  psql -v ON_ERROR_STOP=1 \
  -v platform_oidc_issuer=https://<public-host>:3443/auth/realms/platform \
  -U platform -d platform \
  < platform/deploy/local/seed-node2-native-identity.sql
```

The enterprise dataset import is a development fixture, not a migration:

```bash
docker exec -i openim-platform-local-postgres-1 \
  psql -v ON_ERROR_STOP=1 -U platform -d platform \
  < datasets/enterprise-knowledge/v1/postgres_import.sql
```

## Services

Install the pinned local embedding runtime before deploying the latest Agent workers:

```bash
sudo bash "$DEPLOY_ROOT/ops/install-node2-ollama.sh" \
  /home/qsyy0921/MFL/staging/ollama-v0.32.1/ollama-linux-amd64.tar.zst \
  qsyy0921 \
  /home/qsyy0921/MFL/ollama
```

The installer verifies the pinned archive digest, runs Ollama only on
`127.0.0.1:11434`, stores model blobs on the Node2 NVMe-backed MFL path, pulls
`qwen3-embedding:4b`, and rejects the deployment unless a real embedding has
dimension `2560`.

The native installer verifies the release, creates or verifies TLS material, configures the existing Keycloak client without deleting its volume, runs migrations and fixtures, and invokes the generalized deployment scripts. Nginx routes the exact `/auth/callback` path to the Web SPA before forwarding the remaining `/auth/` namespace to Keycloak:

```bash
sudo bash "$DEPLOY_ROOT/ops/install-node2-native-runtime.sh" \
  "$DEPLOY_ROOT" "$RELEASE_ROOT" qsyy0921 <public-host>
```

Native services bind loopback, so start monitoring with the Node2 host-network override rather than the portable bridge-mode Compose file:

```bash
docker compose \
  -f "$DEPLOY_ROOT/platform/compose.yaml" \
  -f "$DEPLOY_ROOT/platform/compose.node2-observability.yaml" \
  --env-file "$DEPLOY_ROOT/platform/.env" \
  up -d prometheus grafana

bash "$DEPLOY_ROOT/ops/accept-node2-observability.sh"
```

If Docker still points at the retired loopback proxy or NetworkManager accepts only unusable public resolvers, use the bounded recovery scripts after inspecting their preconditions:

```bash
sudo bash "$DEPLOY_ROOT/ops/disable-stale-node2-docker-proxy.sh"
sudo bash "$DEPLOY_ROOT/ops/configure-node2-dns.sh"
```

Both scripts create a backup under `/home/qsyy0921/MFL/staging` and fail when the observed host state does not match the expected stale configuration.

The installer creates hardened systemd units for Platform API, OpenIM ingress, Intelligence Worker, Agent Runtime, Action Executor, Telegram ingress, channel delivery, Memory extraction/projection, and proactive dispatch. It verifies that each running Go unit resolves to the selected immutable release directory.

Provision the DeepSeek key only through standard input into `/etc/openim-platform/credentials/deepseek-api-key`, owned by `root:root` with mode `0400`. Provision the Telegram Bot Token through standard input to `ops/install-node2-telegram-credential.sh`, which validates `getMe` before installing `/etc/openim-platform/credentials/telegram-bot-token` with the same ownership and mode. Do not place either credential in shell arguments, environment files, Compose YAML, release bundles, screenshots, or logs.

## Acceptance

1. Check OpenIM Server and Chat health plus a real admin-token envelope.
2. Check PostgreSQL, Keycloak, Nginx, Platform API, OpenIM/Telegram ingress, Intelligence Worker, Agent Runtime, channel Delivery, Memory workers, Proactive Runtime, and Action Executor.
3. Complete a real OIDC PKCE login and OpenIM WebSocket connection.
4. Send and receive real single/group messages and media.
5. Run an authorized cited enterprise-knowledge query and a no-evidence query.
6. Approve one exact-digest action and verify one idempotent business effect.
7. Restart the host and repeat health plus one real Agent turn.
