import { describe, expect, it, vi } from "vitest";
import type { MessageItem } from "@openim/wasm-client-sdk";

import { AgentController, type AgentDataPort, type AgentTransportPort } from "./agent";
import type { AgentWorkspaceSnapshot } from "./agent-api";

function workspace(runs: AgentWorkspaceSnapshot["runs"] = []): AgentWorkspaceSnapshot {
  return { agent_user_id: "agent-1", runs };
}

function ports(snapshots: AgentWorkspaceSnapshot[]) {
  const data: AgentDataPort = {
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
    const { data, transport } = ports([workspace([{ run_id: "run-1", conversation_id: "si", prompt: "question", state: "succeeded", answer: "answer", created_at: "2030-01-01T00:00:00Z", updated_at: "2030-01-01T00:00:01Z", citations: [] }])]);
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
    expect(transport.createText).toHaveBeenCalledWith("创建工单：检查发布 @Agent");
    expect(transport.send).toHaveBeenCalledWith("agent-1", expect.objectContaining({ clientMsgID: "message-1" }));
    controller.stop();
  });

  it("approves only the exact pending intent digest", async () => {
    const digest = `sha256:${"a".repeat(64)}`;
    const pendingRun = { run_id: "run-1", conversation_id: "si", prompt: "创建工单：检查", state: "waiting_approval" as const, created_at: "2030-01-01T00:00:00Z", updated_at: "2030-01-01T00:00:01Z", citations: [], intent: { intent_id: "intent-1", action_type: "create_ticket" as const, title: "检查", payload_digest: digest, state: "pending_approval" as const, expires_at: "2030-01-01T00:15:00Z" } };
    const { data, transport } = ports([workspace([pendingRun]), workspace([pendingRun])]);
    const controller = new AgentController(data, transport);
    await controller.start();
    await controller.approve("intent-1");
    expect(data.approve).toHaveBeenCalledWith("intent-1", digest);
    controller.stop();
  });
});
