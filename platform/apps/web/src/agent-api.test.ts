import { describe, expect, it, vi } from "vitest";

import { approveAgentIntent, getAgentCatalog, getAgentWorkspace } from "./agent-api";

const checksum = `sha256:${"a".repeat(64)}`;
const agent = {
  agent_id: "agent-1",
  slug: "knowledge-agent",
  display_name: "Enterprise Agent",
  description: "Enterprise knowledge and governed ticket actions",
  trigger_alias: "@agent",
  production_version_number: 1,
  production_version_id: "version-1",
  spec_checksum: checksum,
  bot_user_id: "agent-1"
};

describe("Agent workspace API", () => {
  it("loads the authenticated tenant Agent Catalog", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify([agent]), { status: 200 }));
    await expect(getAgentCatalog("/platform-api", "id-token", "browser-1", request)).resolves.toEqual([agent]);
    expect(request).toHaveBeenCalledWith(
      "/platform-api/v1/agents?platform_id=5&device_id=browser-1",
      { headers: { Authorization: "Bearer id-token" } }
    );
  });

  it("rejects malformed Agent Catalog data", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify([{ ...agent, spec_checksum: "invalid" }]), { status: 200 }));
    await expect(getAgentCatalog("/platform-api", "id-token", "browser-1", request)).rejects.toThrow("malformed");
  });

  it("loads the authenticated device-scoped projection", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ agent_user_id: "agent-1", runs: [] }), { status: 200 }));
    await expect(getAgentWorkspace("/platform-api", "id-token", "browser-1", request)).resolves.toEqual({ agent_user_id: "agent-1", runs: [] });
    expect(request).toHaveBeenCalledWith(
      "/platform-api/v1/agent/workspace?platform_id=5&device_id=browser-1",
      { headers: { Authorization: "Bearer id-token" } }
    );
  });

  it("approves the exact immutable digest", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ intent_id: "intent-1", execution_id: "execution-1", state: "queued" }), { status: 202 }));
    const digest = `sha256:${"a".repeat(64)}`;
    await approveAgentIntent("/platform-api", "id-token", "browser-1", "intent-1", digest, request);
    expect(request).toHaveBeenCalledWith(
      "/platform-api/v1/agent/intents/intent-1/approve",
      expect.objectContaining({ body: JSON.stringify({ platform_id: 5, device_id: "browser-1", payload_digest: digest }) })
    );
  });

  it("preserves typed platform errors", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ code: "APPROVAL_CONFLICT", message: "conflict", correlation_id: "corr-1" }), { status: 409 }));
    await expect(approveAgentIntent("/platform-api", "token", "browser", "intent", `sha256:${"b".repeat(64)}`, request)).rejects.toMatchObject({
      code: "APPROVAL_CONFLICT", correlationID: "corr-1", status: 409
    });
  });
});
