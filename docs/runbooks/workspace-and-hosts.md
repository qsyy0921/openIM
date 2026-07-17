# Workspace and host baseline

Status: native Ubuntu node2 baseline verified on 2026-07-17.

## Repository boundary

`E:\development\OPENIM` is the integration repository root. Upstream OpenIM checkouts remain independent local Git repositories and are pinned by `dependencies/openim.lock.yaml`. The integration repository must not absorb their `.git` histories, source mirrors, runtime databases, image archives, or raw benchmark output.

The existing `openim-sdk-core` checkout contains user-owned benchmark changes and generated test artifacts. They must be preserved until a dedicated benchmark-tooling slice extracts and verifies them.

## Hosts

| Address | Host | Account | Operating system | Intended role |
| --- | --- | --- | --- | --- |
| `172.31.50.1` | `DESKTOP-9RCJP4L` | `10495` | Windows | primary development, control, and local tests |
| `172.31.50.2` / `192.168.0.38` | `qsyy0921-Default-string` | `qsyy0921` | Ubuntu 26.04, 128 GB RAM | integration, migration acceptance, and soak tests |

Public-key SSH is available from `.1` to the native Ubuntu account. The wired service address remains `172.31.50.2/24`; `192.168.0.38/24` is the currently reachable Wi-Fi management address. Service configuration must bind both interfaces but use one explicit OIDC issuer per deployment.

Mac `.3` and Ubuntu `.5` are outside the authoritative development, deployment, and acceptance scope. Host-specific scripts retained from the older lab topology remain local-only and are not part of the integration repository baseline.

## Native Ubuntu node `.2` storage invariant

The operating system, release bundles, Docker Engine image layers, build cache, and container data on `.2` use the Ubuntu M.2 system disk. Before deployment verify:

1. block device and filesystem;
2. SMART/Linux health status;
3. free capacity;
4. effective Docker data root and volume paths.

The verified filesystem is `/dev/nvme0n1p3` mounted at `/` on the 500 GB Kingston M.2 SSD. Docker reports `/var/lib/docker` on the same filesystem. Project releases and host-local deployment data belong under `/home/qsyy0921/MFL`; service configuration belongs under `/etc/openim-platform`.

The previous Windows/WSL2 deployment is historical and is not an active fallback. Native Ubuntu uses systemd for platform processes and Docker restart policies for OpenIM, PostgreSQL, and Keycloak.

## Local-first migration rule

Develop and verify on `.1` first. Move checksum-verified release bundles, pinned images, versioned configuration, database migrations, and explicit seed definitions to `.2`; do not copy local development volumes as a deployment mechanism. Static Linux artifacts remain part of release portability checks. `.5` is not a target.

Local development on `.1` may still use its dedicated WSL runtime. Node2 deployment follows `docs/runbooks/node2-native-ubuntu.md`.
