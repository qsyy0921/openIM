# 注册、登录与三种 Token

## 三种 Token 的边界

| Token | 生成方 | 使用方 | 主要用途 | 是否给客户端 |
| --- | --- | --- | --- | --- |
| Chat Token | `chat/internal/rpc/admin/token.go` | Chat API / 业务前端 | 访问 Chat 产品层接口 | 可以给业务客户端 |
| OpenIM Admin Token | `open-im-server/internal/rpc/auth/auth.go` | Chat 服务端、管理端、运维工具 | 服务端调用 OpenIM 管理/用户注册等 API | 不应给普通客户端 |
| OpenIM User Token | `open-im-server/internal/rpc/auth/auth.go` | SDK / 设备端 | WebSocket 建连、IM 用户身份鉴权 | 给对应登录用户 |

核心原则：Chat Token 证明“你是 Chat 产品层登录用户”；OpenIM User Token 证明“你是 OpenIM IM 域中的某个用户”；OpenIM Admin Token 证明“服务端有权限调用 OpenIM 管理能力”。

## 注册流程

源码入口：

- HTTP：`chat/internal/api/chat/chat.go` 的 `RegisterUser`
- RPC：`chat/internal/rpc/chat/login.go` 的 `RegisterUser`
- Chat 调 OpenIM：`chat/pkg/common/imapi/caller.go` 的 `RegisterUser`
- OpenIM 用户注册 API：`open-im-server/internal/api/user.go`、`open-im-server/internal/rpc/user`

运行时流程：

```text
客户端提交注册信息
  -> chat api RegisterUser
  -> chat rpc RegisterUser
  -> 校验验证码/账号/密码/设备信息
  -> 写 Chat 侧账号数据
  -> 通过 imapi 获取 OpenIM Admin Token
  -> 调 OpenIM 用户注册接口
  -> 返回 Chat Token / OpenIM User Token 所需信息
```

Chat 侧持久化数据主要包括：

- `registers` / `register`：注册设备和来源，模型在 `chat/pkg/common/db/table/chat/register.go`、`chat/pkg/common/db/model/chat/register.go`。
- `accounts` / `account`：账号和密码摘要，路径 `chat/pkg/common/db/table/chat/account.go`、`chat/pkg/common/db/model/chat/account.go`。
- `attributes` / `attribute`：昵称、头像、手机号等用户属性。
- `credentials` / `credential`：登录凭证，区分手机号/邮箱等登录类型。
- `verify_codes` / `verify_code`：验证码。
- `user_login_records` / `user_login_record`：登录记录。

设计思想：Chat 维护“产品账号”，OpenIM 维护“IM 用户”。这样业务登录方式可以变化，比如手机号、邮箱、OAuth，而 IM 域只关心最终 userID。

## 登录流程

源码入口：

- HTTP：`chat/internal/api/chat/chat.go` 的 `Login`
- RPC：`chat/internal/rpc/chat/login.go` 的 `Login`
- Chat Token 创建：`chat/internal/rpc/admin/token.go` 的 `CreateToken`
- OpenIM Admin Token 获取：`chat/pkg/common/imapi/caller.go` 的 `GetAdminTokenCache`、`getAdminTokenServer`
- OpenIM User Token 获取：`chat/pkg/common/imapi/caller.go` 的 `GetUserToken`

运行时流程：

```text
客户端登录 Chat
  -> chat api Login
  -> chat rpc Login
  -> 校验账号密码/验证码/风控
  -> 生成 Chat Token
  -> Chat 服务端拿 OpenIM Admin Token
  -> Chat 服务端用 Admin Token 向 OpenIM 获取 User Token
  -> 返回业务信息 + OpenIM User Token
  -> SDK 使用 OpenIM User Token 建立 WebSocket
```

面试里要强调：登录返回的并不只是“一个 token”，而是跨产品域和 IM 域的登录材料。

## Chat Token

源码依据：

- `chat/internal/rpc/admin/token.go`：`CreateToken`、`ParseToken`
- `chat/pkg/common/db/cache/token.go`：`chatToken = "CHAT_UID_TOKEN_STATUS:"`、`SetTokenExpire`
- `chat/internal/api/mw/mw.go`：Chat API middleware 解析 Token

Redis key：

```text
CHAT_UID_TOKEN_STATUS:{userID}
```

值语义：按 token 维护状态，源码注释和图中常见状态包括正常、无效、被踢、过期。具体状态值以当前 `chat/pkg/common/db/cache/token.go` 和 token verify 实现为准。

设计取舍：

- Chat Token 独立后，Chat 可以拥有自己的过期策略、设备策略、后台管理策略。
- 缺点是登录链路更长，排查问题时要区分“Chat 登录失败”和“OpenIM 接入失败”。

## OpenIM Admin Token

源码依据：

- `open-im-server/internal/rpc/auth/auth.go`：`GetAdminToken`
- `open-im-server/pkg/common/storage/controller/auth.go`：`CreateToken`
- `chat/pkg/common/imapi/caller.go`：`GetAdminTokenCache`、`getAdminTokenServer`
- API 路由：`open-im-server/internal/api/router.go` 中 `/auth/get_admin_token`

使用方式：Chat 服务端作为业务后端，需要注册 OpenIM 用户、获取用户 Token、管理用户资料时，先用默认 IM 管理员身份拿 OpenIM Admin Token，再调用 OpenIM API。

注意：OpenIM Admin Token 不应该下发给普通客户端。它代表系统级能力，下发会扩大权限面。

## OpenIM User Token

源码依据：

- `open-im-server/internal/rpc/auth/auth.go`：`GetUserToken`、`ParseToken`
- `open-im-server/pkg/common/storage/controller/auth.go`：`CreateToken`
- `open-im-server/pkg/common/storage/cache/cachekey/token.go`：`UidPidToken = "UID_PID_TOKEN_STATUS:"`
- `open-im-server/pkg/common/storage/cache/redis/token.go`：`SetTokenFlagEx`、`SetTokenMapByUidPid`
- `open-im-server/internal/msggateway/ws_server.go`：`validate` 调 Auth RPC `ParseToken`

Redis key：

```text
UID_PID_TOKEN_STATUS:{userID}:{platformName}
```

设计含义：OpenIM User Token 绑定 userID 和 platformID。这样才能表达同一用户在 iOS、Android、Web、PC 等平台的登录状态，并支持同端踢、同类端踢、全端踢等策略。

## WebSocket 鉴权

源码入口：

- `open-im-server/internal/msggateway/context.go`：解析 token、sendID、platformID、operationID、sdkType。
- `open-im-server/internal/msggateway/ws_server.go`：`validate`、`validateRespWithRequest`。
- `open-im-server/internal/rpc/auth/auth.go`：`ParseToken`。

流程：

```text
SDK 发起 WebSocket
  -> URL/header 带 token、sendID、platformID、operationID
  -> msggateway ParseEssentialArgs
  -> 调 auth rpc ParseToken
  -> 校验 token 状态、userID、platformID
  -> 通过后生成 Client，加入本节点 user map
  -> 触发多端登录策略和在线状态同步
```

## Token 踢端和失效

源码依据：

- `open-im-server/internal/msggateway/ws_server.go`：`multiTerminalLoginChecker`、`KickUserConn`。
- `open-im-server/internal/rpc/auth/auth.go`：`ForceLogout`、`InvalidateToken`、`KickTokens`。
- `open-im-server/pkg/common/storage/cache/redis/token.go`：写 Redis token 状态。

设计思想：

- 连接踢端是“物理连接层”的动作，表现为 WebSocket 被关闭或收到踢下线消息。
- Token 失效是“认证状态层”的动作，表现为旧 token 后续无法通过鉴权。
- 两者必须配合，否则只断连接不改 token，客户端重连可能又成功；只改 token 不断连接，旧连接可能继续存在一段时间。

## 面试讲法

> OpenIM 的认证不是一个 token 走天下。Chat Token 是业务产品层登录态，OpenIM User Token 是 IM 域用户接入凭证，OpenIM Admin Token 是服务端调用 OpenIM 管理接口的凭证。登录时 Chat 先完成业务账号校验，再代表服务端获取 OpenIM User Token，SDK 用这个 token 建 WebSocket。WebSocket 鉴权在 msggateway 中调用 Auth RPC 解析 token，同时结合 userID 和 platformID 做多端策略。

## 容易说错的点

- 把 Chat Token 说成 WebSocket Token。
- 把 OpenIM Admin Token 下发给普通客户端。
- 只说踢连接，不说 token 状态失效。
- 只讲 JWT，不讲 Redis 中 token 状态表。
- 忘记 platformID，导致解释不了多端登录策略。
