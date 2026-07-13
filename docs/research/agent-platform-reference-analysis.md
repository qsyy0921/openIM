# Agent 平台参考项目研究与取舍

> 状态：Completed
> 日期：2026-07-12
> 范围：只为下一阶段 `Agent Catalog v1` 提供设计依据，不构成依赖选型或代码引入决定。

## 1. 当前问题

当前平台已经有真实的 OpenIM 入口、耐久 Run、ACL 检索、DeepSeek 候选生成、引用校验、审批和幂等 `create_ticket` 动作，但仍只有一个隐含 Agent：

- `@agent` 在 `platform/services/platform-api/internal/agent/event.go` 中硬编码。
- 检索目的和数量在 Worker 中固定为 `agent_answer` 和 `5`。
- 每个租户只有一个确定性 OpenIM Bot，Run 没有 `agent_id` 或 `agent_version_id`。
- 提示、模型路由、检索策略和动作集合没有形成不可变执行快照。

因此，现有 Run 可以证明“哪次消息触发了哪次执行”，但不能严格证明“使用了哪个 Agent 的哪一版配置”。这是多 Agent、回滚、评测和审计之前必须补齐的控制面。

## 2. 一手项目与设计取舍

| 参考 | 可借鉴设计 | 不直接采用的部分 | 本项目决定 |
| --- | --- | --- | --- |
| [Mattermost Agents](https://github.com/mattermost/mattermost-plugin-agents) | Agent 独立配置名称、提示、模型、用户/团队/频道访问规则、原生工具和 MCP 工具白名单、最大工具轮次 | 配置主要按当前值运行；自动启用以后新增的 MCP 工具会扩大能力；provider fallback 与本项目单一生产路径冲突；Enterprise 目录有独立许可证 | **改造采用**权限和工具白名单的分离；新工具默认不可用；当前切片不引入其代码 |
| [Dify](https://github.com/langgenius/dify) | Workflow 草稿与发布版本分离，发布后按版本运行 | [Dify 许可证](https://github.com/langgenius/dify/blob/main/LICENSE)对多租户 SaaS 有额外条件；其完整 Workflow Runtime 会与现有 Go Runtime 重叠 | **仅采用概念**：草稿验证后发布不可变版本，不嵌入 Dify Runtime |
| [LangSmith Assistants](https://docs.langchain.com/langsmith/assistants) | “执行图/运行时实现”与“Assistant 配置”分离；每次更新产生新版本；可激活旧版本回滚 | Assistants 属于 LangSmith Deployment，不是开源 LangGraph 核心能力 | **采用模型**：`runtime_kind` 与 Agent 配置分离；本地 PostgreSQL 自主实现版本控制 |
| [Langfuse Prompt Version Control](https://langfuse.com/docs/prompt-management/features/prompt-version-control) | 不可变版本与可移动 `production`/`staging` 标签分离；回滚只移动指针 | 只管理 Prompt 不能覆盖授权、工具、检索和 Runtime 版本；缓存可能造成短暂旧版本读取 | **改造采用**：不可变 `AgentVersion` + 事务化 Deployment 指针；运行时不远程解析“最新 Prompt” |
| [Agent Skills Specification](https://agentskills.io/specification) | Skill 是带元数据、说明、脚本和参考资料的可移植目录，可声明预批准工具 | `allowed-tools` 仍为实验字段，且不是企业授权保证 | **预留引用**：AgentVersion 将来引用固定 Skill 包版本；本切片不建设 Skill Registry |
| [MCP Tools Specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) | 标准工具名、输入/输出 Schema 和风险提示 | `readOnlyHint`、`destructiveHint`、`idempotentHint` 等只是提示，非可信授权事实 | **边缘协议**：以后接 MCP Gateway；风险、审批和幂等策略仍由 Go 控制面权威维护 |
| [OpenIM OpenClaw Channel](https://github.com/openimsdk/openclaw-channel) | 证明 OpenIM 可作为外部 Agent Runtime 的频道适配器，支持私聊、群聊、提及和白名单 | 它是 Channel Adapter，不提供耐久 Run、版本、ACL-RAG、审批和动作治理；AGPL-3.0 | **兼容参考**：不替换现有 OpenIM Adapter 和 Agent Runtime |

## 3. 形成的设计原则

1. **Agent 是稳定身份，版本才是执行事实。** 名称、描述和生命周期属于 `AgentDefinition`；提示、模型、检索、动作和边界属于不可变 `AgentVersion`。
2. **发布指针与版本分离。** 回滚只改变 `production` Deployment 指向，历史 Run 永远保持原版本。
3. **Run 在入队事务中绑定版本。** Worker 不读取“当前版本”，避免排队期间配置漂移。
4. **运行时实现与业务配置分离。** `runtime_kind=knowledge_ticket_v1` 指向受测试的 Go/Python执行路径；AgentVersion 不能注入任意代码。
5. **能力默认关闭。** 新 Tool、Skill、MCP Server 或 Action 不会自动进入已发布 Agent。
6. **协议元数据不等于安全策略。** MCP 注解可以帮助 UI 展示，但不能替代平台的风险分级、审批和授权。
7. **版本是完整快照，不做运行时合并。** 发布请求必须携带完整规范并通过 Schema 校验、语义校验和 checksum 计算。
8. **不把外部平台变成权威依赖。** Langfuse 可用于观测和实验，但 PostgreSQL 中的 AgentVersion 才是执行依据。

## 4. 明确拒绝与延后

### 本阶段拒绝

- 按运行时“最新配置”执行。
- 自动启用未来新增工具。
- Agent 直接持有 OpenIM Admin Token、数据库凭据或业务系统管理员凭据。
- 用 Dify、LangSmith 或 OpenClaw 替换已有耐久 Run 和审批链。
- 为每个 Agent 立即创建一个 OpenIM 用户。
- 以任意 JSON 直接驱动任意代码、SQL 或 HTTP 调用。

### 后续独立切片

- Agent 管理后台、草稿编辑和发布审批。
- Skill/Tool Registry 与 MCP Gateway。
- 用户、频道、团队级 Agent 访问策略。
- 多 Agent 编排、长期 Memory、评测门禁、灰度和 A/B 实验。
- 每 Agent 独立 OpenIM Bot 身份。

## 5. 对下一步的约束

`Agent Catalog v1` 只引入一个迁移安全的目录基础：稳定定义、不可变版本、生产 Deployment、提及触发器和 Run 版本绑定。首个版本必须把当前行为原样建模为 `knowledge_ticket_v1`，保证 `@Agent`、ACL-RAG、引用回答、审批和 `create_ticket` 闭环不变。

## 6. 关键一手依据

- Mattermost Agent 配置、访问级别、MCP 白名单和最大工具轮次：[`llm/configuration.go`](https://github.com/mattermost/mattermost-plugin-agents/blob/master/llm/configuration.go)。
- Mattermost Agent 管理和按 Agent 限制 MCP 工具：[`docs/admin_guide.md`](https://github.com/mattermost/mattermost-plugin-agents/blob/master/docs/admin_guide.md)。
- Dify Workflow 草稿创建：[`workflow_converter.py`](https://github.com/langgenius/dify/blob/main/api/services/workflow/workflow_converter.py)。
- LangSmith Assistant 版本与回滚：[Assistants](https://docs.langchain.com/langsmith/assistants)。
- Langfuse 不可变版本与可移动标签：[Version Control](https://langfuse.com/docs/prompt-management/features/prompt-version-control)和[Data Model](https://langfuse.com/docs/prompt-management/data-model)。
- Agent Skills 的 `SKILL.md`、资源目录和实验性 `allowed-tools`：[Specification](https://agentskills.io/specification)。
- MCP 工具 Schema、注解和不可信边界：[Tools Specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)。
- OpenIM 与外部 Agent Runtime 的频道适配参考：[openimsdk/openclaw-channel](https://github.com/openimsdk/openclaw-channel)。
