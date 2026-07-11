# OpenIM 源码深度学习文档

本文档集用于维护 `E:\development\OPENIM` 本地 OpenIM 源码学习笔记，重点服务两个目标：一是把 OpenIM 的运行时架构讲清楚，二是把项目经历转化为后端面试中可信、可追问的表达。

## 阅读边界

这不是第三方文章正文搬运，也不把项目包装成“从零自研 IM”。本文档统一按以下口径描述：

- 源码事实：当前本地源码中能直接看到的入口、函数、结构体、配置、Redis key、Kafka topic、MongoDB model。
- 设计推断：根据调用链和中间件分工推断出的架构意图，会明确标为“设计推断”。
- 面试表达：面向面试官的概括版本，强调边界、取舍和风险，不夸大能力。

## 目录

- [00-study-roadmap.md](./00-study-roadmap.md)：源码学习路线。
- [00-interview-summary.md](./00-interview-summary.md)：面试总纲和项目表达。
- [topics/overall-architecture.md](./topics/overall-architecture.md)：整体架构和设计思想。
- [topics/login-register-token.md](./topics/login-register-token.md)：注册、登录和三种 Token。
- [topics/websocket-gateway.md](./topics/websocket-gateway.md)：WebSocket 接入层。
- [topics/message-send-flow.md](./topics/message-send-flow.md)：消息发送主链路。
- [topics/message-transfer-storage-push.md](./topics/message-transfer-storage-push.md)：异步处理、存储和推送。
- [topics/seq-reliability-sync.md](./topics/seq-reliability-sync.md)：seq、可靠性、补拉和最终一致性。
- [topics/online-status-multi-device.md](./topics/online-status-multi-device.md)：在线状态和多端登录。
- [topics/middleware.md](./topics/middleware.md)：MongoDB、Redis、Kafka、etcd、MinIO、监控。
- [topics/sdk-core.md](./topics/sdk-core.md)：SDK 本地库和同步机制。
- [topics/interview-question-bank.md](./topics/interview-question-bank.md)：高质量面试追问库。
- [topics/resume-project-description.md](./topics/resume-project-description.md)：简历项目描述。
- [articles/](./articles/)：按掘金 18 篇主题对齐的原创源码学习文档。

## 核心源码入口

| 模块 | 路径 | 作用 |
| --- | --- | --- |
| OpenIM API | `open-im-server/cmd/openim-api/main.go`、`open-im-server/internal/api` | HTTP API 入口，路由到各 RPC |
| WebSocket 网关 | `open-im-server/cmd/openim-msggateway/main.go`、`open-im-server/internal/msggateway` | 长连接接入、鉴权、在线连接管理、消息转发 |
| Auth RPC | `open-im-server/cmd/openim-rpc/openim-rpc-auth/main.go`、`open-im-server/internal/rpc/auth` | OpenIM Admin/User Token 签发与解析 |
| Msg RPC | `open-im-server/cmd/openim-rpc/openim-rpc-msg/main.go`、`open-im-server/internal/rpc/msg` | 消息校验、路由、写入 MQ |
| msgtransfer | `open-im-server/cmd/openim-msgtransfer/main.go`、`open-im-server/internal/msgtransfer` | 消费 Kafka、分配 seq、写 Redis、转 Mongo、转 Push |
| push | `open-im-server/cmd/openim-push/main.go`、`open-im-server/internal/push` | 在线推送、离线推送、群成员展开 |
| Chat 产品层 | `chat/cmd`、`chat/internal/api/chat`、`chat/internal/rpc/chat` | 注册、登录、业务账号、Chat Token |
| Chat 调 IM | `chat/pkg/common/imapi/caller.go` | 使用 OpenIM Admin Token 调用 OpenIM API |
| SDK 核心 | `openim-sdk-core/internal/interaction`、`openim-sdk-core/internal/conversation_msg`、`openim-sdk-core/pkg/db` | 长连接、消息同步、本地库、回调 UI |

## 总链路一句话

设备端 App 调用 SDK，SDK 持有 OpenIM User Token 建立 WebSocket；消息进入 `msggateway` 后转给 `msg rpc` 校验，写入 Kafka `toRedis`；`msgtransfer` 消费后分配 seq、写 Redis cache、转 `toMongo` 持久化和 `toPush` 在线推送；`push` 根据在线状态和群成员展开投递到 `msggateway`；SDK 收到 push 后仍以 seq 为准做补拉和本地落库。
