# seq、可靠性、补拉和最终一致性

## 为什么 seq 是 IM 系统核心

IM 系统不能只靠“服务端 push 一次”保证可靠，因为网络、连接、客户端本地库都会失败。OpenIM 使用每个会话的 seq 作为消息流位置，让客户端可以判断自己是否缺消息。

## 服务端 seq 分配

源码依据：

- `open-im-server/pkg/common/storage/controller/msg_transfer.go` 的 `BatchInsertChat2Cache`
- `open-im-server/pkg/common/storage/cache/redis/seq_conversation.go`
- `open-im-server/pkg/common/storage/cache/redis/seq_user.go`
- `open-im-server/pkg/common/storage/model/seq.go`
- `open-im-server/pkg/common/storage/model/seq_user.go`

流程：

```text
msgtransfer 收到一批同会话消息
  -> seqConversation.Malloc(conversationID, count)
  -> currentMaxSeq 递增
  -> 为每条消息写入 msg.Seq
  -> Redis 缓存 seq->msg
  -> Mongo 异步持久化
```

设计意义：seq 分配集中在异步处理阶段，有利于保持同会话消息顺序，并把入口并发写入转换成顺序消息流。

## SDK 对齐 seq

源码依据：

- `openim-sdk-core/internal/interaction/msg_sync.go`
- `compareSeqsAndBatchSync`
- `pushTriggerAndSync`
- `doConnected`
- `syncAndTriggerMsgs`
- `pullMsgBySeqRange`

SDK 场景：

- 建连后：`doConnected` 获取服务端 max seq，和本地 seq 对比。
- 收到 push 后：`pushTriggerAndSync` 判断 push 中消息 seq 是否连续。
- 发现缺口：按 `[localSeq+1, remoteSeq]` 范围补拉。
- 补拉完成：写本地库，触发 UI 回调。

## 最终一致性模型

可以把 OpenIM 的消息可靠性理解为四个状态：

1. 已接收：Msg RPC 校验通过并写入 Kafka。
2. 已排序：msgtransfer 分配 seq 并写 Redis。
3. 已持久：MongoDB 持久化。
4. 已同步：SDK 本地库补齐到对应 seq。

用户看到“发送成功”通常接近第 1 或第 2 阶段，不能直接等同于第 3 和第 4 阶段。

## 补拉为什么必要

补拉解决的问题：

- 在线 push 丢失。
- 客户端断线重连。
- App 后台被系统杀掉。
- 多端登录时某个端错过消息。
- 本地库损坏或重装后需要重新同步。

push 的价值是降低实时延迟，pull 的价值是保证最终完整。

## 消息什么时候才算最终可靠

服务端视角：消息有 seq，已进入持久化链路，并且 MongoDB 最终可查。

客户端视角：SDK 本地库已经按 seq 写入，并回调上层 UI。

系统视角：即使 push 失败，客户端后续通过 max seq 和 range pull 仍然能补齐。

## 面试追问

| 问题 | 回答思路 |
| --- | --- |
| seq 为什么按会话分配？ | 客户端通常按会话展示和补拉，按会话 seq 能降低全局序列争用。 |
| push 收到但 seq 不连续怎么办？ | 不直接信任 push，按缺口范围补拉。 |
| Mongo 落库慢会影响客户端吗？ | 如果 Redis cache 还可用，近期补拉可能不受影响；历史查询和最终持久化会滞后。 |
| Kafka 重复消费怎么办？ | 需要依赖 seq、clientMsgID、serverMsgID、Mongo 分块更新等机制做幂等或重复处理。 |
| App 重装后怎么恢复？ | 本地 seq 为空或较小，SDK 从服务端 max seq 和历史接口重新同步。 |
