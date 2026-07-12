# OpenIM Web 基础客户端连续交付 Goal

状态：active

工作区：`E:\development\OPENIM`

交付模式：每个里程碑独立提交、推送和 Draft PR，不自动合并
最终边界：完成基础 IM 客户端，不进入企业协同业务或新 Agent 功能

## Root outcome

基于当前已验证的 OIDC、OpenIM WASM SDK、单聊、群聊、联系人、成员选择器、图片/文件消息和 Agent 回归能力，连续完成一个可用于后续企业协作平台扩展的基础 Web IM 客户端。

最终客户端必须具备：

1. 单聊、群聊和图片/文件消息真实收发；
2. 会话搜索、置顶和免打扰；
3. 群成员邀请、移除、退出、解散及必要角色约束；
4. 消息引用回复、转发、撤回和已读状态；
5. 锁定 SDK 正式支持范围内的消息搜索；
6. 多端设备、登录状态和踢端结果展示与管理；
7. Node2 真实环境验收、桌面/移动视觉验收和现有 Agent 回归。

完成这些能力后停止 Root Goal。云文档、视频会议、任务、日历、组织架构和新 Agent 平台进入后续独立 Root Goal。

## Authoritative baseline

- 当前基础：`codex/client-foundation-contacts`，提交 `4b42803`；
- 当前开发分支：`codex/client-foundation-media`；
- 联系人 Draft PR：`#5`，base 为 `codex/client-foundation-group-chat`；
- SDK：仓库锁定的 `@openim/wasm-client-sdk@3.8.3-patch.13`，禁止擅自升级；
- IM 用户、好友、群、会话、消息和设备状态由 OpenIM 唯一拥有；
- 企业身份由 Keycloak 和 Platform API 拥有；
- 当前 Agent 运行时和 Web Agent 工作台只做回归，不在本 Goal 扩展；
- Node2 是所有写操作和同步行为的真实验收环境。

执行每个里程碑前必须重新核对实际分支、PR、SDK 类型、Node2 状态和未提交修改。本文中的分支关系是初始计划；如果前序 PR 已合并，后续分支必须从最新 `origin/main` 建立。

## Global invariants

- OpenIM 始终是 IM 领域唯一事实来源；
- 不把会话、群关系、消息、置顶、免打扰或设备状态复制到 PostgreSQL、localStorage 或自建后端；
- 所有写操作只有在官方 SDK 返回成功后才能显示成功；
- SDK 回调必须幂等合并，旧请求和旧回调不得覆盖新状态；
- Token、密码、对象存储凭据和敏感消息不得写入日志、仓库、URL、截图或测试快照；
- 禁止生产 fallback、静默降级、双写、硬编码成功和 mock 生产路径；
- 禁止擅自升级 SDK、修改 OpenIM Server 公共协议或直接操作 OpenIM 数据库；
- 每个里程碑只实现其验收要求，完成后停止该模块的扩展；
- 每个 Draft PR 必须保持边界清楚，可单独审查，并正确指向前序堆叠分支或 `main`；
- 每个里程碑均需通过现有 OIDC、联系人、文本、媒体和 Agent 回归。

## Global non-goals

本 Root Goal 不实现：

- 企业组织架构、部门树和员工目录；
- 云文档、知识库、云盘和多人协同编辑；
- 项目、任务、审批、统一待办和通知中心；
- 日历、会议室、音视频会议和实时音视频通话；
- 语音/视频消息、断点续传、媒体转码、DLP 和病毒扫描；
- 新 Agent、Skill Registry、MCP Gateway、Memory 或多 Agent 编排；
- OpenIM Server 上游重构、新数据库或微服务拆分；
- 桌面客户端打包和移动原生客户端；
- 与当前里程碑无关的通用组件库或设计系统重构。

发现这些需求时，只记录到相应 SDD backlog，不继续实现。

## Milestone 0：交付媒体消息

### Outcome

把当前已通过真实 Node2 验收的图片/文件消息切片形成可审查交付。

### Required work

1. 保留当前媒体实现和 `im-client-media.md`；
2. 重新执行单元测试、类型检查、生产构建、仓库校验、严格 SDD 校验和 Node2 E2E；
3. 审计无 fallback、密钥、双写和范围扩展；
4. 只暂存媒体 Goal 拥有的代码、测试、SDD 和 E2E 助手；
5. 提交并推送 `codex/client-foundation-media`；
6. 创建以 `codex/client-foundation-contacts` 为 base 的 Draft PR；
7. PR 记录真实 MinIO 上传、单群聊收发、入站媒体、文件字节校验、视觉证据和 SDK 进度粒度限制。

### Stop rule

Draft PR 创建且 CI 可运行后停止媒体功能修改。媒体重试、取消、批量发送和保留策略不在本里程碑实现。

## Milestone 1：会话搜索、置顶与免打扰

### Outcome

用户可以过滤现有会话、置顶/取消置顶，并使用 OpenIM 的“接收但不通知”语义开启/关闭免打扰；重连和重载后状态保持一致。

### Required work

- 调查锁定 SDK 的会话置顶、接收消息选项、回调和枚举；
- 新增 `docs/sdd/im-client-conversations.md`；
- 搜索仅过滤 OpenIM 当前会话投影，支持名称、用户 ID 和群 ID；
- 为置顶和免打扰实现逐会话并发保护和明确失败；
- SDK 回调驱动权威状态更新；
- 会话行提供克制的操作菜单、置顶和免打扰状态；
- 桌面和移动均无重叠或横向溢出；
- Node2 验证置顶、重载、免打扰下仍接收真实消息、状态恢复及测试清理。

### Non-goals

不实现消息正文搜索、会话删除、归档、定时免打扰、草稿或自建设置存储。

### Delivery

- 建议分支：`codex/client-conversation-management`；
- 前序未合并时 base 为 `codex/client-foundation-media`；
- 独立提交、推送和 Draft PR。

## Milestone 2：群成员与群生命周期

### Outcome

群主、管理员和普通成员根据 OpenIM 权限完成成员邀请、成员移除、主动退群和群主解散群；客户端实时反映成员和会话变化。

### Required work

- 调查锁定 SDK 的群信息、角色、邀请、踢人、退群、解散和相关回调；
- 新增 `docs/sdd/im-client-group-lifecycle.md`；
- 复用现有 `MemberPicker` 邀请好友，不恢复文本 ID fallback；
- 按 OpenIM 返回的角色和权限显示命令；
- 所有危险操作使用明确确认界面；
- 重复提交、过期成员列表和切换群聊竞态得到保护；
- 退群或解散后会话和详情状态由 SDK 回调收敛；
- Node2 使用隔离群验证群主、普通成员路径并清理测试群。

### Non-goals

不实现企业部门群、群审批策略、禁言、公告、群机器人、群文件空间或服务端权限扩展。

### Delivery

- 建议分支：`codex/client-group-lifecycle`；
- base 为已交付的会话管理分支或最新 `main`；
- 独立提交、推送和 Draft PR。

## Milestone 3：消息引用、转发、撤回与已读

### Outcome

用户可以对文本、图片和文件消息执行锁定 SDK 支持的引用回复、转发和撤回，并在单聊中看到真实已读状态；所有结果可在回调和历史恢复后重建。

### Required work

- 调查 SDK 的 quote、forward、revoke、read receipt 和通知消息结构；
- 新增 `docs/sdd/im-client-message-actions.md`；
- 实现有作用域的消息操作菜单；
- 引用消息必须保存 SDK 官方引用结构，不拼接文本模拟；
- 转发必须使用官方消息创建/发送路径并明确目标会话；
- 撤回权限、时间窗口和失败由 OpenIM 决定；
- 撤回通知幂等替换原消息展示；
- 已读状态使用官方回执，不从本地焦点或时间推断；
- Node2 验证双向引用、单/群转发、允许和拒绝的撤回路径以及单聊已读。

### Non-goals

不实现批量转发、合并转发编辑器、表情回应、消息编辑、收藏、举报或自定义撤回策略。

### Delivery

- 建议分支：`codex/client-message-actions`；
- base 为群生命周期分支或最新 `main`；
- 独立提交、推送和 Draft PR。

## Milestone 4：消息搜索

### Outcome

用户可以在锁定 OpenIM SDK 正式支持的索引和同步范围内搜索已同步消息，并跳转到确定的会话和消息上下文。

### Required work

- 先确认锁定 SDK 的真实搜索接口、索引范围、分页和内容类型限制；
- 如果 SDK 没有正式搜索能力，输出源码证据并将本里程碑标记为外部能力阻塞，不建立浏览器遍历或自建索引 fallback；
- 有正式接口时新增 `docs/sdd/im-client-message-search.md`；
- 搜索支持并发请求取消/序号保护、分页、空状态和明确错误；
- 结果展示会话、发送者、时间、类型和安全摘要；
- 跳转只在 SDK 能提供稳定消息定位时实现；
- Node2 创建唯一文本消息，验证搜索、分页/边界和重载。

### Non-goals

不实现企业全局搜索、服务端 Elasticsearch、附件 OCR、语义搜索、RAG 或自建浏览器全文索引。

### Delivery

- 建议分支：`codex/client-message-search`；
- base 为消息操作分支或最新 `main`；
- 独立提交、推送和 Draft PR；
- 如果上游能力缺失，只交付证据充分的 SDD/阻塞报告，不伪造功能 PR。

## Milestone 5：多端设备与登录管理

### Outcome

用户可以查看当前 OpenIM 多端登录状态和设备信息，识别当前设备，并通过 OpenIM 正式能力执行允许的设备管理或踢端操作；被踢端客户端明确退出当前 IM 会话。

### Required work

- 调查 Chat、OpenIM Auth RPC、锁定 SDK 的多端策略、设备 Token、踢端回调和可用管理 API；
- 先做安全边界评审：浏览器不得获得 Admin Token，设备管理必须使用当前用户被授权的公开路径；
- 新增 `docs/sdd/im-client-device-management.md`；
- 展示设备平台、状态、最后活动信息和当前设备标识，仅使用真实可得字段；
- 踢端写操作需要明确确认、幂等保护和审计可关联操作 ID；
- `OnKickedOffline`、Token 过期和连接失败必须区分显示；
- 使用 `.1` 浏览器与 Node2/第二浏览器上下文完成真实多端验收；
- 验收结束恢复测试身份到可登录状态。

### Non-goals

不实现管理员全员设备后台、设备指纹风控、MDM、密码修改、短信二次认证或自建 Token 服务。

### Delivery

- 建议分支：`codex/client-device-management`；
- base 为消息搜索分支或最新 `main`；
- 独立提交、推送和 Draft PR。

## Per-milestone verification

每个实现里程碑至少执行：

- 受影响单元测试；
- Web 全量 Vitest；
- `npm run typecheck`；
- `npm run build`；
- `python ops/validate-repository.py`；
- 受影响 SDD 严格校验；
- Node2 真实功能 E2E；
- 现有 OIDC、联系人、单群聊、媒体和 Agent 回归；
- 桌面 `1280x720` 与移动 `390x844` Playwright 截图；
- 浏览器控制台、HTTP 失败、Token/localStorage 和横向溢出检查；
- `git diff --check` 与提交后的 `git show --check`；
- diff 中 fallback、placeholder、mock 生产路径、密钥和无关改动审计；
- GitHub CI 检查可运行并记录状态。

## Root acceptance criteria

Root Goal 只有在以下条件全部满足后才能完成：

1. Milestone 0、1、2、3 和 5 的真实生产路径均已实现并验收；
2. Milestone 4 已实现，或用锁定 SDK 源码证明正式能力缺失且未添加 fallback；
3. 每个里程碑都有同步且验证过的 SDD；
4. 每个已实现里程碑都有独立分支、提交、推送和 Draft PR；
5. PR 栈 base 关系正确且没有把无关分支内容混入；
6. Node2 最终根级 E2E 覆盖身份、联系人、会话、群、消息、媒体、设备和 Agent 回归；
7. 没有生产 fallback、双写、密钥泄漏、SDK 漂移或未声明的公共后端变更；
8. 根级文档记录最终能力、已知上游限制和后续企业协作平台入口；
9. 所有里程碑完成后停止，不继续实现企业协同或新 Agent 功能。

## Escalation conditions

只有以下情况允许暂停当前可执行路径：

- 锁定 SDK 缺少里程碑不可替代的正式接口；
- 需要修改公共后端 API、持久化结构、安全边界或分布式一致性规则；
- Node2 缺少不可自动建立的第二身份或多端环境；
- 上游真实缺陷连续复现并阻塞核心闭环；
- GitHub、Node2 或认证凭据不可用，且其他独立工作也已完成。

普通实现、样式、测试、回调竞态和分支交付问题必须自行处理。一个里程碑遇到阻塞时，只有与其独立且不会污染依赖关系的调查或文档工作可以继续。
