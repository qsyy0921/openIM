# 08 单聊第二阶段：在线推送流程

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（八）：单聊第二阶段：在线推送流程》：https://juejin.cn/post/7517475973228789786

## 本文目标

解释单聊消息进入异步流水线后，如何从 `toPush` 被 push 服务消费并投递到在线接收端。

## 核心结论

- 在线推送由 `push` 服务完成，不由 Msg RPC 直接完成。
- push 先判断用户在线状态，再通过 gateway 投递。
- 如果在线推送失败，可能进入离线推送，但消息可靠性仍靠 seq 补拉。
- 单聊可根据 `IsSenderSync` 决定是否同步发送者其他端。

## 源码入口

- `open-im-server/internal/msgtransfer/online_history_msg_handler.go`：`toPushTopic`
- `open-im-server/internal/push/push_handler.go`：`handleMs2PsChat`、`Push2User`、`GetConnsAndOnlinePush`
- `open-im-server/internal/push/onlinepusher.go`
- `open-im-server/internal/msggateway`
- Kafka topic：`toPush`、`toOfflinePush`

## 详细源码解析

`msgtransfer` 分配 seq 并写 Redis cache 后，会调用 `toPushTopic` 把消息写入 Kafka `toPush`。`push` 服务消费该 topic，在 `handleMs2PsChat` 中判断消息类型。

单聊进入 `Push2User`。它先执行 `webhookBeforeOnlinePush`，允许业务在在线推送前拦截或修改目标用户。随后调用 `GetConnsAndOnlinePush`：该函数先通过 online cache 得到在线用户和离线用户，再由 `onlinePusher` 找到对应 gateway 连接并投递。

如果接收者不在线或在线投递失败，并且消息 options 允许离线推送，就会触发离线推送逻辑。

## 数据结构与存储

- Kafka `toPush`：在线推送消息。
- Kafka `toOfflinePush`：离线推送消息。
- 在线状态 cache：判断 userID 是否在线。
- gateway 内存连接表：实际 WebSocket 连接。
- `MsgData.Options`：是否离线推送、是否 sender sync、是否计未读等。

## 设计取舍

将 push 从 Msg RPC 拆出，可以避免在线状态查询和 WebSocket 投递拖慢发送入口。代价是 push 失败和延迟要异步监控，客户端不能只依赖 push 完成可靠性。

## 面试讲法

> 单聊在线推送是在消息进入 `toPush` 后由 push 服务处理的。push 服务根据在线状态找到接收者连接，再通过 gateway 投递。如果投递失败，可能走离线推送；但最终消息是否完整仍由 SDK 按 seq 补拉保证。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| push 服务直接持有 socket 吗？ | 职责边界 | socket 在 gateway，push 通过 gateway 投递 | `onlinepusher.go` | push 直接写连接 |
| 如何判断在线？ | 在线状态 | online cache + gateway 结果 | `GetConnsAndOnlinePush` | 只查用户表 |
| 发送者其他端要不要收到？ | 多端同步 | 根据 `IsSenderSync` | `handleMs2PsChat` | 只推接收者 |
| 离线推送何时触发？ | 降级 | 在线失败且 options 允许 | `shouldPushOffline` | 所有失败都离线推 |
| push 成功就可靠吗？ | 可靠性 | 不等于，SDK 还要按 seq 对齐 | `msg_sync.go` | push 即可靠 |

## 和本地项目的关系

本地 `push_handler.go` 可以直接看到单聊推送和离线推送的分支。

## 小结

在线推送解决实时性，不解决全部可靠性。OpenIM 把实时通知和最终补拉分成两条路径。
