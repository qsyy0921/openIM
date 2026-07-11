# 12 群聊读扩散机制

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十二）：群聊读扩散机制》：https://juejin.cn/post/7517854096175497242

## 本文目标

解释群消息为什么更偏读扩散/会话流模型，以及它和在线 fanout 的区别。

## 核心结论

- 群消息持久化不应简单理解成给每个群成员写一份完整消息。
- 当前源码中群消息按群 conversation key 进入消息流。
- 在线推送仍然需要展开群成员，这是 fanout 成本。
- 读扩散降低写入放大，但会把部分成本转移到读取、同步和在线推送。

## 源码入口

- `open-im-server/internal/rpc/msg/send.go`：`sendMsgGroupChat`
- `open-im-server/internal/msgtransfer/online_history_msg_handler.go`
- `open-im-server/pkg/common/storage/controller/msg_transfer.go`
- `open-im-server/internal/push/push_handler.go`
- `open-im-server/internal/rpc/group/group.go`
- `openim-sdk-core/internal/interaction/msg_sync.go`

## 详细源码解析

群消息发送时，`sendMsgGroupChat` 使用群 conversation key 写入 MQ。`msgtransfer` 对该 conversationID 分配 seq，并写 Redis cache/Mongo。这个阶段更像维护“一条群消息流”。

当群会话第一次出现时，`msgtransfer` 会获取群成员并创建群会话记录。这不是给每个成员复制完整消息，而是让成员拥有该群会话入口和同步位置。

在线推送阶段是另一回事。`Push2Group` 必须调用 `groupMessagesHandler` 展开群成员，并对在线成员投递 WebSocket。这是 fanout，不等同于持久化写扩散。

SDK 读取时根据群 conversationID 和 seq 同步消息。如果本地缺 seq，就从服务端补拉。

## 数据结构与存储

- 群消息流：conversationID = group conversation。
- 群成员：`group_members`。
- 群会话：`conversations`。
- seq：群会话 seq。
- Kafka：`toRedis` -> `toMongo`/`toPush`。
- SDK：群会话本地 seq 和消息表。

## 设计取舍

读扩散的优势是发送入口和持久化写入不会随群成员数线性复制完整消息。缺点是大群成员拉取、未读数、在线推送、离线推送、客户端补拉都会变复杂。尤其在线 fanout 仍然与在线成员数相关。

## 面试讲法

> OpenIM 的群消息不能简单说成每个成员一份 inbox。源码里群消息按群 conversation key 进入消息流，msgtransfer 分配群会话 seq 并存储；在线推送阶段再展开成员 fanout。所以它更接近读扩散/会话流模型，但实时推送仍然有 fanout 成本。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 读扩散省了什么？ | 写放大 | 不为每个成员复制完整消息 | `GenConversationUniqueKeyForGroup` | 说完全无成本 |
| 成本转移到哪里？ | 取舍 | 读取、同步、在线 fanout | `Push2Group`、SDK sync | 只说性能好 |
| 在线推送是不是写扩散？ | 概念区分 | 是实时 fanout，不是持久化写扩散 | `groupMessagesHandler` | 混为一谈 |
| 大群消息怎么测？ | 压测设计 | 测 send QPS、fanout、延迟、Kafka lag | push/msgtransfer | 只测建群 |
| 群成员变更影响？ | 同步 | 会话/成员增量同步和推送目标变化 | `group.go`、SDK sync | 静态成员 |

## 和本地项目的关系

本地源码中群消息路径从 `sendMsgGroupChat` 到 `Push2Group` 都可验证。后续压测应单独测群消息 QPS 和 fanout 延迟。

## 小结

群聊读扩散的核心是减少持久化写放大，但在线 fanout 仍是大群系统绕不开的成本。
