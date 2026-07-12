import { describe, expect, it, vi } from "vitest";

import { DeviceController, type DeviceDataPort } from "./device";
import type { DeviceSnapshot } from "./device-api";

function snapshot(deviceID: string, online = true): DeviceSnapshot {
  return { devices: [{ device_id: deviceID, platform_id: deviceID === "web" ? 5 : 3, status: "active", created_at: "2030-01-01T00:00:00Z", updated_at: "2030-01-01T00:00:00Z", current: deviceID === "web", online }], online_platform_ids: online ? [deviceID === "web" ? 5 : 3] : [] };
}

describe("DeviceController", () => {
  it("prevents an older refresh from overwriting the latest projection", async () => {
    let resolveOld!: (value: DeviceSnapshot) => void;
    const old = new Promise<DeviceSnapshot>((resolve) => { resolveOld = resolve; });
    const data: DeviceDataPort = { list: vi.fn().mockReturnValueOnce(old).mockResolvedValueOnce(snapshot("web")), logout: vi.fn() };
    const controller = new DeviceController(data);
    const first = controller.refresh();
    await controller.refresh();
    resolveOld(snapshot("windows"));
    await first;
    expect(controller.getState().devices[0].device_id).toBe("web");
  });

  it("blocks duplicate logout and refreshes only after acknowledgement", async () => {
    let acknowledge!: () => void;
    const pending = new Promise<void>((resolve) => { acknowledge = resolve; });
    const list = vi.fn().mockResolvedValueOnce(snapshot("windows")).mockResolvedValueOnce(snapshot("windows", false));
    const logout = vi.fn(async () => { await pending; return { platform_id: 3, state: "logged_out" as const, correlation_id: "corr-1" }; });
    const controller = new DeviceController({ list, logout });
    await controller.start();
    const action = controller.logout(3);
    await expect(controller.logout(3)).rejects.toThrow("正在进行");
    expect(list).toHaveBeenCalledTimes(1);
    acknowledge();
    await action;
    expect(list).toHaveBeenCalledTimes(2);
    expect(controller.getState().lastCorrelationID).toBe("corr-1");
  });

  it("keeps logout failures explicit and leaves the projection unchanged", async () => {
    const data: DeviceDataPort = { list: vi.fn().mockResolvedValue(snapshot("windows")), logout: vi.fn().mockRejectedValue(new Error("OpenIM unavailable")) };
    const controller = new DeviceController(data);
    await controller.start();
    await expect(controller.logout(3)).rejects.toThrow("OpenIM unavailable");
    expect(controller.getState().devices[0].online).toBe(true);
    expect(controller.getState().error).toBe("OpenIM unavailable");
  });

  it("suppresses completion updates after close", async () => {
    let resolve!: (value: DeviceSnapshot) => void;
    const controller = new DeviceController({ list: () => new Promise((done) => { resolve = done; }), logout: vi.fn() });
    const request = controller.refresh();
    controller.close();
    resolve(snapshot("web"));
    await request;
    expect(controller.getState().devices).toEqual([]);
  });
});
