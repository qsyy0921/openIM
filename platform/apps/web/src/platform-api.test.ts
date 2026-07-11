import { describe, expect, it, vi } from "vitest";

import { createIMSession, PlatformAPIError } from "./platform-api";

describe("createIMSession", () => {
  it("exchanges an ID token without exposing it in the response", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          user_id: "ent_user",
          ws_url: "ws://openim.test:10001",
          user_token: "openim-user-token",
          expires_at: "2030-01-02T03:04:05Z"
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );
    const session = await createIMSession("/platform-api", "enterprise-id-token", "web-device", request);

    expect(session.userID).toBe("ent_user");
    expect(request).toHaveBeenCalledWith(
      "/platform-api/v1/im/session",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ Authorization: "Bearer enterprise-id-token" })
      })
    );
  });

  it("preserves typed platform errors", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ code: "MEMBER_OR_DEVICE_FORBIDDEN", message: "forbidden", correlation_id: "corr-1" }),
        { status: 403, headers: { "Content-Type": "application/json" } }
      )
    );
    await expect(createIMSession("/platform-api", "token", "device", request)).rejects.toEqual(
      new PlatformAPIError("forbidden", "MEMBER_OR_DEVICE_FORBIDDEN", "corr-1", 403)
    );
  });

  it("rejects success-shaped malformed responses", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ user_id: "ent_user" }), { status: 200, headers: { "Content-Type": "application/json" } })
    );
    await expect(createIMSession("/platform-api", "token", "device", request)).rejects.toThrow(
      "platform session response is malformed"
    );
  });
});
