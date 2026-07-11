# 11 群聊系统架构与业务设计

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十一）：群聊系统架构与业务设计》：https://juejin.cn/post/7517558404753653786

## 本文目标

解释群、群成员、群会话、群消息发送和在线 fanout 的核心设计。

## 核心结论

- 群聊不是单聊的简单 N 倍。
- 群消息存储按群会话组织，在线推送阶段需要成员展开。
- 首次群消息可能触发群会话创建。
- 大群瓶颈通常在 fanout、在线状态、离线推送和 SDK 同步。

## 源码入口

- 群 RPC：`open-im-server/internal/rpc/group/group.go`
- 群模型：`open-im-server/pkg/common/storage/model/group.go`、`group_member.go`
- 群缓存：`open-im-server/pkg/common/storage/cache/redis/group.go`
- 群消息发送：`open-im-server/internal/rpc/msg/send.go` 的 `sendMsgGroupChat`
- 群会话创建：`open-im-server/internal/msgtransfer/online_history_msg_handler.go`
- 群推送：`open-im-server/internal/push/push_handler.go` 的 `Push2Group`

## 详细源码解析

群的创建、加入、退出、获取成员等逻辑在 `group.go` 中。群成员存储在 `GroupMember` 模型中，缓存层提供 `GetGroupMemberIDs`、`GetGroupMembersInfo` 等能力。

群消息发送时，`sendMsgGroupChat` 会做群成员和权限校验，然后用 `conversationutil.GenConversationUniqueKeyForGroup(groupID)` 作为 MQ key 写入 Kafka。

`msgtransfer` 写缓存和分配 seq 时，如果发现是新会话，会调用 `groupClient.GetGroupMemberUserIDs` 获取群成员，并调用 `conversationClient.CreateGroupChatConversations` 为成员创建群会话。

在线推送阶段，`push_handler.go` 的 `Push2Group` 调用 `groupMessagesHandler` 展开群成员，然后批量判断在线状态并推送。

## 数据结构与存储

- MongoDB：`groups`、`group_members`、`group_requests`、`conversations`、消息分块。
- Redis：群成员缓存、群资料缓存、在线状态。
- Kafka：群消息同样经过 `toRedis`、`toMongo`、`toPush`。
- SDK 本地：群资料、群成员、群会话、群消息。

## 设计取舍

群消息不能简单地在发送入口给每个成员写一份完整消息，否则大群会产生巨大写放大。OpenIM 更接近按群会话维护消息流，在线阶段再做 fanout。这样降低存储写放大，但 fanout 压力仍然存在。

## 面试讲法

> 群聊的复杂点在成员关系和 fanout。OpenIM 的群消息发送会以 group conversation 作为消息流，msgtransfer 分配 seq 并写存储。在线推送时 push 服务展开群成员，判断在线状态，再通过 gateway 投递。大群真正瓶颈不是能不能保存一条消息，而是给多少在线成员实时投递。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 群聊和单聊最大区别？ | fanout | 群成员展开和多用户投递 | `Push2Group` | 只说 recvID 变 groupID |
| 群成员在哪取？ | 成员关系 | Group RPC/cache | `GetGroupMemberUserIDs` | 每次查 Mongo |
| 首次群会话如何创建？ | 会话生成 | msgtransfer 调 conversation RPC | `CreateGroupChatConversations` | 登录时全量创建 |
| 大群瓶颈在哪？ | 性能 | 成员展开、在线状态、gateway push | `groupMessagesHandler` | 只看 Mongo |
| 群消息是否每人存一份？ | 存储模型 | 更接近按群会话消息流 | `GenConversationUniqueKeyForGroup` | 简单写扩散 |

## 和本地项目的关系

本地源码可完整追踪群创建、群成员缓存、群消息发送、群会话创建和群在线推送。

## 小结

群聊架构要围绕“成员关系 + 会话消息流 + 在线 fanout”讲，不能把单聊模型机械放大。
