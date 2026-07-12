import { GroupMemberRole, MessageReceiveOptType, MessageStatus, MessageType, SessionType, type ConversationItem, type GroupMemberItem, type MessageItem } from "@openim/wasm-client-sdk";
import { describe, expect, it } from "vitest";

import { MAX_FILE_BYTES, MAX_IMAGE_BYTES, ConversationController, canInviteGroupMembers, canRemoveGroupMember, type ChatEvents, type ChatPort } from "./chat";

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
    isPinned: false,
    recvMsgOpt: MessageReceiveOptType.Normal
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

function mediaMessage(
  id: string,
  contentType: MessageType.PictureMessage | MessageType.FileMessage,
  sendID = "self",
  recvID = "peer",
  sessionType = SessionType.Single,
  groupID = ""
): MessageItem {
  const common = {
    ...message(id, sendID, recvID, "", MessageStatus.Sending, sessionType, groupID),
    contentType,
    textElem: undefined
  };
  if (contentType === MessageType.PictureMessage) {
    const picture = { uuid: `uuid-${id}`, type: "image/png", size: 4, width: 2, height: 2, url: "https://media.example/image.png" };
    return { ...common, pictureElem: { sourcePath: "", sourcePicture: picture, bigPicture: picture, snapshotPicture: picture } } as MessageItem;
  }
  return {
    ...common,
    fileElem: {
      filePath: "",
      uuid: `uuid-${id}`,
      sourceUrl: "https://media.example/report.txt",
      fileName: "report.txt",
      fileSize: 4,
      fileType: "text/plain"
    }
  } as MessageItem;
}

function groupMember(userID: string, roleLevel = GroupMemberRole.Normal, groupID = "group-1"): GroupMemberItem {
  return { groupID, userID, nickname: userID, roleLevel } as GroupMemberItem;
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
  imageCreates = 0;
  fileCreates = 0;
  settingCalls: Array<{ conversationID: string; patch: { isPinned?: boolean; recvMsgOpt?: MessageReceiveOptType } }> = [];
  settingError: Error | null = null;
  groupInvites: Array<{ groupID: string; userIDs: string[] }> = [];
  groupRemovals: Array<{ groupID: string; userID: string }> = [];
  leftGroups: string[] = [];
  dismissedGroups: string[] = [];
  groupActionError: Error | null = null;

  listConversations = async () => this.conversations;
  setConversation = async (conversationID: string, patch: { isPinned?: boolean; recvMsgOpt?: MessageReceiveOptType }) => {
    this.settingCalls.push({ conversationID, patch });
    if (this.settingError) throw this.settingError;
  };
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
  createImage = async (_file: File) => {
    this.imageCreates += 1;
    return mediaMessage("image-100", MessageType.PictureMessage);
  };
  createFile = async (_file: File) => {
    this.fileCreates += 1;
    return mediaMessage("file-100", MessageType.FileMessage);
  };
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
  inviteGroupMembers = async (groupID: string, userIDs: string[]) => {
    this.groupInvites.push({ groupID, userIDs });
    if (this.groupActionError) throw this.groupActionError;
    this.members = [...this.members, ...userIDs.map((userID) => groupMember(userID, GroupMemberRole.Normal, groupID))];
  };
  removeGroupMember = async (groupID: string, userID: string) => {
    this.groupRemovals.push({ groupID, userID });
    if (this.groupActionError) throw this.groupActionError;
    this.members = this.members.filter((member) => member.userID !== userID);
  };
  leaveGroup = async (groupID: string) => {
    this.leftGroups.push(groupID);
    if (this.groupActionError) throw this.groupActionError;
  };
  dismissGroup = async (groupID: string) => {
    this.dismissedGroups.push(groupID);
    if (this.groupActionError) throw this.groupActionError;
  };
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

  it("filters the authoritative conversation projection by name, user ID, and group ID", async () => {
    const port = new FakePort();
    port.conversations = [
      { ...conversation("single", "peer-user"), showName: "Design Team" },
      { ...conversation("group", "group-42", 0, SessionType.Group), showName: "Release Room" }
    ];
    const controller = new ConversationController(port);
    await controller.start("self");

    controller.setConversationQuery("  DESIGN ");
    expect(controller.visibleConversations().map((item) => item.conversationID)).toEqual(["single"]);
    controller.setConversationQuery("peer-USER");
    expect(controller.visibleConversations().map((item) => item.conversationID)).toEqual(["single"]);
    controller.setConversationQuery("GROUP-42");
    expect(controller.visibleConversations().map((item) => item.conversationID)).toEqual(["group"]);
    controller.setConversationQuery("");
    expect(controller.visibleConversations()).toHaveLength(2);
    expect(controller.getState().conversations).toHaveLength(2);
  });

  it("pins only after the SDK setting call succeeds and keeps pinned ordering stable", async () => {
    const port = new FakePort();
    port.conversations = [
      { ...conversation("older", "older"), latestMsgSendTime: 10 },
      { ...conversation("newer", "newer"), latestMsgSendTime: 20 }
    ];
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.setPinned("older", true);

    expect(port.settingCalls).toEqual([{ conversationID: "older", patch: { isPinned: true } }]);
    expect(controller.getState().conversations.map((item) => [item.conversationID, item.isPinned])).toEqual([
      ["older", true],
      ["newer", false]
    ]);
    expect(controller.getState().conversationActionByID).toEqual({});
  });

  it("maps do-not-disturb to NotNotify and restores Normal when disabled", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.setMuted("single", true);
    await controller.setMuted("single", false);

    expect(port.settingCalls).toEqual([
      { conversationID: "single", patch: { recvMsgOpt: MessageReceiveOptType.NotNotify } },
      { conversationID: "single", patch: { recvMsgOpt: MessageReceiveOptType.Normal } }
    ]);
    expect(controller.getState().conversations[0].recvMsgOpt).toBe(MessageReceiveOptType.Normal);
  });

  it("rejects unsupported NotReceive state without changing it", async () => {
    const port = new FakePort();
    port.conversations = [{ ...conversation("single", "peer"), recvMsgOpt: MessageReceiveOptType.NotReceive }];
    const controller = new ConversationController(port);
    await controller.start("self");

    await expect(controller.setMuted("single", true)).rejects.toThrow("不接收消息");

    expect(port.settingCalls).toEqual([]);
    expect(controller.getState().conversations[0].recvMsgOpt).toBe(MessageReceiveOptType.NotReceive);
    expect(controller.getState().error).toContain("不能在此切换");
  });

  it("keeps prior settings on SDK failure and clears the mutation marker", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    port.settingError = new Error("setting rejected");
    const controller = new ConversationController(port);
    await controller.start("self");

    await expect(controller.setPinned("single", true)).rejects.toThrow("setting rejected");

    expect(controller.getState().conversations[0].isPinned).toBe(false);
    expect(controller.getState().conversationActionByID).toEqual({});
    expect(controller.getState().error).toContain("setting rejected");
  });

  it("rejects a duplicate write for one conversation while another conversation can update", async () => {
    const port = new FakePort();
    port.conversations = [conversation("first", "first"), conversation("second", "second")];
    let releaseFirst!: () => void;
    port.setConversation = async (conversationID, patch) => {
      port.settingCalls.push({ conversationID, patch });
      if (conversationID === "first") await new Promise<void>((resolve) => { releaseFirst = resolve; });
    };
    const controller = new ConversationController(port);
    await controller.start("self");

    const first = controller.setPinned("first", true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await expect(controller.setMuted("first", true)).rejects.toThrow("正在更新");
    await controller.setMuted("second", true);
    releaseFirst();
    await first;

    expect(port.settingCalls.map((item) => item.conversationID)).toEqual(["first", "second"]);
    expect(controller.getState().conversations.find((item) => item.conversationID === "first")?.isPinned).toBe(true);
    expect(controller.getState().conversations.find((item) => item.conversationID === "second")?.recvMsgOpt).toBe(MessageReceiveOptType.NotNotify);
  });

  it("loads supported media history while excluding unsupported custom messages", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    port.historyMessages = [
      message("m1", "peer", "self"),
      mediaMessage("image-1", MessageType.PictureMessage, "peer", "self"),
      mediaMessage("file-1", MessageType.FileMessage, "peer", "self"),
      { ...message("custom-1", "peer", "self"), contentType: MessageType.CustomMessage }
    ];
    const controller = new ConversationController(port);
    await controller.start("self");

    await controller.select("single");

    expect(controller.getState().messages.map((item) => item.clientMsgID)).toEqual(["m1", "image-1", "file-1"]);
  });

  it("scopes and clamps real upload progress to the optimistic image", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    let resolveSend!: (message: MessageItem) => void;
    port.send = async (_target, draft) => new Promise<MessageItem>((resolve) => {
      resolveSend = () => resolve({ ...draft, serverMsgID: "server-image", status: MessageStatus.Succeed });
    });
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("single");

    const sending = controller.sendImage(new File(["png"], "photo.png", { type: "image/png" }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    port.handlers?.uploadProgress("unknown", 55);
    port.handlers?.uploadProgress("image-100", 145.4);

    expect(controller.getState().uploadProgressByClientMsgID).toEqual({ "image-100": 100 });
    resolveSend(mediaMessage("unused", MessageType.PictureMessage));
    await sending;
    expect(controller.getState().messages[0]).toMatchObject({ clientMsgID: "image-100", serverMsgID: "server-image", status: MessageStatus.Succeed });
    expect(controller.getState().uploadProgressByClientMsgID).toEqual({});
  });

  it("sends a file through the selected group conversation", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");

    await controller.sendFile(new File(["data"], "report.txt", { type: "text/plain" }));

    expect(port.fileCreates).toBe(1);
    expect(port.sentConversations[0]).toMatchObject({ conversationID: "group", groupID: "group-1" });
    expect(controller.getState().messages[0]).toMatchObject({ contentType: MessageType.FileMessage, status: MessageStatus.Succeed });
  });

  it("rejects invalid media before invoking the SDK creation path", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("single");

    await expect(controller.sendImage(new File(["svg"], "vector.svg", { type: "image/svg+xml" }))).rejects.toThrow("仅支持");
    await expect(controller.sendImage({ name: "large.png", size: MAX_IMAGE_BYTES + 1, type: "image/png" } as File)).rejects.toThrow("20 MiB");
    await expect(controller.sendFile(new File([], "empty.txt", { type: "text/plain" }))).rejects.toThrow("空文件");
    await expect(controller.sendFile({ name: "large.bin", size: MAX_FILE_BYTES + 1, type: "application/octet-stream" } as File)).rejects.toThrow("100 MiB");
    expect(port.imageCreates).toBe(0);
    expect(port.fileCreates).toBe(0);
    expect(controller.getState().error).toContain("100 MiB");
  });

  it("keeps failed media visible and retains its last upload progress", async () => {
    const port = new FakePort();
    port.conversations = [conversation("single", "peer")];
    port.send = async (_target, draft) => {
      port.handlers?.uploadProgress(draft.clientMsgID, 42);
      throw new Error("upload rejected");
    };
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("single");

    await expect(controller.sendFile(new File(["data"], "report.txt", { type: "text/plain" }))).rejects.toThrow("upload rejected");

    expect(controller.getState().messages[0].status).toBe(MessageStatus.Failed);
    expect(controller.getState().uploadProgressByClientMsgID).toEqual({ "file-100": 42 });
    expect(controller.getState().error).toContain("发送失败");
  });

  it("removes SDK event handlers when stopped", async () => {
    const port = new FakePort();
    const controller = new ConversationController(port);
    await controller.start("self");
    expect(port.handlers).not.toBeNull();

    controller.stop();

    expect(port.handlers).toBeNull();
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

  it("projects owner, admin, and normal member removal permissions", () => {
    const ownerMembers = [groupMember("owner", GroupMemberRole.Owner), groupMember("admin", GroupMemberRole.Admin), groupMember("normal")];
    expect(canInviteGroupMembers(ownerMembers, "owner")).toBe(true);
    expect(canInviteGroupMembers(ownerMembers, "admin")).toBe(true);
    expect(canInviteGroupMembers(ownerMembers, "normal")).toBe(false);
    expect(canRemoveGroupMember(ownerMembers, "owner", "admin")).toBe(true);
    expect(canRemoveGroupMember(ownerMembers, "admin", "normal")).toBe(true);
    expect(canRemoveGroupMember(ownerMembers, "admin", "owner")).toBe(false);
    expect(canRemoveGroupMember(ownerMembers, "admin", "admin")).toBe(false);
    expect(canRemoveGroupMember(ownerMembers, "normal", "admin")).toBe(false);
  });

  it("invites only unique non-members and refreshes the active group", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    port.members = [groupMember("self", GroupMemberRole.Owner), groupMember("existing")];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");

    await controller.inviteGroupMembers(["existing", "new-member", "new-member", "self"]);

    expect(port.groupInvites).toEqual([{ groupID: "group-1", userIDs: ["new-member"] }]);
    expect(controller.getState().groupMembers.map((member) => member.userID)).toEqual(["self", "existing", "new-member"]);
    expect(controller.getState().groupAction).toBeNull();
  });

  it("removes one permitted member and rejects a stale or forbidden target", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    port.members = [groupMember("self", GroupMemberRole.Admin), groupMember("normal"), groupMember("other-admin", GroupMemberRole.Admin)];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");

    await controller.removeGroupMember("normal");
    await expect(controller.removeGroupMember("other-admin")).rejects.toThrow("不能移除");

    expect(port.groupRemovals).toEqual([{ groupID: "group-1", userID: "normal" }]);
    expect(controller.getState().groupMembers.map((member) => member.userID)).toEqual(["self", "other-admin"]);
    expect(controller.getState().error).toContain("不能移除");
  });

  it("keeps group state on lifecycle failure and rejects a concurrent action", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    port.members = [groupMember("self", GroupMemberRole.Owner), groupMember("normal")];
    let release!: () => void;
    port.removeGroupMember = async (groupID, userID) => {
      port.groupRemovals.push({ groupID, userID });
      await new Promise<void>((resolve) => { release = resolve; });
      throw new Error("remove rejected");
    };
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");

    const removing = controller.removeGroupMember("normal");
    await new Promise((resolve) => setTimeout(resolve, 0));
    await expect(controller.dismissActiveGroup()).rejects.toThrow("正在执行");
    release();
    await expect(removing).rejects.toThrow("remove rejected");

    expect(controller.getState().activeConversationID).toBe("group");
    expect(controller.getState().groupMembers.map((member) => member.userID)).toContain("normal");
    expect(controller.getState().groupAction).toBeNull();
  });

  it("clears the active projection after non-owner leave and owner dismiss", async () => {
    const leavePort = new FakePort();
    leavePort.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    leavePort.members = [groupMember("owner", GroupMemberRole.Owner), groupMember("self")];
    const leaving = new ConversationController(leavePort);
    await leaving.start("self");
    await leaving.select("group");
    await leaving.leaveActiveGroup();
    expect(leavePort.leftGroups).toEqual(["group-1"]);
    expect(leaving.getState()).toMatchObject({ activeConversationID: null, conversations: [], groupMembers: [] });

    const dismissPort = new FakePort();
    dismissPort.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    dismissPort.members = [groupMember("self", GroupMemberRole.Owner), groupMember("normal")];
    const dismissing = new ConversationController(dismissPort);
    await dismissing.start("self");
    await dismissing.select("group");
    await dismissing.dismissActiveGroup();
    expect(dismissPort.dismissedGroups).toEqual(["group-1"]);
    expect(dismissing.getState()).toMatchObject({ activeConversationID: null, conversations: [], groupMembers: [] });
  });

  it("scopes member callbacks and clears a group-unavailable callback idempotently", async () => {
    const port = new FakePort();
    port.conversations = [conversation("group", "group-1", 0, SessionType.Group)];
    port.members = [groupMember("self", GroupMemberRole.Owner)];
    const controller = new ConversationController(port);
    await controller.start("self");
    await controller.select("group");
    port.members = [...port.members, groupMember("new-member")];

    port.handlers?.groupMembersChanged("other-group");
    expect(controller.getState().groupMembers).toHaveLength(1);
    port.handlers?.groupMembersChanged("group-1");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(controller.getState().groupMembers).toHaveLength(2);

    port.handlers?.groupUnavailable("group-1");
    port.handlers?.groupUnavailable("group-1");
    expect(controller.getState()).toMatchObject({ activeConversationID: null, conversations: [], groupMembers: [] });
    port.handlers?.conversationsChanged([conversation("group", "group-1", 0, SessionType.Group)]);
    expect(controller.getState().conversations).toEqual([]);
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

  it("keeps group creation failure explicit without creating a local conversation", async () => {
    const port = new FakePort();
    port.createGroup = async () => { throw new Error("group rejected"); };
    const controller = new ConversationController(port);
    await controller.start("self");

    await expect(controller.createGroup("Project", ["member-1"])).rejects.toThrow("group rejected");

    expect(controller.getState().conversations).toEqual([]);
    expect(controller.getState().error).toContain("group rejected");
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
