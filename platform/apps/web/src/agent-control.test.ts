import { describe, expect, it, vi } from "vitest";

import { AgentControlController, type AgentControlDataPort } from "./agent-control";
import type { AgentAdminCatalog, AgentAdminSnapshot, GroupMemorySnapshot, MemorySnapshot, ProactiveSnapshot } from "./agent-control-api";
import { PlatformAPIError } from "./platform-api";

const emptyMemory: MemorySnapshot = { facts: [], exposures: [] };
const emptyGroupMemory: GroupMemorySnapshot = { source_channel: "openim", conversation_id: "sg_group", facts: [], proposals: [] };
const proactive: ProactiveSnapshot = {
  preference: { enabled: true, timezone: "Asia/Shanghai", quiet_start: "22:00", quiet_end: "08:00", daily_budget: 5, minimum_score: 0.55 },
  subscriptions: [], events: []
};
const queue = { states: {}, oldest_ready_seconds: 0 };
const admin: AgentAdminSnapshot = {
  roles: ["platform_admin"],
  controls: [
    { component: "agent_execution", paused: false, reason: "not configured", revision: 0 },
    { component: "agent_delivery", paused: false, reason: "not configured", revision: 0 },
    { component: "proactive_dispatch", paused: false, reason: "not configured", revision: 0 }
  ],
  operations: {
    generated_at: "2030-01-01T00:00:00Z", agent_runs: queue, deliveries: queue,
    proactive_events: queue, memory_extractions: queue, delegations: queue,
    tool_approvals: queue, mcp_health: queue
  }
};
const adminCatalog: AgentAdminCatalog = {
  roles: ["platform_admin"], agents: [], skills: [], tools: [], mcp_servers: [], members: [], capability_snapshots: [], remote_a2a_enabled: false, remote_agents: []
};

function port(overrides: Partial<AgentControlDataPort> = {}): AgentControlDataPort {
  return {
    memory: vi.fn(async () => emptyMemory),
    groupMemory: vi.fn(async () => emptyGroupMemory),
    reviewGroupMemory: vi.fn(async () => undefined),
    deleteMemory: vi.fn(async () => undefined),
    feedbackMemory: vi.fn(async () => undefined),
    proactive: vi.fn(async () => proactive),
    createSubscription: vi.fn(async () => undefined),
    setSubscriptionEnabled: vi.fn(async () => undefined),
    updatePreference: vi.fn(async () => undefined),
    acknowledge: vi.fn(async () => undefined),
    toolApprovals: vi.fn(async () => []),
    delegations: vi.fn(async () => []),
    decideToolApproval: vi.fn(async () => undefined),
    replay: vi.fn(async () => ({ schema_version: 1, checksum: `sha256:${"b".repeat(64)}` })),
    adminOperations: vi.fn(async () => admin),
    adminCatalog: vi.fn(async () => adminCatalog),
    setRuntimeControl: vi.fn(async (control, paused, reason) => ({ ...control, paused, reason, revision: control.revision + 1 })),
    createAgent: vi.fn(async () => undefined),
    publishSkill: vi.fn(async () => undefined),
    publishTool: vi.fn(async () => undefined),
    publishCapabilitySnapshot: vi.fn(async () => undefined),
    setMCPEnabled: vi.fn(async () => undefined),
    setMemberRole: vi.fn(async () => undefined),
    registerRemoteAgent: vi.fn(async () => undefined),
    verifyRemoteAgent: vi.fn(async () => undefined),
    setRemoteAgentEnabled: vi.fn(async () => undefined),
    ...overrides
  };
}

describe("AgentControlController", () => {
  it("loads memory and proactive state together", async () => {
    const data = port({
      memory: vi.fn(async () => ({ facts: [{ fact_id: "fact-1", category: "preference" as const, content: "concise", checksum: "sha256:x", updated_at: "2030-01-01T00:00:00Z" }], exposures: [] }))
    });
    const controller = new AgentControlController(data);
    await controller.start();
    expect(controller.getState().memory.facts[0].fact_id).toBe("fact-1");
    expect(controller.getState().proactive?.preference.timezone).toBe("Asia/Shanghai");
    controller.stop();
  });

  it("does not report a failed deletion as success", async () => {
    const data = port({ deleteMemory: vi.fn(async () => { throw new Error("database unavailable"); }) });
    const controller = new AgentControlController(data, () => "request-1");
    await controller.start();
    await expect(controller.deleteMemory("fact-1")).rejects.toThrow("database unavailable");
    expect(controller.getState().pendingDeletionIDs).toEqual([]);
    expect(controller.getState().error).toBe("database unavailable");
    controller.stop();
  });

  it("uses an idempotency key and records projection-pending deletion", async () => {
    const facts: MemorySnapshot = { facts: [{ fact_id: "fact-1", category: "context", content: "project", checksum: "sha256:x", updated_at: "2030-01-01T00:00:00Z" }], exposures: [] };
    const data = port({ memory: vi.fn(async () => facts) });
    const controller = new AgentControlController(data, () => "request-1");
    await controller.start();
    await controller.deleteMemory("fact-1");
    expect(data.deleteMemory).toHaveBeenCalledWith("fact-1", "request-1");
    expect(controller.getState().pendingDeletionIDs).toEqual(["fact-1"]);
    controller.stop();
  });

  it("prevents overlapping mutations", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const data = port({ deleteMemory: vi.fn(async () => pending) });
    const controller = new AgentControlController(data);
    await controller.start();
    const first = controller.deleteMemory("fact-1");
    await expect(controller.feedbackMemory("exposure-1", "helpful")).rejects.toThrow("operation is in progress");
    release();
    await first;
    controller.stop();
  });

  it("binds a tool decision to the server-provided arguments digest", async () => {
    const approval = {
      approval_id: "approval-1", run_id: "run-1", operation_id: "calendar.event.create",
      arguments_digest: `sha256:${"a".repeat(64)}`, risk: "external_side_effect" as const,
      policy_reason: "side_effect_requires_approval", expires_at: "2030-01-01T00:10:00Z"
    };
    const data = port({ toolApprovals: vi.fn(async () => [approval]) });
    const controller = new AgentControlController(data);
    await controller.start();
    await controller.decideToolApproval(approval.approval_id, approval.arguments_digest, "approve");
    expect(data.decideToolApproval).toHaveBeenCalledWith("approval-1", approval.arguments_digest, "approve");
    controller.stop();
  });

  it("loads administrator operations only when requested", async () => {
    const data = port();
    const controller = new AgentControlController(data);
    await controller.start();
    expect(data.adminOperations).not.toHaveBeenCalled();
    await controller.loadAdmin();
    expect(controller.getState().admin?.roles).toEqual(["platform_admin"]);
    expect(controller.getState().adminCatalog?.roles).toEqual(["platform_admin"]);
    controller.stop();
  });

  it("represents administrator authorization failure without fake data", async () => {
    const data = port({ adminOperations: vi.fn(async () => { throw new PlatformAPIError("forbidden", "ADMINISTRATION_FORBIDDEN", "corr", 403); }) });
    const controller = new AgentControlController(data);
    await controller.start();
    await expect(controller.loadAdmin()).rejects.toThrow("forbidden");
    expect(controller.getState().admin).toBeNull();
    expect(controller.getState().adminCatalog).toBeNull();
    expect(controller.getState().adminForbidden).toBe(true);
    controller.stop();
  });

  it("refreshes the catalog only after a successful mutation", async () => {
    const data = port();
    const controller = new AgentControlController(data);
    await controller.start();
    await controller.loadAdmin();
    await controller.setMCPEnabled("knowledge", false);
    expect(data.setMCPEnabled).toHaveBeenCalledWith("knowledge", false);
    expect(data.adminCatalog).toHaveBeenCalledTimes(2);
    controller.stop();
  });

  it("does not refresh or report success after a rejected catalog mutation", async () => {
    const data = port({ publishCapabilitySnapshot: vi.fn(async () => { throw new Error("conflict"); }) });
    const controller = new AgentControlController(data);
    await controller.start();
    await controller.loadAdmin();
    await expect(controller.publishCapabilitySnapshot([{ operation_id: "knowledge.search", version: "1" }])).rejects.toThrow("conflict");
    expect(data.adminCatalog).toHaveBeenCalledTimes(1);
    expect(controller.getState().error).toBe("conflict");
    controller.stop();
  });
});
