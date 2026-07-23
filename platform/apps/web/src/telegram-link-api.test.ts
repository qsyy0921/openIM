import { describe, expect, it, vi } from "vitest";

import { PlatformAPIError } from "./platform-api";
import { getTelegramLinkStatus, issueTelegramLinkChallenge } from "./telegram-link-api";

describe("Telegram link API", () => {
  it("reads member status with the current device context", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ state: "bound" }), { status: 200 }));
    await expect(getTelegramLinkStatus("/platform-api", "id-token", "browser", 5, request)).resolves.toEqual({ state: "bound" });
    expect(request).toHaveBeenCalledWith("/platform-api/v1/agent/channels/telegram/link?platform_id=5&device_id=browser", {
      headers: { Authorization: "Bearer id-token" }
    });
  });

  it("issues an empty-body challenge request and validates the exact command", async () => {
    const payload = {
      state: "pending",
      code: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
      command: "/link ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
      expires_at: "2030-01-01T00:05:00Z"
    };
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload), { status: 201 }));
    await expect(issueTelegramLinkChallenge("/platform-api", "id-token", "browser", 5, request)).resolves.toEqual(payload);
    expect(request).toHaveBeenCalledWith("/platform-api/v1/agent/channels/telegram/link-challenges?platform_id=5&device_id=browser", {
      method: "POST",
      headers: { Authorization: "Bearer id-token" }
    });
  });

  it("rejects malformed status and challenge payloads", async () => {
    const malformedStatus = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ state: "pending" }), { status: 200 }));
    await expect(getTelegramLinkStatus("/platform-api", "token", "browser", 5, malformedStatus)).rejects.toThrow("malformed");

    const malformedChallenge = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      state: "pending", code: "short", command: "/link short", expires_at: "2030-01-01T00:05:00Z"
    }), { status: 201 }));
    await expect(issueTelegramLinkChallenge("/platform-api", "token", "browser", 5, malformedChallenge)).rejects.toThrow("malformed");
  });

  it("preserves typed server errors and correlation IDs", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      code: "TELEGRAM_LINK_RATE_LIMITED", message: "wait", correlation_id: "corr-1"
    }), { status: 429 }));
    await expect(issueTelegramLinkChallenge("/platform-api", "token", "browser", 5, request)).rejects.toEqual(
      new PlatformAPIError("wait", "TELEGRAM_LINK_RATE_LIMITED", "corr-1", 429)
    );
  });
});
