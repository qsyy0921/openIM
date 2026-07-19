import { Activity, Bell, BellOff, Bot, Boxes, Check, ExternalLink, LockKeyhole, Pause, Play, Plus, RefreshCw, Server, ShieldAlert, ThumbsDown, ThumbsUp, Trash2, UsersRound, Workflow, Wrench, X } from "lucide-react";
import { useEffect, useState } from "react";

import type { AgentControlController, AgentControlState } from "./agent-control";
import type { AgentAdminRole, AgentCatalogTool, CreateAgentInput, ProactivePreference, PublishSkillInput, RegisterRemoteA2AInput } from "./agent-control-api";

type ControlPanelProps = { controller: AgentControlController; state: AgentControlState };

export function AgentToolApprovalPanel({ controller, state }: ControlPanelProps) {
  if (state.toolApprovals.length === 0) return null;
  return (
    <section className="tool-approval-strip" aria-label="待审批工具调用">
      {state.toolApprovals.map((approval) => (
        <div className="tool-approval-row" key={approval.approval_id}>
          <ShieldAlert size={18} />
          <div><strong>{approval.operation_id}</strong><small>{approval.risk} · {approval.arguments_digest.slice(0, 18)}… · {new Date(approval.expires_at).toLocaleTimeString()}</small></div>
          <button className="icon-button approve" aria-label="批准工具调用" title="批准" disabled={state.activeMutation !== null} onClick={() => void controller.decideToolApproval(approval.approval_id, approval.arguments_digest, "approve").catch(() => undefined)}><Check size={16} /></button>
          <button className="icon-button reject" aria-label="拒绝工具调用" title="拒绝" disabled={state.activeMutation !== null} onClick={() => void controller.decideToolApproval(approval.approval_id, approval.arguments_digest, "reject").catch(() => undefined)}><X size={16} /></button>
        </div>
      ))}
    </section>
  );
}

const delegationState: Record<string, string> = {
  queued: "排队中", running: "执行中", completed: "已完成", failed: "失败", unknown: "结果待核验"
};

export function AgentDelegationPanel({ state }: Pick<ControlPanelProps, "state">) {
  if (state.delegations.length === 0) return null;
  return (
    <section className="delegation-strip" aria-label="后台 Agent 委派">
      <header><Workflow size={16} /><strong>后台委派</strong></header>
      {state.delegations.slice(0, 5).map((job) => (
        <div className="delegation-row" key={job.delegation_id}>
          <div><strong>{job.target_agent_slug}</strong><small>{job.task}</small></div>
          <span className={`delegation-state ${job.state}`}>{delegationState[job.state] ?? job.state}</span>
        </div>
      ))}
    </section>
  );
}

const memoryLabels: Record<string, string> = {
  preference: "偏好",
  profile: "档案",
  procedure: "流程",
  context: "上下文"
};

export function AgentMemoryPanel({ controller, state }: ControlPanelProps) {
  return (
    <div className="agent-control-panel">
      <header className="control-toolbar">
        <div><h2>个人记忆</h2><span>{state.memory.facts.length} 条有效事实</span></div>
        <button className="icon-button" aria-label="刷新个人记忆" title="刷新" disabled={state.loading} onClick={() => void controller.refresh().catch(() => undefined)}><RefreshCw size={17} /></button>
      </header>
      {state.error && <div className="chat-error" role="alert"><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>}
      <section className="control-section" aria-labelledby="memory-facts-title">
        <h3 id="memory-facts-title">有效记忆</h3>
        {state.loading && <div className="control-empty"><RefreshCw className="spin" size={20} />正在读取</div>}
        {!state.loading && state.memory.facts.length === 0 && <div className="control-empty">暂无个人记忆</div>}
        <div className="control-list">
          {state.memory.facts.map((fact) => {
            const pending = state.pendingDeletionIDs.includes(fact.fact_id);
            return (
              <div className="control-row memory-row" key={fact.fact_id}>
                <span className="control-kind">{memoryLabels[fact.category] ?? fact.category}</span>
                <div className="control-copy"><strong>{fact.content}</strong><small>{new Date(fact.updated_at).toLocaleString()}</small></div>
                <button className="icon-button danger" aria-label="删除记忆" title="删除" disabled={pending || state.activeMutation !== null} onClick={() => void controller.deleteMemory(fact.fact_id).catch(() => undefined)}>
                  {pending ? <RefreshCw className="spin" size={16} /> : <Trash2 size={16} />}
                </button>
              </div>
            );
          })}
        </div>
      </section>
      <section className="control-section" aria-labelledby="memory-exposures-title">
        <h3 id="memory-exposures-title">近期使用记录</h3>
        {state.memory.exposures.length === 0 && <div className="control-empty">暂无使用记录</div>}
        <div className="control-list">
          {state.memory.exposures.map((exposure) => (
            <div className="control-row exposure-row" key={exposure.exposure_id}>
              <div className="control-copy"><strong>{exposure.content}</strong><small>{exposure.retrieval_reason} · {new Date(exposure.created_at).toLocaleString()}</small></div>
              <div className="row-actions">
                <button className={exposure.feedback === "helpful" ? "icon-button selected" : "icon-button"} aria-label="这条记忆有帮助" title="有帮助" disabled={Boolean(exposure.feedback) || state.activeMutation !== null} onClick={() => void controller.feedbackMemory(exposure.exposure_id, "helpful").catch(() => undefined)}><ThumbsUp size={15} /></button>
                <button className={exposure.feedback === "not_helpful" ? "icon-button selected" : "icon-button"} aria-label="这条记忆没有帮助" title="没有帮助" disabled={Boolean(exposure.feedback) || state.activeMutation !== null} onClick={() => void controller.feedbackMemory(exposure.exposure_id, "not_helpful").catch(() => undefined)}><ThumbsDown size={15} /></button>
              </div>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

type GroupMemoryPanelProps = ControlPanelProps & { groups: Array<{ conversationID: string; name: string }> };

export function AgentGroupMemoryPanel({ controller, state, groups }: GroupMemoryPanelProps) {
  const [conversationID, setConversationID] = useState("");

  useEffect(() => {
    if (!conversationID && groups.length > 0) setConversationID(groups[0].conversationID);
  }, [conversationID, groups]);

  useEffect(() => {
    if (conversationID) void controller.loadGroupMemory(conversationID).catch(() => undefined);
  }, [controller, conversationID]);

  const snapshot = state.groupMemory?.conversation_id === conversationID ? state.groupMemory : null;
  return (
    <div className="agent-control-panel">
      <header className="control-toolbar">
        <div><h2>群组记忆</h2><span>{snapshot?.facts.length ?? 0} 条共享事实</span></div>
        <div className="row-actions">
          <select aria-label="选择群聊" value={conversationID} onChange={(event) => setConversationID(event.target.value)}>
            {groups.map((group) => <option key={group.conversationID} value={group.conversationID}>{group.name}</option>)}
          </select>
          <button className="icon-button" aria-label="刷新群组记忆" title="刷新" disabled={!conversationID || state.groupMemoryLoading} onClick={() => void controller.loadGroupMemory(conversationID).catch(() => undefined)}><RefreshCw className={state.groupMemoryLoading ? "spin" : ""} size={17} /></button>
        </div>
      </header>
      {state.error && <div className="chat-error" role="alert"><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>}
      {groups.length === 0 && <div className="control-empty"><UsersRound size={21} />暂无可用群聊</div>}
      {groups.length > 0 && state.groupMemoryLoading && <div className="control-empty"><RefreshCw className="spin" size={20} />正在读取</div>}
      {snapshot && <>
        <section className="control-section" aria-labelledby="group-memory-proposals-title">
          <h3 id="group-memory-proposals-title">待审核提取</h3>
          {snapshot.proposals.filter((item) => item.state === "pending").length === 0 && <div className="control-empty">暂无待审核内容</div>}
          <div className="control-list">
            {snapshot.proposals.filter((item) => item.state === "pending").map((proposal) => (
              <div className="control-row memory-row" key={proposal.proposal_id}>
                <span className="control-kind">{memoryLabels[proposal.category] ?? proposal.category}</span>
                <div className="control-copy"><strong>{proposal.content}</strong><small>置信度 {(proposal.confidence * 100).toFixed(0)}% · {new Date(proposal.created_at).toLocaleString()}</small></div>
                <div className="row-actions">
                  <button className="icon-button approve" aria-label="批准群组记忆" title="批准" disabled={state.activeMutation !== null} onClick={() => void controller.reviewGroupMemory(conversationID, proposal.proposal_id, "approve").catch(() => undefined)}><Check size={16} /></button>
                  <button className="icon-button reject" aria-label="拒绝群组记忆" title="拒绝" disabled={state.activeMutation !== null} onClick={() => void controller.reviewGroupMemory(conversationID, proposal.proposal_id, "reject").catch(() => undefined)}><X size={16} /></button>
                </div>
              </div>
            ))}
          </div>
        </section>
        <section className="control-section" aria-labelledby="group-memory-facts-title">
          <h3 id="group-memory-facts-title">有效共享记忆</h3>
          {snapshot.facts.length === 0 && <div className="control-empty">暂无共享记忆</div>}
          <div className="control-list">
            {snapshot.facts.map((fact) => (
              <div className="control-row memory-row" key={fact.fact_id}>
                <span className="control-kind">{memoryLabels[fact.category] ?? fact.category}</span>
                <div className="control-copy"><strong>{fact.content}</strong><small>{new Date(fact.updated_at).toLocaleString()}</small></div>
              </div>
            ))}
          </div>
        </section>
      </>}
    </div>
  );
}

const operationLabels: Record<string, string> = {
  agent_execution: "Agent 执行",
  agent_delivery: "消息投递",
  proactive_dispatch: "主动推送",
  agent_runs: "Agent Run",
  deliveries: "投递队列",
  proactive_events: "主动事件",
  memory_extractions: "记忆提取",
  delegations: "后台委派",
  tool_approvals: "工具审批",
  mcp_health: "MCP 实例"
};

export function AgentOperationsPanel({ controller, state }: ControlPanelProps) {
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const snapshot = state.admin;
  const canMutate = snapshot?.roles.includes("platform_admin") ?? false;

  if (state.adminLoading) return <div className="agent-control-panel"><div className="control-empty"><RefreshCw className="spin" size={20} />正在读取运行状态</div></div>;
  if (state.adminForbidden) return <div className="agent-control-panel"><div className="control-empty"><LockKeyhole size={22} />当前账号没有 Agent 管理权限</div></div>;
  if (!snapshot) return <div className="agent-control-panel"><div className="control-empty"><Activity size={22} />运行状态尚未加载</div></div>;

  const queues = Object.entries(snapshot.operations).filter(([key]) => key !== "generated_at") as Array<[string, { states: Record<string, number>; oldest_ready_seconds: number }]>;
  return (
    <div className="agent-control-panel">
      <header className="control-toolbar">
        <div><h2>Agent 运行管理</h2><span>{new Date(snapshot.operations.generated_at).toLocaleString()}</span></div>
        <button className="icon-button" aria-label="刷新 Agent 运行状态" title="刷新" disabled={state.adminLoading} onClick={() => void controller.loadAdmin().catch(() => undefined)}><RefreshCw size={17} /></button>
      </header>
      {state.error && <div className="chat-error" role="alert"><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>}
      <section className="control-section" aria-labelledby="runtime-queues-title">
        <h3 id="runtime-queues-title">运行队列</h3>
        <div className="runtime-table">
          {queues.map(([key, queue]) => (
            <div className="runtime-row" key={key}>
              <strong>{operationLabels[key] ?? key}</strong>
              <span>{Object.values(queue.states).reduce((sum, value) => sum + value, 0)} 项</span>
              <small>{Object.entries(queue.states).map(([name, count]) => `${name} ${count}`).join(" · ") || "空闲"}</small>
              <code>{queue.oldest_ready_seconds > 0 ? `最久等待 ${Math.round(queue.oldest_ready_seconds)}s` : "无等待"}</code>
            </div>
          ))}
        </div>
      </section>
      <section className="control-section" aria-labelledby="runtime-controls-title">
        <h3 id="runtime-controls-title">事故开关</h3>
        {!canMutate && <div className="control-note">agent_admin 只能查看，platform_admin 才能修改。</div>}
        <div className="control-list">
          {snapshot.controls.map((control) => {
            const reason = reasons[control.component] ?? "";
            return (
              <div className="runtime-control-row" key={control.component}>
                <span className={control.paused ? "runtime-indicator paused" : "runtime-indicator running"}>{control.paused ? <Pause size={15} /> : <Play size={15} />}</span>
                <div className="control-copy"><strong>{operationLabels[control.component]}</strong><small>{control.paused ? `已暂停 · ${control.reason}` : "运行中"} · revision {control.revision}</small></div>
                <input aria-label={`${operationLabels[control.component]}变更原因`} placeholder="填写审计原因" maxLength={500} value={reason} onChange={(event) => setReasons({ ...reasons, [control.component]: event.target.value })} />
                <button className={control.paused ? "secondary-button compact" : "danger-button compact"} disabled={!canMutate || !reason.trim() || state.activeMutation !== null} onClick={() => void controller.setRuntimeControl(control, !control.paused, reason.trim()).then(() => setReasons((current) => ({ ...current, [control.component]: "" }))).catch(() => undefined)}>{control.paused ? "恢复" : "暂停"}</button>
              </div>
            );
          })}
        </div>
      </section>
    </div>
  );
}

const catalogDrafts = {
  skill: `{
  "skill_id": "knowledge.lookup",
  "version": "1",
  "name": "企业知识检索",
  "summary": "从授权企业知识中检索并给出引用",
  "instructions": "仅根据可见证据回答；证据不足时明确拒答。",
  "tool_operations": ["knowledge.search"],
  "audience": "passive"
}`,
  tool: `{
  "operation_id": "knowledge.search",
  "version": "1",
  "name": "企业知识检索",
  "summary": "检索当前成员有权访问的企业知识",
  "source_id": "enterprise-knowledge",
  "source_operation": "knowledge_search",
  "risk": "read",
  "permissions": ["knowledge:read"],
  "idempotency": "native",
  "retry_semantics": "safe",
  "timeout_ms": 10000,
  "audience": "passive",
  "parameter_terms": ["知识", "文档", "制度"],
  "examples": ["查询报销制度"],
  "output_kinds": ["citations"],
  "input_schema": {"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}
}`,
  agent: `{
  "slug": "knowledge-assistant",
  "display_name": "企业知识助手",
  "description": "面向内部制度和流程的引用式问答",
  "mention_alias": "知识助手",
  "capability_snapshot_id": "capability-v1:替换为已发布快照",
  "skill_ids": ["knowledge.lookup@1"],
  "spec": {"schema_version":1,"execution_plane":"passive","system_prompt":"严格依据授权证据回答。"}
}`,
  remote: `{
  "slug": "remote-research",
  "display_name": "远程研究 Agent",
  "card_url": "https://agent.example.com/.well-known/agent-card.json",
  "expected_card_digest": "sha256:替换为管理员核验的64位摘要",
  "auth_env_key": "REMOTE_RESEARCH_A2A_TOKEN"
}`
};

type CatalogDraftKind = keyof typeof catalogDrafts;

export function AgentCatalogAdminPanel({ controller, state }: ControlPanelProps) {
  const [draftKind, setDraftKind] = useState<CatalogDraftKind>("skill");
  const [draft, setDraft] = useState(catalogDrafts.skill);
  const [selectedTools, setSelectedTools] = useState<string[]>([]);
  const catalog = state.adminCatalog;
  const canPublish = catalog?.roles.includes("platform_admin") ?? false;
  const canCreateAgent = canPublish || (catalog?.roles.includes("agent_admin") ?? false);

  const chooseDraft = (kind: CatalogDraftKind) => {
    setDraftKind(kind);
    setDraft(catalogDrafts[kind]);
  };

  const publish = async () => {
    const value = JSON.parse(draft) as Record<string, unknown>;
    if (draftKind === "skill") await controller.publishSkill(value as PublishSkillInput);
    if (draftKind === "tool") await controller.publishTool(value as Omit<AgentCatalogTool, "id" | "schema_digest" | "source_type">);
    if (draftKind === "agent") await controller.createAgent(value as CreateAgentInput);
    if (draftKind === "remote") await controller.registerRemoteAgent(value as RegisterRemoteA2AInput);
  };

  const toggleTool = (key: string) => setSelectedTools((current) => current.includes(key) ? current.filter((item) => item !== key) : [...current, key]);
  const publishSnapshot = async () => {
    const selected = (catalog?.tools ?? []).filter((tool) => selectedTools.includes(`${tool.operation_id}@${tool.version}`));
    await controller.publishCapabilitySnapshot(selected.map((tool) => ({ operation_id: tool.operation_id, version: tool.version })));
    setSelectedTools([]);
  };

  if (state.adminLoading) return <div className="agent-control-panel"><div className="control-empty"><RefreshCw className="spin" size={20} />正在读取管理目录</div></div>;
  if (state.adminForbidden) return <div className="agent-control-panel"><div className="control-empty"><LockKeyhole size={22} />当前账号没有 Agent 管理权限</div></div>;
  if (!catalog) return <div className="agent-control-panel"><div className="control-empty"><Boxes size={22} />管理目录尚未加载</div></div>;

  return (
    <div className="agent-control-panel">
      <header className="control-toolbar">
        <div><h2>Agent 目录管理</h2><span>{catalog.agents.length} Agent · {catalog.skills.length} Skill · {catalog.tools.length} Tool</span></div>
        <button className="icon-button" aria-label="刷新 Agent 管理目录" title="刷新" disabled={state.adminLoading} onClick={() => void controller.loadAdmin().catch(() => undefined)}><RefreshCw size={17} /></button>
      </header>
      {state.error && <div className="chat-error" role="alert"><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>}
      <section className="control-section catalog-summary" aria-label="Agent 目录清单">
        <div><Bot size={17} /><strong>{catalog.agents.length}</strong><span>Agent</span></div>
        <div><Boxes size={17} /><strong>{catalog.skills.length}</strong><span>Skill</span></div>
        <div><Wrench size={17} /><strong>{catalog.tools.length}</strong><span>Tool</span></div>
        <div><Server size={17} /><strong>{catalog.mcp_servers.length}</strong><span>MCP</span></div>
      </section>
      <section className="control-section" aria-labelledby="catalog-mcp-title">
        <h3 id="catalog-mcp-title">MCP 运行入口</h3>
        <div className="control-list">
          {catalog.mcp_servers.map((server) => <div className="control-row" key={server.id}>
            <span className={`runtime-indicator ${server.state === "healthy" ? "running" : "paused"}`}><Server size={15} /></span>
            <div className="control-copy"><strong>{server.slug}</strong><small>{server.state}{server.last_error_code ? ` · ${server.last_error_code}` : ""} · 重启 {server.restart_count}</small></div>
            <label className="catalog-toggle"><input type="checkbox" checked={server.enabled} disabled={!canPublish || state.activeMutation !== null} onChange={(event) => void controller.setMCPEnabled(server.slug, event.target.checked).catch(() => undefined)} /><span>{server.enabled ? "已启用" : "已停用"}</span></label>
          </div>)}
          {catalog.mcp_servers.length === 0 && <div className="control-empty">暂无注册的 MCP Server</div>}
        </div>
      </section>
      <section className="control-section" aria-labelledby="catalog-a2a-title">
        <h3 id="catalog-a2a-title">远程 A2A 1.0</h3>
        {!catalog.remote_a2a_enabled && <div className="control-note">部署未配置远程主机白名单，A2A 管理和调用均处于关闭状态。</div>}
        <div className="control-list">
          {catalog.remote_agents.map((remote) => <div className="control-row" key={remote.id}>
            <span className={`runtime-indicator ${remote.enabled && remote.lifecycle_state === "verified" ? "running" : "paused"}`}><Workflow size={15} /></span>
            <div className="control-copy"><strong>{remote.display_name} · {remote.slug}</strong><small>{remote.lifecycle_state} · revision {remote.lifecycle_revision}{remote.last_error_code ? ` · ${remote.last_error_code}` : ""}</small></div>
            <div className="row-actions">
              <button className="secondary-button compact" disabled={!canPublish || !catalog.remote_a2a_enabled || state.activeMutation !== null} onClick={() => void controller.verifyRemoteAgent(remote.slug, remote.lifecycle_revision).catch(() => undefined)}>核验</button>
              <label className="catalog-toggle"><input type="checkbox" checked={remote.enabled} disabled={!canPublish || remote.lifecycle_state !== "verified" || state.activeMutation !== null} onChange={(event) => void controller.setRemoteAgentEnabled(remote.slug, event.target.checked, remote.lifecycle_revision).catch(() => undefined)} /><span>{remote.enabled ? "已启用" : "已停用"}</span></label>
            </div>
          </div>)}
          {catalog.remote_agents.length === 0 && <div className="control-empty">暂无显式注册的远程 Agent</div>}
        </div>
      </section>
      <section className="control-section" aria-labelledby="catalog-role-title">
        <h3 id="catalog-role-title">管理员角色</h3>
        <div className="catalog-member-table">
          {catalog.members.map((member) => <div className="catalog-member-row" key={member.id}>
            <div className="control-copy"><strong>{member.display_name || member.id}</strong><small>{member.status} · {member.id}</small></div>
            {(["platform_admin", "agent_admin", "knowledge_admin"] as AgentAdminRole[]).map((role) => <label key={role}><input type="checkbox" checked={member.roles.includes(role)} disabled={!canPublish || state.activeMutation !== null} onChange={(event) => void controller.setMemberRole(member.id, role, event.target.checked).catch(() => undefined)} /><span>{role}</span></label>)}
          </div>)}
        </div>
      </section>
      <section className="control-section" aria-labelledby="catalog-tools-title">
        <h3 id="catalog-tools-title">能力快照</h3>
        <div className="catalog-tool-grid">
          {catalog.tools.map((tool) => {
            const key = `${tool.operation_id}@${tool.version}`;
            return <label key={tool.id}><input type="checkbox" checked={selectedTools.includes(key)} onChange={() => toggleTool(key)} /><span><strong>{tool.operation_id}</strong><small>{tool.risk} · {tool.source_id}</small></span></label>;
          })}
        </div>
        <div className="catalog-action-row"><span>已选择 {selectedTools.length} 个 Tool；已发布 {catalog.capability_snapshots.length} 个不可变快照</span><button className="secondary-button compact" disabled={!canPublish || selectedTools.length === 0 || state.activeMutation !== null} onClick={() => void publishSnapshot().catch(() => undefined)}><Plus size={15} />发布快照</button></div>
      </section>
      <section className="control-section" aria-labelledby="catalog-publish-title">
        <h3 id="catalog-publish-title">版本发布</h3>
        <div className="catalog-mode" role="tablist" aria-label="发布资源类型">
          {(["skill", "tool", "agent", "remote"] as CatalogDraftKind[]).map((kind) => <button key={kind} className={draftKind === kind ? "active" : ""} role="tab" aria-selected={draftKind === kind} onClick={() => chooseDraft(kind)}>{kind === "skill" ? "Skill" : kind === "tool" ? "MCP Tool" : kind === "remote" ? "远程 A2A" : "Agent"}</button>)}
        </div>
        <textarea className="catalog-editor" aria-label="资源发布 JSON" spellCheck={false} value={draft} onChange={(event) => setDraft(event.target.value)} />
        <div className="catalog-action-row"><span>发布为不可变版本；冲突和校验失败会明确返回失败。</span><button className="primary-button compact" disabled={state.activeMutation !== null || (draftKind === "agent" ? !canCreateAgent : !canPublish) || (draftKind === "remote" && !catalog.remote_a2a_enabled)} onClick={() => void publish().catch(() => undefined)}><Plus size={15} />发布</button></div>
      </section>
    </div>
  );
}

type ProactivePanelProps = ControlPanelProps & { agentID: string };

export function AgentProactivePanel({ controller, state, agentID }: ProactivePanelProps) {
  const [query, setQuery] = useState("");
  const [categories, setCategories] = useState("cs.AI");
  const [channel, setChannel] = useState<"openim" | "telegram">("openim");
  const [preference, setPreference] = useState<ProactivePreference | null>(null);

  useEffect(() => {
    if (state.proactive) setPreference(state.proactive.preference);
  }, [state.proactive]);

  const create = async () => {
    const value = query.trim();
    if (!value || !agentID) return;
    await controller.createSubscription({
      agent_id: agentID,
      query: value,
      categories: categories.split(",").map((item) => item.trim()).filter(Boolean),
      source_channel: channel,
      poll_interval_seconds: 1800
    });
    setQuery("");
  };

  return (
    <div className="agent-control-panel">
      <header className="control-toolbar">
        <div><h2>主动订阅</h2><span>{state.proactive?.subscriptions.length ?? 0} 个主题</span></div>
        <button className="icon-button" aria-label="刷新主动订阅" title="刷新" disabled={state.loading} onClick={() => void controller.refresh().catch(() => undefined)}><RefreshCw size={17} /></button>
      </header>
      {state.error && <div className="chat-error" role="alert"><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>}
      <section className="control-section subscription-create" aria-labelledby="subscription-create-title">
        <h3 id="subscription-create-title">新增 arXiv 订阅</h3>
        <div className="control-form-row">
          <input aria-label="订阅主题" placeholder="例如 Agent memory" maxLength={500} value={query} onChange={(event) => setQuery(event.target.value)} />
          <input aria-label="arXiv 类别" placeholder="cs.AI, cs.CL" value={categories} onChange={(event) => setCategories(event.target.value)} />
          <select aria-label="投递渠道" value={channel} onChange={(event) => setChannel(event.target.value as "openim" | "telegram")}>
            <option value="openim">OpenIM</option><option value="telegram">Telegram</option>
          </select>
          <button className="primary-button compact" disabled={!query.trim() || state.activeMutation !== null} onClick={() => void create().catch(() => undefined)}>订阅</button>
        </div>
      </section>
      {preference && (
        <section className="control-section" aria-labelledby="proactive-preference-title">
          <h3 id="proactive-preference-title">通知偏好</h3>
          <div className="preference-grid">
            <label><span>启用通知</span><input type="checkbox" checked={preference.enabled} onChange={(event) => setPreference({ ...preference, enabled: event.target.checked })} /></label>
            <label><span>时区</span><input value={preference.timezone} onChange={(event) => setPreference({ ...preference, timezone: event.target.value })} /></label>
            <label><span>免打扰开始</span><input type="time" value={preference.quiet_start} onChange={(event) => setPreference({ ...preference, quiet_start: event.target.value })} /></label>
            <label><span>免打扰结束</span><input type="time" value={preference.quiet_end} onChange={(event) => setPreference({ ...preference, quiet_end: event.target.value })} /></label>
            <label><span>每日上限</span><input type="number" min={0} max={50} value={preference.daily_budget} onChange={(event) => setPreference({ ...preference, daily_budget: Number(event.target.value) })} /></label>
            <label><span>最低相关度</span><input type="number" min={0} max={1} step={0.05} value={preference.minimum_score} onChange={(event) => setPreference({ ...preference, minimum_score: Number(event.target.value) })} /></label>
          </div>
          <button className="secondary-button" disabled={state.activeMutation !== null} onClick={() => void controller.updatePreference(preference).catch(() => undefined)}>保存偏好</button>
        </section>
      )}
      <section className="control-section" aria-labelledby="subscriptions-title">
        <h3 id="subscriptions-title">订阅主题</h3>
        {!state.loading && (state.proactive?.subscriptions.length ?? 0) === 0 && <div className="control-empty">暂无订阅</div>}
        <div className="control-list">
          {state.proactive?.subscriptions.map((subscription) => (
            <div className="control-row subscription-row" key={subscription.subscription_id}>
              <span className="control-kind">{subscription.source_channel === "openim" ? "OpenIM" : "Telegram"}</span>
              <div className="control-copy"><strong>{subscription.query}</strong><small>{subscription.categories.join(", ") || "全部类别"}{subscription.last_error ? ` · ${subscription.last_error}` : ""}</small></div>
              <button className={subscription.enabled ? "icon-button selected" : "icon-button"} aria-label={subscription.enabled ? "暂停订阅" : "启用订阅"} title={subscription.enabled ? "暂停" : "启用"} disabled={state.activeMutation !== null} onClick={() => void controller.setSubscriptionEnabled(subscription.subscription_id, !subscription.enabled).catch(() => undefined)}>{subscription.enabled ? <Bell size={16} /> : <BellOff size={16} />}</button>
            </div>
          ))}
        </div>
      </section>
      <section className="control-section" aria-labelledby="proactive-events-title">
        <h3 id="proactive-events-title">近期推荐</h3>
        {(state.proactive?.events.length ?? 0) === 0 && <div className="control-empty">暂无推荐</div>}
        <div className="control-list">
          {state.proactive?.events.map((event) => (
            <div className="control-row event-row" key={event.event_id}>
              <div className="control-copy"><strong>{event.title}</strong><small>{event.state}{typeof event.score === "number" ? ` · ${(event.score * 100).toFixed(0)}%` : ""}</small></div>
              <div className="row-actions">
                <a className="icon-button" href={event.url} target="_blank" rel="noreferrer" aria-label="打开论文" title="打开论文"><ExternalLink size={15} /></a>
                {event.state === "delivered" && !event.feedback && <button className="icon-button" aria-label="感兴趣" title="感兴趣" disabled={state.activeMutation !== null} onClick={() => void controller.acknowledge(event.event_id, "interesting").catch(() => undefined)}><ThumbsUp size={15} /></button>}
                {event.state === "delivered" && !event.feedback && <button className="icon-button" aria-label="不感兴趣" title="不感兴趣" disabled={state.activeMutation !== null} onClick={() => void controller.acknowledge(event.event_id, "not_interesting").catch(() => undefined)}><ThumbsDown size={15} /></button>}
              </div>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
