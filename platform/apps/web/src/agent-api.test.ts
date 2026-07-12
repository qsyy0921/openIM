import { describe, expect, it, vi } from "vitest";

import { approveAgentIntent, getAgentWorkspace } from "./agent-api";

describe("Agent workspace API", () => {
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
