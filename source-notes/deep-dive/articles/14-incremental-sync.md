# 14 事件增量同步机制

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十四）：事件增量同步机制》：https://juejin.cn/post/7517957492140752947

## 本文目标

解释 OpenIM 如何用 version log 支撑好友、群、会话等非消息数据的增量同步。

## 核心结论

- 消息同步靠 seq，关系/群/会话同步靠 version log。
- 增量同步避免每次登录全量拉取。
- 服务端变更要写 version log，SDK 根据版本差异拉取。
- 增量同步失败时需要回退到全量同步或重试。

## 源码入口

- 服务端模型：`open-im-server/pkg/common/storage/model/version_log.go`
- Mongo 实现：`open-im-server/pkg/common/storage/database/mgo/version_log.go`
- 关键函数：`IncrVersion`、`FindChangeLog`、`BatchFindChangeLog`
- SDK：`openim-sdk-core/pkg/syncer/version_synchronizer.go`
- SDK 领域增量：`openim-sdk-core/internal/relation/incremental_sync.go`、`internal/group/incremental_sync.go`、`internal/conversation_msg/incremental_sync.go`

## 详细源码解析

服务端 `version_log` 保存某类对象的版本变化。每次用户资料、好友、群、会话等对象变化时，服务端递增版本并写 change log。

SDK 的 `VersionSynchronizer` 会保存本地版本。登录、重连或收到变更通知后，SDK 请求服务端对比版本。如果服务端发现客户端版本落后，就返回变更列表，SDK 再按对象类型更新本地库。

消息不走这个模型，而是走 conversation seq。因为消息是高频追加流，使用 seq 更自然；关系/群/会话是低频对象变化，用 version log 更合适。

## 数据结构与存储

- `VersionLog`：对象版本记录。
- `VersionLogElem`：变更元素。
- Mongo collection：version log 相关 collection。
- SDK 本地版本：每类同步器保存本地 version。
- 领域本地表：好友、群、会话、用户资料等。

## 设计取舍

增量同步节省带宽和启动时间，但要求每次服务端变更都正确记录 version log。漏写 change log 会导致客户端长期不一致，因此需要全量同步兜底。

## 面试讲法

> OpenIM 把同步分成两类：消息用 seq，因为消息是按会话顺序追加；好友、群、会话等对象用 version log，因为它们是对象状态变化。SDK 保存本地版本，服务端记录变更日志，重连或登录时按版本差异拉取增量。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 为什么消息不用 version log？ | 数据模型 | 消息是顺序流，seq 更适合 | `msg_sync.go` | 所有数据一套机制 |
| version log 漏写怎么办？ | 兜底 | 全量同步/校验修复 | `syncer.go` | 永不出错 |
| 增量同步触发时机？ | 生命周期 | 登录、重连、通知触发 | SDK syncer | 只首次登录 |
| 服务端存什么？ | 变更记录 | object/version/change log | `version_log.go` | 只存当前值 |
| 客户端如何应用？ | 本地库 | 按对象类型更新本地表 | `incremental_sync.go` | 只放内存 |

## 和本地项目的关系

本地同时有服务端 version log 和 SDK synchronizer 源码，适合把服务端变更和客户端落库连起来读。

## 小结

增量同步体现了 OpenIM 的客户端状态管理能力。消息靠 seq，关系对象靠 version log，这是两套互补机制。
