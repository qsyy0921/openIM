import { PlatformAPIError } from "./platform-api";

export type MemberDevice = {
  device_id: string;
  platform_id: number;
  status: "active" | "disabled";
  created_at: string;
  updated_at: string;
  current: boolean;
  online: boolean;
};

export type DeviceSnapshot = {
  devices: MemberDevice[];
  online_platform_ids: number[];
};

export type PlatformLogoutResult = {
  platform_id: number;
  state: "logged_out";
  correlation_id: string;
};

type ErrorPayload = { code?: unknown; message?: unknown; correlation_id?: unknown };

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

async function checkedPayload(response: Response, operation: string): Promise<Record<string, unknown>> {
  const text = await response.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text) as unknown;
    } catch {
      if (response.ok) throw new Error(`${operation} response is not valid JSON`);
    }
  }
  const body = record(payload);
  if (!response.ok) {
    const error = (body ?? {}) as ErrorPayload;
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : `${operation} request failed`,
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  if (!body) throw new Error(`${operation} response is malformed`);
  return body;
}

function isDevice(value: unknown): value is MemberDevice {
  const item = record(value);
  return Boolean(item &&
    typeof item.device_id === "string" && item.device_id.length > 0 &&
    typeof item.platform_id === "number" && Number.isInteger(item.platform_id) &&
    (item.status === "active" || item.status === "disabled") &&
    typeof item.created_at === "string" && typeof item.updated_at === "string" &&
    typeof item.current === "boolean" && typeof item.online === "boolean");
}

export async function getDevices(
  baseURL: string,
  idToken: string,
  deviceID: string,
  platformID = 5,
  request: typeof fetch = fetch
): Promise<DeviceSnapshot> {
  const query = new URLSearchParams({ platform_id: String(platformID), device_id: deviceID });
  const response = await request(`${baseURL}/v1/im/devices?${query}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  });
  const body = await checkedPayload(response, "device projection");
  if (!Array.isArray(body.devices) || !body.devices.every(isDevice) ||
      !Array.isArray(body.online_platform_ids) || !body.online_platform_ids.every((value) => typeof value === "number" && Number.isInteger(value))) {
    throw new Error("device projection response is malformed");
  }
  return body as DeviceSnapshot & Record<string, unknown>;
}

export async function logoutPlatform(
  baseURL: string,
  idToken: string,
  currentDeviceID: string,
  targetPlatformID: number,
  currentPlatformID = 5,
  request: typeof fetch = fetch
): Promise<PlatformLogoutResult> {
  const response = await request(`${baseURL}/v1/im/platforms/${targetPlatformID}/logout`, {
    method: "POST",
    headers: { Authorization: `Bearer ${idToken}`, "Content-Type": "application/json" },
    body: JSON.stringify({ current_platform_id: currentPlatformID, current_device_id: currentDeviceID })
  });
  const body = await checkedPayload(response, "platform logout");
  if (body.platform_id !== targetPlatformID || body.state !== "logged_out" || typeof body.correlation_id !== "string" || !body.correlation_id) {
    throw new Error("platform logout response is malformed");
  }
  return body as PlatformLogoutResult & Record<string, unknown>;
}
