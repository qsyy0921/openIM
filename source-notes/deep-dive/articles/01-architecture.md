# 01 OpenIM 项目概要与分层架构设计

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（一）：项目概要与分层架构设计》：https://juejin.cn/post/7516142336243023898

本文不复制原文，按其主题结合本地源码重写。

## 本文目标

建立 OpenIM 的运行时地图，讲清楚 Chat、OpenIM Server、SDK、设备端 App 的边界，以及 API/RPC/gateway/msgtransfer/push/中间件为什么要拆开。

## 核心结论

- OpenIM 不是单体服务，而是按接入层、领域 RPC、异步消息流水线、推送、SDK 同步层拆分。
- Chat 是产品层账号系统，OpenIM Server 是 IM 能力层，SDK 是客户端一致性和体验层。
- 消息链路的关键不是“WebSocket 发一下”，而是 Kafka、seq、Redis/Mongo、push、SDK 补拉组成的闭环。
- 架构设计的核心取舍是低入口延迟和最终一致之间的平衡。

## 源码入口

| 入口 | 路径 |
| --- | --- |
| OpenIM API | `open-im-server/cmd/openim-api/main.go`、`open-im-server/internal/api/router.go` |
| WebSocket Gateway | `open-im-server/cmd/openim-msggateway/main.go`、`open-im-server/internal/msggateway/ws_server.go` |
| RPC 服务 | `open-im-server/cmd/openim-rpc/openim-rpc-*/main.go` |
| Msg RPC | `open-im-server/internal/rpc/msg/send.go` |
| msgtransfer | `open-im-server/cmd/openim-msgtransfer/main.go`、`open-im-server/internal/msgtransfer` |
| push | `open-im-server/cmd/openim-push/main.go`、`open-im-server/internal/push` |
| Chat | `chat/internal/api/chat/chat.go`、`chat/internal/rpc/chat/login.go` |
| SDK | `openim-sdk-core/internal/interaction/msg_sync.go`、`openim-sdk-core/pkg/syncer` |

## 详细源码解析

从 `cmd` 目录可以看到，OpenIM 把不同职责拆成独立进程：`openim-api` 处理 HTTP API，`openim-msggateway` 处理 WebSocket 长连接，`openim-rpc-*` 处理领域能力，`openim-msgtransfer` 处理消息异步流，`openim-push` 处理在线和离线推送。

`open-im-server/internal/api/router.go` 会通过服务发现拿到 Auth、User、Group、Friend、Conversation、Msg、Third 等 RPC 连接。API 层本身不保存领域状态，而是把请求路由给对应 RPC。

`open-im-server/internal/msggateway/ws_server.go` 也通过服务发现连接 Auth、Msg、Push、User 等服务。gateway 的职责是长连接接入和请求转发，不直接写 MongoDB，也不直接做完整群成员和好友业务。

消息链路中，`open-im-server/internal/rpc/msg/send.go` 根据 `SessionType` 分发单聊、群聊、通知消息。通过校验后调用 `MsgToMQ` 写 Kafka。后续 `msgtransfer` 消费 `toRedis`，分配 seq，写 Redis cache，再发往 `toMongo` 和 `toPush`。`push` 消费后根据在线状态投递到 gateway。

## 数据结构与存储

- MongoDB：`users`、`friends`、`groups`、`group_members`、`conversations`、消息分块文档、`version_log`。
- Redis：`CHAT_UID_TOKEN_STATUS:{userID}`、`UID_PID_TOKEN_STATUS:{userID}:{platform}`、在线状态、seq/cache、热点关系。
- Kafka topic：`toRedis`、`toMongo`、`toPush`、`toOfflinePush`，配置在 `open-im-server/config/kafka.yml`。
- SDK 本地表：消息、会话、群、好友、黑名单、已同步 seq，入口在 `openim-sdk-core/pkg/db` 和 `wasm/indexdb`。

## 设计取舍

OpenIM 选择拆分式架构，是因为 IM 系统存在多种完全不同的负载：长连接、消息校验、消息排序、持久化、在线 fanout、离线推送、SDK 补拉。拆开后每一层可以独立扩容，但系统会从同步一致变成最终一致，需要 Kafka、seq、补拉和监控配合。

## 面试讲法

> OpenIM 的核心架构可以按四层讲：设备端 App、SDK、Chat 产品层、OpenIM Server。OpenIM Server 内部又拆成 API、WebSocket gateway、领域 RPC、msgtransfer 和 push。消息进入 gateway 后不会直接落 Mongo，而是经 Msg RPC 写 Kafka，由 msgtransfer 分配 seq 并写 Redis/Mongo，再由 push 投递。SDK 最终通过 seq 补拉保证本地库一致。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| gateway 为什么单独拆？ | 负载隔离 | 长连接服务连接密集，不能被业务慢逻辑拖垮 | `internal/msggateway/ws_server.go` | 只说“微服务更高级” |
| API 层做什么？ | 层次边界 | HTTP 路由和鉴权后转 RPC | `internal/api/router.go` | 说 API 层直接处理所有业务 |
| msgtransfer 是什么？ | 异步核心 | 消费 Kafka、分配 seq、写缓存、转落库/推送 | `internal/msgtransfer` | 说成普通转发 |
| SDK 为什么重要？ | 客户端一致性 | WebSocket、local DB、seq 补拉、断线重连 | `openim-sdk-core/internal/interaction/msg_sync.go` | 忽略 SDK，全部归服务端 |
| OpenIM 是自研吗？ | 项目真实性 | 是基于开源 OpenIM 的源码学习、部署和分析 | 本地源码目录 | 包装成从零自研 |

## 和本地项目的关系

当前 `E:\development\OPENIM` 已包含 `open-im-server`、`chat`、`openim-sdk-core`、`openim-docker-v3.8`、`openim-bench` 等目录，足以支撑从源码、部署、压测三条线学习 OpenIM。

## 小结

OpenIM 的架构重点是职责拆分和异步消息流。面试中不要背模块名，要能把一次登录、一次建连、一次发消息讲成真实运行时链路。
