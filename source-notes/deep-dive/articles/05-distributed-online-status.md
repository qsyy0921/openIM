# 05 分布式在线状态管理实现

## 原文主题

对应掘金文章《OpenIM 源码深度解析系列（五）：分布式在线状态管理的完整实现》：https://juejin.cn/post/7516487355952283686

## 本文目标

从多 gateway 部署角度解释在线状态如何跨节点查询、同步和服务于推送。

## 核心结论

- 单节点在线表不能代表全局在线状态。
- 分布式在线状态需要服务发现、gateway RPC、cache 和订阅通知配合。
- push 服务关心的不是“用户是否在线”这个抽象值，而是“哪些连接可投递”。
- 在线状态最终服务于实时推送，但可靠性仍靠 seq 补拉。

## 源码入口

- `open-im-server/internal/msggateway/ws_server.go`
- `open-im-server/internal/msggateway/subscription.go`
- `open-im-server/internal/api/user.go`
- `open-im-server/internal/push/onlinepusher.go`
- `open-im-server/internal/push/push_handler.go`
- `open-im-server/pkg/rpccache`
- `open-im-server/pkg/common/config/config.go`

## 详细源码解析

`ws_server.go` 建连成功后会维护本节点连接数和用户连接映射，并可通过 `sendUserOnlineInfoToOtherNode` 把在线信息传播给其他 gateway。

`internal/api/user.go` 查询在线状态时通过服务发现获取多个 gateway 节点连接，然后逐个调用并合并在线结果。这种做法体现了分布式在线状态的基本事实：每个 gateway 只掌握自己节点上的连接。

`push` 服务通过 `OnlinePusher` 和 `onlineCache` 查找在线用户并投递。群聊时，`Push2Group` 先展开群成员，再判断成员在线状态。

## 数据结构与存储

- gateway 内存连接表：本节点真实在线连接。
- Redis/cache：辅助记录在线平台、订阅、热点关系。
- 服务发现：gateway 节点列表和 RPC 连接。
- Prometheus：在线连接数和用户数指标。

## 设计取舍

分布式在线状态不追求事务级强一致，因为连接状态变化太频繁。OpenIM 更关注“足够及时地让 push 找到可投递连接”。如果误判在线但投递失败，仍可转离线推送或依赖 SDK 后续补拉；如果误判离线，用户可能走离线推送但不会丢历史消息。

## 面试讲法

> 多 gateway 下，每个 gateway 只知道本节点连接。OpenIM 通过服务发现拿到所有 gateway，再做在线状态汇总；push 服务也要先判断用户在哪些节点在线，再由对应 gateway 投递。在线状态只影响实时性，不是消息可靠性的唯一保障。

## 面试官追问

| 问题 | 考察点 | 回答思路 | 源码依据 | 容易说错的点 |
| --- | --- | --- | --- | --- |
| gateway 扩容后在线状态怎么查？ | 分布式汇总 | 遍历 gateway RPC 汇总 | `internal/api/user.go` | 只查 Redis |
| 在线状态强一致吗？ | 一致性模型 | 不强一致，追求及时和可恢复 | `ws_server.go` | 承诺完全准确 |
| push 找不到连接怎么办？ | 降级 | 走离线推送或 SDK 补拉 | `push_handler.go` | 认为消息丢失 |
| 群聊在线判断成本？ | fanout | 群成员展开后批量判断在线 | `Push2Group` | 忽略成员规模 |
| 服务发现故障影响？ | 可用性 | API/push 可能找不到 gateway/RPC | `discovery.GetConn(s)` | 当作数据问题 |

## 和本地项目的关系

本地 `open-im-server` 包含 gateway、push、API 和服务发现调用，能完整分析多节点在线状态路径。

## 小结

分布式在线状态的本质是连接对象分散在多个 gateway 节点中，系统要用服务发现和缓存把它们组织起来服务 push。
