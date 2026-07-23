import { Activity, Bot, BookOpen, Brain, CheckCircle2, CircleAlert, Clock3, Download, FileJson, Radio, Send, Settings2, ShieldCheck, Ticket, UsersRound, X } from "lucide-react";
import { useMemo, useState } from "react";

import { serializeAgentReplay } from "./agent-control-api";
import type { AgentControlController, AgentControlState } from "./agent-control";
import { AgentCatalogAdminPanel, AgentDelegationPanel, AgentGroupMemoryPanel, AgentMemoryPanel, AgentOperationsPanel, AgentProactivePanel, AgentToolApprovalPanel } from "./AgentControlPanels";
import type { AgentController, AgentState } from "./agent";
import type { AgentRun } from "./agent-api";

type AgentWorkspaceProps = {
  controller: AgentController;
  state: AgentState;
  controlController: AgentControlController;
  controlState: AgentControlState;
  groups: Array<{ conversationID: string; name: string }>;
};

function runStatus(run: AgentRun): string {
  if (run.state === "queued") return "排队中";
  if (run.state === "running") return "分析中";
  if (run.state === "reply_pending") return "正在回复";
  if (run.state === "waiting_approval") return "等待审批";
  if (run.state === "failed") return "执行失败";
  return "已完成";
}

function executionStatus(run: AgentRun): string {
  const state = run.intent?.execution_state ?? run.intent?.state;
  if (state === "queued" || state === "approved") return "已批准，等待执行";
  if (state === "running" || state === "executing") return "正在执行";
  if (state === "unknown") return "正在核验执行结果";
  if (state === "succeeded") return "工单已创建";
  if (state === "failed") return "动作执行失败";
  if (state === "expired") return "审批已过期";
  return "需要你的批准";
}

export function AgentWorkspace({ controller, state, controlController, controlState, groups }: AgentWorkspaceProps) {
  const [draft, setDraft] = useState("");
  const [panel, setPanel] = useState<"assistant" | "memory" | "group-memory" | "proactive" | "operations" | "catalog">("assistant");
  const orderedRuns = useMemo(() => [...state.runs].reverse(), [state.runs]);

  const send = async () => {
    if (!draft.trim() || state.sending) return;
    const content = draft;
    setDraft("");
    try { await controller.sendPrompt(content); } catch { /* State contains the explicit error. */ }
  };

  const downloadReplay = () => {
    if (!controlState.replay) return;
    const blob = new Blob([serializeAgentReplay(controlState.replay)], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `agent-replay-${controlState.replay.checksum.replace("sha256:", "").slice(0, 16)}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="agent-layout">
      <aside className="agent-sidebar" aria-label="智能助手列表">
        <header className="agent-sidebar-header">
          <h1>智能助手</h1>
          <p>企业知识与受控动作</p>
        </header>
        <button className={`agent-item ${panel === "assistant" ? "active" : ""}`} aria-current={panel === "assistant" ? "page" : undefined} onClick={() => setPanel("assistant")}>
          <span className="agent-avatar"><Bot size={20} /></span>
          <span><strong>{state.agent?.display_name ?? "正在加载"}</strong><small>{state.agent?.description ?? "读取 Agent Catalog"}</small></span>
        </button>
        <div className="agent-control-nav">
          <button className={`agent-item compact ${panel === "memory" ? "active" : ""}`} aria-current={panel === "memory" ? "page" : undefined} onClick={() => setPanel("memory")}><span className="agent-avatar"><Brain size={18} /></span><span><strong>个人记忆</strong><small>{controlState.memory.facts.length} 条有效事实</small></span></button>
          <button className={`agent-item compact ${panel === "group-memory" ? "active" : ""}`} aria-current={panel === "group-memory" ? "page" : undefined} onClick={() => setPanel("group-memory")}><span className="agent-avatar"><UsersRound size={18} /></span><span><strong>群组记忆</strong><small>共享事实与审核</small></span></button>
          <button className={`agent-item compact ${panel === "proactive" ? "active" : ""}`} aria-current={panel === "proactive" ? "page" : undefined} onClick={() => setPanel("proactive")}><span className="agent-avatar"><Radio size={18} /></span><span><strong>主动订阅</strong><small>{controlState.proactive?.subscriptions.length ?? 0} 个主题</small></span></button>
          <button className={`agent-item compact ${panel === "operations" ? "active" : ""}`} aria-current={panel === "operations" ? "page" : undefined} onClick={() => { setPanel("operations"); if (!controlState.admin && !controlState.adminForbidden) void controlController.loadAdmin().catch(() => undefined); }}><span className="agent-avatar"><Activity size={18} /></span><span><strong>运行管理</strong><small>队列与事故开关</small></span></button>
          <button className={`agent-item compact ${panel === "catalog" ? "active" : ""}`} aria-current={panel === "catalog" ? "page" : undefined} onClick={() => { setPanel("catalog"); if (!controlState.adminCatalog && !controlState.adminForbidden) void controlController.loadAdmin().catch(() => undefined); }}><span className="agent-avatar"><Settings2 size={18} /></span><span><strong>目录管理</strong><small>Agent、Skill 与 Tool</small></span></button>
        </div>
      </aside>

      <section className="agent-main" aria-label="Agent 工作台">
        <nav className="agent-mobile-nav" aria-label="智能助手视图">
          <button className={panel === "assistant" ? "active" : ""} aria-label="助手对话" title="助手对话" onClick={() => setPanel("assistant")}><Bot size={18} /></button>
          <button className={panel === "memory" ? "active" : ""} aria-label="个人记忆" title="个人记忆" onClick={() => setPanel("memory")}><Brain size={18} /></button>
          <button className={panel === "group-memory" ? "active" : ""} aria-label="群组记忆" title="群组记忆" onClick={() => setPanel("group-memory")}><UsersRound size={18} /></button>
          <button className={panel === "proactive" ? "active" : ""} aria-label="主动订阅" title="主动订阅" onClick={() => setPanel("proactive")}><Radio size={18} /></button>
          <button className={panel === "operations" ? "active" : ""} aria-label="运行管理" title="运行管理" onClick={() => { setPanel("operations"); if (!controlState.admin && !controlState.adminForbidden) void controlController.loadAdmin().catch(() => undefined); }}><Activity size={18} /></button>
          <button className={panel === "catalog" ? "active" : ""} aria-label="目录管理" title="目录管理" onClick={() => { setPanel("catalog"); if (!controlState.adminCatalog && !controlState.adminForbidden) void controlController.loadAdmin().catch(() => undefined); }}><Settings2 size={18} /></button>
        </nav>
        {panel === "memory" && <AgentMemoryPanel controller={controlController} state={controlState} />}
        {panel === "group-memory" && <AgentGroupMemoryPanel controller={controlController} state={controlState} groups={groups} />}
        {panel === "proactive" && <AgentProactivePanel controller={controlController} state={controlState} agentID={state.agent?.agent_id ?? ""} />}
        {panel === "operations" && <AgentOperationsPanel controller={controlController} state={controlState} />}
        {panel === "catalog" && <AgentCatalogAdminPanel controller={controlController} state={controlState} />}
        {panel === "assistant" && <>
        <header className="agent-header">
          <span className="agent-avatar"><Bot size={20} /></span>
          <div><h2>{state.agent?.display_name ?? "Agent"}</h2><span data-testid="agent-user-id">{state.agentUserID || "正在初始化"}</span></div>
          <span className="agent-policy"><ShieldCheck size={15} />权限约束</span>
        </header>

        {state.error && (
          <div className="chat-error" role="alert"><CircleAlert size={16} /><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>
        )}
        <AgentToolApprovalPanel controller={controlController} state={controlState} />
        <AgentDelegationPanel state={controlState} />

        <div className="agent-timeline" aria-label="Agent 运行记录">
          {state.loading && <div className="agent-empty"><Clock3 className="spin" size={24} /><span>正在恢复工作台</span></div>}
          {!state.loading && orderedRuns.length === 0 && !state.pendingPrompt && (
            <div className="agent-empty"><Bot size={30} /><strong>开始一次企业知识问答</strong></div>
          )}
          {orderedRuns.map((run) => (
            <article className="agent-run" key={run.run_id} data-testid={`agent-run-${run.run_id}`}>
              <div className="agent-question"><span>你</span><p>{run.prompt}</p></div>
              <div className="agent-answer">
                <span className="agent-avatar small"><Bot size={15} /></span>
                <div className="agent-response">
                  <div className={`run-state ${run.state}`}><Clock3 size={13} />{runStatus(run)}</div>
                  <div className="agent-version-row"><small className="agent-version" title={run.agent_spec_checksum}>{run.agent_display_name} · v{run.agent_version_number}</small><button className="replay-button" aria-label="查看运行证据" title="运行证据" disabled={controlState.activeMutation !== null} onClick={() => void controlController.loadReplay(run.run_id).catch(() => undefined)}><FileJson size={14} /></button></div>
                  {run.answer && <p>{run.answer}</p>}
                  {run.last_error && <p className="agent-run-error">{run.last_error}</p>}
                  {run.citations.length > 0 && (
                    <section className="citation-section" aria-label="引用来源">
                      <h3><BookOpen size={15} />引用来源</h3>
                      {run.citations.map((citation) => (
                        <div className="citation-row" key={citation.citation_id}>
                          <b>[{citation.citation_id}]</b><span>{citation.title}</span><code>{citation.source_uri}</code>
                        </div>
                      ))}
                    </section>
                  )}
                  {run.intent && (
                    <section className="approval-panel" data-testid={`intent-${run.intent.intent_id}`}>
                      <div className="approval-icon"><Ticket size={18} /></div>
                      <div className="approval-copy">
                        <strong>{run.intent.title}</strong>
                        <span>{executionStatus(run)}</span>
                        {run.intent.ticket_id && <code>Ticket {run.intent.ticket_id}</code>}
                      </div>
                      {run.intent.state === "pending_approval" && (
                        <button
                          className="approve-button"
                          disabled={state.approvingIntentID === run.intent.intent_id}
                          onClick={() => void controller.approve(run.intent!.intent_id).catch(() => undefined)}
                        >
                          <CheckCircle2 size={16} />{state.approvingIntentID === run.intent.intent_id ? "批准中" : "批准创建"}
                        </button>
                      )}
                    </section>
                  )}
                </div>
              </div>
            </article>
          ))}
          {state.pendingPrompt && (
            <article className="agent-run pending" data-testid="agent-pending-run">
              <div className="agent-question"><span>你</span><p>{state.pendingPrompt}</p></div>
              <div className="agent-answer"><span className="agent-avatar small"><Bot size={15} /></span><div className="run-state running"><Clock3 className="spin" size={13} />正在接收任务</div></div>
            </article>
          )}
        </div>

        <footer className="agent-composer">
          <textarea
            aria-label="向 Agent 提问"
            placeholder={`向 ${state.agent?.display_name ?? "Agent"} 提问`}
            maxLength={4000}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(); }
            }}
          />
          <button className="send-button" aria-label="发送给 Agent" title="发送" disabled={!draft.trim() || state.sending || !state.agentUserID} onClick={() => void send()}><Send size={18} /></button>
        </footer>
        </>}
      </section>
      {controlState.replay && (
        <div className="replay-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) controlController.closeReplay(); }}>
          <section className="replay-dialog" role="dialog" aria-modal="true" aria-labelledby="replay-title">
            <header><div><h2 id="replay-title">运行证据</h2><code>{controlState.replay.checksum}</code></div><span className="replay-actions"><button className="icon-button" aria-label="下载运行证据" title="下载" onClick={downloadReplay}><Download size={17} /></button><button className="icon-button" aria-label="关闭运行证据" title="关闭" onClick={() => controlController.closeReplay()}><X size={17} /></button></span></header>
            <pre>{JSON.stringify(controlState.replay, null, 2)}</pre>
          </section>
        </div>
      )}
    </div>
  );
}
