export type IMSession = {
  userID: string;
  wsURL: string;
  userToken: string;
  expiresAt: string;
};

type ErrorPayload = {
  code?: unknown;
  message?: unknown;
  correlation_id?: unknown;
};

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

export class PlatformAPIError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly correlationID: string,
    readonly status: number
  ) {
    super(message);
    this.name = "PlatformAPIError";
  }
}

export async function createIMSession(
  baseURL: string,
  idToken: string,
  deviceID: string,
  request: typeof fetch = fetch
): Promise<IMSession> {
  if (!idToken || !deviceID) {
    throw new Error("enterprise identity and device are required");
  }
  const response = await request(`${baseURL}/v1/im/session`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${idToken}`,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({ platform_id: 5, device_id: deviceID })
  });
  const payload: unknown = await response.json();
  const body = record(payload);
  if (!response.ok) {
    const error = (body ?? {}) as ErrorPayload;
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : "platform session request failed",
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  const userID = body?.user_id;
  const wsURL = body?.ws_url;
  const userToken = body?.user_token;
  const expiresAt = body?.expires_at;
  if (![userID, wsURL, userToken, expiresAt].every((value) => typeof value === "string" && value.length > 0)) {
    throw new Error("platform session response is malformed");
  }
  return {
    userID: userID as string,
    wsURL: wsURL as string,
    userToken: userToken as string,
    expiresAt: expiresAt as string
  };
}
