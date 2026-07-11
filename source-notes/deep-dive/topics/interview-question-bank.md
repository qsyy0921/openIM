# OpenIM 高质量面试追问库

## 1. 你说发送成功，那消息一定落库了吗？

- 考察点：异步链路语义。
- 回答思路：不一定。`SendMsg` 成功更接近“校验通过并写入 Kafka”，Mongo 落库由 `msgtransfer` 消费 `toMongo` 完成。
- 源码依据：`open-im-server/internal/rpc/msg/send.go`、`open-im-server/pkg/common/storage/controller/msg.go`、`open-im-server/internal/msgtransfer/online_msg_to_mongo_handler.go`。
- 容易说错：把 API 返回成功等同于 MongoDB 已持久化。

## 2. 如果 WebSocket push 丢了怎么办？

- 考察点：可靠性闭环。
- 回答思路：SDK 用 seq 判断缺口，断线重连或收到不连续 push 后按 seq 范围补拉。
- 源码依据：`openim-sdk-core/internal/interaction/msg_sync.go` 的 `compareSeqsAndBatchSync`、`pushTriggerAndSync`。
- 容易说错：说“服务端重推一次就行”，忽略客户端补拉。

## 3. 为什么 Kafka 在这里是必要的？

- 考察点：削峰解耦。
- 回答思路：发送入口、落库、推送、离线推送的耗时差异大，Kafka 把主链路和慢操作拆开，并支持批量和独立扩容。
- 源码依据：`open-im-server/config/kafka.yml`、`online_history_msg_handler.go`、`push_handler.go`。
- 容易说错：只说“为了高并发”，没有说明代价是最终一致和 MQ 堆积风险。

## 4. Redis 挂了会怎样？

- 考察点：Redis 在系统中的真实角色。
- 回答思路：会影响 Token、在线状态、seq/cache、消息补拉热点，不是普通缓存失效那么简单。
- 源码依据：`cachekey/token.go`、`cache/redis/token.go`、`cache/redis/seq_conversation.go`、`cache/redis/msg.go`。
- 容易说错：说 Redis 挂了只会慢一点。

## 5. MongoDB 为什么适合这里，为什么不是 PostgreSQL？

- 考察点：数据库选型。
- 回答思路：当前实现按文档保存用户、关系、群和消息分块，消息体扩展字段多、按会话 seq 范围读写；PG 也能做，但要额外设计分区、索引、冷热归档和写放大控制。
- 源码依据：`model/msg.go`、`database/mgo/msg.go`、`controller/msg_transfer.go`。
- 容易说错：贬低 PG 或说 MongoDB 天然更快。

## 6. 群聊是读扩散还是写扩散？

- 考察点：群消息模型。
- 回答思路：当前代码更接近按群会话写消息流，在线阶段再对成员 fanout；首次会话创建会为成员创建会话记录。不能简单说成“每个成员写一份消息”。
- 源码依据：`sendMsgGroupChat`、`BatchInsertChat2Cache`、`Push2Group`、`CreateGroupChatConversations`。
- 容易说错：把在线 fanout 和持久化写扩散混为一谈。

## 7. 5 万人大群真正瓶颈在哪里？

- 考察点：压测指标解释。
- 回答思路：要区分群成员规模、连接保持、消息 fanout、每秒消息处理能力。真正瓶颈可能在群成员展开、在线状态查询、gateway 推送、Kafka/msgtransfer、客户端补拉。
- 源码依据：`push_handler.go` 的 `groupMessagesHandler`、`GetConnsAndOnlinePush`。
- 容易说错：用“能建 5 万人群”证明“能高 QPS 群发”。

## 8. Chat Token 和 OpenIM User Token 为什么分开？

- 考察点：领域边界。
- 回答思路：Chat 是产品账号域，OpenIM 是 IM 能力域。分开后业务登录策略和 IM 接入权限可以独立演进。
- 源码依据：`chat/pkg/common/db/cache/token.go`、`open-im-server/pkg/common/storage/cache/cachekey/token.go`。
- 容易说错：把两个 token 说成同一个。

## 9. 多端登录如何踢端？

- 考察点：连接状态和 token 状态协同。
- 回答思路：gateway 根据 platformID 和策略踢旧连接，同时调用 Auth RPC 修改 token 状态。
- 源码依据：`msggateway/ws_server.go` 的 `multiTerminalLoginChecker`、`KickUserConn`，`auth.go` 的 `KickTokens`。
- 容易说错：只讲关闭连接。

## 10. SDK 本地库和服务端不一致怎么办？

- 考察点：客户端同步。
- 回答思路：以服务端 max seq/version log 为准，SDK 对比本地状态后补拉消息或增量同步关系/群/会话。
- 源码依据：`msg_sync.go`、`syncer.go`、`version_synchronizer.go`。
- 容易说错：说清空本地库重新登录，忽略增量机制。

## 11. 你做的是自研 IM 还是基于 OpenIM？

- 考察点：项目真实性。
- 回答思路：诚实说是基于开源 OpenIM 的源码分析、私有化部署、压测验证和二次开发理解。
- 源码依据：本地 `open-im-server`、`chat`、`openim-sdk-core`。
- 容易说错：包装成从零自研。

## 12. 你的压测结果能证明什么，不能证明什么？

- 考察点：工程严谨性。
- 回答思路：连接压测证明连接保持能力；群成员规模证明关系数据承载；消息压测才证明消息处理能力。还要说明端口、客户端机器、服务端机器、Kafka/Mongo/Redis 指标。
- 源码依据：`openim-bench`、部署和压测记录。
- 容易说错：单个数字泛化成整体性能结论。
