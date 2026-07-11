import { CbEvents, getSDK, LogLevel, type WSEvent } from "@openim/wasm-client-sdk";

import type { WebConfig } from "./config";
import type { IMSession } from "./platform-api";

export type ConnectionState = "connecting" | "connected" | "failed" | "expired" | "kicked";
export type ConnectionUpdate = { state: ConnectionState; message?: string };

const sdk = getSDK();

export async function connectOpenIM(
  config: WebConfig,
  session: IMSession,
  update: (state: ConnectionUpdate) => void
): Promise<() => void> {
  if (session.wsURL !== config.openIMWSURL) {
    throw new Error("platform session returned an unexpected OpenIM WebSocket endpoint");
  }
  const connecting = () => update({ state: "connecting" });
  const connected = () => update({ state: "connected" });
  const failed = (event: WSEvent) => update({ state: "failed", message: `${event.errCode}: ${event.errMsg}` });
  const expired = () => update({ state: "expired", message: "OpenIM user token expired" });
  const kicked = () => update({ state: "kicked", message: "This device was signed out by the server" });

  sdk.on(CbEvents.OnConnecting, connecting);
  sdk.on(CbEvents.OnConnectSuccess, connected);
  sdk.on(CbEvents.OnConnectFailed, failed);
  sdk.on(CbEvents.OnUserTokenExpired, expired);
  sdk.on(CbEvents.OnUserTokenInvalid, expired);
  sdk.on(CbEvents.OnKickedOffline, kicked);

  update({ state: "connecting" });
  try {
    await sdk.login({
      userID: session.userID,
      token: session.userToken,
      platformID: 5,
      apiAddr: config.openIMAPIURL,
      wsAddr: session.wsURL,
      logLevel: LogLevel.Error,
      isLogStandardOutput: false
    });
    update({ state: "connected" });
  } catch (error) {
    detach();
    throw error;
  }

  function detach(): void {
    sdk.off(CbEvents.OnConnecting, connecting);
    sdk.off(CbEvents.OnConnectSuccess, connected);
    sdk.off(CbEvents.OnConnectFailed, failed);
    sdk.off(CbEvents.OnUserTokenExpired, expired);
    sdk.off(CbEvents.OnUserTokenInvalid, expired);
    sdk.off(CbEvents.OnKickedOffline, kicked);
  }
  return detach;
}

export async function disconnectOpenIM(): Promise<void> {
  await sdk.logout();
}
