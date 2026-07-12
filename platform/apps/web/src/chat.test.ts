import { GroupMemberRole, MessageStatus, MessageType, SessionType, type ConversationItem, type GroupMemberItem, type MessageItem } from "@openim/wasm-client-sdk";
import { describe, expect, it } from "vitest";

import { ConversationController, type ChatEvents, type ChatPort } from "./chat";

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

function message(
  id: string,
  sendID: string,
  recvID: string,
  text = id,
  status = MessageStatus.Succeed,
  type = SessionType.Single,
  groupID = ""
): MessageItem {
  return {
    clientMsgID: id,
    serverMsgID: `server-${id}`,
    createTime: Number(id.replace(/\D/g, "")) || 1,
    sendTime: Number(id.replace(/\D/g, "")) || 1,
    sessionType: type,
    sendID,
    recvID,
    groupID,
    senderNickname: sendID,
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
  historyCalls = 0;
  sentResult: MessageItem | null = null;
  sendError: Error | null = null;
  handlers: ChatEvents | null = null;
  markedRead: string[] = [];
  sentConversations: ConversationItem[] = [];
  members: GroupMemberItem[] = [];
  createdGroup = conversation("group-new", "group-new", 0, SessionType.Group);
  createdGroupArgs: { name: string; memberUserIDs: string[] } | null = null;

  listConversations = async () => this.conversations;
  totalUnread = async () => this.unread;
  oneConversation = async (sourceID: string, sessionType: SessionType) => conversation(
    sessionType === SessionType.Group ? `group_${sourceID}` : `si_self_${sourceID}`,
    sourceID,
    0,
    sessionType
  );
  history = async () => {
    this.historyCalls += 1;
    return { isEnd: this.historyEnd, messageList: this.historyMessages };
  };
  createText = async (text: string) => message("m100", "self", "peer", text, MessageStatus.Sending);
  send = async (target: ConversationItem, draft: MessageItem) => {
    this.sentConversations.push(target);
    if (this.sendError) throw this.sendError;
    return this.sentResult ?? { ...draft, serverMsgID: "server-sent", status: MessageStatus.Succeed };
  };
  createGroup = async (name: string, memberUserIDs: string[]) => {
    this.createdGroupArgs = { name, memberUserIDs };
    return this.createdGroup;
  };
  groupMembers = async () => this.members;
  markRead = async (conversationID: string) => { this.markedRead.push(conversationID); };
  subscribe = (events: ChatEvents) => {
    this.handlers = events;
    return () => { this.handlers = null; };
  };
}

describe("ConversationController", () => {
  it("loads single and group conversations with total unread", async () => {
    const port = new FakePort();
    const dismissed = { ...conversation("dismissed", "group-old", 0, SessionType.Group), isNotInGroup: true };
    port.conversations = [conversation("single", "peer", 3), conversation("group", "group-1", 9, SessionType.Group), dismissed];
    port.unread = 12;
    const controller = new ConversationController(port);

    await controller.start("self");

    expect(controller.getState().conversations.map((item) => item.conversationID)).toEqual(["single", "group"]);
    expect(controller.getState().totalUnread).toBe(12);
  });

  it("opens a direct conversation, loads history, and marks it read", async () => {
    const port = new FakePort();
    port.historyMessages = [message("m1", "peer", "self")];
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.openDirect("peer");

    expect(controller.getState().activeConversationID).toBe("si_self_peer");
    expect(controller.getState().messages.map((item) => item.clientMsgID)).toEqual(["m1"]);
    expect(port.markedRead).toEqual(["si_self_peer"]);
  });

  it("moves an optimistic text message from sending to succeeded", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("single");

    await controller.sendText("hello");

    expect(controller.getState().messages).toHaveLength(1);
    expect(controller.getState().messages[0]).toMatchObject({ clientMsgID: "m100", serverMsgID: "server-sent", status: MessageStatus.Succeed });
    expect(port.sentConversations[0].conversationID).toBe("single");
  });

  it("loads group history and members, then sends to the selected group", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 2, SessionType.Group)];
    port.historyMessages = [message("m1", "member-1", "", "group history", MessageStatus.Succeed, SessionType.Group, "group-1")];
    port.members = [{ groupID: "group-1", userID: "member-1", nickname: "Member", roleLevel: GroupMemberRole.Normal }] as GroupMemberItem[];
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.select("group");
    await controller.sendText("group message");

    expect(controller.getState().messages.map((item) => item.textElem?.content)).toEqual(["group history", "group message"]);
    expect(controller.getState().groupMembers[0].userID).toBe("member-1");
    expect(port.sentConversations[0].groupID).toBe("group-1");
  });

  it("discards stale group-member results after switching conversations", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group), conversation("single", "peer")];
    let resolveMembers!: (members: GroupMemberItem[]) => void;
    const membersPromise = new Promise<GroupMemberItem[]>((resolve) => { resolveMembers = resolve; });
    port.groupMembers = () => membersPromise;
    const controller = new ConversationController(port);
    await controller.start("self");

    const groupSelection = controller.select("group");
    await new Promise((resolve) => setTimeout(resolve, 0));
    await controller.select("single");
    resolveMembers([{ groupID: "group-1", userID: "late", nickname: "Late", roleLevel: GroupMemberRole.Normal } as GroupMemberItem]);
    await groupSelection;

    expect(controller.getState()).toMatchObject({ activeConversationID: "single", groupMembers: [], loadingGroupMembers: false });
  });

  it("creates and opens a group from unique explicit member IDs", async () => {
    const port = new FakePort();
    port.createdGroup = conversation("group-new", "group-new", 0, SessionType.Group);
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.createGroup("Project", ["member-1", "member-2", "member-1", "self"]);

    expect(controller.getState().activeConversationID).toBe("group-new");
    expect(controller.getState().conversations[0].groupID).toBe("group-new");
    expect(port.createdGroupArgs).toEqual({ name: "Project", memberUserIDs: ["member-1", "member-2"] });
    expect(port.historyCalls).toBe(0);
  });

  it("keeps a failed optimistic message and exposes the error", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    port.sendError = new Error("transport rejected");
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("single");

    await expect(controller.sendText("hello")).rejects.toThrow("transport rejected");

    expect(controller.getState().messages[0].status).toBe(MessageStatus.Failed);
    expect(controller.getState().error).toContain("发送失败");
  });

  it("deduplicates real-time text and marks the active conversation read", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer", 2)];
    const controller = new ConversationController(port);
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

  it("scopes real-time group text by exact group ID", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 2, SessionType.Group)];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");
    port.markedRead = [];

    port.handlers?.messagesReceived([
      message("m2", "member-1", "", "included", MessageStatus.Succeed, SessionType.Group, "group-1"),
      message("m3", "member-2", "", "excluded", MessageStatus.Succeed, SessionType.Group, "group-2")
    ]);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(controller.getState().messages.map((item) => item.textElem?.content)).toEqual(["included"]);
    expect(port.markedRead).toEqual(["group"]);
  });

  it("reloads conversations, unread, active history, and read state after reconnect", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer", 1)];
    port.historyMessages = [message("m1", "peer", "self", "before")];
    const controller = new ConversationController(port);
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
