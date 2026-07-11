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
  let syncPending = true;
  const connecting = () => update({ state: "connecting" });
  const transportConnected = () => update(syncPending
    ? { state: "connecting", message: "OpenIM connected; synchronizing local data" }
    : { state: "connected" });
  const failed = (event: WSEvent) => update({ state: "failed", message: `${event.errCode}: ${event.errMsg}` });
  let resolveInitialSync: (() => void) | null = null;
  let rejectInitialSync: ((error: Error) => void) | null = null;
  const initialSync = new Promise<void>((resolve, reject) => {
    resolveInitialSync = resolve;
    rejectInitialSync = reject;
  });
  const syncStarted = () => update({ state: "connecting", message: "Synchronizing OpenIM local data" });
  const syncFinished = () => {
    update({ state: "connected" });
    if (!syncPending) return;
    syncPending = false;
    resolveInitialSync?.();
  };
  const syncFailed = () => {
    const error = new Error("OpenIM local data synchronization failed");
    update({ state: "failed", message: error.message });
    if (!syncPending) return;
    syncPending = false;
    rejectInitialSync?.(error);
  };
  const expired = () => update({ state: "expired", message: "OpenIM user token expired" });
  const kicked = () => update({ state: "kicked", message: "This device was signed out by the server" });
  const offline = () => update({ state: "failed", message: "Browser network is offline" });
  const online = () => {
    update({ state: "connecting" });
    void sdk.networkStatusChanged().catch((error: unknown) => {
      const message = error instanceof Error ? error.message : "OpenIM network recovery failed";
      update({ state: "failed", message });
    });
  };

  sdk.on(CbEvents.OnConnecting, connecting);
  sdk.on(CbEvents.OnConnectSuccess, transportConnected);
  sdk.on(CbEvents.OnConnectFailed, failed);
  sdk.on(CbEvents.OnSyncServerStart, syncStarted);
  sdk.on(CbEvents.OnSyncServerFinish, syncFinished);
  sdk.on(CbEvents.OnSyncServerFailed, syncFailed);
  sdk.on(CbEvents.OnUserTokenExpired, expired);
  sdk.on(CbEvents.OnUserTokenInvalid, expired);
  sdk.on(CbEvents.OnKickedOffline, kicked);
  window.addEventListener("offline", offline);
  window.addEventListener("online", online);

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
    let syncTimeout = 0;
    try {
      await Promise.race([
        initialSync,
        new Promise<never>((_, reject) => {
          syncTimeout = window.setTimeout(
            () => reject(new Error("OpenIM local data synchronization timed out")),
            60_000
          );
        })
      ]);
    } finally {
      window.clearTimeout(syncTimeout);
    }
  } catch (error) {
    detach();
    throw error;
  }

  function detach(): void {
    sdk.off(CbEvents.OnConnecting, connecting);
    sdk.off(CbEvents.OnConnectSuccess, transportConnected);
    sdk.off(CbEvents.OnConnectFailed, failed);
    sdk.off(CbEvents.OnSyncServerStart, syncStarted);
    sdk.off(CbEvents.OnSyncServerFinish, syncFinished);
    sdk.off(CbEvents.OnSyncServerFailed, syncFailed);
    sdk.off(CbEvents.OnUserTokenExpired, expired);
    sdk.off(CbEvents.OnUserTokenInvalid, expired);
    sdk.off(CbEvents.OnKickedOffline, kicked);
    window.removeEventListener("offline", offline);
    window.removeEventListener("online", online);
  }
  return detach;
}

export async function disconnectOpenIM(): Promise<void> {
  await sdk.logout();
}
