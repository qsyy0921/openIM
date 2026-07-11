# OpenIM 后端面试总纲

## 项目定位

推荐表达：

> 我做的是“基于 OpenIM 的企业级 IM 系统源码分析、私有化部署与性能验证项目”。重点不是把开源项目包装成自研，而是理解它的服务拆分、消息链路、Token 体系、中间件分工、SDK 同步机制，并基于本地环境做过部署和压测验证。

不要说：

> 从零自研了 OpenIM。

## 30 秒版本

OpenIM 的核心是把 IM 后端拆成接入层、RPC 业务层、异步消息流水线和 SDK 同步层。设备端通过 SDK 登录后建立 WebSocket，`msggateway` 做连接鉴权和长连接管理，消息进入 `msg rpc` 校验后写 Kafka，`msgtransfer` 分配 seq 并写 Redis/Mongo，再通过 `push` 投递在线用户。SDK 不完全信任 push，而是用 seq 检查缺口并补拉，最终保证客户端本地库和服务端消息流一致。

## 1 分钟版本

这个项目可以从四层讲：设备端 App、`openim-sdk-core`、Chat 产品层、OpenIM Server。Chat 负责注册登录和业务账号，OpenIM Server 负责 IM 能力本身，包括 WebSocket、消息、用户、好友、群、会话、推送。SDK 负责连接、消息状态、本地数据库、断线重连和补拉。

我重点分析了三条链路。第一是认证链路：Chat Token 用于 Chat API，OpenIM Admin Token 用于 Chat 服务端调用 OpenIM API，OpenIM User Token 用于客户端连接和调用 IM 能力。第二是消息链路：`msg rpc` 只做校验和入 Kafka，真正的 seq、缓存、落库和推送由 `msgtransfer`、`push` 异步完成。第三是可靠性链路：push 只是通知，最终以服务端 seq 和 SDK 补拉为准。

## 3 分钟版本

OpenIM 的设计思想不是把所有逻辑堆在一个 IM Server 里，而是把系统拆成几个边界清晰的部分。`openim-api` 面向 HTTP 接口，`msggateway` 面向长连接，`auth/user/group/friend/conversation/msg` 等 RPC 负责领域能力，`msgtransfer` 承担异步消息流水线，`push` 承担在线和离线推送。中间件上，MongoDB 做用户、关系、群、会话、消息、version log 的持久化；Redis 做 Token、在线状态、seq/cache 和热点数据；Kafka 把发送、落库、推送解耦；etcd 做服务发现；MinIO 做对象存储；Prometheus/Grafana 做观测。

它的关键取舍在消息链路。`SendMsg` 不是直接写 MongoDB，而是把消息写入 Kafka `toRedis`，后续由 `msgtransfer` 批量消费、按会话分配 seq、写 Redis cache，再分别转 `toMongo` 和 `toPush`。这样可以降低发送入口的尾延迟，也能把 MongoDB 慢、push 慢、离线推送慢从主请求链路中拆出去。代价是系统变成最终一致，需要用 seq、幂等、补拉、重试和监控处理异步失败。

群聊的难点不是单条消息存储，而是群成员展开和在线 fanout。当前代码中群消息按群会话写消息流，在线推送阶段通过 `Push2Group` 和 `groupMessagesHandler` 展开群成员，再调用在线推送。大群压测要区分“能保持多少连接”“一个群能容纳多少成员”“每秒能处理多少群消息 fanout”。5 万人群只能说明某个维度通过，不能等价证明消息处理能力。

## 面试最容易被追问的点

- 发送成功是否等于落库成功：不等于，通常代表入口校验和 MQ 写入成功，Mongo 落库在后续异步阶段。
- push 丢了怎么办：SDK 用 seq 判断是否连续，不连续就补拉；push 是提醒，不是可靠性的唯一来源。
- 为什么需要 Kafka：削峰、解耦、批量、隔离慢组件，并让存储和推送可以独立扩缩容。
- 为什么不是 PostgreSQL 为核心：IM 消息流、变长内容、按会话 seq 分块、关系和会话频繁读写，更贴合 MongoDB + Redis + Kafka 的组合；PG 也能做，但需要更强的分表、索引、写放大和冷热分层设计。
- OpenIM User Token 和 Chat Token 为什么分开：Chat 是产品账号域，OpenIM 是 IM 能力域；分开后业务身份和 IM 接入权限可以独立演进。
