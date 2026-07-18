import { describe, expect, it, vi } from "vitest";
import type { MessageItem } from "@openim/wasm-client-sdk";

import { AgentController, type AgentDataPort, type AgentTransportPort } from "./agent";
import type { AgentRun, AgentSummary, AgentWorkspaceSnapshot } from "./agent-api";

const checksum = `sha256:${"a".repeat(64)}`;
const catalog: AgentSummary[] = [{
  agent_id: "agent-1",
  slug: "knowledge-agent",
  display_name: "Enterprise Agent",
  description: "Enterprise knowledge and governed ticket actions",
  trigger_alias: "@agent",
  production_version_number: 1,
  production_version_id: "version-1",
  spec_checksum: checksum,
  bot_user_id: "agent-1"
}];

function run(overrides: Partial<AgentRun> = {}): AgentRun {
  return {
    run_id: "run-1",
    agent_id: "agent-1",
    agent_display_name: "Enterprise Agent",
    agent_version_number: 1,
    agent_spec_checksum: checksum,
    conversation_id: "si",
    prompt: "question",
    state: "succeeded",
    created_at: "2030-01-01T00:00:00Z",
    updated_at: "2030-01-01T00:00:01Z",
    citations: [],
    ...overrides
  };
}

function workspace(runs: AgentWorkspaceSnapshot["runs"] = []): AgentWorkspaceSnapshot {
  return { agent_user_id: "agent-1", runs };
}

function ports(snapshots: AgentWorkspaceSnapshot[]) {
  const data: AgentDataPort = {
    catalog: vi.fn(async () => catalog),
    workspace: vi.fn(async () => snapshots.shift() ?? workspace()),
    approve: vi.fn(async (intentID: string) => ({ intent_id: intentID, execution_id: "execution-1", state: "queued" as const }))
  };
  const transport: AgentTransportPort = {
    ensureConversation: vi.fn(async () => undefined),
    createText: vi.fn(async (content) => ({ clientMsgID: "message-1", textElem: { content } }) as MessageItem),
    send: vi.fn(async (_userID, message) => message)
  };
  return { data, transport };
}

describe("AgentController", () => {
  it("restores the authoritative workspace and bot conversation", async () => {
    const { data, transport } = ports([workspace([run({ answer: "answer" })])]);
    const controller = new AgentController(data, transport);
    await controller.start();
    expect(controller.getState().runs).toHaveLength(1);
    expect(transport.ensureConversation).toHaveBeenCalledWith("agent-1");
    controller.stop();
  });

  it("submits through OpenIM with the trigger after the user protocol", async () => {
    const { data, transport } = ports([workspace(), workspace()]);
    const controller = new AgentController(data, transport);
    await controller.start();
    await controller.sendPrompt("创建工单：检查发布");
    expect(transport.createText).toHaveBeenCalledWith("创建工单：检查发布 @agent");
    expect(transport.send).toHaveBeenCalledWith("agent-1", expect.objectContaining({ clientMsgID: "message-1" }));
    controller.stop();
  });

  it("approves only the exact pending intent digest", async () => {
    const digest = `sha256:${"a".repeat(64)}`;
    const pendingRun = run({ prompt: "创建工单：检查", state: "waiting_approval", intent: { intent_id: "intent-1", action_type: "create_ticket", title: "检查", payload_digest: digest, state: "pending_approval", expires_at: "2030-01-01T00:15:00Z" } });
    const { data, transport } = ports([workspace([pendingRun]), workspace([pendingRun])]);
    const controller = new AgentController(data, transport);
    await controller.start();
    await controller.approve("intent-1");
    expect(data.approve).toHaveBeenCalledWith("intent-1", digest);
    controller.stop();
  });

  it("fails closed when the Catalog does not resolve one active Agent", async () => {
    const { data, transport } = ports([workspace()]);
    vi.mocked(data.catalog).mockResolvedValue([]);
    const controller = new AgentController(data, transport);
    await expect(controller.start()).rejects.toThrow("exactly one active Agent");
    expect(transport.ensureConversation).not.toHaveBeenCalled();
    controller.stop();
  });

  it("fails closed when Catalog and workspace bot identities differ", async () => {
    const { data, transport } = ports([workspace()]);
    vi.mocked(data.catalog).mockResolvedValue([{ ...catalog[0], bot_user_id: "agent-2" }]);
    const controller = new AgentController(data, transport);
    await expect(controller.start()).rejects.toThrow("bot identities do not match");
    expect(transport.ensureConversation).not.toHaveBeenCalled();
    controller.stop();
  });
});
