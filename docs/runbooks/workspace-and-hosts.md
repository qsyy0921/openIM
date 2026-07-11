# Workspace and host baseline

Status: verified local baseline on 2026-07-11.

## Repository boundary

`E:\development\OPENIM` is the integration repository root. Upstream OpenIM checkouts remain independent local Git repositories and are pinned by `dependencies/openim.lock.yaml`. The integration repository must not absorb their `.git` histories, source mirrors, runtime databases, image archives, or raw benchmark output.

The existing `openim-sdk-core` checkout contains user-owned benchmark changes and generated test artifacts. They must be preserved until a dedicated benchmark-tooling slice extracts and verifies them.

## Hosts

| Address | Host | Account | Operating system | Intended role |
| --- | --- | --- | --- | --- |
| `172.31.50.1` | `DESKTOP-9RCJP4L` | `10495` | Windows | primary development, control, and local tests |
| `172.31.50.2` | `DESKTOP-G6JLGE5` | `qsyy0921` | Windows, 128 GB RAM, WSL2 | integration, migration acceptance, and soak tests |

Strict host-key-checked public-key SSH is configured in both directions. `.1` uses alias `openim-node2`; `.2` uses alias `openim-node1`. The verified `.2` ED25519 fingerprint is `SHA256:DVI6iwrMeNbMsBjPp7Dol0Dlgcf9k1KpDCD2HKZAj3s`.

Mac `.3` and Ubuntu `.5` are outside the authoritative development, deployment, and acceptance scope. Host-specific scripts retained from the older lab topology remain local-only and are not part of the integration repository baseline.

## Windows node `.2` storage invariant

The dedicated WSL virtual disk, Docker Engine image layers, build cache, and container data on `.2` may only use the large healthy mechanical disk selected after checking:

1. drive letter and filesystem;
2. SMART/Windows health status;
3. free capacity;
4. WSL distribution VHDX location;
5. effective Docker data root and volume paths.

No deployment may default to the system drive. Because the selected device is mechanical storage, `.2` results are valid for functional integration and soak behavior but not for database latency or throughput claims.

Live inspection on 2026-07-11 mapped `E:` to healthy HDD `WDC WD5000AAKX-603CA0`; `D:` was rejected after reporting uncorrected read errors. The dedicated `OpenIM-Ubuntu` WSL2 virtual disk is stored under `E:\MFL\wsl\openim-ubuntu`, and Docker reports `/var/lib/docker` inside that `E:`-backed virtual disk. Runtime bundles and deployment definitions are kept under `/home/ubuntu/MFL` in the same virtual disk.

Modern WSL 2.7.10, kernel 6.18.33.2-2, Docker Engine 29.6.1, containerd 2.2.6, Buildx 0.35.0, and Compose 5.3.1 are installed. The `OpenIM-Node2-Runtime` scheduled task rebuilds the WSL proxy and published-port mappings after address changes, restarts Docker with its explicit proxy environment, and keeps the distribution active. Consumers wait for `node2 runtime recovery completed` in `E:\MFL\logs\start-node2-runtime.log` before assuming readiness.

## Local-first migration rule

Develop and verify on `.1` first. Move checksum-verified release bundles, pinned images, versioned configuration, database migrations, and explicit seed definitions to `.2`; do not copy local development volumes as a deployment mechanism. Static Linux artifacts remain part of release portability checks. `.5` is not a target.

Local container development uses the dedicated `swe-docker` WSL distribution under `H:\wsl\swe-docker`, with Docker data root `/var/lib/docker`. See `platform/deploy/local/README.md`.
