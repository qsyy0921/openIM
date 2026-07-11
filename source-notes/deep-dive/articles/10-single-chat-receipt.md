# 10 单聊第四阶段：消息回执流程

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十）：单聊第四阶段：消息回执流程》：https://juejin.cn/post/7517520859864449033

## 本文目标

解释已读回执如何作为一种通知消息进入消息流，并更新用户 hasReadSeq。

## 核心结论

- 回执不是简单更新一个消息字段，而是和 seq/会话读位置相关。
- 已读通知会进入消息处理链路。
- `msgtransfer` 会识别 `HasReadReceipt` 并更新 hasReadSeq。
- 读状态和消息体分离，便于按会话维护。

## 源码入口

- `open-im-server/internal/msgtransfer/online_history_msg_handler.go`
- `doSetReadSeq`
- `HandleUserHasReadSeqMessages`
- `open-im-server/pkg/common/storage/controller/msg_transfer.go`
- `open-im-server/pkg/common/storage/model/seq_user.go`
- `open-im-server/internal/rpc/conversation`

## 详细源码解析

`online_history_msg_handler.go` 中 `doSetReadSeq` 会扫描消息列表，找出 `constant.HasReadReceipt`。它解析通知内容中的 `MarkAsReadTips`，计算当前用户对某个 conversationID 的最大已读 seq。

随后它调用 `SetHasReadSeqToDB` 或通过 `conversationUserHasReadChan` 异步写入。`msg_transfer.go` 中 `SetHasReadSeqs` 和 `SetHasReadSeqToDB` 会更新 `seqUser`。

这说明已读回执本质上是“某用户读到了某会话的哪个 seq”，而不是给每条消息单独打一个全局已读布尔值。

## 数据结构与存储

- `HasReadReceipt`：已读回执通知类型。
- `MarkAsReadTips`：包含 conversationID、hasReadSeq、seqs、markAsReadUserID。
- `SeqUser`：用户维度的已读 seq。
- conversation read state：会话最大读位置、未读数计算依赖它。

## 设计取舍

按会话 hasReadSeq 维护读状态，比逐条消息维护每个用户已读状态更轻量。代价是复杂回执场景需要根据 seq 范围推导，且群聊回执会更复杂。

## 面试讲法

> 单聊回执不是每条消息一个已读字段，而是维护用户在会话里的 hasReadSeq。客户端标记已读后产生回执通知，msgtransfer 在异步链路中解析 `HasReadReceipt`，更新用户对该 conversationID 的已读位置。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 回执是同步更新吗？ | 异步链路 | 在 msgtransfer 中处理 | `doSetReadSeq` | 直接改消息 |
| 已读状态存哪里？ | 读位置 | `SeqUser`/hasReadSeq | `seq_user.go` | 每消息布尔 |
| 为什么用 seq 表示已读？ | 顺序模型 | 已读到某位置即可推导前序 | `MarkAsReadTips` | 不理解连续性 |
| 群聊回执难在哪？ | 规模 | 多成员已读状态成本高 | 群读状态 | 套单聊模型 |
| 回执失败会怎样？ | 一致性 | 未读数可能短暂不准，可重试/同步 | `SetHasReadSeqToDB` | 消息丢失 |

## 和本地项目的关系

本地 `online_history_msg_handler.go` 可以直接看到回执在消息异步链路中的处理。

## 小结

回执的核心是 hasReadSeq。理解它可以更好解释未读数、会话同步和 SDK 本地状态。
