# 07 单聊第一阶段：消息发送流程

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（七）：单聊第一阶段：消息发送流程》：https://juejin.cn/post/7517497597885235237

## 本文目标

按真实调用链解释单聊消息从 SDK 发出到 Msg RPC 写入 Kafka 的同步入口阶段。

## 核心结论

- 单聊发送入口最终在 `sendMsgSingleChat`。
- Msg RPC 负责校验和入 MQ，不直接落 Mongo。
- 发送成功不等于接收方收到，也不等于 MongoDB 已落库。
- 单聊发送阶段要处理关系、黑名单、接收选项、webhook 等业务规则。

## 源码入口

- SDK：`openim-sdk-core/internal/conversation_msg`
- gateway：`open-im-server/internal/msggateway`
- Msg RPC：`open-im-server/internal/rpc/msg/send.go`
- 校验：`open-im-server/internal/rpc/msg/verify.go`
- MQ：`open-im-server/pkg/common/storage/controller/msg.go`
- Kafka topic：`open-im-server/config/kafka.yml` 的 `toRedis`

## 详细源码解析

SDK 先构造本地消息和 `clientMsgID`，通过 WebSocket 发送到 gateway。gateway 已在建连阶段验证 OpenIM User Token，因此请求进入后会根据命令转给 Msg RPC。

`Msg RPC` 的 `SendMsg` 根据 `SessionType` 分发。单聊进入 `sendMsgSingleChat`。该函数先调用 `messageVerification` 做消息校验，然后处理接收方会话接收选项。如果用户设置了不接收或免打扰相关选项，消息可能被修改或不发送。

校验通过后，代码调用 `webhookBeforeMsgModify`，允许业务侧在发送前修改消息。最后调用 `m.MsgDatabase.MsgToMQ(ctx, conversationID, msgData)` 写入 Kafka。

返回的 `SendMsgResp` 包含 `ServerMsgID`、`ClientMsgID`、`SendTime`。这时消息进入异步流水线。

## 数据结构与存储

- `sdkws.MsgData`：消息主体，包含 sendID、recvID、clientMsgID、serverMsgID、contentType、sessionType、options 等。
- conversation key：`conversationutil.GenConversationUniqueKeyForSingle(sendID, recvID)`。
- Kafka：`toRedis`。
- 当前阶段通常还没有 MongoDB 最终落库。

## 设计取舍

把同步发送阶段控制得较短，可以降低用户发送请求的尾延迟。代价是成功语义变弱：业务必须接受“已入队”和“已落库/已投递”之间存在时间差。

## 面试讲法

> 单聊发送第一阶段是同步入口。SDK 经 WebSocket 到 gateway，再转 Msg RPC。Msg RPC 做关系、黑名单、接收选项和 webhook 校验，通过后写 Kafka `toRedis` 并返回。这个成功表示消息进入服务端异步流水线，不保证已经落 Mongo 或接收方已收到。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 发送成功等于什么？ | 语义边界 | 校验通过并入 MQ | `sendMsgSingleChat` | 等同于已读/已收 |
| 校验在哪做？ | 服务端权威 | Msg RPC `messageVerification` | `verify.go` | 交给 SDK |
| 为什么要 webhook？ | 扩展点 | 发送前修改/审核/拦截 | `webhookBeforeMsgModify` | 忽略业务扩展 |
| MQ key 为什么按会话？ | 顺序性 | 同会话顺序处理更容易 | `conversationutil` | 随机 key |
| 如果 Kafka 写失败？ | 错误处理 | SendMsg 返回失败，消息不进入后续链路 | `MsgToMQ` | 仍说发送成功 |

## 和本地项目的关系

当前源码可直接从 `sendMsgSingleChat` 追踪到 `MsgToMQ`，是理解 OpenIM 消息系统的第一入口。

## 小结

单聊发送阶段的重点是“短同步链路 + 异步后处理”。面试时一定要区分入队、落库、投递和已读。
