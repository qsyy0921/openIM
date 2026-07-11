# 中间件分工与设计取舍

## 总览

OpenIM 当前源码和部署配置中，核心中间件分工如下：

| 中间件 | 主要职责 | 典型源码/配置 |
| --- | --- | --- |
| MongoDB | 用户、关系、群、会话、消息、version log 持久化 | `open-im-server/pkg/common/storage/database/mgo`、`open-im-server/pkg/common/storage/model` |
| Redis | Token、在线状态、seq/cache、热点关系缓存 | `open-im-server/pkg/common/storage/cache/redis`、`chat/pkg/common/db/cache` |
| Kafka | 消息异步流水线：`toRedis`、`toMongo`、`toPush`、`toOfflinePush` | `open-im-server/config/kafka.yml`、`open-im-server/internal/msgtransfer`、`open-im-server/internal/push` |
| etcd | 服务注册发现 | `open-im-server/internal/api/router.go`、`open-im-server/internal/msggateway/ws_server.go` 通过 discovery 获取 RPC 连接 |
| MinIO | 对象存储，图片、文件、语音、视频等媒体对象 | `open-im-server/internal/tools/s3.go`、`open-im-server/pkg/common/storage/model/object.go` |
| Prometheus/Grafana | 指标和可视化 | `open-im-server/pkg/common/prommetrics`、`open-im-server/internal/api/third.go` |

## MongoDB

源码事实：

- 用户模型：`open-im-server/pkg/common/storage/model/user.go`
- 好友/黑名单/申请：`friend.go`、`black.go`、`friend_request.go`
- 群/群成员/群申请：`group.go`、`group_member.go`、`group_request.go`
- 会话：`conversation.go`
- 消息：`msg.go`、`database/mgo/msg.go`
- version log：`version_log.go`、`database/mgo/version_log.go`

消息存储设计：`model.MsgDocModel` 不是一条消息一个 Mongo document 的简单模型，而是按 conversationID 和 seq 分块组织。`msg_transfer.go` 中 `BatchInsertBlock` 会根据 `GetDocID(conversationID, seq)` 和 `GetMsgIndex(seq)` 更新或创建块文档。

设计推断：

- IM 消息是高写入、按会话顺序读取、内容结构变化多的文档型数据。
- MongoDB 适合保存变长消息体、撤回信息、离线推送信息和扩展字段。
- 分块文档减少单条消息 document 的数量，但会引入块内更新、重复写处理和 seq 边界校验。

## Redis

Redis 在 OpenIM 中不是单纯缓存。

典型用途：

- Chat Token：`chat/pkg/common/db/cache/token.go` 的 `CHAT_UID_TOKEN_STATUS:{userID}`。
- OpenIM User Token：`open-im-server/pkg/common/storage/cache/cachekey/token.go` 的 `UID_PID_TOKEN_STATUS:{userID}:{platform}`。
- 消息短期缓存：`open-im-server/pkg/common/storage/cache/msg.go`、`cache/redis/msg.go`。
- seq：`open-im-server/pkg/common/storage/cache/redis/seq_conversation.go`、`seq_user.go`。
- 在线状态和热点关系：`open-im-server/pkg/common/storage/cache/redis`、`pkg/rpccache`。

Redis 挂了会怎样：

- 新登录、Token 解析、多端状态会受影响。
- 消息 seq 分配和消息 cache 可能失败，`msgtransfer` 无法正常推进。
- 在线状态查询会退化或失败，push 可能无法准确判断在线用户。
- 已有 WebSocket 连接可能短时间还在，但系统整体无法可靠处理新鉴权和新消息。

面试回答要点：Redis 不是“丢了可以从 DB 重建的普通缓存”，在部分链路中它是在线状态和 seq 的实时状态存储。

## Kafka

配置依据：`open-im-server/config/kafka.yml`

```yaml
toRedisTopic: toRedis
toMongoTopic: toMongo
toPushTopic: toPush
toOfflinePushTopic: toOfflinePush
toRedisGroupID: redis
toMongoGroupID: mongo
toPushGroupID: push
toOfflinePushGroupID: offlinePush
```

源码入口：

- 写 `toRedis`：`open-im-server/pkg/common/storage/controller/msg.go`
- 消费 `toRedis`：`open-im-server/internal/msgtransfer/online_history_msg_handler.go`
- 写 `toMongo`、`toPush`：`open-im-server/pkg/common/storage/controller/msg_transfer.go`
- 消费 `toMongo`：`open-im-server/internal/msgtransfer/online_msg_to_mongo_handler.go`
- 消费 `toPush`：`open-im-server/internal/push/push_handler.go`
- 消费 `toOfflinePush`：`open-im-server/internal/push/offlinepush_handler.go`

Kafka 的核心价值：

- 削峰：发送入口不用等待落库和 fanout。
- 解耦：Mongo 慢、push 慢、离线推送慢不会直接卡住 `SendMsg`。
- 批量：`msgtransfer` 可以按 key 聚合、批量处理。
- 可扩展：不同 topic/consumer group 可以独立扩缩容。

Kafka 堆积会怎样：

- 发送入口可能仍然返回成功，但消息 seq 分配、落库、推送延迟上升。
- SDK 看到 push 变慢或补拉不到最新消息。
- Mongo 落库滞后，历史查询不完整。
- 面试里应说“发送成功语义要重新定义”，不能承诺同步落库。

## etcd

OpenIM 使用服务发现连接 RPC 服务。`open-im-server/internal/api/router.go` 和 `open-im-server/internal/msggateway/ws_server.go` 都会根据配置中的 RPC 注册名获取连接。

设计意义：

- API 和 gateway 不需要硬编码每个 RPC 地址。
- RPC 服务可横向扩容。
- 网关、push、msgtransfer 等组件可以动态发现依赖服务。

如果服务发现异常，表现通常不是数据丢失，而是 API 或 gateway 无法找到对应 RPC，导致请求失败。

## MinIO

MinIO 承担对象存储，不适合把图片、语音、视频等大对象直接塞进消息体。

设计思想：

- 消息体里保存 URL、object key、元数据。
- 对象数据走 MinIO，减轻 MongoDB 和 Kafka 的负担。
- 文件类消息可以复用对象存储的权限、分片、生命周期管理。

## Prometheus/Grafana

源码中 `open-im-server/pkg/common/prommetrics` 定义了在线用户、消息处理成功/失败、离线推送失败等指标。监控的价值在异步架构里尤其重要，因为用户看到“发送成功”后，真实落库/推送可能在后台继续推进。

重点指标：

- Kafka backlog / consumer lag。
- msgtransfer 处理耗时和失败数。
- MongoDB 写入耗时。
- Redis 错误率和延迟。
- push 成功率、离线推送失败率。
- gateway 在线连接数。

## 为什么不是 PostgreSQL 为核心

不是说 PostgreSQL 不能做 IM，而是 OpenIM 当前实现更偏向 MongoDB + Redis + Kafka 的组合。

PostgreSQL 的优势：

- 强事务、SQL 查询、约束和一致性能力强。
- 适合复杂关系查询和管理后台。
- 运维成熟，数据分析生态好。

在 IM 消息主链路中的挑战：

- 单聊/群聊消息高写入，按会话 seq 顺序追加，需要分区和冷热归档。
- 群聊 fanout 场景下，如果设计成每个用户 inbox 行，会产生明显写放大。
- 消息体结构变化多，扩展字段多，JSONB 可以做但索引和更新策略要谨慎。
- 历史消息通常按 conversationID + seq/range 读取，不是复杂 join。

OpenIM 当前选择的组合：

- MongoDB：承接文档型消息和业务实体持久化。
- Redis：承接低延迟状态、Token、seq/cache。
- Kafka：承接异步流水线和削峰。

面试表达：

> PostgreSQL 可以作为 IM 系统的核心数据库，但要重新设计分区、索引、冷热数据、写放大和 fanout 模型。OpenIM 当前实现选择 MongoDB，不是因为 PG 不行，而是因为消息文档、按会话 seq 分块、异步写入和扩展字段更贴合 MongoDB；同时用 Redis 和 Kafka 分别解决实时状态与异步削峰问题。
