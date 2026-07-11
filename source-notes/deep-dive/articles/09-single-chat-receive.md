# 09 单聊第三阶段：消息接收流程

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（九）：单聊第三阶段：消息接收流程》：https://juejin.cn/post/7517474788174184485

## 本文目标

解释 SDK 收到 push 后如何判断 seq、补拉缺失消息、写本地库并回调 UI。

## 核心结论

- 客户端接收不是“收到 WebSocket 消息就展示”这么简单。
- SDK 必须判断会话 seq 是否连续。
- 不连续时按 seq 范围补拉。
- 本地库写入成功后才适合回调 UI 展示。

## 源码入口

- `openim-sdk-core/internal/interaction/msg_sync.go`
- `openim-sdk-core/internal/conversation_msg`
- `openim-sdk-core/pkg/db`
- `openim-sdk-core/wasm/indexdb/chat_log_model.go`
- 服务端拉取接口：`open-im-server/internal/rpc/msg`

## 详细源码解析

SDK 长连接收到 push 后，会进入消息同步器。`pushTriggerAndSync` 会遍历 pushMessages，按 conversationID 检查服务端推来的消息 seq 是否和本地 `syncedMaxSeqs` 连续。

如果连续，SDK 可以走快路径写本地库并触发消息回调。如果发现 `lastSeq > localSeq + messageCount` 或中间存在缺口，则构造需要同步的 seq 范围，调用 `syncAndTriggerMsgs` 补拉。

连接恢复时，`doConnected` 会主动向服务端获取 max seq，再调用 `compareSeqsAndBatchSync` 补齐断线期间缺失的消息。

## 数据结构与存储

- SDK 内存：`syncedMaxSeqs`。
- SDK 本地库：`LocalChatLogs`、notification seq、conversation seq。
- 服务端 Redis/Mongo：按 conversationID + seq 返回消息。
- `PullMsgs`：push 或 pull 的消息集合。

## 设计取舍

SDK 端维护同步状态，会增加客户端复杂度，但这是 IM 弱网可靠性的关键。如果服务端只靠 push，断线期间消息很容易丢；如果客户端每次都全量拉，性能和体验都差。seq 补拉在实时性和完整性之间取得平衡。

## 面试讲法

> SDK 收到 push 后不会盲目展示，而是先用 conversationID 的本地 max seq 和服务端消息 seq 对比。连续就写本地库并回调；不连续就按缺失 seq 范围补拉。断线重连时也会先获取服务端 max seq，再补齐缺口。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| push 到了为什么还 pull？ | 完整性 | 检查 seq 是否连续 | `pushTriggerAndSync` | 认为 push 必然完整 |
| 重连后怎么补消息？ | 断线恢复 | 获取 max seq 后批量同步 | `doConnected` | 只靠离线推送 |
| 本地 seq 存哪？ | SDK 状态 | 内存 + 本地库 | `LoadSeq`、`LocalChatLogs` | 只在内存 |
| 补拉范围怎么定？ | seq 差异 | `[local+1, remote]` | `compareSeqsAndBatchSync` | 全量拉 |
| UI 何时更新？ | 本地一致 | 写本地库后回调 | SDK db/回调 | 收到包就展示 |

## 和本地项目的关系

本地 `sources/openimsdk/openim-sdk-core/internal/interaction/msg_sync.go` 中也有完整注释和实现，可作为 SDK 学习主入口。

## 小结

消息接收阶段体现了 OpenIM 的可靠性设计：push 管实时，seq + pull 管完整。
