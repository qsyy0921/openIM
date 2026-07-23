# Migrate the local milestone

Status: Milestone 0 and the complete node2 migration acceptance are complete: WSL/Docker, identity/session, WebSocket, durable ingress, real DeepSeek, ACL-RAG, restricted Action Executor, approval, idempotency, and UNKNOWN reconciliation all passed.

## Transfer boundary

Transfer source revisions, pinned images, configuration templates, migrations, and explicit seed/bootstrap procedures. Never transfer WSL VHDX files, Docker volumes, PostgreSQL data directories, Kafka logs, local `.env` files, API keys, or benchmark output.

Required application artifacts:

- Go commands under `platform/services/platform-api/cmd/`;
- Python package under `platform/services/intelligence-worker/`;
- migrations `0001` through `0007` embedded in `platform-migrate`;
- contracts and SDDs under `contracts/` and `docs/sdd/`;
- pinned OpenIM revisions in `dependencies/openim.lock.yaml`.

Generate a local release bundle with:

```powershell
./ops/build-release.ps1 -Version 0.1.0-dev
```

The ignored `.runtime/release/<version>/` output contains Windows amd64 binaries, static Linux amd64 binaries, the Python wheel, contracts, the OpenIM lock, metadata, and `SHA256SUMS`. A bundle built from an uncommitted tree records `working_tree_clean=false` and is not a publishable release.

Local verification produced 5 Windows binaries, 5 static Linux amd64 ELF binaries, an installable Python wheel, contracts, lock/metadata files, and a checksum manifest covering all 15 non-manifest files with zero mismatches. Each Linux process executed under WSL and failed non-zero when required configuration was absent, as designed.

## Configuration boundary

Provision secrets independently on each host. The Agent Runtime receives PostgreSQL, Kafka, intelligence-worker, and OpenIM Adapter configuration. The intelligence worker receives only its scoped model credential. The Action Executor receives only `ACTION_DATABASE_URL` plus timing configuration; it receives no OpenIM or model credential.

Create the Action Executor database role outside the migration chain. Grant only schema usage; intent/execution reads and updates; Run state update; ticket select/insert; and action-audit insert/sequence usage. Do not grant ticket UPDATE merely to support idempotency: implementation uses `INSERT ... ON CONFLICT DO NOTHING` followed by authoritative read-back.

## `.2` Windows gate

Do not initialize Docker or WSL until the approximately 466 GB healthy mechanical disk is mapped to its drive letter. Place the imported WSL virtual disk, Docker data root, images, cache, and volumes there. Use `.2` for functional integration and soak tests, not storage-latency or throughput claims.

Live recheck on 2026-07-11 completed the host gate. The `.2` console verified ED25519 fingerprint `SHA256:DVI6iwrMeNbMsBjPp7Dol0Dlgcf9k1KpDCD2HKZAj3s`; `.1 -> .2` and `.2 -> .1` then passed strict `BatchMode=yes` public-key authentication. Aliases are `openim-node2` on `.1` and `openim-node1` on `.2`.

Storage inspection mapped `E:` to healthy HDD `WDC WD5000AAKX-603CA0` with zero reported read and uncorrected-read errors and SMART `PredictFailure=False`. `D:` was rejected because its reliability counters reported 93 total and 92 uncorrected read errors. `OpenIM-Ubuntu` is a WSL2 distribution whose registry `BasePath` and `ext4.vhdx` are under `E:\MFL\wsl\openim-ubuntu`.

Docker Engine 29.6.1, containerd 2.2.6, Buildx 0.35.0, and Compose 5.3.1 were installed inside that distribution. Docker started successfully after selecting the supported `iptables-legacy` backend and reported `DockerRootDir=/var/lib/docker`; this path is inside the `E:`-backed VHDX. Packages are held to prevent unattended runtime drift.

The portable Clash core under `E:\Clash.for.Windows-0.20.39-win` is managed by the `OpenIM-Clash-Core` startup task. Proxy ports `7890` through `7893` and controller `9090` are loopback-only. A firewall-scoped bridge exposes port `17893` only on the current WSL virtual adapter and only to its connected subnet. `ops/start-node2-runtime.ps1` dynamically rebuilds that bridge after WSL subnet changes.

Modern WSL 2.7.10 was downloaded from the official Microsoft GitHub release, matched SHA-256 `1a62f90a43c03cc5bda47dfd0b6faf496ac70fd4389190518120a4f84fc895cf`, passed Microsoft Authenticode validation, and installed with `/norestart` and MSI exit code 0. After the explicitly approved reboot, its executable reports WSL 2.7.10 and kernel 6.18.33.2-2; `WslService` is running and the legacy `LxssManager` is stopped.

The `OpenIM-Node2-Runtime` scheduled task was then tested from a terminated `OpenIM-Ubuntu` distribution. It rebuilt the WSL-scoped proxy bridge, restarted Docker with the proxy environment loaded into `dockerd`, completed recovery, and entered its WSL keepalive state. A direct Docker Hub authentication probe returned HTTP 200, `busybox:1.37.0` was pulled through the same official endpoint, and a container printed `scheduled-runtime-container-ok`. The clean WSL start took about 96 seconds because the imported Ubuntu image completed its systemd/cloud-init boot before Docker's queued restart; consumers must wait for the runtime log entry `node2 runtime recovery completed` instead of assuming immediate readiness.

The startup script now republishes only ports `12001`, `12002`, `12005`, `12008`, `12009`, `18080`, and `18081` on `172.31.50.2`, with the Windows firewall source restricted to controller `172.31.50.1`. PostgreSQL `15432` and Kafka `19094` remain WSL-loopback-only. Keycloak advertises the stable issuer `http://172.31.50.2:18081/realms/platform`; one WSL-local iptables OUTPUT rule redirects only that exact issuer address and port to the local Keycloak listener so the server and `.1` clients validate the same issuer without DNS or Windows hairpin behavior.

The checksum-matched `goal-final` development bundle and pinned OpenIM amd64 image archive were transferred to `E:\MFL\staging`, then extracted under `/home/ubuntu/MFL` inside the `E:`-backed WSL virtual disk. Host secrets were generated on `.2`; neither the local `.env` files nor API tokens were transferred. Because a tar file created on Windows did not preserve Linux execute bits, the five verified ELF commands must receive mode `0755` after extraction. This bundle records `working_tree_clean=false` and is valid for migration testing only, not publication.

Milestone 0 subsequently produced the clean immutable release `d663256` from source commit `d663256d7e2ab43f3da6f0208c724db40b2a2fc5`. Its archive SHA-256 is `7BA18AA25F80D9B44FB98D0D79FD0A1C380BBEB6E3381B53977987A129D6286A`; the local and node2 hashes matched before extraction to `/home/ubuntu/MFL/releases/d663256`. The platform health endpoint now reports the release directory name rather than a hard-coded development label.

PostgreSQL 17.10 and Keycloak 26.7.0 were pulled by digest through the configured official endpoints. OpenIM Server, Chat, MongoDB, Redis, Kafka, etcd, and MinIO were loaded from the pinned archive and started with clean named volumes. PostgreSQL, Keycloak, OpenIM Server, and Chat passed health checks; a real `/auth/get_admin_token` call returned a non-empty token and valid expiry without logging the token.

Migrations `0001` through `0007` and the local identity/knowledge fixtures were applied. `openim-platform-api` and `openim-platform-ingress` run as hardened systemd services using `/etc/openim-platform/platform.env`; the host-local file is `0640` and is not part of the release. From `.1`, an unregistered device was rejected with `MEMBER_OR_DEVICE_FORBIDDEN`, the registered `local-browser` device received an OpenIM User Token, and a real WebSocket handshake opened on `172.31.50.2:12001`. A separate OpenIM message was accepted, consumed from `toRedis`, persisted once in `integration.ingress_messages`, and its matching `integration.outbox_events` row reached `published`.

The clean release wheel is installed in `/home/ubuntu/MFL/venvs/intelligence-worker`. `openim-action-executor.service` runs as the dedicated PostgreSQL login `platform_action_executor`; the role is non-superuser, has no role/database creation, replication, or RLS bypass capability, and receives only the exact table and sequence grants listed above. Direct checks confirmed that ticket update/delete, member reads, and approval reads are denied. The worker and Agent services remain disabled until a credential is installed, so missing model configuration cannot degrade into a false-success path.

The current generation key stays inside the Windows CLIProxyAPI configuration and Worker process. Start the Worker with `openim-intelligence-local`; Node2 reaches only the Worker's loopback port through `openim-intelligence-tunnel.service`. Do not copy the generation key or expose CLIProxyAPI port `8317` to Node2, the LAN, or the Internet.

Remaining migration work:

None for the bounded node2 migration slice. By explicit owner decision, the existing DeepSeek credential was reused instead of rotated; it was provisioned through the clipboard-to-standard-input mechanism and completed a real model call. Rotation remains an operational security recommendation, not a blocker for this accepted development environment.

## Acceptance order

1. Required configuration missing: every process exits non-zero. Accepted locally; node2 service units use explicit required environment files.
2. OIDC member/device produces an OpenIM session and real WebSocket handshake. Accepted on node2 from `.1`.
3. OpenIM `toRedis` event becomes one ingress row and one versioned event. Accepted on node2.
4. Cross-tenant, ungranted, restricted, and revoked documents produce zero RAG context. Accepted through integration checks plus a live node2 immediate-revocation Run.
5. Valid evidence produces persisted document/version/chunk citations. Accepted on node2 with one real citation.
6. No ticket exists before exact-digest approval. Accepted on node2.
7. Duplicate approval/execution produces one ticket and one business effect. Accepted on node2.
8. Read-back mismatch never succeeds; UNKNOWN reconciles by the same idempotency key. Existing and absent outcomes both accepted on node2.
9. The fixed `gpt-5.6-terra` Responses route with high reasoning effort completes a real cited call and the full OpenIM/ACL-RAG path through the loopback tunnel. Node2 accepted the immutable Terra release, authorized/revoked/no-match OpenIM paths, and the cited Telegram delivery without changing model, provider, or endpoint.

The current deployment scope is `.1` and `.2` only. Linux artifacts are still built and checksum-verified for portability, but no `.5` host deployment is required by this milestone.

## Rollback

Stop platform API/Runtime/Executor independently; OpenIM communication remains available. Roll back application binaries/configuration to the prior immutable version. Database migrations in this milestone are forward-only; restore from a tested backup for destructive rollback rather than editing migration history or copying local volumes.
