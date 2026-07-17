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

The current reachable management address is `192.168.0.38`. The static wired address is `172.31.50.2`. Select one as `PLATFORM_NODE2_PUBLIC_HOST` before starting Keycloak and building Web; the OIDC issuer and Web build must use the same host.

Published ports:

| Port | Service |
| --- | --- |
| `3000` | enterprise collaboration Web |
| `18080` | Platform API |
| `18081` | Keycloak |
| `12001` / `12002` | OpenIM WebSocket / API aliases |
| `12005` | OpenIM MinIO alias |
| `12008` / `12009` | OpenIM Chat / Admin API aliases |

PostgreSQL `15432`, Kafka `19094`, and intelligence worker `18082` remain loopback-only.

## Release

From a clean Windows worktree:

```powershell
npm --prefix platform/apps/web ci
./ops/build-release.ps1 `
  -Version <version> `
  -WebPublicOrigin http://<public-host>:3000 `
  -WebOIDCAuthority http://<public-host>:18081/realms/platform `
  -WebOpenIMWSURL ws://<public-host>:12001
```

Verify every entry in `SHA256SUMS` after transfer. Do not transfer `.env`, API keys, tokens, database volumes, or upstream source mirrors.

## Infrastructure

Copy `platform/deploy/local` into `$DEPLOY_ROOT/platform`. Generate `platform/.env` on node2 with independent PostgreSQL and Keycloak passwords plus `PLATFORM_NODE2_PUBLIC_HOST`.

Apply `node2-native-ubuntu.override.yaml` when starting PostgreSQL and Keycloak. Apply `openim-native-ubuntu.override.yaml` to the pinned OpenIM Compose project to publish the stable aliases and host-only Kafka listener.

Run migrations `0001` through `0008`, then apply the explicit local identity fixture. The enterprise dataset import is a development fixture, not a migration:

```bash
docker exec -i openim-platform-local-postgres-1 \
  psql -v ON_ERROR_STOP=1 -U platform -d platform \
  < datasets/enterprise-knowledge/v1/postgres_import.sql
```

## Services

Run the generalized deployment scripts as root with:

```bash
export OPENIM_PLATFORM_RUNTIME_USER=qsyy0921
export OPENIM_PLATFORM_RUNTIME_GROUP=qsyy0921
export OPENIM_PLATFORM_MFL_ROOT=/home/qsyy0921/MFL
export OPENIM_PLATFORM_OIDC_ISSUER=http://<public-host>:18081/realms/platform
export OPENIM_PLATFORM_OPENIM_WS_URL=ws://<public-host>:12001
export OPENIM_INTELLIGENCE_INSTALL_DEPENDENCIES=true

bash ops/deploy-node2-platform-runtime.sh "$DEPLOY_ROOT" "$RELEASE_ROOT"
bash ops/deploy-node2-agent-runtime.sh "$DEPLOY_ROOT" "$RELEASE_ROOT"
bash ops/deploy-node2-native-web.sh \
  "$RELEASE_ROOT" \
  "$DEPLOY_ROOT/native-ubuntu/nginx-openim-platform.conf"
```

Provision the DeepSeek key only through standard input into `/etc/openim-platform/credentials/deepseek-api-key`, owned by `root:root` with mode `0400`. Do not place it in shell arguments, environment files, Compose YAML, release bundles, screenshots, or logs.

## Acceptance

1. Check OpenIM Server and Chat health plus a real admin-token envelope.
2. Check PostgreSQL, Keycloak, Nginx, Platform API, Ingress, Intelligence Worker, Agent Runtime, and Action Executor.
3. Complete a real OIDC PKCE login and OpenIM WebSocket connection.
4. Send and receive real single/group messages and media.
5. Run an authorized cited enterprise-knowledge query and a no-evidence query.
6. Approve one exact-digest action and verify one idempotent business effect.
7. Restart the host and repeat health plus one real Agent turn.
