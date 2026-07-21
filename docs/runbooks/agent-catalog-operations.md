# Agent Catalog Operations

## Boundary

Agent Catalog v1 exposes only authenticated read projections over HTTP. Publication and deployment mutation are host-operator operations through `agent-catalog-admin`; the command is not a network service and requires the PostgreSQL URL plus an existing tenant member as the audit actor.

The specification contains execution policy, never provider keys, database URLs, OpenIM credentials, or arbitrary endpoint URLs.

## Publish

Create a complete JSON specification and run the release binary with the exact next version number:

```bash
agent-catalog-admin \
  -operation publish \
  -tenant-id "$TENANT_ID" \
  -agent-id "$AGENT_ID" \
  -actor-member-id "$ACTOR_MEMBER_ID" \
  -expected-version 2 \
  -spec-file agent-v2.json
```

The command rejects unknown fields, unsupported runtime/model/action policy, invalid limits, archived Agents, a stale next-version expectation, and database errors. A successful row is immutable; correction requires a later version.

## Activate And Roll Back

Read the current deployment revision, then activate by exact version ID:

```bash
agent-catalog-admin \
  -operation activate \
  -tenant-id "$TENANT_ID" \
  -agent-id "$AGENT_ID" \
  -actor-member-id "$ACTOR_MEMBER_ID" \
  -version-id "$VERSION_ID" \
  -expected-revision "$CURRENT_REVISION"
```

The command validates the target schema and checksum before moving the production pointer. Concurrent operators cannot both succeed because revision is optimistic. Rollback is the same command with an older immutable version ID and the new current revision.

Activation affects only Runs inserted after its transaction commits. Existing queued, retrying, and completed Runs keep their stored Agent, version, deployment, trigger, and checksum.

## Node2 Acceptance

`ops/accept-node2-agent-catalog.sh` captures the current production version as its baseline, publishes or reuses a checksum-matched acceptance version, activates it, sends and completes one real OpenIM/ACL-RAG generation Run, then rolls production back to the captured baseline and completes a second real Run. It verifies the first Run remains pinned to its immutable acceptance version. A failure after activation triggers a best-effort rollback to the captured baseline; the script never assumes version 1 or 2 is production.

Before a production operation, retain a PostgreSQL backup and verify the exact release `SHA256SUMS`. Do not expose `PLATFORM_DATABASE_URL` to the browser or place it in shell history.

After deployment, verify the running executable through `/proc/<MainPID>/exe`; health metadata alone is insufficient because a systemd drop-in can override `ExecStart` while the environment reports the new release version. The Node2 deployment scripts fail when the live Platform API, Ingress, Agent Runtime, or Action Executor binary does not resolve to the requested release directory.
