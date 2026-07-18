import { Bot, BookOpen, CheckCircle2, CircleAlert, Clock3, Send, ShieldCheck, Ticket } from "lucide-react";
import { useMemo, useState } from "react";

import type { AgentController, AgentState } from "./agent";
import type { AgentRun } from "./agent-api";

type AgentWorkspaceProps = { controller: AgentController; state: AgentState };

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

export function AgentWorkspace({ controller, state }: AgentWorkspaceProps) {
  const [draft, setDraft] = useState("");
  const orderedRuns = useMemo(() => [...state.runs].reverse(), [state.runs]);

  const send = async () => {
    if (!draft.trim() || state.sending) return;
    const content = draft;
    setDraft("");
    try { await controller.sendPrompt(content); } catch { /* State contains the explicit error. */ }
  };

  return (
    <div className="agent-layout">
      <aside className="agent-sidebar" aria-label="智能助手列表">
        <header className="agent-sidebar-header">
          <h1>智能助手</h1>
          <p>企业知识与受控动作</p>
        </header>
        <button className="agent-item active" aria-current="page">
          <span className="agent-avatar"><Bot size={20} /></span>
          <span><strong>{state.agent?.display_name ?? "正在加载"}</strong><small>{state.agent?.description ?? "读取 Agent Catalog"}</small></span>
        </button>
      </aside>

      <section className="agent-main" aria-label="Agent 工作台">
        <header className="agent-header">
          <span className="agent-avatar"><Bot size={20} /></span>
          <div><h2>{state.agent?.display_name ?? "Agent"}</h2><span data-testid="agent-user-id">{state.agentUserID || "正在初始化"}</span></div>
          <span className="agent-policy"><ShieldCheck size={15} />权限约束</span>
        </header>

        {state.error && (
          <div className="chat-error" role="alert"><CircleAlert size={16} /><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>
        )}

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
                  <small className="agent-version" title={run.agent_spec_checksum}>{run.agent_display_name} · v{run.agent_version_number}</small>
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
      </section>
    </div>
  );
}
