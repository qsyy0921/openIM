# 04 在线状态相关存储结构

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（四）：在线状态相关存储结构》：https://juejin.cn/post/7516494221155614729

## 本文目标

解释在线状态在 gateway 内存、Redis/cache、API 汇总和 push 链路中的存储与使用。

## 核心结论

- 在线状态首先是 gateway 内存状态，不是 MongoDB 用户字段。
- Redis/cache 用于跨组件快速判断和订阅，但真实 socket 在 gateway。
- push 依赖在线状态决定走 WebSocket 还是离线推送。
- 在线状态天然存在短暂不一致，需要容忍和补偿。

## 源码入口

- `open-im-server/internal/msggateway/user_map.go`
- `open-im-server/internal/msggateway/ws_server.go`
- `open-im-server/internal/msggateway/subscription.go`
- `open-im-server/internal/api/user.go`
- `open-im-server/internal/push/push_handler.go`
- `open-im-server/pkg/rpccache`

## 详细源码解析

`msggateway/user_map.go` 是在线状态的本节点核心。它保存 userID 到 platform/client 的映射。用户建连时加入，断开时删除。

`open-im-server/internal/api/user.go` 的 `GetUsersOnlineStatus` 会拿到所有 gateway 连接并逐个查询，再合并结果。这说明在线状态在多 gateway 场景下需要汇总，不是单点查询。

`open-im-server/internal/push/push_handler.go` 中 `GetConnsAndOnlinePush` 会先通过 online cache 判断 onlineUserIDs 和 offlineUserIDs。在线用户尝试 WebSocket 投递，离线用户根据消息选项进入离线推送。

## 数据结构与存储

- gateway 内存：userID -> platformID -> client。
- Redis/cache：在线状态、订阅关系、热点群/用户关系。
- API 响应：在线/离线、平台列表、token detail。
- Prometheus 指标：`online_user_num` 等，见 `pkg/common/prommetrics`。

## 设计取舍

在线状态如果完全写 MongoDB，会有严重写放大和延迟；如果只放 gateway 内存，跨节点查询和 push 不方便。OpenIM 采用“gateway 内存为真实连接来源 + cache/RPC 辅助查询”的方式，牺牲强一致，换取实时性和扩展性。

## 面试讲法

> 在线状态是连接层状态，真实连接在 gateway 内存里。Redis 和 cache 只是帮助跨组件快速判断，不能把在线状态理解成数据库用户字段。push 服务判断用户在线后，还要通过 gateway 实际投递；如果投递失败，再转离线推送或等待 SDK 补拉。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 在线状态存在 Mongo 吗？ | 状态模型 | 不应作为核心实时来源 | `user_map.go` | 当作用户字段 |
| 为什么在线状态会不准？ | 分布式延迟 | 断线、心跳、跨节点同步都有延迟 | `ws_server.go` | 承诺强一致 |
| push 如何用在线状态？ | 投递路径 | online 走 gateway，offline 走离线推送 | `GetConnsAndOnlinePush` | 直接推所有人 |
| 多 gateway 怎么查在线？ | 跨节点汇总 | API 遍历 gateway RPC 合并结果 | `internal/api/user.go` | 只查本节点 |
| 在线状态订阅怎么做？ | 感知机制 | gateway 推送用户在线变化 | `subscription.go` | 轮询用户表 |

## 和本地项目的关系

本地源码能看到在线状态从 gateway 建连、API 查询到 push 使用的完整链路。

## 小结

在线状态的关键是“真实连接在 gateway”。面试时要承认它是实时状态，不是强一致持久字段。
