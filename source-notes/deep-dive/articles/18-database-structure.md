# 18 附录二：数据库结构

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十八）：附录二数据库结构》：https://juejin.cn/post/7518572607723929650

## 本文目标

总结 Chat、OpenIM Server、SDK 本地库涉及的核心存储结构，并说明 MongoDB、Redis、Kafka 如何配合。

## 核心结论

- Chat 和 OpenIM Server 有不同数据域。
- MongoDB 负责持久化主体数据，Redis 负责实时状态和热点缓存，Kafka 负责异步流水线。
- 消息存储按 conversationID + seq 分块，不是普通单表插入。
- SDK 本地库是客户端一致性的一部分。

## 源码入口

- Chat 表/模型：`chat/pkg/common/db/table`、`chat/pkg/common/db/model`
- OpenIM 模型：`open-im-server/pkg/common/storage/model`
- Mongo 实现：`open-im-server/pkg/common/storage/database/mgo`
- Redis 实现：`open-im-server/pkg/common/storage/cache/redis`
- Kafka 配置：`open-im-server/config/kafka.yml`
- SDK 本地库：`openim-sdk-core/pkg/db`、`openim-sdk-core/wasm/indexdb`

## 详细源码解析

Chat 数据域包含注册、账号、属性、凭证、验证码、登录记录等。它服务产品层登录，不直接代表 IM 消息能力。

OpenIM 数据域包含用户、好友、黑名单、好友申请、群、群成员、群申请、会话、消息、对象存储记录、version log、seq 等。它服务 IM 能力。

消息数据的特殊点在于 `MsgDocModel`。`BatchInsertBlock` 按 conversationID 和 seq 计算文档块，把多条消息放入一个 MongoDB 文档数组位置。这和传统 `messages(id, conversation_id, seq, content)` 表模型不同。

Redis 侧保存 Token 状态、在线状态、seq、消息 cache、热点关系。Kafka 侧用 `toRedis`、`toMongo`、`toPush`、`toOfflinePush` 串起异步处理。

## 数据结构与存储

| 数据域 | 核心结构 |
| --- | --- |
| Chat | `account`、`attribute`、`credential`、`verify_code`、`register`、`user_login_record` |
| OpenIM 用户 | `users` |
| 关系 | `friends`、`friend_requests`、`blacks` |
| 群 | `groups`、`group_members`、`group_requests` |
| 会话 | `conversations` |
| 消息 | `MsgDocModel` 分块文档 |
| 同步 | `version_log`、`SeqConversation`、`SeqUser` |
| Redis Token | `CHAT_UID_TOKEN_STATUS:{userID}`、`UID_PID_TOKEN_STATUS:{userID}:{platform}` |
| Kafka | `toRedis`、`toMongo`、`toPush`、`toOfflinePush` |
| SDK 本地 | 消息、会话、好友、群、seq、version |

## 设计取舍

MongoDB 文档模型适合 OpenIM 当前的消息体扩展和分块存储；Redis 适合低延迟状态；Kafka 适合异步解耦。代价是数据一致性不再由单个关系数据库事务覆盖，需要靠 seq、version、重试、补拉和监控保证。

## 面试讲法

> OpenIM 的数据库结构要分 Chat 域、IM 服务端域和 SDK 本地域。Chat 保存业务账号；OpenIM 用 Mongo 保存用户、关系、群、会话和消息分块，用 Redis 保存 Token、在线状态和 seq/cache，用 Kafka 串起异步消息流；SDK 本地库保存已同步消息和对象状态。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 消息是一条一行吗？ | 存储模型 | Mongo 分块文档 | `MsgDocModel` | 套 MySQL 表 |
| Chat 和 OpenIM 数据能混吗？ | 数据域 | 账号域和 IM 域分离 | `chat/pkg/common/db`、`storage/model` | 混为一库 |
| Redis 是缓存吗？ | 实时状态 | Token/seq/在线状态是关键状态 | `cache/redis` | 可随便丢 |
| Kafka 数据算最终存储吗？ | MQ 角色 | 异步通道，不是长期历史库 | `kafka.yml` | 当数据库 |
| SDK 本地库重要吗？ | 客户端一致性 | 补拉、离线、重启恢复依赖它 | `pkg/db` | 只是 UI 缓存 |

## 和本地项目的关系

本地源码包含 Chat/OpenIM/SDK 三套存储结构。后续可以用本篇作为数据库反查索引。

## 小结

OpenIM 的存储不是单数据库模型，而是 MongoDB、Redis、Kafka、SDK 本地库协同完成实时和最终一致。
