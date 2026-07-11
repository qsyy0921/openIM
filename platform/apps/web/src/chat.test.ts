import { MessageStatus, MessageType, SessionType, type ConversationItem, type MessageItem } from "@openim/wasm-client-sdk";
import { describe, expect, it } from "vitest";

import { SingleChatController, type ChatEvents, type ChatPort } from "./chat";

function conversation(id: string, userID: string, unreadCount = 0, type = SessionType.Single): ConversationItem {
  return {
    conversationID: id,
    conversationType: type,
    userID,
    groupID: type === SessionType.Single ? "" : userID,
    showName: userID,
    unreadCount,
    latestMsg: "",
    latestMsgSendTime: 1,
    isPinned: false
  } as ConversationItem;
}

function message(id: string, sendID: string, recvID: string, text = id, status = MessageStatus.Succeed): MessageItem {
  return {
    clientMsgID: id,
    serverMsgID: `server-${id}`,
    createTime: Number(id.replace(/\D/g, "")) || 1,
    sendTime: Number(id.replace(/\D/g, "")) || 1,
    sessionType: SessionType.Single,
    sendID,
    recvID,
    contentType: MessageType.TextMessage,
    status,
    textElem: { content: text }
  } as MessageItem;
}

class FakePort implements ChatPort {
  conversations: ConversationItem[] = [];
  unread = 0;
  historyMessages: MessageItem[] = [];
  historyEnd = true;
  sentResult: MessageItem | null = null;
  sendError: Error | null = null;
  handlers: ChatEvents | null = null;
  markedRead: string[] = [];

  listConversations = async () => this.conversations;
  totalUnread = async () => this.unread;
  oneConversation = async (userID: string) => conversation(`si_self_${userID}`, userID);
  history = async () => ({ isEnd: this.historyEnd, messageList: this.historyMessages });
  createText = async (text: string) => message("m100", "self", "peer", text, MessageStatus.Sending);
  send = async (_receiverID: string, draft: MessageItem) => {
    if (this.sendError) throw this.sendError;
    return this.sentResult ?? { ...draft, serverMsgID: "server-sent", status: MessageStatus.Succeed };
  };
  markRead = async (conversationID: string) => { this.markedRead.push(conversationID); };
  subscribe = (events: ChatEvents) => {
    this.handlers = events;
    return () => { this.handlers = null; };
  };
}

describe("SingleChatController", () => {
  it("loads only single conversations and total unread", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer", 3), conversation("group", "group-1", 9, SessionType.Group)];
    port.unread = 12;
    const controller = new SingleChatController(port);

    await controller.start("self");

    expect(controller.getState().conversations.map((item) => item.conversationID)).toEqual(["single"]);
    expect(controller.getState().totalUnread).toBe(12);
  });

  it("opens a direct conversation, loads history, and marks it read", async () => {
    const port = new FakePort();
    port.historyMessages = [message("m1", "peer", "self")];
    const controller = new SingleChatController(port);
    await controller.start("self");

    await controller.openDirect("peer");

    expect(controller.getState().activeConversationID).toBe("si_self_peer");
    expect(controller.getState().messages.map((item) => item.clientMsgID)).toEqual(["m1"]);
    expect(port.markedRead).toEqual(["si_self_peer"]);
  });

  it("moves an optimistic text message from sending to succeeded", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    const controller = new SingleChatController(port);
    await controller.start("self");
    await controller.select("single");

    await controller.sendText("hello");

    expect(controller.getState().messages).toHaveLength(1);
    expect(controller.getState().messages[0]).toMatchObject({ clientMsgID: "m100", serverMsgID: "server-sent", status: MessageStatus.Succeed });
  });

  it("keeps a failed optimistic message and exposes the error", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    port.sendError = new Error("transport rejected");
    const controller = new SingleChatController(port);
    await controller.start("self");
    await controller.select("single");

    await expect(controller.sendText("hello")).rejects.toThrow("transport rejected");

    expect(controller.getState().messages[0].status).toBe(MessageStatus.Failed);
    expect(controller.getState().error).toContain("发送失败");
  });

  it("deduplicates real-time text and marks the active conversation read", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer", 2)];
    const controller = new SingleChatController(port);
    await controller.start("self");
    await controller.select("single");
    port.markedRead = [];
    const incoming = message("m2", "peer", "self", "live");

    port.handlers?.messagesReceived([incoming, incoming]);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(controller.getState().messages.map((item) => item.clientMsgID)).toEqual(["m2"]);
    expect(port.markedRead).toEqual(["single"]);
    expect(controller.getState().conversations[0].unreadCount).toBe(0);
  });

  it("reloads conversations, unread, active history, and read state after reconnect", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer", 1)];
    port.historyMessages = [message("m1", "peer", "self", "before")];
    const controller = new SingleChatController(port);
    await controller.start("self");
    await controller.select("single");
    port.unread = 4;
    port.historyMessages = [message("m2", "peer", "self", "after")];
    port.markedRead = [];

    await controller.restore();

    expect(controller.getState()).toMatchObject({ totalUnread: 4, restoring: false, activeConversationID: "single" });
    expect(controller.getState().messages.map((item) => item.clientMsgID)).toEqual(["m2"]);
    expect(port.markedRead).toEqual(["single"]);
  });
});
