# WebSocket 网关设计

## 网关职责

`msggateway` 是 OpenIM 的长连接接入层，主要职责不是处理完整业务，而是维护连接、鉴权、路由请求和承接服务端推送。

源码入口：

- 启动入口：`open-im-server/cmd/openim-msggateway/main.go`
- 网关主体：`open-im-server/internal/msggateway/ws_server.go`
- 连接上下文：`open-im-server/internal/msggateway/context.go`
- 长连接封装：`open-im-server/internal/msggateway/long_conn.go`
- 用户连接表：`open-im-server/internal/msggateway/user_map.go`
- 在线状态订阅：`open-im-server/internal/msggateway/subscription.go`

## 建连流程

```text
SDK 发起 WebSocket 请求
  -> gateway 解析 token/sendID/platformID/operationID/sdkType
  -> 调 Auth RPC ParseToken
  -> 校验 token 归属和 platformID
  -> 校验当前连接数是否超过 websocketMaxConnNum
  -> WebSocket upgrade
  -> 创建 Client 并写入本节点 user map
  -> 执行多端登录策略
  -> 向其他 gateway 节点广播在线信息
```

关键源码：

- `UserConnContext.ParseEssentialArgs`：从请求中解析必要参数。
- `WsServer.validate`：调用 Auth RPC 校验 token。
- `WsServer.validateRespWithRequest`：校验 Auth 返回结果和请求参数是否一致。
- `WsServer.SetKickHandlerInfo`、`multiTerminalLoginChecker`：处理多端策略。

## 为什么网关不直接处理全部消息

设计推断：网关是连接密集型服务，重心在 fd、内存、心跳、读写协程和连接映射。如果把好友关系校验、群成员校验、消息写库、离线推送都放进网关，连接层会被业务慢逻辑拖垮。

OpenIM 的做法是：

- WebSocket 负责请求承载和推送通道。
- Msg RPC 负责消息业务校验。
- msgtransfer 负责异步 seq/缓存/落库。
- push 负责在线和离线投递。

这样 gateway 可以按连接数扩容，msg/push/msgtransfer 可以按消息量扩容。

## 多端连接管理

源码依据：

- `open-im-server/internal/msggateway/ws_server.go` 的 `multiTerminalLoginChecker`
- `open-im-server/internal/msggateway/ws_server.go` 的 `KickUserConn`
- `open-im-server/internal/rpc/auth/auth.go` 的 `KickTokens`
- `open-im-server/pkg/common/config/config.go` 中 longConn 和多端策略相关配置

设计上要同时处理两件事：

1. 当前连接是否要踢掉旧连接。
2. 被踢连接对应的旧 token 是否要置为失效。

只踢连接不改 token，旧客户端可能重连；只改 token 不踢连接，旧连接可能短时间还在。

## 在线状态

`msggateway` 本地掌握真实连接对象，因此在线状态首先发生在 gateway 内存中。跨节点时，API 或 push 需要汇总多个 gateway 的在线结果。

源码依据：

- `open-im-server/internal/api/user.go` 的 `GetUsersOnlineStatus`：遍历 gateway RPC 连接汇总在线状态。
- `open-im-server/internal/msggateway/user_map.go`：维护本节点 user/platform/client 映射。
- `open-im-server/internal/msggateway/subscription.go`：用户在线状态订阅和推送。

面试表达：在线状态是“强实时但弱持久”的状态，不能简单当作 MongoDB 里的用户字段。真正在线与否取决于网关连接、心跳、断线清理、跨节点同步和缓存状态。

## 推送入口

`push` 服务不直接拥有客户端 socket，它要通过 gateway 把消息发给具体连接。

相关源码：

- `open-im-server/internal/push/push_handler.go` 的 `GetConnsAndOnlinePush`
- `open-im-server/internal/push/onlinepusher.go`
- `open-im-server/internal/msggateway` 的 push RPC handler

设计意义：连接在哪里，推送就应该交给那个 gateway 节点完成。否则 push 服务需要维护所有 socket，会破坏职责边界。

## 面试追问

| 问题 | 回答思路 |
| --- | --- |
| WebSocket 鉴权在哪里做？ | gateway 解析参数后调 Auth RPC `ParseToken`，不是 Chat Token 直接过。 |
| gateway 为什么要拆出来？ | 连接密集型和业务计算型负载不同，拆开后可以分别扩容。 |
| push 服务为什么不直接写 socket？ | socket 在 gateway，本地连接对象不能跨进程直接访问。 |
| 多端踢端只断连接够吗？ | 不够，还要让旧 token 失效。 |
| 在线状态为什么不好做强一致？ | 连接断开、网络抖动、跨节点同步都有延迟，通常追求最终准确和及时通知。 |
