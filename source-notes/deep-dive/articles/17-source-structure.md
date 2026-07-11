# 17 附录一：源码详细项目结构

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十七）：附录一源码详细项目结构》：https://juejin.cn/post/7518427600650698790

## 本文目标

为本地 `E:\development\OPENIM` 建立源码目录索引，方便后续按问题定位源码。

## 核心结论

- `open-im-server` 是 IM 能力服务端核心。
- `chat` 是业务产品层，不等同于 IM 核心。
- `openim-sdk-core` 是客户端核心同步和协议层。
- `openim-docker-v3.8` 和 `openim-bench` 分别服务部署和压测。

## 源码入口

```text
E:\development\OPENIM
  open-im-server
  chat
  openim-sdk-core
  sources/openimsdk/openim-sdk-core
  openim-docker-v3.8
  openim-bench
  source-notes
```

## 详细源码解析

`open-im-server/cmd` 是所有服务进程入口。`internal/api` 是 HTTP API；`internal/msggateway` 是 WebSocket；`internal/rpc` 下按 auth、msg、user、group、relation、conversation、third 拆分领域服务；`internal/msgtransfer` 是消息异步处理；`internal/push` 是在线/离线推送。

`open-im-server/pkg/common/storage` 是服务端存储抽象，里面有 model、database、cache、controller。读源码时建议从 controller 看领域操作，再下钻 database/mgo 和 cache/redis。

`chat` 下 `internal/api/chat` 是产品层 HTTP 入口，`internal/rpc/chat` 是注册登录逻辑，`pkg/common/imapi` 是调用 OpenIM API 的封装，`pkg/common/db` 是 Chat 侧账号数据。

`openim-sdk-core` 下 `internal/interaction` 负责长连接和消息同步，`internal/conversation_msg` 负责会话消息，`pkg/syncer` 负责同步框架，`pkg/db` 和 `wasm/indexdb` 负责本地存储。

## 数据结构与存储

- 服务端模型：`open-im-server/pkg/common/storage/model`
- Mongo 实现：`open-im-server/pkg/common/storage/database/mgo`
- Redis 实现：`open-im-server/pkg/common/storage/cache/redis`
- Chat 模型：`chat/pkg/common/db/model`
- Chat 表定义：`chat/pkg/common/db/table`
- SDK 本地库：`openim-sdk-core/pkg/db`、`openim-sdk-core/wasm/indexdb`

## 设计取舍

OpenIM 目录结构体现了领域拆分。好处是模块边界清晰；代价是阅读成本高，需要按运行时链路横跨多个目录。

## 面试讲法

> 我读 OpenIM 源码不是按目录平铺读，而是按链路读。登录链路从 `chat` 到 `auth` 再到 `msggateway`；消息链路从 SDK 到 gateway、Msg RPC、Kafka、msgtransfer、push；同步链路从服务端 seq/version log 到 SDK 本地库。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| open-im-server 和 chat 区别？ | 模块边界 | IM 核心 vs 产品层账号 | 两目录 | 混成一个服务 |
| 从哪看消息入口？ | 定位能力 | `internal/rpc/msg/send.go` | Msg RPC | 只说 API |
| 从哪看 WebSocket？ | 接入层 | `internal/msggateway` | gateway | 找 chat |
| 从哪看 SDK 补拉？ | 客户端 | `internal/interaction/msg_sync.go` | SDK | 只看服务端 |
| 从哪看数据库？ | 存储层 | `storage/model`、`database/mgo`、`cache/redis` | storage | 只看配置 |

## 和本地项目的关系

本篇就是本地源码目录的维护索引。后续新增源码分析可继续追加路径和函数。

## 小结

读 OpenIM 要按链路穿透目录。目录是地图，运行时调用链才是重点。
