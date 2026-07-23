import { describe, expect, it, vi } from "vitest";

import { createAgentSubscription, deleteAgentMemoryFact, getAgentAdminCatalog, getAgentAdminSnapshot, getAgentDelegations, getAgentMemory, getAgentToolApprovals, serializeAgentReplay, setAgentRuntimeControl, setCatalogMemberRole } from "./agent-control-api";

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("Agent control API", () => {
  it("serializes a replay bundle as a stable newline-terminated artifact", () => {
    expect(serializeAgentReplay({ schema_version: 1, checksum: "sha256:abc", run: { run_id: "run-1" } })).toBe(
      '{\n  "schema_version": 1,\n  "checksum": "sha256:abc",\n  "run": {\n    "run_id": "run-1"\n  }\n}\n'
    );
  });

  it("loads memory with bearer and device context", async () => {
    const request = vi.fn(async () => response({ facts: [], exposures: [] }));
    await getAgentMemory("https://platform.example", "token", "browser", request as typeof fetch);
    expect(request).toHaveBeenCalledWith(
      "https://platform.example/v1/agent/memory?platform_id=5&device_id=browser",
      { headers: { Authorization: "Bearer token" } }
    );
  });

  it("sends deletion with the caller idempotency key", async () => {
    let captured: RequestInit | undefined;
    const request = vi.fn(async (_url: string, init?: RequestInit) => {
      captured = init;
      return response({ event_id: "event-1", state: "projection_pending" }, 202);
    });
    await deleteAgentMemoryFact("https://platform.example", "token", "browser", "fact-1", "request-1", request as typeof fetch);
    expect(captured?.method).toBe("DELETE");
    expect(captured?.headers).toMatchObject({ "Idempotency-Key": "request-1" });
  });

  it("does not allow a proactive target ID in the client contract", async () => {
    const request = vi.fn(async (_url: string, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      expect(body.target_id).toBeUndefined();
      expect(body.source_channel).toBe("telegram");
      return response({ subscription_id: "subscription-1" }, 201);
    });
    await createAgentSubscription("https://platform.example", "token", "browser", {
      agent_id: "agent-1", query: "agents", categories: ["cs.AI"],
      source_channel: "telegram", poll_interval_seconds: 1800
    }, request as typeof fetch);
  });

  it("rejects malformed tool approval entries", async () => {
    const request = vi.fn(async () => response({ approvals: [{ approval_id: "approval-1" }] }));
    await expect(getAgentToolApprovals("https://platform.example", "token", "browser", request as typeof fetch))
      .rejects.toThrow("Agent tool approval response is malformed");
  });

  it("rejects malformed delegation entries", async () => {
    const request = vi.fn(async () => response({ delegations: [{ delegation_id: "delegation-1", state: "completed" }] }));
    await expect(getAgentDelegations("https://platform.example", "token", "browser", request as typeof fetch))
      .rejects.toThrow("Agent delegation response is malformed");
  });

  it("loads a strictly typed administrator snapshot", async () => {
    const queue = { states: { queued: 2 }, oldest_ready_seconds: 4.5 };
    const request = vi.fn(async () => response({
      roles: ["platform_admin"],
      controls: [
        { component: "agent_execution", paused: false, reason: "not configured", revision: 0 },
        { component: "agent_delivery", paused: true, reason: "incident", revision: 2 },
        { component: "proactive_dispatch", paused: false, reason: "not configured", revision: 0 }
      ],
      operations: {
        generated_at: "2030-01-01T00:00:00Z", agent_runs: queue, deliveries: queue,
        proactive_events: queue, memory_extractions: queue, delegations: queue,
        tool_approvals: queue, mcp_health: queue
      }
    }));
    const snapshot = await getAgentAdminSnapshot("https://platform.example", "token", "browser", request as typeof fetch);
    expect(snapshot.operations.agent_runs.states.queued).toBe(2);
  });

  it("binds a runtime mutation to the current revision", async () => {
    let body: Record<string, unknown> = {};
    const request = vi.fn(async (_url: string, init?: RequestInit) => {
      body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      return response({ component: "agent_execution", paused: true, reason: "incident", revision: 4 });
    });
    await setAgentRuntimeControl(
      "https://platform.example", "token", "browser",
      { component: "agent_execution", paused: false, reason: "healthy", revision: 3 },
      true, "incident", request as typeof fetch
    );
    expect(body.expected_revision).toBe(3);
    expect(body).not.toHaveProperty("tenant_id");
  });

  it("loads the administrator catalog with device context", async () => {
    let requestedURL = "";
    const request = vi.fn(async (url: string) => {
      requestedURL = url;
      return response({ roles: ["platform_admin"], agents: [], skills: [], tools: [], mcp_servers: [], members: [], capability_snapshots: [], remote_a2a_enabled: false, remote_agents: [] });
    });
    const catalog = await getAgentAdminCatalog("https://platform.example", "token", "browser", request as typeof fetch);
    expect(catalog.roles).toEqual(["platform_admin"]);
    expect(requestedURL).toBe("https://platform.example/v1/admin/agent/catalog?platform_id=5&device_id=browser");
  });

  it("never accepts tenant identity from a role mutation caller", async () => {
    let body: Record<string, unknown> = {};
    const request = vi.fn(async (_url: string, init?: RequestInit) => {
      body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      return response({ member_id: "member-1", role: "agent_admin", enabled: true });
    });
    await setCatalogMemberRole("https://platform.example", "token", "browser", "member-1", "agent_admin", true, request as typeof fetch);
    expect(body).toEqual({ platform_id: 5, device_id: "browser", enabled: true });
    expect(body).not.toHaveProperty("tenant_id");
  });
});
