---
unit: openim-adapter
status: verified
depends_on:
  - platform-api
  - adr-0001
  - adr-0003
---

# OpenIM adapter

## Scope

Own all platform-to-OpenIM API calls and normalize accepted OpenIM messages into durable platform events.

## Responsibilities and non-goals

The current unit owns Admin Token acquisition and caching, user/Bot registration, User Token requests, text-message submission, direct `toRedis` consumption, ingress deduplication, and Outbox publication. It does not own OpenIM delivery semantics, enterprise authorization, Agent Run state, or direct writes to other modules' schemas.

## Contracts and dependencies

- OpenIM HTTP APIs pinned by `dependencies/openim.lock.yaml`
- `contracts/events/im.message.accepted.v1.schema.json`
- PostgreSQL `integration` schema
- OpenIM `toRedis` protobuf contract pinned to `github.com/openimsdk/protocol v0.0.73-alpha.12`
- Kafka consumer group `platform-ingress-v1`, independent from OpenIM msgtransfer group `redis`

## Invariants

- Other platform modules do not call OpenIM internal RPC or HTTP APIs directly.
- A source Kafka offset is committed only after ingress and Outbox are committed, or after a durable rejection is recorded.
- Source message identity has a unique database constraint.
- Event success means OpenIM message acceptance, not MongoDB persistence or user delivery.
- OpenIM after-send Webhooks are not a production ingress path because their current implementation uses a non-durable in-process memory queue.

## Runtime flow

1. Consume and decode `sdkws.MsgData` from OpenIM `toRedis` using an independent consumer group.
2. Resolve the sender's authoritative tenant through a ready IdentityLink or tenant Bot mapping and normalize message semantics.
3. In one PostgreSQL transaction, insert the unique ingress record and Outbox event.
4. Commit the OpenIM Kafka offset only after the transaction; malformed or unmapped records require a durable rejection first.
5. Claim Outbox rows with a fenced lease, publish the versioned event, and retain publish evidence.

## Data ownership and state

The adapter owns OpenIM identity mappings needed for integration, ingress deduplication records, and adapter Outbox state in the `integration` schema. OpenIM remains authoritative for users, messages, sequence numbers, and tokens.

## Failure handling

Invalid protobuf or unsupported records are durably rejected before their offset is committed. Database failures leave the source offset uncommitted. Publish failures return the Outbox row to `pending` with bounded backoff; a publish-success/mark-failure may duplicate the same `event_id`, so downstream consumers deduplicate by event ID. The adapter does not acknowledge into an in-memory queue and does not publish an uncommitted event.

## Security

Admin credentials are process secrets available only to the adapter. Kafka TLS is explicitly configured and supports a CA plus optional client certificate; plaintext is allowed only by explicit local configuration. Message content is classified before downstream use.

## Observability

Measure decode/rejection outcomes, consumer lag, commit latency, duplicate source keys, Outbox age, publish attempts, and schema failures. Correlate by source coordinates, source message ID, and event ID without logging tokens or message content.

## Acceptance criteria

- Duplicate source delivery creates one ingress record and one event.
- A database failure leaves the Kafka offset uncommitted and creates no partial event.
- Published events validate against the versioned JSON Schema.
- The adapter can obtain an OpenIM User Token without exposing its Admin Token.
- An Outbox publish failure remains pending and later publishes the same event ID.

## Source evidence

- `open-im-server/internal/rpc/msg/callback.go`
- `open-im-server/pkg/common/webhook/http_client.go`
- `open-im-server/pkg/common/storage/controller/msg.go`
- `open-im-server/pkg/common/storage/kafka/producer.go`
- `open-im-server/internal/rpc/auth/auth.go`
- `contracts/events/im.message.accepted.v1.schema.json`
- `platform/services/platform-api/internal/ingress/`
- `platform/services/platform-api/cmd/platform-ingress/main.go`
- `platform/services/platform-api/internal/migrations/sql/0002_integration.sql`
- `platform/services/platform-api/internal/migrations/sql/0004_agent_bot_identity.sql`

## Verification evidence

- Unit tests cover single/group normalization, malformed inputs, deterministic conversation IDs, and bounded retry delay.
- PostgreSQL integration tests prove source deduplication plus fenced Outbox publication.
- A live OpenIM `/msg/send_msg` produced one accepted ingress record and one Outbox event through the independent `toRedis` consumer group.
- Kafka initially returned `NotLeaderForPartition` during automatic topic creation; the Outbox stayed pending and published the same event ID on attempt 2.
- The published Kafka payload matched the PostgreSQL Outbox event ID and validated against `im.message.accepted.v1.schema.json`.
- A real tenant Bot reply was accepted by OpenIM, attributed back to the same tenant, and published without recursively creating a Run.

## Open questions

- Production Kafka authentication mode (mTLS only or mTLS plus SASL) must be selected before server migration; local plaintext is not a production setting.
- OpenIM upgrades must run protobuf fixture compatibility tests before changing the pinned protocol module.
