# 简历项目描述

## 项目一句话

基于 OpenIM 的企业级 IM 系统源码分析、私有化部署与性能验证项目。

## 技术栈

Go、gRPC、WebSocket、Kafka、Redis、MongoDB、etcd、MinIO、Docker Compose、Prometheus/Grafana、openim-sdk-core。

## 推荐简历描述

项目：基于 OpenIM 的企业级 IM 系统源码分析、私有化部署与性能验证

- 基于 OpenIM `open-im-server`、`chat`、`openim-sdk-core` 完成 IM 系统源码学习，梳理 Chat 产品层、OpenIM Server、WebSocket Gateway、RPC 服务、msgtransfer、push、SDK 同步器之间的运行时关系。
- 私有化部署 OpenIM 及 MongoDB、Redis、Kafka、etcd、MinIO 等依赖组件，分析单机 Compose 和多机压测场景下的连接数、端口范围、消息链路瓶颈。
- 深入分析注册登录和三类 Token 机制，区分 Chat Token、OpenIM Admin Token、OpenIM User Token 的生成方、使用方、Redis 存储和 WebSocket 鉴权流程。
- 梳理消息发送全链路：SDK -> WebSocket Gateway -> Msg RPC -> Kafka -> msgtransfer -> Redis/MongoDB -> push -> SDK，并总结发送成功、落库成功、在线推送成功、客户端最终可见之间的语义差异。
- 分析 seq、消息补拉、SDK 本地库和 version log 增量同步机制，理解 OpenIM 在弱网、断线重连、多端同步下的最终一致性设计。
- 结合群聊源码分析大群 fanout、群成员展开、在线状态查询和离线推送成本，区分连接压测、群规模压测和消息处理能力压测的不同含义。

## 成果表述

保守写法：

- 完成 OpenIM 核心链路源码阅读和本地部署验证，形成可复用的架构学习文档和面试问题库。
- 基于本地硬件环境验证大连接场景和大群场景的可行性，并分析端口、客户端资源、服务端资源和消息 fanout 的瓶颈边界。

如果后续补充了稳定压测数据，可以再写：

- 在本地多机环境中完成 X 万 WebSocket 连接保持测试，持续 Y 分钟，记录服务端 CPU、内存、网络和错误率。
- 完成群消息 QPS 压测，统计 Kafka backlog、msgtransfer 消费延迟、MongoDB 写入延迟和 SDK 收消息延迟。

## 禁止表达

- 从零自研 OpenIM。
- 独立开发企业级 IM 全套系统。
- 单凭连接数证明消息吞吐能力。

## 30 秒口头版

我做的是基于 OpenIM 的 IM 后端源码分析和部署压测项目。重点分析了它的 Chat 产品层、OpenIM Server、WebSocket 网关、消息异步流水线和 SDK 同步机制。消息链路上，发送入口只做校验和入 Kafka，后续由 msgtransfer 分配 seq、写 Redis/Mongo，再由 push 投递；SDK 通过 seq 补拉保证最终一致。

## 1 分钟口头版

这个项目我主要从三条链路学习。第一是认证链路，Chat Token、OpenIM Admin Token、OpenIM User Token 分别对应产品层登录、服务端调用 OpenIM、客户端 IM 接入。第二是消息链路，SDK 经 WebSocket 到 msggateway，再到 Msg RPC，写 Kafka 后由 msgtransfer 做 seq 和存储，由 push 做在线/离线投递。第三是可靠性链路，push 只解决实时性，SDK 还要根据 seq 补拉并写本地库，保证断线重连和多端同步下的最终一致。

## 3 分钟口头版

OpenIM 的架构可以分为设备端、SDK、Chat 系统和 OpenIM Server。Chat 做业务账号和登录，OpenIM Server 做 IM 能力，SDK 做客户端同步和本地库。服务端内部又拆成 API、WebSocket gateway、多个 RPC、msgtransfer 和 push。这样拆是因为 IM 的负载类型不同：gateway 是连接密集型，Msg RPC 是校验型，msgtransfer 是异步批处理，push 是 fanout 型。

消息发送不是直接写 MongoDB，而是先写 Kafka `toRedis`，再由 msgtransfer 分配 seq、写 Redis cache、转 Mongo 持久化和 push 推送。这样入口延迟低、组件可以独立扩容，但代价是最终一致，需要通过 seq、补拉和监控兜底。群聊方面，群消息持久化更像按群会话写消息流，在线推送阶段再展开成员 fanout，所以大群压测必须区分群成员规模、连接保持和消息吞吐能力。
