# 在线状态与多端登录

## 在线状态是什么

在线状态不是用户资料字段，而是连接层实时状态。它由 WebSocket 连接、platformID、connID、gateway 节点、心跳和缓存共同决定。

源码入口：

- `open-im-server/internal/msggateway/ws_server.go`
- `open-im-server/internal/msggateway/user_map.go`
- `open-im-server/internal/msggateway/subscription.go`
- `open-im-server/internal/api/user.go`
- `open-im-server/pkg/rpccache`

## 本节点连接表

`user_map.go` 维护 userID -> platformID -> clients 的结构。它解决两个问题：

- 当前 gateway 节点有哪些用户在线。
- 给某个 userID/platformID 推送时，应该写哪些连接。

设计推断：连接对象不能放进 MongoDB，只能存在 gateway 进程内存里。跨节点查询时要遍历 gateway RPC 或借助在线状态缓存。

## 跨节点在线状态

源码依据：

- `WsServer.sendUserOnlineInfoToOtherNode`
- `internal/api/user.go` 的 `GetUsersOnlineStatus`
- `subscription.go` 中的在线状态订阅通知

设计意义：多 gateway 部署时，一个用户可能连接在任意节点上。push 服务要投递消息，需要先知道用户是否在线以及在哪些节点有连接。

## 多端登录策略

源码依据：

- `WsServer.multiTerminalLoginChecker`
- `WsServer.KickUserConn`
- `authClient.KickTokens`
- `internal/rpc/auth/auth.go`

常见策略：

- 不踢端：所有端可以同时在线。
- 同平台踢：新 iOS 登录踢旧 iOS。
- 同类端踢：移动端之间互踢，PC/Web 单独处理。
- 全端踢：新登录踢所有旧连接。

面试要点：多端不是只看 userID，还要看 platformID 和 token 状态。否则解释不了“同一用户多个设备”的行为。

## token 失效与连接关闭

踢端要同时完成：

1. gateway 向旧连接发送 KickOnlineMessage 并关闭连接。
2. Auth RPC 将旧 token 标记为失效或被踢。

只做第一步，客户端可能用旧 token 重连。只做第二步，旧连接可能在断开前继续接收消息。

## 在线状态和 push 的关系

`push_handler.go` 中 `GetConnsAndOnlinePush` 会通过 online cache 判断 onlineUserIDs 和 offlineUserIDs。在线用户走 WebSocket push，离线用户根据配置和消息选项进入离线推送。

大群场景下，在线状态查询会被放大：一个群消息要面对成千上万成员，系统必须快速判断哪些在线、哪些离线、哪些免打扰。

## 面试讲法

> OpenIM 的在线状态由 gateway 维护，因为只有 gateway 持有真实 WebSocket 连接。跨节点时通过服务发现和在线缓存汇总。多端登录策略不是单纯踢连接，还要修改 token 状态，确保旧 token 不能继续重连。在线状态服务于 push，但 push 失败并不影响最终可靠性，因为 SDK 还能按 seq 补拉。
