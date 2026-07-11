# 15 设备端 SDK 核心同步器

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十五）：设备端 SDK 核心同步器》：https://juejin.cn/post/7518212477651484722

## 本文目标

解释 SDK 同步器如何把服务端数据同步到本地库，并协调消息、会话、好友、群等模块。

## 核心结论

- SDK 同步器是客户端一致性的核心。
- 消息同步器按 seq 补拉，VersionSynchronizer 按版本同步对象。
- 同步器要处理登录、重连、断网恢复、后台切前台等场景。
- 本地库是同步结果的承载，不是临时缓存。

## 源码入口

- `openim-sdk-core/pkg/syncer/syncer.go`
- `openim-sdk-core/pkg/syncer/version_synchronizer.go`
- `openim-sdk-core/internal/interaction/msg_sync.go`
- `openim-sdk-core/internal/conversation_msg`
- `openim-sdk-core/pkg/db`

## 详细源码解析

`Syncer` 负责组织全量同步和增量同步。`FullSync` 通常用于初次登录、本地状态缺失或差异过大时。`VersionSynchronizer` 则负责按版本做增量。

消息同步不完全由通用 `Syncer` 承担，而是有专门 `MsgSyncer`。`MsgSyncer` 启动时 `LoadSeq` 从本地库加载已同步 seq；建连后 `doConnected` 获取服务端 max seq；收到 push 后 `pushTriggerAndSync` 判断是否需要补拉。

同步完成后，SDK 写本地库并触发上层事件，App 再更新 UI。

## 数据结构与存储

- SDK 本地消息表。
- SDK 本地会话表。
- SDK 本地好友/群/黑名单表。
- 本地已同步 seq。
- 本地版本号。
- 服务端 max seq 和 version log。

## 设计取舍

把同步逻辑放在 SDK 可以统一多端 App 行为，降低业务 App 的复杂度。但 SDK 会变重，需要处理并发、重试、本地库异常和生命周期问题。

## 面试讲法

> OpenIM 的 SDK 不是简单网络库，它有同步器。消息同步靠 `MsgSyncer` 的 seq 补拉，好友/群/会话等对象同步靠 `VersionSynchronizer`。同步结果写本地库，上层 App 只接收回调和展示。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| SDK 为什么要本地库？ | 离线和恢复 | 快速展示、断线补偿、重启恢复 | `pkg/db` | 只是缓存 |
| FullSync 何时用？ | 兜底 | 初次、版本差异大、本地损坏 | `syncer.go` | 每次都全量 |
| MsgSyncer 和 VersionSynchronizer 区别？ | 数据模型 | 消息流 vs 对象状态 | `msg_sync.go`、`version_synchronizer.go` | 混用 |
| push 后同步器做什么？ | 实时+完整 | 检查 seq，必要时补拉 | `pushTriggerAndSync` | 直接回调 |
| 多端一致靠什么？ | 同步机制 | 服务端 seq/version + 各端 SDK 同步 | SDK syncer | 服务端主动写本地 |

## 和本地项目的关系

本地 `openim-sdk-core` 和 `sources/openimsdk/openim-sdk-core` 都可阅读 SDK 同步逻辑。

## 小结

SDK 同步器把服务端最终一致变成客户端最终可见，是 OpenIM 架构中非常重要的一层。
