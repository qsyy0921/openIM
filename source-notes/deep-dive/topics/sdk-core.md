# openim-sdk-core 设计

## SDK 为什么重要

OpenIM 不是一个“服务端 push 到客户端就结束”的系统。客户端 SDK 承担了大量可靠性和体验逻辑：

- 登录和 token 管理。
- WebSocket 长连接。
- 请求/响应和服务端 push 的协议处理。
- 本地数据库。
- 消息发送状态。
- seq 补拉。
- 会话、好友、群、黑名单增量同步。
- 断线重连和重装恢复。
- UI 回调。

源码入口：

- `openim-sdk-core/internal/interaction`
- `openim-sdk-core/internal/conversation_msg`
- `openim-sdk-core/pkg/db`
- `openim-sdk-core/pkg/syncer/syncer.go`
- `openim-sdk-core/pkg/syncer/version_synchronizer.go`
- `openim-sdk-core/internal/relation/incremental_sync.go`
- `openim-sdk-core/internal/group/incremental_sync.go`
- `openim-sdk-core/internal/conversation_msg/incremental_sync.go`

## SDK 和设备端 App 的边界

设备端 App 负责 UI、页面、业务交互、系统推送权限和应用生命周期。SDK 负责 IM 协议和数据一致性。

面试表达：App 不应该直接拼 WebSocket 协议、直接处理 seq 和本地消息表；这些属于 SDK 内核职责。这样 Android/iOS/Flutter/RN/Web 可以复用同一套 IM 语义。

## 消息同步器

源码依据：`openim-sdk-core/internal/interaction/msg_sync.go`

关键对象和函数：

- `MsgSyncer`
- `LoadSeq`
- `compareSeqsAndBatchSync`
- `pushTriggerAndSync`
- `doConnected`
- `syncAndTriggerMsgs`

核心思想：

- 本地维护每个 conversationID 的已同步最大 seq。
- 收到服务端 max seq 或 push seq 后比较。
- 连续则直接写本地并触发 UI。
- 不连续则按范围补拉。

## VersionSynchronizer

源码依据：`openim-sdk-core/pkg/syncer/version_synchronizer.go`

关系、群、会话等非消息数据不能只靠全量拉取，否则登录和重连成本很高。OpenIM 使用 version log 思路做增量同步。

服务端依据：

- `open-im-server/pkg/common/storage/model/version_log.go`
- `open-im-server/pkg/common/storage/database/mgo/version_log.go`
- `IncrVersion`
- `FindChangeLog`
- `BatchFindChangeLog`

设计思想：服务端记录对象版本变化，SDK 按版本差异拉取变更。这样新增好友、群成员变化、会话变化可以走增量同步。

## 本地数据库

SDK 本地库保存：

- 消息表。
- 会话表。
- 群和群成员。
- 好友、黑名单。
- 已同步 seq。
- 发送失败或异常消息。

本地库不是“可有可无的缓存”，而是客户端离线可用、重启恢复、弱网补偿和 UI 快速展示的基础。

## push 到达后为什么还要 pull

因为 push 只证明“某个消息通知到达了当前连接”，不能证明：

- 之前没有缺消息。
- 这个 push 对应的消息已经完整写入本地库。
- 其他端也同步了。
- 服务端历史消息已经完全落库。

SDK 以 seq 为准。push 中的 seq 连续时可以快路径处理，不连续时必须 pull。

## 面试讲法

> OpenIM 的 SDK 是可靠性闭环的一部分。服务端负责生成 seq 和保存消息流，SDK 负责用本地 seq 和服务端 max seq 对齐。WebSocket push 解决实时性，SDK 补拉解决完整性，本地库解决重启和弱网体验。没有 SDK 这层，服务端的异步最终一致很难落到用户可见的一致体验上。
