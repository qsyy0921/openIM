# 16 设备端 SDK 多种同步流程

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（十六）：设备端 SDK 多种情况同步流程》：https://juejin.cn/post/7518275819531059227

## 本文目标

按场景解释 SDK 在首次登录、普通重连、断线期间漏消息、App 重装、多端同步时如何同步。

## 核心结论

- 不同场景同步策略不同，不能全部全量拉取。
- 消息按 seq 判断差异。
- 关系/群/会话按 version 判断差异。
- 本地状态缺失时需要全量兜底。

## 源码入口

- `openim-sdk-core/internal/interaction/msg_sync.go`
- `openim-sdk-core/pkg/syncer/syncer.go`
- `openim-sdk-core/pkg/syncer/version_synchronizer.go`
- `openim-sdk-core/internal/relation/incremental_sync.go`
- `openim-sdk-core/internal/group/incremental_sync.go`
- `openim-sdk-core/internal/conversation_msg/incremental_sync.go`

## 详细源码解析

首次登录：本地库没有有效 seq 和版本，SDK 需要初始化本地数据，拉取会话、好友、群和历史消息。

普通重连：本地库仍在，`doConnected` 获取服务端 max seq，调用 `compareSeqsAndBatchSync` 补齐断线期间消息。

收到 push：`pushTriggerAndSync` 判断 push 消息 seq 是否连续。连续走快路径；不连续补拉。

App 重装：本地状态丢失，相当于新设备，需要全量或从服务端历史重新建立本地库。

多端同步：每个端都有自己的本地库和同步进度，服务端只维护统一消息流和对象版本，各端自己追平。

## 数据结构与存储

- 本地 seq：每个 conversationID 的已同步位置。
- 服务端 max seq：用于差异判断。
- 本地 version：好友/群/会话等对象版本。
- 服务端 version log：对象变更记录。
- 本地库：同步落地位置。

## 设计取舍

全量同步简单但成本高，增量同步复杂但体验好。OpenIM 同时保留两者：正常走增量，异常走全量兜底。

## 面试讲法

> SDK 同步不是一个固定流程。首次登录和 App 重装偏全量；普通重连按服务端 max seq 补消息；收到 push 时检查 seq 连续性；好友、群、会话等对象则用 version log 增量同步。这样既保证弱网恢复，又避免每次都全量拉取。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 断线期间消息怎么补？ | 断线恢复 | max seq 对比后 range pull | `doConnected` | 依赖离线推送 |
| App 重装怎么办？ | 本地缺失 | 重新全量/历史同步 | `FullSync` | 本地还能恢复 |
| 多端是否共享本地 seq？ | 多端模型 | 各端本地独立追平 | SDK 本地库 | 服务端保存每端库 |
| 为什么不每次全量？ | 性能 | 数据量大，启动慢 | `VersionSynchronizer` | 全量最稳 |
| 增量失败怎么办？ | 兜底 | 重试或全量同步 | `syncer.go` | 无兜底 |

## 和本地项目的关系

本地 SDK 源码中消息同步和版本同步都存在，适合按实际场景调试阅读。

## 小结

SDK 多场景同步体现了 OpenIM 对弱网和多端的工程处理。面试中要按场景说，不要只说“会同步”。
