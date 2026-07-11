# 消息发送主链路

## 主链路总览

```text
SDK 创建本地消息
  -> SDK 通过 WebSocket 发送
  -> msggateway 收到命令
  -> msggateway 转发到 Msg RPC
  -> Msg RPC 校验消息
  -> Msg RPC 写 Kafka toRedis
  -> 返回发送响应
```

后续落库和推送不在这个同步请求内完成，而是由 `msgtransfer` 和 `push` 异步推进。

## SDK 创建本地消息

SDK 侧会先生成本地消息对象、clientMsgID、发送状态，并写入本地库或临时状态。源码重点看：

- `openim-sdk-core/internal/conversation_msg`
- `openim-sdk-core/internal/interaction`
- `openim-sdk-core/pkg/db`

设计意义：客户端需要立即展示“发送中”，并在网络返回后更新为成功/失败。如果完全等服务端返回再显示，弱网体验会很差。

## WebSocket 到 msggateway

SDK 长连接承载消息请求。gateway 校验连接身份，然后通过内部 handler 转发到 Msg RPC。

源码依据：

- `open-im-server/internal/msggateway/ws_server.go`：连接校验和 handler 初始化。
- `open-im-server/internal/msggateway`：请求解析、命令路由。
- `open-im-server/internal/rpc/msg/send.go`：最终业务发送入口。

## Msg RPC 校验

`open-im-server/internal/rpc/msg/send.go` 的 `SendMsg` 根据 `SessionType` 分发：

- `constant.SingleChatType` -> `sendMsgSingleChat`
- `constant.NotificationChatType` -> `sendMsgNotification`
- `constant.ReadGroupChatType` -> `sendMsgGroupChat`

校验内容包括：

- 消息基本结构。
- 发送者、接收者、群 ID。
- 好友/黑名单/群成员关系。
- 消息接收选项。
- webhook 前置修改或拦截。

设计意义：校验放在 Msg RPC，而不是 SDK 或 gateway，是因为服务端必须是权限判断的最终来源。

## 写 Kafka

源码事实：单聊和群聊最终都调用 `m.MsgDatabase.MsgToMQ(...)`。

- 单聊 key：`conversationutil.GenConversationUniqueKeyForSingle(sendID, recvID)`
- 群聊 key：`conversationutil.GenConversationUniqueKeyForGroup(groupID)`
- 目标 topic：`open-im-server/config/kafka.yml` 的 `toRedis`

设计意义：

- key 按会话生成，有助于同会话消息在异步阶段保持顺序。
- 写入 Kafka 后，入口可以返回，不等待 MongoDB 和 push。
- 后续阶段可以通过 consumer group 独立扩容。

## 发送成功的真实语义

面试中必须讲清楚：`SendMsgResp` 返回成功通常代表消息通过服务端校验并成功进入后续异步处理队列，不等价于：

- 已经写入 MongoDB。
- 接收方已经收到。
- 离线推送已经成功。
- 所有端本地库已经同步。

最终可靠要看：

1. Kafka 消费成功。
2. `msgtransfer` 分配 seq 并写缓存。
3. MongoDB 持久化成功。
4. SDK 通过 push 或补拉拿到消息并写本地库。

## 为什么不是 SDK 直连 Msg RPC

设计推断：

- 客户端面对公网，不能直接暴露内部 RPC。
- WebSocket 提供双向通道，既能发请求也能收 push。
- gateway 统一处理连接数、心跳、压缩、鉴权、多端策略。

## 面试讲法

> OpenIM 的发送入口是快路径。SDK 发消息到 WebSocket gateway，gateway 转 Msg RPC，Msg RPC 做关系和权限校验后写 Kafka `toRedis`。这一步成功只能说明消息被服务端接收并进入异步流水线，后续 seq 分配、缓存、Mongo 落库、在线推送都由后台组件完成。这样降低了入口延迟，但需要通过 seq、补拉和监控处理最终一致。
