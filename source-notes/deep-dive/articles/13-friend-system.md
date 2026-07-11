# 13 好友系统架构与业务设计

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十三）：好友系统架构与业务设计》：https://juejin.cn/post/7517906186264789018

## 本文目标

解释好友、好友申请、黑名单、关系缓存和消息校验之间的关系。

## 核心结论

- 好友系统是消息权限校验的重要依赖，不只是通讯录展示。
- 好友、申请、黑名单分成不同模型。
- Redis cache 用于加速好友 ID、黑名单等关系查询。
- SDK 端需要同步好友关系变化，否则 UI 和服务端权限会不一致。

## 源码入口

- API：`open-im-server/internal/api/friend.go`
- RPC：`open-im-server/internal/rpc/relation/friend.go`
- 通知：`open-im-server/internal/rpc/relation/notification.go`
- 存储模型：`open-im-server/pkg/common/storage/model/friend.go`、`friend_request.go`、`black.go`
- 存储控制：`open-im-server/pkg/common/storage/controller/friend.go`
- Redis cache：`open-im-server/pkg/common/storage/cache/redis/friend.go`
- 消息校验：`open-im-server/internal/rpc/msg/verify.go`

## 详细源码解析

好友申请入口在 `ApplyToAddFriend`，好友删除在 `DeleteFriend`，好友列表查询在 `GetFriendIDs`。这些 RPC 会操作 `Friend`、`FriendRequest` 和相关通知。

黑名单模型单独存在，因为“不是好友”和“在黑名单”语义不同。消息发送校验时需要判断双方关系和接收策略，黑名单会直接影响消息能否发送。

Redis `FriendCacheRedis` 提供 `GetFriendIDs` 等缓存能力。高频发送校验如果每次都查 MongoDB，会增加消息入口延迟。

## 数据结构与存储

- `friends`：好友关系。
- `friend_requests`：好友申请。
- `blacks`：黑名单关系。
- Redis friend cache：好友 ID 列表、关系热点缓存。
- SDK 本地：好友列表、申请状态、黑名单。
- version log：关系变化需要同步到 SDK。

## 设计取舍

好友关系影响发送权限，因此必须以服务端为准；SDK 本地关系用于展示和预校验，但不能作为最终权限判断。Redis cache 提升校验性能，但关系变更时要注意缓存失效和增量同步。

## 面试讲法

> 好友系统不仅是通讯录，它会参与消息发送校验。OpenIM 把好友、好友申请、黑名单分开建模，服务端 Msg RPC 校验时会查关系和黑名单，Redis 缓存热点关系，SDK 端通过增量同步更新本地好友列表。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 好友关系只影响 UI 吗？ | 权限 | 也影响发消息校验 | `verify.go` | 只说通讯录 |
| 黑名单和非好友区别？ | 关系语义 | 黑名单是显式拒收/限制 | `black.go` | 混为一谈 |
| 为什么要 Redis 缓存？ | 性能 | 发送校验高频查关系 | `cache/redis/friend.go` | 每次查 Mongo |
| SDK 关系不一致怎么办？ | 同步 | version log/增量同步修复 | SDK syncer | 只重登 |
| 删除好友消息怎么处理？ | 业务规则 | 后续发送按关系校验失败或受限 | `friend.go`、`verify.go` | 历史消息也删除 |

## 和本地项目的关系

本地源码有完整 relation RPC 和 storage/cache 层，可以结合消息校验一起读。

## 小结

好友系统要和消息权限、缓存、增量同步一起讲，不能只停留在 CRUD。
