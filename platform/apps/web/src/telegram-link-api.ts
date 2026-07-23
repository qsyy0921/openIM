import { PlatformAPIError } from "./platform-api";

export type TelegramLinkStatus = {
  state: "unbound" | "pending" | "bound";
  expires_at?: string;
};

export type TelegramLinkChallenge = {
  state: "pending";
  code: string;
  command: string;
  expires_at: string;
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

function linkQuery(deviceID: string, platformID: number): string {
  return new URLSearchParams({ platform_id: String(platformID), device_id: deviceID }).toString();
}

function validTimestamp(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && !Number.isNaN(Date.parse(value));
}

export async function getTelegramLinkStatus(
  baseURL: string,
  idToken: string,
  deviceID: string,
  platformID = 5,
  request: typeof fetch = fetch
): Promise<TelegramLinkStatus> {
  const response = await request(`${baseURL}/v1/agent/channels/telegram/link?${linkQuery(deviceID, platformID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  });
  const body = await checkedPayload(response, "Telegram link status");
  if (body.state !== "unbound" && body.state !== "pending" && body.state !== "bound") {
    throw new Error("Telegram link status response is malformed");
  }
  if (body.state === "pending") {
    if (!validTimestamp(body.expires_at)) throw new Error("Telegram link status response is malformed");
    return { state: "pending", expires_at: body.expires_at };
  }
  if (body.expires_at !== undefined) throw new Error("Telegram link status response is malformed");
  return { state: body.state };
}

export async function issueTelegramLinkChallenge(
  baseURL: string,
  idToken: string,
  deviceID: string,
  platformID = 5,
  request: typeof fetch = fetch
): Promise<TelegramLinkChallenge> {
  const response = await request(`${baseURL}/v1/agent/channels/telegram/link-challenges?${linkQuery(deviceID, platformID)}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${idToken}` }
  });
  const body = await checkedPayload(response, "Telegram link challenge");
  const validCode = typeof body.code === "string" && /^(?:[A-Z2-7]{4}-){7}[A-Z2-7]{4}$/.test(body.code);
  if (body.state !== "pending" || !validCode || body.command !== `/link ${body.code}` || !validTimestamp(body.expires_at)) {
    throw new Error("Telegram link challenge response is malformed");
  }
  return body as TelegramLinkChallenge & Record<string, unknown>;
}
