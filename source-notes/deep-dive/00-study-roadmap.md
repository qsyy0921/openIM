# OpenIM 源码学习路线

## 阶段一：先建立运行时地图

目标不是背目录，而是知道一次登录、一次建连、一次发消息会经过哪些进程。

1. 先看启动入口：`open-im-server/cmd/openim-api/main.go`、`open-im-server/cmd/openim-msggateway/main.go`、`open-im-server/cmd/openim-rpc/*/main.go`、`open-im-server/cmd/openim-msgtransfer/main.go`、`open-im-server/cmd/openim-push/main.go`。
2. 再看路由：`open-im-server/internal/api/router.go`、`open-im-server/internal/msggateway/ws_server.go`。
3. 最后看配置：`open-im-server/config/kafka.yml`、`open-im-server/docker-compose.yml`、`open-im-server/pkg/common/config/config.go`。

面试目标：能画出“API/RPC/gateway/msgtransfer/push/中间件/SDK”的完整关系图。

## 阶段二：把认证链路讲清楚

重点看 `chat` 和 `open-im-server` 的边界。

- Chat 注册登录入口：`chat/internal/api/chat/chat.go`。
- Chat RPC 登录实现：`chat/internal/rpc/chat/login.go`。
- Chat Token：`chat/internal/rpc/admin/token.go`、`chat/pkg/common/db/cache/token.go`。
- Chat 调 OpenIM：`chat/pkg/common/imapi/caller.go`。
- OpenIM Token：`open-im-server/internal/rpc/auth/auth.go`、`open-im-server/pkg/common/storage/controller/auth.go`。
- WebSocket 鉴权：`open-im-server/internal/msggateway/ws_server.go`。

面试目标：能解释 Chat Token、OpenIM Admin Token、OpenIM User Token 为什么不能混用。

## 阶段三：攻克消息主链路

按“同步入口 + 异步流水线 + SDK 补拉”理解。

- 发送入口：`open-im-server/internal/rpc/msg/send.go`。
- MQ 写入：`open-im-server/pkg/common/storage/controller/msg.go`。
- Redis 阶段：`open-im-server/internal/msgtransfer/online_history_msg_handler.go`。
- Mongo 阶段：`open-im-server/internal/msgtransfer/online_msg_to_mongo_handler.go`。
- Push 阶段：`open-im-server/internal/push/push_handler.go`。
- SDK 同步：`openim-sdk-core/internal/interaction/msg_sync.go`。

面试目标：能回答“发送成功是不是等于消息落库”和“push 丢了怎么办”。

## 阶段四：理解在线状态、多端和群聊

这部分最能体现 IM 系统的工程复杂度。

- 多端踢端：`open-im-server/internal/msggateway/ws_server.go` 的 `multiTerminalLoginChecker`、`KickUserConn`。
- token 失效：`open-im-server/internal/rpc/auth/auth.go` 的 `KickTokens`、`ForceLogout`、`InvalidateToken`。
- 在线状态查询：`open-im-server/internal/api/user.go`、`open-im-server/internal/msggateway/user_map.go`。
- 群成员展开：`open-im-server/internal/push/push_handler.go` 的 `Push2Group`、`groupMessagesHandler`。
- 群会话创建：`open-im-server/internal/msgtransfer/online_history_msg_handler.go` 调用 `CreateGroupChatConversations`。

面试目标：能解释 5 万人群“能建群/能连接/能 fanout/能稳定处理消息”不是一回事。

## 阶段五：回到 SDK

OpenIM 的可靠性很大一部分放在 SDK 端完成。

- 同步器：`openim-sdk-core/pkg/syncer/syncer.go`。
- 版本同步：`openim-sdk-core/pkg/syncer/version_synchronizer.go`。
- 消息同步：`openim-sdk-core/internal/interaction/msg_sync.go`。
- 本地库：`openim-sdk-core/pkg/db`、`openim-sdk-core/wasm/indexdb`。

面试目标：能解释为什么 push 到达后 SDK 还要 pull，为什么本地库不是缓存那么简单。
