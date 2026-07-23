import { getSDK, SessionType, type MessageItem } from "@openim/wasm-client-sdk";

import type { AgentRun, AgentSummary, AgentWorkspaceSnapshot, ApprovalResult } from "./agent-api";

export type AgentState = {
  agentUserID: string;
  agent: AgentSummary | null;
  runs: AgentRun[];
  loading: boolean;
  sending: boolean;
  approvingIntentID: string | null;
  pendingPrompt: string | null;
  error: string | null;
};

export const initialAgentState: AgentState = {
  agentUserID: "",
  agent: null,
  runs: [],
  loading: false,
  sending: false,
  approvingIntentID: null,
  pendingPrompt: null,
  error: null
};

export type AgentDataPort = {
  catalog: () => Promise<AgentSummary[]>;
  workspace: () => Promise<AgentWorkspaceSnapshot>;
  approve: (intentID: string, digest: string) => Promise<ApprovalResult>;
};

export type AgentTransportPort = {
  ensureConversation: (agentUserID: string) => Promise<void>;
  createText: (content: string) => Promise<MessageItem>;
  send: (agentUserID: string, message: MessageItem) => Promise<MessageItem>;
};

function messageOf(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null && "errMsg" in error && typeof error.errMsg === "string") return error.errMsg;
  return "Agent operation failed";
}

function runIsActive(run: AgentRun): boolean {
  if (["queued", "running", "reply_pending"].includes(run.state)) return true;
  if (!run.intent) return false;
  return ["approved", "executing"].includes(run.intent.state) ||
    ["queued", "running", "unknown"].includes(run.intent.execution_state ?? "");
}

export class AgentController {
  private state = initialAgentState;
  private listeners = new Set<(state: AgentState) => void>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = true;
  private pollDeadline = 0;

  constructor(private readonly data: AgentDataPort, private readonly transport: AgentTransportPort) {}

  subscribe(listener: (state: AgentState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  getState(): AgentState { return this.state; }

  async start(): Promise<void> {
    this.stopped = false;
    this.update({ loading: true, error: null });
    try {
      await this.refresh();
      this.update({ loading: false });
      if (this.state.runs.some(runIsActive)) this.beginPolling();
    } catch (error) {
      this.fail(error, { loading: false });
      throw error;
    }
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }

  async sendPrompt(prompt: string): Promise<void> {
    const content = prompt.trim();
    if (!content) throw new Error("Agent question is required");
    if (content.length > 4000) throw new Error("Agent question exceeds 4000 characters");
    if (!this.state.agentUserID || !this.state.agent) throw new Error("Agent workspace is not ready");
    this.update({ sending: true, pendingPrompt: content, error: null });
    try {
      const message = await this.transport.createText(`${content} ${this.state.agent.trigger_alias}`);
      await this.transport.send(this.state.agentUserID, message);
      await this.refresh();
      this.update({ sending: false });
      this.beginPolling();
    } catch (error) {
      this.fail(error, { sending: false, pendingPrompt: null });
      throw error;
    }
  }

  async approve(intentID: string): Promise<void> {
    const run = this.state.runs.find((item) => item.intent?.intent_id === intentID);
    const intent = run?.intent;
    if (!intent || intent.state !== "pending_approval") throw new Error("pending Agent intent is unavailable");
    this.update({ approvingIntentID: intentID, error: null });
    try {
      await this.data.approve(intent.intent_id, intent.payload_digest);
      await this.refresh();
      this.update({ approvingIntentID: null });
      this.beginPolling();
    } catch (error) {
      this.fail(error, { approvingIntentID: null });
      throw error;
    }
  }

  clearError(): void { this.update({ error: null }); }

  private async refresh(): Promise<void> {
    const [catalog, snapshot] = await Promise.all([this.data.catalog(), this.data.workspace()]);
    if (catalog.length !== 1) throw new Error("Agent Catalog v1 requires exactly one active Agent");
    const agent = catalog[0];
    if (agent.bot_user_id !== snapshot.agent_user_id) throw new Error("Agent catalog and workspace bot identities do not match");
    if (this.state.agentUserID && snapshot.agent_user_id !== this.state.agentUserID) {
      throw new Error("Agent workspace identity changed unexpectedly");
    }
    await this.transport.ensureConversation(snapshot.agent_user_id);
    const pending = this.state.pendingPrompt;
    this.update({
      agentUserID: snapshot.agent_user_id,
      agent,
      runs: snapshot.runs,
      pendingPrompt: pending && snapshot.runs.some((run) => run.prompt === pending) ? null : pending
    });
  }

  private beginPolling(): void {
    this.pollDeadline = Date.now() + 120_000;
    this.schedulePoll();
  }

  private schedulePoll(): void {
    if (this.stopped || this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.poll();
    }, 1000);
  }

  private async poll(): Promise<void> {
    if (this.stopped) return;
    if (Date.now() > this.pollDeadline) {
      this.fail(new Error("Agent status refresh timed out"));
      return;
    }
    try {
      await this.refresh();
      if (this.state.pendingPrompt || this.state.runs.some(runIsActive)) this.schedulePoll();
    } catch (error) {
      this.fail(error);
    }
  }

  private fail(error: unknown, patch: Partial<AgentState> = {}): void {
    this.update({ ...patch, error: messageOf(error) });
  }

  private update(patch: Partial<AgentState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

export function createOpenIMAgentTransport(): AgentTransportPort {
  const sdk = getSDK();
  return {
    ensureConversation: async (agentUserID) => {
      await sdk.getOneConversation({ sourceID: agentUserID, sessionType: SessionType.Single });
    },
    createText: async (content) => (await sdk.createTextMessage(content)).data,
    send: async (agentUserID, message) => (await sdk.sendMessage({ recvID: agentUserID, groupID: "", message })).data
  };
}
