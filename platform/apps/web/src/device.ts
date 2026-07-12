import type { DeviceSnapshot, MemberDevice, PlatformLogoutResult } from "./device-api";

export type DeviceState = {
  devices: MemberDevice[];
  onlinePlatformIDs: number[];
  loading: boolean;
  loggingOutPlatformID: number | null;
  error: string | null;
  lastCorrelationID: string | null;
};

export const initialDeviceState: DeviceState = {
  devices: [],
  onlinePlatformIDs: [],
  loading: false,
  loggingOutPlatformID: null,
  error: null,
  lastCorrelationID: null
};

export type DeviceDataPort = {
  list: () => Promise<DeviceSnapshot>;
  logout: (platformID: number) => Promise<PlatformLogoutResult>;
};

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "设备操作失败";
}

export class DeviceController {
  private state = initialDeviceState;
  private listeners = new Set<(state: DeviceState) => void>();
  private requestVersion = 0;
  private closed = false;

  constructor(private readonly data: DeviceDataPort) {}

  subscribe(listener: (state: DeviceState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  getState(): DeviceState { return this.state; }

  async start(): Promise<void> {
    this.closed = false;
    await this.refresh();
  }

  close(): void {
    this.closed = true;
    this.requestVersion += 1;
    this.listeners.clear();
  }

  async refresh(): Promise<void> {
    const version = ++this.requestVersion;
    this.update({ loading: true, error: null });
    try {
      const snapshot = await this.data.list();
      if (this.closed || version !== this.requestVersion) return;
      this.update({ devices: snapshot.devices, onlinePlatformIDs: snapshot.online_platform_ids, loading: false });
    } catch (error) {
      if (this.closed || version !== this.requestVersion) return;
      this.update({ loading: false, error: messageOf(error) });
      throw error;
    }
  }

  async logout(platformID: number): Promise<void> {
    if (this.state.loggingOutPlatformID !== null) throw new Error("另一个设备注销操作正在进行");
    const target = this.state.devices.find((device) => device.platform_id === platformID && device.status === "active");
    if (!target) throw new Error("目标平台没有有效的设备登记");
    if (target.current) throw new Error("不能从设备管理中注销当前平台");
    this.update({ loggingOutPlatformID: platformID, error: null });
    try {
      const result = await this.data.logout(platformID);
      if (this.closed) return;
      this.update({ lastCorrelationID: result.correlation_id });
      await this.refresh();
      if (!this.closed) this.update({ loggingOutPlatformID: null });
    } catch (error) {
      if (!this.closed) this.update({ loggingOutPlatformID: null, error: messageOf(error) });
      throw error;
    }
  }

  clearError(): void { this.update({ error: null }); }

  private update(patch: Partial<DeviceState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

export function platformLabel(platformID: number): string {
  return ({ 1: "iOS", 2: "Android", 3: "Windows", 4: "macOS", 5: "Web", 7: "Linux", 8: "Android Tablet", 9: "iPad", 11: "HarmonyOS" } as Record<number, string>)[platformID] ?? `平台 ${platformID}`;
}
