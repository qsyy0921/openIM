import type {
  AgentReplayBundle,
  AgentAdminSnapshot,
  AgentAdminCatalog,
  AgentAdminRole,
  AgentCatalogTool,
  AgentDelegation,
  CreateAgentInput,
  GroupMemorySnapshot,
  CreateProactiveSubscription,
  MemorySnapshot,
  ProactivePreference,
  ProactiveSnapshot,
  PublishSkillInput,
  RegisterRemoteA2AInput,
  ToolApproval,
  RuntimeControl
} from "./agent-control-api";
import { PlatformAPIError } from "./platform-api";

export type AgentControlState = {
  memory: MemorySnapshot;
  groupMemory: GroupMemorySnapshot | null;
  groupMemoryLoading: boolean;
  proactive: ProactiveSnapshot | null;
  toolApprovals: ToolApproval[];
  loading: boolean;
  activeMutation: string | null;
  pendingDeletionIDs: string[];
  error: string | null;
  replay: AgentReplayBundle | null;
  delegations: AgentDelegation[];
  admin: AgentAdminSnapshot | null;
  adminCatalog: AgentAdminCatalog | null;
  adminLoading: boolean;
  adminForbidden: boolean;
};

export const initialAgentControlState: AgentControlState = {
  memory: { facts: [], exposures: [] },
  groupMemory: null,
  groupMemoryLoading: false,
  proactive: null,
  toolApprovals: [],
  loading: false,
  activeMutation: null,
  pendingDeletionIDs: [],
  error: null,
  replay: null,
  delegations: [],
  admin: null,
  adminCatalog: null,
  adminLoading: false,
  adminForbidden: false
};

export type AgentControlDataPort = {
  memory: () => Promise<MemorySnapshot>;
  groupMemory: (conversationID: string) => Promise<GroupMemorySnapshot>;
  reviewGroupMemory: (conversationID: string, proposalID: string, decision: "approve" | "reject") => Promise<void>;
  deleteMemory: (factID: string, idempotencyKey: string) => Promise<void>;
  feedbackMemory: (exposureID: string, signal: "helpful" | "not_helpful" | "incorrect") => Promise<void>;
  proactive: () => Promise<ProactiveSnapshot>;
  createSubscription: (input: CreateProactiveSubscription) => Promise<void>;
  setSubscriptionEnabled: (subscriptionID: string, enabled: boolean) => Promise<void>;
  updatePreference: (preference: ProactivePreference) => Promise<void>;
  acknowledge: (eventID: string, signal: "interesting" | "not_interesting" | "dismissed") => Promise<void>;
  toolApprovals: () => Promise<ToolApproval[]>;
  decideToolApproval: (approvalID: string, argumentsDigest: string, decision: "approve" | "reject") => Promise<void>;
  replay: (runID: string) => Promise<AgentReplayBundle>;
  delegations: () => Promise<AgentDelegation[]>;
  adminOperations: () => Promise<AgentAdminSnapshot>;
  adminCatalog: () => Promise<AgentAdminCatalog>;
  setRuntimeControl: (control: RuntimeControl, paused: boolean, reason: string) => Promise<RuntimeControl>;
  createAgent: (input: CreateAgentInput) => Promise<void>;
  publishSkill: (input: PublishSkillInput) => Promise<void>;
  publishTool: (input: Omit<AgentCatalogTool, "id" | "schema_digest" | "source_type">) => Promise<void>;
  publishCapabilitySnapshot: (tools: Array<{ operation_id: string; version: string }>) => Promise<void>;
  setMCPEnabled: (slug: string, enabled: boolean) => Promise<void>;
  setMemberRole: (memberID: string, role: AgentAdminRole, enabled: boolean) => Promise<void>;
  registerRemoteAgent: (input: RegisterRemoteA2AInput) => Promise<void>;
  verifyRemoteAgent: (slug: string, expectedRevision: number) => Promise<void>;
  setRemoteAgentEnabled: (slug: string, enabled: boolean, expectedRevision: number) => Promise<void>;
};

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "Agent control operation failed";
}

export class AgentControlController {
  private state = initialAgentControlState;
  private listeners = new Set<(state: AgentControlState) => void>();
  private generation = 0;
  private stopped = true;

  constructor(
    private readonly data: AgentControlDataPort,
    private readonly idempotencyKey: () => string = () => crypto.randomUUID()
  ) {}

  subscribe(listener: (state: AgentControlState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  getState(): AgentControlState { return this.state; }

  async start(): Promise<void> {
    this.stopped = false;
    this.update({ loading: true, error: null });
    try {
      await this.refresh();
      if (!this.stopped) this.update({ loading: false });
    } catch (error) {
      if (!this.stopped) this.fail(error, { loading: false });
      throw error;
    }
  }

  stop(): void {
    this.stopped = true;
    this.generation += 1;
  }

  async refresh(): Promise<void> {
    const generation = ++this.generation;
    const [memory, proactive, toolApprovals, delegations] = await Promise.all([
      this.data.memory(), this.data.proactive(), this.data.toolApprovals(), this.data.delegations()
    ]);
    if (this.stopped || generation !== this.generation) return;
    const visibleFacts = new Set(memory.facts.map((fact) => fact.fact_id));
    this.update({
      memory,
      proactive,
      toolApprovals,
      delegations,
      pendingDeletionIDs: this.state.pendingDeletionIDs.filter((factID) => visibleFacts.has(factID))
    });
  }

  async deleteMemory(factID: string): Promise<void> {
    await this.mutate(`memory-delete:${factID}`, async () => {
      await this.data.deleteMemory(factID, this.idempotencyKey());
      this.update({ pendingDeletionIDs: [...new Set([...this.state.pendingDeletionIDs, factID])] });
      await this.refresh();
    });
  }

  async loadGroupMemory(conversationID: string): Promise<void> {
    if (!conversationID) { this.update({ groupMemory: null }); return; }
    this.update({ groupMemoryLoading: true, error: null });
    try {
      const groupMemory = await this.data.groupMemory(conversationID);
      if (!this.stopped) this.update({ groupMemory, groupMemoryLoading: false });
    } catch (error) {
      if (!this.stopped) this.fail(error, { groupMemoryLoading: false });
      throw error;
    }
  }

  async reviewGroupMemory(conversationID: string, proposalID: string, decision: "approve" | "reject"): Promise<void> {
    await this.mutate(`group-memory:${proposalID}`, async () => {
      await this.data.reviewGroupMemory(conversationID, proposalID, decision);
      const groupMemory = await this.data.groupMemory(conversationID);
      if (!this.stopped) this.update({ groupMemory });
    });
  }

  async feedbackMemory(exposureID: string, signal: "helpful" | "not_helpful" | "incorrect"): Promise<void> {
    await this.mutate(`memory-feedback:${exposureID}`, async () => {
      await this.data.feedbackMemory(exposureID, signal);
      await this.refresh();
    });
  }

  async createSubscription(input: CreateProactiveSubscription): Promise<void> {
    await this.mutate("subscription-create", async () => {
      await this.data.createSubscription(input);
      await this.refresh();
    });
  }

  async setSubscriptionEnabled(subscriptionID: string, enabled: boolean): Promise<void> {
    await this.mutate(`subscription-state:${subscriptionID}`, async () => {
      await this.data.setSubscriptionEnabled(subscriptionID, enabled);
      await this.refresh();
    });
  }

  async updatePreference(preference: ProactivePreference): Promise<void> {
    await this.mutate("preference-update", async () => {
      await this.data.updatePreference(preference);
      await this.refresh();
    });
  }

  async acknowledge(eventID: string, signal: "interesting" | "not_interesting" | "dismissed"): Promise<void> {
    await this.mutate(`event-ack:${eventID}`, async () => {
      await this.data.acknowledge(eventID, signal);
      await this.refresh();
    });
  }

  async decideToolApproval(approvalID: string, argumentsDigest: string, decision: "approve" | "reject"): Promise<void> {
    await this.mutate(`tool-approval:${approvalID}`, async () => {
      await this.data.decideToolApproval(approvalID, argumentsDigest, decision);
      await this.refresh();
    });
  }

  async loadReplay(runID: string): Promise<void> {
    await this.mutate(`replay:${runID}`, async () => {
      const replay = await this.data.replay(runID);
      if (!this.stopped) this.update({ replay });
    });
  }

  closeReplay(): void { this.update({ replay: null }); }

  async loadAdmin(): Promise<void> {
    if (this.state.activeMutation !== null || this.state.adminLoading) {
      throw new Error("Another Agent control operation is in progress");
    }
    this.update({ adminLoading: true, adminForbidden: false, error: null });
    try {
      const [admin, adminCatalog] = await Promise.all([this.data.adminOperations(), this.data.adminCatalog()]);
      if (!this.stopped) this.update({ admin, adminCatalog, adminLoading: false });
    } catch (error) {
      if (!this.stopped && error instanceof PlatformAPIError && error.status === 403) {
        this.update({ admin: null, adminCatalog: null, adminLoading: false, adminForbidden: true });
      } else if (!this.stopped) {
        this.fail(error, { adminLoading: false });
      }
      throw error;
    }
  }

  async setRuntimeControl(control: RuntimeControl, paused: boolean, reason: string): Promise<void> {
    await this.mutate(`runtime-control:${control.component}`, async () => {
      await this.data.setRuntimeControl(control, paused, reason);
      const admin = await this.data.adminOperations();
      if (!this.stopped) this.update({ admin });
    });
  }

  async createAgent(input: CreateAgentInput): Promise<void> {
    await this.mutateCatalog("catalog-agent-create", () => this.data.createAgent(input));
  }

  async publishSkill(input: PublishSkillInput): Promise<void> {
    await this.mutateCatalog(`catalog-skill:${input.skill_id}@${input.version}`, () => this.data.publishSkill(input));
  }

  async publishTool(input: Omit<AgentCatalogTool, "id" | "schema_digest" | "source_type">): Promise<void> {
    await this.mutateCatalog(`catalog-tool:${input.operation_id}@${input.version}`, () => this.data.publishTool(input));
  }

  async publishCapabilitySnapshot(tools: Array<{ operation_id: string; version: string }>): Promise<void> {
    await this.mutateCatalog("catalog-capability-snapshot", () => this.data.publishCapabilitySnapshot(tools));
  }

  async setMCPEnabled(slug: string, enabled: boolean): Promise<void> {
    await this.mutateCatalog(`catalog-mcp:${slug}`, () => this.data.setMCPEnabled(slug, enabled));
  }

  async setMemberRole(memberID: string, role: AgentAdminRole, enabled: boolean): Promise<void> {
    await this.mutateCatalog(`catalog-role:${memberID}:${role}`, () => this.data.setMemberRole(memberID, role, enabled));
  }

  async registerRemoteAgent(input: RegisterRemoteA2AInput): Promise<void> {
    await this.mutateCatalog(`a2a-register:${input.slug}`, () => this.data.registerRemoteAgent(input));
  }

  async verifyRemoteAgent(slug: string, expectedRevision: number): Promise<void> {
    await this.mutateCatalog(`a2a-verify:${slug}`, () => this.data.verifyRemoteAgent(slug, expectedRevision));
  }

  async setRemoteAgentEnabled(slug: string, enabled: boolean, expectedRevision: number): Promise<void> {
    await this.mutateCatalog(`a2a-state:${slug}`, () => this.data.setRemoteAgentEnabled(slug, enabled, expectedRevision));
  }

  clearError(): void { this.update({ error: null }); }

  private async mutate(id: string, operation: () => Promise<void>): Promise<void> {
    if (this.state.activeMutation !== null) throw new Error("Another Agent control operation is in progress");
    this.update({ activeMutation: id, error: null });
    try {
      await operation();
      if (!this.stopped) this.update({ activeMutation: null });
    } catch (error) {
      if (!this.stopped) this.fail(error, { activeMutation: null });
      throw error;
    }
  }

  private async mutateCatalog(id: string, operation: () => Promise<void>): Promise<void> {
    await this.mutate(id, async () => {
      await operation();
      const adminCatalog = await this.data.adminCatalog();
      if (!this.stopped) this.update({ adminCatalog });
    });
  }

  private fail(error: unknown, patch: Partial<AgentControlState> = {}): void {
    this.update({ ...patch, error: messageOf(error) });
  }

  private update(patch: Partial<AgentControlState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}
