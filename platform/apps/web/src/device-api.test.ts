import { describe, expect, it, vi } from "vitest";

import { getDevices, logoutPlatform } from "./device-api";
import { PlatformAPIError } from "./platform-api";

describe("device API", () => {
  it("reads only the safe projection with current device context", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      devices: [{ device_id: "browser-1", platform_id: 5, status: "active", created_at: "2030-01-01T00:00:00Z", updated_at: "2030-01-01T00:00:00Z", current: true, online: true }],
      online_platform_ids: [5]
    }), { status: 200 }));
    const snapshot = await getDevices("/platform-api", "id-token", "browser-1", 5, request);
    expect(snapshot.devices[0].current).toBe(true);
    expect(request).toHaveBeenCalledWith("/platform-api/v1/im/devices?platform_id=5&device_id=browser-1", {
      headers: { Authorization: "Bearer id-token" }
    });
  });

  it("preserves typed logout errors and correlation IDs", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      code: "CURRENT_PLATFORM_CONFLICT", message: "current platform", correlation_id: "corr-1"
    }), { status: 409 }));
    await expect(logoutPlatform("/platform-api", "token", "browser", 5, 5, request)).rejects.toEqual(
      new PlatformAPIError("current platform", "CURRENT_PLATFORM_CONFLICT", "corr-1", 409)
    );
  });

  it("requires the exact acknowledged target and correlation ID", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ platform_id: 4, state: "logged_out", correlation_id: "corr" }), { status: 200 }));
    await expect(logoutPlatform("/platform-api", "token", "browser", 3, 5, request)).rejects.toThrow("platform logout response is malformed");
  });
});
