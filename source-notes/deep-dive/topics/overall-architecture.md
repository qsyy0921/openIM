# OpenIM 整体架构与设计思想

## 架构分层

OpenIM 在当前本地源码中可以拆成四个大层次：

1. 设备端应用：Android、iOS、Web、React Native 等最终 App。它负责 UI、业务交互和调用 SDK，不直接实现 IM 协议细节。
2. `openim-sdk-core`：客户端 IM 内核。它负责登录态、WebSocket 长连接、本地数据库、消息发送状态、seq 补拉、断线重连、会话和关系同步。
3. Chat 系统：产品层账号系统。它负责注册、登录、验证码、业务账号、Chat Token，并代表业务服务端调用 OpenIM。
4. OpenIM Server：IM 能力层。它负责用户、好友、群、会话、消息、WebSocket 网关、Token 鉴权、推送、消息异步处理。

源码入口：

- Chat API：`chat/cmd/api/chat-api/main.go`、`chat/internal/api/chat/chat.go`
- Chat RPC：`chat/cmd/rpc/chat-rpc/main.go`、`chat/internal/rpc/chat/login.go`
- OpenIM API：`open-im-server/cmd/openim-api/main.go`、`open-im-server/internal/api/router.go`
- WebSocket Gateway：`open-im-server/cmd/openim-msggateway/main.go`、`open-im-server/internal/msggateway/ws_server.go`
- RPC 服务：`open-im-server/cmd/openim-rpc/openim-rpc-*/main.go`
- 消息异步处理：`open-im-server/cmd/openim-msgtransfer/main.go`、`open-im-server/internal/msgtransfer`
- 推送服务：`open-im-server/cmd/openim-push/main.go`、`open-im-server/internal/push`
- SDK 同步：`openim-sdk-core/internal/interaction/msg_sync.go`、`openim-sdk-core/pkg/syncer`

## 服务拆分思想

源码事实：`open-im-server/cmd` 下没有一个“全功能单体进程”，而是按 API、网关、RPC、消息转发、推送拆分启动入口。`open-im-server/internal/api/router.go` 通过服务发现拿到 Auth/User/Group/Friend/Conversation/Msg/Third 等 RPC 连接；`open-im-server/internal/msggateway/ws_server.go` 同样通过服务发现连接 User、Push、Auth、Msg。

设计推断：OpenIM 的拆分不是为了“微服务而微服务”，而是因为 IM 系统的负载类型差异明显：

- HTTP API 是短请求，适合无状态扩容。
- WebSocket 网关是长连接，瓶颈在连接数、心跳、内存和 fd。
- Msg RPC 是业务校验和入队，瓶颈在校验、关系读取、MQ 写入。
- msgtransfer 是异步批处理，瓶颈在 Kafka 消费、seq 分配、Redis/Mongo 写入。
- push 是 fanout 和在线投递，瓶颈在在线状态、群成员展开、网关 RPC。

如果这些职责放在一个进程里，任何一个慢组件都会拖慢整个发送链路；拆开后可以分别扩容和监控。

## 架构设计思想总纲

### 1. 把实时性和可靠性拆开

OpenIM 没有试图让 WebSocket push 一次完成所有可靠性。WebSocket push 负责“尽快让在线用户看到消息”，seq + SDK pull 负责“最终补齐消息”。这是一种典型 IM 设计取舍：实时链路越短越好，可靠链路必须可恢复。

面试时可以这样说：

> push 是低延迟通知路径，pull 是可靠性修复路径。OpenIM 用 push 提升在线体验，用 seq 补拉处理断线、丢包、网关重启和客户端本地库异常。

### 2. 把入口延迟和后台吞吐拆开

`Msg RPC` 不直接写 MongoDB，也不直接 fanout。它校验完成后写 Kafka，让 `msgtransfer` 和 `push` 后台处理。这样入口延迟不会被 MongoDB 抖动、离线推送、群成员展开拖慢。

代价是成功语义变弱：用户看到发送成功时，可能只是入队成功。这个代价必须用 Kafka 监控、seq、补拉、失败重试来弥补。

### 3. 把长连接状态和业务状态拆开

gateway 维护 socket、connID、platformID、心跳和在线用户；User/Group/Friend/Conversation/Msg RPC 维护业务数据。这样 gateway 可以按连接数扩容，RPC 可以按业务 QPS 扩容。

如果让 gateway 直接处理所有业务，它会同时承受连接数、群成员查询、MongoDB 写入、Kafka 生产、离线推送，排障和扩容都会变困难。

### 4. 把账号域和 IM 域拆开

Chat 系统维护业务账号、验证码、登录风控和 Chat Token；OpenIM Server 维护 IM 用户、OpenIM Token 和消息能力。这个拆分使业务登录方式可以变化，而 IM 能力层保持稳定。

这也是三种 Token 存在的根本原因：Chat Token 管产品层，OpenIM Admin Token 管服务端调用权限，OpenIM User Token 管 SDK 接入。

### 5. 把高频状态和持久数据拆开

Redis 承担 Token 状态、在线状态、seq/cache 等高频实时状态；MongoDB 承担用户、关系、群、会话、消息、version log 的持久化。这样低延迟状态查询不必压到 MongoDB 上。

但 Redis 在这里不是“可随便丢的缓存”。Token 状态、seq/cache、在线状态对运行时链路非常关键。面试里要避免说“Redis 挂了只是慢一点”。

### 6. 把消息流同步和对象状态同步拆开

消息是按会话顺序追加的流，适合用 seq；好友、群、会话、用户资料是对象状态变化，适合用 version log。OpenIM 同时使用这两种同步模型。

这解释了为什么 SDK 既有 `MsgSyncer`，也有 `VersionSynchronizer`。前者解决“消息缺哪几条”，后者解决“对象状态变了哪些”。

### 7. 把群消息存储和在线 fanout 拆开

群消息持久化更接近按群会话写消息流，而在线推送阶段才展开成员 fanout。这样可以避免发送入口为每个成员复制完整消息，但不能消灭大群实时推送成本。

面试要主动说明：

> 5 万人大群能证明成员规模或连接维度可行，但不能自动证明高 QPS 群消息处理能力。真正要看 fanout、Kafka lag、msgtransfer 延迟、push 成功率、gateway 连接和 SDK 端到端延迟。

### 8. 把服务端最终一致和客户端体验连接起来

服务端异步化后，系统需要一个地方把最终一致转成用户可见的一致体验，这个地方就是 SDK。本地库、seq、补拉、回调共同组成客户端状态机。

因此 OpenIM 的 SDK 不是普通 API wrapper，而是 IM 系统的一部分。没有 SDK 的本地一致性设计，服务端异步链路很难在弱网场景下保证用户体验。

### 9. 把扩展点放在链路边界

源码中可以看到多处 webhook：发送前修改、发送后通知、在线推送前、离线推送前等。它们出现在链路边界，而不是侵入核心存储结构。

设计意义是：业务可以做内容审核、消息改写、推送过滤、风控拦截，但核心消息流仍然保持相对稳定。

### 10. 把可观测性作为异步架构的一部分

异步架构最怕“入口成功，但后台失败没人知道”。OpenIM 在 `pkg/common/prommetrics` 中维护在线用户数、消息处理成功失败、离线推送失败等指标。真正压测时，还要补充 Kafka lag、Redis 延迟、MongoDB 写入耗时和 gateway 推送成功率。

面试时可以总结：

> OpenIM 的设计不是单纯堆中间件，而是围绕 IM 的核心矛盾做拆分：实时性与可靠性、入口延迟与后台吞吐、连接状态与业务状态、消息流与对象状态、群消息存储与在线 fanout。

## 核心消息链路

```text
SDK/App
  -> WebSocket
  -> msggateway
  -> msg rpc
  -> Kafka toRedis
  -> msgtransfer
  -> Redis cache / seq
  -> Kafka toMongo
  -> MongoDB
  -> Kafka toPush
  -> push
  -> msggateway
  -> SDK local DB
```

每一段存在的原因：

| 环节 | 作用 | 设计意义 |
| --- | --- | --- |
| SDK -> msggateway | 长连接接入和命令承载 | 让客户端保留实时通道，减少轮询 |
| msggateway -> msg rpc | 网关不做重业务 | 避免长连接进程被业务校验拖慢 |
| msg rpc -> Kafka | 校验后入异步流水线 | 降低主请求尾延迟，隔离存储和推送 |
| Kafka -> msgtransfer | 异步批量消费 | 方便按会话聚合、批量分配 seq |
| msgtransfer -> Redis | 写热点缓存和最新消息 | SDK 补拉和短期读取更快 |
| msgtransfer -> Mongo | 持久化消息 | 保证历史消息可查 |
| msgtransfer -> push | 推送在线用户 | 把在线投递从存储逻辑拆出 |
| push -> msggateway | 复用连接所在网关 | 网关掌握具体连接对象 |
| SDK local DB | 本地最终展示和恢复 | 断线、重启、弱网下保持体验 |

## 为什么不是直接写 MongoDB

源码事实：`open-im-server/internal/rpc/msg/send.go` 中 `sendMsgSingleChat`、`sendMsgGroupChat` 最终调用 `m.MsgDatabase.MsgToMQ(...)`，而不是直接调用 MongoDB 写入。`open-im-server/internal/msgtransfer/online_history_msg_handler.go` 消费 `toRedis` 后调用 `BatchInsertChat2Cache`，再调用 `MsgToMongoMQ` 和 `toPushTopic`。

设计推断：

- 发送入口需要快：用户点击发送时，入口要尽快完成鉴权、关系校验、消息封装和入队。
- MongoDB 写入可能慢：索引、磁盘、复制集、分块更新都会引入抖动。
- 推送可能更慢：群聊 fanout 和离线推送耗时不可控。
- Kafka 把系统变成“入口接收 + 后台处理”，主链路只承诺接收成功，最终落库和投递由后续阶段完成。

代价也很明确：需要处理 MQ 堆积、重复消费、消息顺序、补偿和监控。面试时不能只说“用了 Kafka 提高性能”，要补一句“代价是最终一致，需要 seq 和 SDK 补拉兜底”。

## seq 是架构中心

OpenIM 消息可靠性不是靠 WebSocket push 的一次成功来保证，而是靠每个会话上的 seq。

源码事实：

- 服务端分配 seq：`open-im-server/pkg/common/storage/controller/msg_transfer.go` 的 `BatchInsertChat2Cache` 调用 `seqConversation.Malloc`，然后给每条消息递增设置 `m.Seq`。
- SDK 对比 seq：`openim-sdk-core/internal/interaction/msg_sync.go` 的 `compareSeqsAndBatchSync`、`pushTriggerAndSync`、`doConnected` 会根据服务端 max seq 和本地已同步 seq 判断是否需要补拉。
- SDK 本地库提供按 seq 查询、缺口检查：`openim-sdk-core/wasm/indexdb/chat_log_model.go`、`openim-sdk-core/pkg/db`。

设计思想：

- push 是低延迟路径，不是最终可靠路径。
- seq 是一致性坐标，客户端只要知道服务端最新 seq，就能判断自己缺不缺消息。
- SDK 本地库不是简单缓存，而是客户端的“已同步状态机”。

## Chat 和 OpenIM Server 的边界

Chat 是业务产品层，OpenIM Server 是 IM 能力层。

| 项目 | Chat | OpenIM Server |
| --- | --- | --- |
| 主要职责 | 注册、登录、验证码、业务账号、Chat Token | IM 用户、消息、群、好友、会话、WebSocket、OpenIM Token |
| Token | Chat Token | OpenIM Admin Token、OpenIM User Token |
| 数据 | `accounts`、`attributes`、`credentials`、`verify_codes`、`registers` | `users`、`friends`、`groups`、`group_members`、`conversations`、消息文档 |
| 调用方式 | HTTP/RPC + `imapi` 调 OpenIM | API/RPC/WebSocket |
| 面试口径 | 产品层 | IM 基础能力层 |

容易说错的点：不能说“Chat Token 就是 WebSocket Token”。WebSocket 接入需要 OpenIM User Token，Chat Token 主要用于 Chat API。

## 群聊架构的核心矛盾

源码事实：

- 群消息发送入口在 `open-im-server/internal/rpc/msg/send.go` 的 `sendMsgGroupChat`，以 `conversationutil.GenConversationUniqueKeyForGroup` 作为 MQ key。
- `msgtransfer` 按群会话写消息流，首次出现会话时调用 `groupClient.GetGroupMemberUserIDs` 和 `conversationClient.CreateGroupChatConversations`。
- 在线推送在 `open-im-server/internal/push/push_handler.go` 的 `Push2Group`、`groupMessagesHandler` 中展开群成员并调用 `GetConnsAndOnlinePush`。

设计推断：群消息持久化更接近“按群会话写一条消息流”，而在线推送是“对在线成员 fanout”。这能避免发送阶段为每个成员写一份完整消息，但 fanout 成本仍然存在，并且大群的真正瓶颈通常在成员展开、在线状态查询、网关投递、失败离线推送和客户端同步。

面试里要区分：

- 5 万人群成员存在：说明关系/群成员数据能承载。
- 5 万连接保持：说明网关连接层能承载。
- 5 万人群发消息：说明 fanout 和异步流水线能承载。
- 高 QPS 群消息：说明 Kafka、msgtransfer、push、网关、SDK 同步都能承载。

## 当前代码与图/网络资料可能不一致的地方

源码学习时要以当前本地源码为准：

- Chat Mongo model 与 table 命名可能有单复数差异，例如 `chat/pkg/common/db/table/chat/*.go` 和 `chat/pkg/common/db/model/chat/*.go` 都要看。
- 文章或架构图可能把“Redis 缓存层”“Mongo 持久层”画得很整齐，但实际链路中 Redis 还承担 Token、在线状态、seq/cache、局部热点关系等多种职责。
- “双 Token”文章常以 Chat Token + OpenIM User Token 为主，但完整工程里还要讲 OpenIM Admin Token，因为 Chat 服务端调用 OpenIM API 需要它。
- “push 成功”不能等价为“消息最终可靠”，SDK 仍然必须按 seq 同步。

## 面试讲法

可以这样讲：

> OpenIM 的设计核心是把实时接入、领域校验、异步消息处理、在线推送和客户端同步拆开。服务端通过 Kafka 把发送入口、缓存/落库、在线/离线推送解耦，通过 Redis 承担 Token、在线状态和消息热点，通过 MongoDB 做持久化，通过 seq 给 SDK 一个可验证的同步坐标。这样做的收益是扩容边界清楚、入口延迟低、弱网可恢复；代价是系统变成最终一致，需要处理 MQ 堆积、重复消费、补拉和监控。
