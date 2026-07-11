# 03 多设备登录剔除与感知机制

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（三）：多设备登录剔除与感知机制》：https://juejin.cn/post/7516365798455410738

## 本文目标

解释 OpenIM 如何通过 platformID、gateway 本地连接表和 Token 状态实现多端登录与踢端。

## 核心结论

- 多端控制发生在 WebSocket 建连阶段。
- gateway 负责踢连接，Auth RPC 负责让旧 Token 失效。
- platformID 是多端策略的关键维度。
- 多 gateway 部署时还需要跨节点同步在线信息。

## 源码入口

- `open-im-server/internal/msggateway/ws_server.go`
- `open-im-server/internal/msggateway/user_map.go`
- `open-im-server/internal/rpc/auth/auth.go`
- `open-im-server/pkg/common/storage/cache/redis/token.go`
- `open-im-server/pkg/common/config/config.go`

## 详细源码解析

SDK 建连通过 `msggateway` 鉴权后，gateway 会把新连接加入本地用户连接表。随后 `multiTerminalLoginChecker` 根据配置判断新连接是否需要踢掉旧连接。被踢连接通过 `KickUserConn` 或 `KickOnlineMessage` 感知下线。

同时，gateway 会调用 Auth RPC 的 `KickTokens`，把旧 token 在 Redis 中的状态改掉。这样旧连接即使重连，也无法继续使用旧 token 通过鉴权。

`user_map.go` 维护本节点 userID、platformID、client 的关系。它支撑同平台踢、同类端踢、全端踢等策略。

## 数据结构与存储

- Redis key：`UID_PID_TOKEN_STATUS:{userID}:{platformName}`。
- 内存结构：`internal/msggateway/user_map.go` 中 user -> platform -> clients。
- Token 状态：正常、失效、被踢、过期等状态由 Redis hash 维护。

## 设计取舍

多端登录不能只依赖 Redis，因为真实连接对象在 gateway 进程内存中；也不能只依赖 gateway，因为 token 是否有效必须跨连接和跨节点统一。因此 OpenIM 用 gateway 管连接，用 Redis 管 token 状态。

## 面试讲法

> 多端踢端分两层：连接层由 gateway 维护，它知道当前 userID/platformID 有哪些连接；认证层由 Auth RPC 和 Redis 维护，它知道哪些 token 仍然有效。新设备登录后，gateway 按策略踢旧连接，并调用 Auth RPC 标记旧 token 失效。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| 为什么要 platformID？ | 多端区分 | 同一用户不同端策略不同 | `context.go`、`cachekey/token.go` | 只按 userID |
| 只改 Redis token 行不行？ | 连接状态 | 旧连接可能还在线，要主动踢 | `KickUserConn` | 忽略已有连接 |
| 只关闭连接行不行？ | 认证状态 | 旧 token 可能重连 | `KickTokens` | 忽略 token 失效 |
| 多节点怎么知道在线？ | 分布式状态 | gateway 本地 + 跨节点 RPC/缓存汇总 | `sendUserOnlineInfoToOtherNode` | 假设单节点 |
| 踢端消息可靠吗？ | 用户感知 | 踢消息是通知，最终以连接关闭和 token 失效为准 | `KickOnlineMessage` | 只依赖客户端弹窗 |

## 和本地项目的关系

本地源码中 `msggateway` 和 Auth RPC 均完整存在，可以直接追踪建连后多端检查和 token 状态修改。

## 小结

多端登录是连接层和认证层的组合问题。面试时重点讲 platformID、gateway 连接表、Redis token 状态三者如何配合。
