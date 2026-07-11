# msgtransfer、存储与推送

## msgtransfer 的定位

`msgtransfer` 是 OpenIM 消息系统的异步核心。它不是简单搬运 Kafka 消息，而是在这里完成：

- 消费 `toRedis`。
- 按会话聚合和批量处理。
- 区分需要存储和不需要存储的消息/通知。
- 分配会话 seq。
- 写 Redis message cache。
- 写用户已读 seq。
- 转发到 `toMongo`。
- 转发到 `toPush`。

源码入口：

- `open-im-server/cmd/openim-msgtransfer/main.go`
- `open-im-server/internal/msgtransfer/init.go`
- `open-im-server/internal/msgtransfer/online_history_msg_handler.go`
- `open-im-server/internal/msgtransfer/online_msg_to_mongo_handler.go`
- `open-im-server/pkg/common/storage/controller/msg_transfer.go`

## toRedis 阶段

`OnlineHistoryRedisConsumerHandler` 消费 Kafka `toRedis`。它使用 batcher 按 key 分片处理，同一会话 key 会落到相对固定的 worker。

关键函数：

- `NewOnlineHistoryRedisConsumerHandler`
- `do`
- `parseConsumerMessages`
- `categorizeMessageLists`
- `handleMsg`
- `handleNotification`

设计意义：在这里把入口的“单条请求”转成后台的“批量消息流”，更适合 Redis 和 Mongo 的批量写入。

## seq 分配和 Redis cache

源码依据：`open-im-server/pkg/common/storage/controller/msg_transfer.go` 的 `BatchInsertChat2Cache`。

流程：

```text
BatchInsertChat2Cache
  -> seqConversation.Malloc(conversationID, len(msgs))
  -> 为每条消息递增设置 msg.Seq
  -> 构造 userSeqMap，更新发送者 hasReadSeq
  -> msgCache.SetMessageBySeqs
  -> 返回 lastSeq / isNewConversation / userSeqMap
```

设计意义：

- seq 在异步阶段统一分配，避免入口并发写时产生乱序。
- Redis cache 让 SDK 补拉近期消息时更快。
- `userSeqMap` 让发送者自己的已读位置可以同步推进。

## toMongo 阶段

源码依据：

- 写入 MQ：`MsgToMongoMQ`
- 消费 MQ：`open-im-server/internal/msgtransfer/online_msg_to_mongo_handler.go`
- 真正落库：`BatchInsertChat2DB`、`BatchInsertBlock`

消息落库采用分块模型，按 conversationID 和 seq 计算文档 ID 与数组下标。这种设计能减少 MongoDB document 数量，但要求 seq 连续、块边界清晰。

## toPush 阶段

源码依据：

- `online_history_msg_handler.go` 的 `toPushTopic`
- `open-im-server/internal/push/push_handler.go` 的 `handleMs2PsChat`
- 单聊：`Push2User`
- 群聊：`Push2Group`
- 在线投递：`GetConnsAndOnlinePush`
- 离线推送：`asyncOfflinePush`、`offlinepush_handler.go`

单聊 push：

```text
Push2User
  -> webhookBeforeOnlinePush
  -> GetConnsAndOnlinePush
  -> 判断在线推送结果
  -> 需要时触发 offline push
```

群聊 push：

```text
Push2Group
  -> webhookBeforeGroupOnlinePush
  -> groupMessagesHandler 展开群成员
  -> GetConnsAndOnlinePush
  -> 失败用户过滤
  -> toOfflinePush
```

## push 为什么不等于可靠送达

push 只是低延迟通知路径，有几个天然问题：

- WebSocket 连接可能刚好断开。
- gateway 节点可能重启。
- 在线状态可能短时间不准确。
- 客户端收到 push 后本地落库也可能失败。
- 群聊 fanout 中部分用户成功、部分用户失败是常态。

因此 SDK 必须基于 seq 做补拉。真正可靠的判断标准是“服务端消息流中有 seq，客户端最终补齐到该 seq 并写入本地库”。

## 面试讲法

> `msgtransfer` 是 OpenIM 消息可靠性的中心，它消费 `toRedis`，按会话分配 seq，写 Redis cache，然后把消息分别送到 Mongo 落库和 push 投递。它让发送入口不用等待慢操作，但也让系统变成异步最终一致。所以面试里我会把发送成功、落库成功、在线 push 成功和客户端最终可见区分开讲。
