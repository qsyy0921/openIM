import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TelegramLinkController, type TelegramLinkDataPort } from "./telegram-link";
import type { TelegramLinkChallenge, TelegramLinkStatus } from "./telegram-link-api";

const challenge: TelegramLinkChallenge = {
  state: "pending",
  code: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
  command: "/link ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
  expires_at: "2030-01-01T00:05:00Z"
};

describe("TelegramLinkController", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("prevents an older status request from overwriting the latest result", async () => {
    let resolveOld!: (value: TelegramLinkStatus) => void;
    const old = new Promise<TelegramLinkStatus>((resolve) => { resolveOld = resolve; });
    const data: TelegramLinkDataPort = {
      status: vi.fn().mockReturnValueOnce(old).mockResolvedValueOnce({ state: "bound" }),
      issue: vi.fn()
    };
    const controller = new TelegramLinkController(data);
    const first = controller.refresh();
    await controller.refresh();
    resolveOld({ state: "unbound" });
    await first;
    expect(controller.getState().status).toBe("bound");
  });

  it("blocks duplicate challenge issuance and exposes failure without fake success", async () => {
    let reject!: (error: Error) => void;
    const pending = new Promise<TelegramLinkChallenge>((_, fail) => { reject = fail; });
    const data: TelegramLinkDataPort = { status: vi.fn(), issue: vi.fn().mockReturnValue(pending) };
    const controller = new TelegramLinkController(data);
    const first = controller.issue();
    await expect(controller.issue()).rejects.toThrow("正在生成");
    reject(new Error("database unavailable"));
    await expect(first).rejects.toThrow("database unavailable");
    expect(controller.getState()).toMatchObject({ status: "idle", issuing: false, error: "database unavailable", challenge: null });
  });

  it("polls a pending challenge until the server confirms the binding", async () => {
    const status = vi.fn().mockResolvedValueOnce({ state: "pending", expires_at: challenge.expires_at }).mockResolvedValueOnce({ state: "bound" });
    const controller = new TelegramLinkController({ status, issue: vi.fn().mockResolvedValue(challenge) }, undefined, () => Date.parse("2030-01-01T00:00:00Z"));
    await controller.start();
    expect(controller.getState().status).toBe("pending");
    await vi.advanceTimersByTimeAsync(2_000);
    expect(status).toHaveBeenCalledTimes(2);
    expect(controller.getState()).toMatchObject({ status: "bound", expiresAt: null, challenge: null });
  });

  it("keeps a freshly issued command only in memory while the same challenge is pending", async () => {
    const status = vi.fn().mockResolvedValue({ state: "pending", expires_at: challenge.expires_at });
    const controller = new TelegramLinkController({ status, issue: vi.fn().mockResolvedValue(challenge) }, undefined, () => Date.parse("2030-01-01T00:00:00Z"));
    await controller.issue();
    expect(controller.getState().challenge?.command).toBe(challenge.command);
    await controller.refresh();
    expect(controller.getState().challenge?.command).toBe(challenge.command);
  });

  it("cancels pending polling and suppresses completion updates after close", async () => {
    let resolve!: (value: TelegramLinkStatus) => void;
    const status = vi.fn()
      .mockResolvedValueOnce({ state: "pending", expires_at: challenge.expires_at })
      .mockReturnValueOnce(new Promise<TelegramLinkStatus>((done) => { resolve = done; }));
    const controller = new TelegramLinkController({ status, issue: vi.fn() }, undefined, () => Date.parse("2030-01-01T00:00:00Z"));
    await controller.start();
    expect(vi.getTimerCount()).toBe(1);
    await vi.advanceTimersByTimeAsync(2_000);
    controller.close();
    resolve({ state: "bound" });
    await Promise.resolve();
    expect(vi.getTimerCount()).toBe(0);
    expect(controller.getState().status).toBe("pending");
  });
});
