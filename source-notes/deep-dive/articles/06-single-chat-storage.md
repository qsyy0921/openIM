# 06 单聊核心存储结构

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（六）：单聊核心存储结构》：https://juejin.cn/post/7517474788174151717

## 本文目标

解释单聊消息、会话、seq、已读位置、Redis cache 和 MongoDB 分块文档之间的关系。

## 核心结论

- 单聊消息按 conversationID 组织，而不是简单按接收者 inbox 存储。
- seq 是会话内消息顺序坐标。
- Redis 保存近期消息和 seq/cache，MongoDB 保存持久化分块文档。
- 会话数据和消息数据分离，便于会话列表和历史消息分别优化。

## 源码入口

- `open-im-server/internal/rpc/msg/send.go`
- `open-im-server/pkg/util/conversationutil`
- `open-im-server/pkg/common/storage/model/msg.go`
- `open-im-server/pkg/common/storage/model/conversation.go`
- `open-im-server/pkg/common/storage/model/seq.go`
- `open-im-server/pkg/common/storage/model/seq_user.go`
- `open-im-server/pkg/common/storage/controller/msg_transfer.go`
- `open-im-server/pkg/common/storage/database/mgo/msg.go`

## 详细源码解析

单聊发送时，`sendMsgSingleChat` 使用 `conversationutil.GenConversationUniqueKeyForSingle(sendID, recvID)` 生成会话唯一 key，并调用 `MsgToMQ` 写入 Kafka。

`msgtransfer` 消费后调用 `BatchInsertChat2Cache`。这个函数先通过 `seqConversation.Malloc` 为当前 conversationID 申请 seq 区间，再给每条消息设置 `MsgData.Seq`，然后写 Redis message cache。

持久化阶段由 `BatchInsertChat2DB` 和 `BatchInsertBlock` 完成。`MsgDocModel` 通过 `GetDocID(conversationID, seq)` 和 `GetMsgIndex(seq)` 将多条消息放入一个 MongoDB 文档块中。

## 数据结构与存储

- conversationID：单聊双方生成唯一会话 ID。
- `SeqConversation`：会话最大 seq。
- `SeqUser`：用户在会话中的已读 seq。
- Redis message cache：conversationID + seq -> message。
- Mongo `MsgDocModel`：按 conversationID 和 seq 分块保存消息。
- 会话模型：`model/conversation.go` 保存会话属性、置顶、免打扰、最大 seq 等。

## 设计取舍

按会话 seq 存储适合聊天窗口按时间顺序拉取；Mongo 分块减少 document 数量，但比单条 document 复杂，需要处理块更新、重复插入和 seq 校验。Redis cache 能提升近期补拉速度，但 Redis 故障会影响消息流推进。

## 面试讲法

> 单聊消息不是简单写一张 messages 表，而是按 conversationID 维护消息流。msgtransfer 为每个会话分配连续 seq，近期消息写 Redis cache，历史消息按 seq 分块写 MongoDB。SDK 本地也按 conversationID + seq 判断是否缺消息。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 单聊 seq 是全局的吗？ | 顺序模型 | 更偏会话内 seq | `SeqConversation` | 说成全局自增 |
| 为什么按会话存？ | 读取模式 | 聊天窗口按会话范围读 | `conversationutil` | 按用户 inbox 简化 |
| Redis 里保存什么？ | 热点数据 | 近期消息、seq/cache、已读状态 | `cache/msg.go` | 只说缓存 |
| Mongo 如何存消息？ | 分块文档 | `MsgDocModel` 按 docID/index 存 | `model/msg.go` | 一条消息一个文档 |
| 已读位置在哪里？ | 读状态 | `SeqUser` 和 hasReadSeq | `seq_user.go` | 和消息体混在一起 |

## 和本地项目的关系

本地源码可从 `sendMsgSingleChat` 追到 `BatchInsertChat2Cache` 和 `BatchInsertChat2DB`，完整看到单聊存储结构。

## 小结

单聊存储的核心是 conversationID + seq。理解它，后续消息发送、接收、回执和补拉才讲得清楚。
