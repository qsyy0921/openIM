/// <reference types="vite/client" />

import { CbEvents, getSDK, LogLevel, ViewType, type MessageItem, type WSEvent } from "@openim/wasm-client-sdk";

const sdk = getSDK();

function conversationID(left: string, right: string): string {
  return `si_${[left, right].sort().join("_")}`;
}

function textOf(message: MessageItem): string {
  return message.textElem?.content || message.quoteElem?.text || "";
}

export async function loginPeer(userID: string, token: string): Promise<void> {
  let resolveSync!: () => void;
  let rejectSync!: (error: Error) => void;
  const synchronized = new Promise<void>((resolve, reject) => {
    resolveSync = resolve;
    rejectSync = reject;
  });
  const synced = () => resolveSync();
  const syncFailed = (event: WSEvent) => rejectSync(new Error(`${event.errCode}: ${event.errMsg}`));
  sdk.on(CbEvents.OnSyncServerFinish, synced);
  sdk.on(CbEvents.OnSyncServerFailed, syncFailed);
  try {
    await sdk.login({
      userID,
      token,
      platformID: 5,
      apiAddr: import.meta.env.VITE_OPENIM_API_URL,
      wsAddr: import.meta.env.VITE_OPENIM_WS_URL,
      logLevel: LogLevel.Error,
      isLogStandardOutput: false
    });
    await Promise.race([
      synchronized,
      new Promise<never>((_, reject) => window.setTimeout(() => reject(new Error("peer OpenIM synchronization timed out")), 60_000))
    ]);
  } finally {
    sdk.off(CbEvents.OnSyncServerFinish, synced);
    sdk.off(CbEvents.OnSyncServerFailed, syncFailed);
  }
}

export async function sendPeerText(targetUserID: string, text: string): Promise<void> {
  const message = (await sdk.createTextMessage(text)).data;
  await sdk.sendMessage({ recvID: targetUserID, groupID: "", message });
}

async function waitForText(userID: string, targetUserID: string, text: string): Promise<MessageItem> {
  const id = conversationID(userID, targetUserID);
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const result = (await sdk.getAdvancedHistoryMessageList({
      conversationID: id,
      startClientMsgID: "",
      count: 100,
      viewType: ViewType.History
    })).data;
    const message = result.messageList.find((item) => textOf(item) === text);
    if (message) return message;
    await new Promise((resolve) => window.setTimeout(resolve, 500));
  }
  throw new Error("peer did not synchronize the expected source message");
}

export async function findPeerMessage(userID: string, targetUserID: string, sourceText: string): Promise<{ sourceClientMsgID: string; sourceSeq: number }> {
  const source = await waitForText(userID, targetUserID, sourceText);
  return { sourceClientMsgID: source.clientMsgID, sourceSeq: source.seq };
}

export async function quotePeerMessage(userID: string, targetUserID: string, sourceText: string, replyText: string): Promise<void> {
  const source = await waitForText(userID, targetUserID, sourceText);
  const quote = (await sdk.createQuoteMessage({ text: replyText, message: JSON.stringify(source) })).data;
  await sdk.sendMessage({ recvID: targetUserID, groupID: "", message: quote });
}

export async function logoutPeer(): Promise<void> {
  await sdk.logout();
}
