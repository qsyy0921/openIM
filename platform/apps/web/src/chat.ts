import {
  CbEvents,
  getSDK,
  GroupMemberFilter,
  GroupMemberRole,
  GroupType,
  MessageReceiveOptType,
  MessageStatus,
  MessageType,
  SessionType,
  ViewType,
  type ConversationItem,
  type GroupItem,
  type GroupMemberItem,
  type MessageItem,
  type WSEvent
} from "@openim/wasm-client-sdk";

export type ChatState = {
  conversations: ConversationItem[];
  conversationQuery: string;
  conversationActionByID: Record<string, "pin" | "mute">;
  totalUnread: number;
  activeConversationID: string | null;
  messages: MessageItem[];
  historyEnded: boolean;
  loadingHistory: boolean;
  groupMembers: GroupMemberItem[];
  loadingGroupMembers: boolean;
  groupAction: { groupID: string; kind: "invite" | "remove" | "leave" | "dismiss"; userID?: string } | null;
  restoring: boolean;
  uploadProgressByClientMsgID: Record<string, number>;
  error: string | null;
};

export type ChatEvents = {
  conversationsChanged: (items: ConversationItem[]) => void;
  totalUnreadChanged: (count: number) => void;
  messagesReceived: (items: MessageItem[]) => void;
  uploadProgress: (clientMsgID: string, progress: number) => void;
  groupMembersChanged: (groupID: string) => void;
  groupUnavailable: (groupID: string) => void;
};

export type ChatPort = {
  listConversations: () => Promise<ConversationItem[]>;
  setConversation: (conversationID: string, patch: { isPinned?: boolean; recvMsgOpt?: MessageReceiveOptType }) => Promise<void>;
  totalUnread: () => Promise<number>;
  oneConversation: (sourceID: string, sessionType: SessionType) => Promise<ConversationItem>;
  history: (conversationID: string, startClientMsgID: string) => Promise<{ isEnd: boolean; messageList: MessageItem[] }>;
  createText: (text: string) => Promise<MessageItem>;
  createImage: (file: File) => Promise<MessageItem>;
  createFile: (file: File) => Promise<MessageItem>;
  send: (conversation: ConversationItem, message: MessageItem) => Promise<MessageItem>;
  createGroup: (name: string, memberUserIDs: string[]) => Promise<ConversationItem>;
  groupMembers: (groupID: string) => Promise<GroupMemberItem[]>;
  inviteGroupMembers: (groupID: string, userIDs: string[]) => Promise<void>;
  removeGroupMember: (groupID: string, userID: string) => Promise<void>;
  leaveGroup: (groupID: string) => Promise<void>;
  dismissGroup: (groupID: string) => Promise<void>;
  markRead: (conversationID: string) => Promise<void>;
  subscribe: (events: ChatEvents) => () => void;
};

export const initialChatState: ChatState = {
  conversations: [],
  conversationQuery: "",
  conversationActionByID: {},
  totalUnread: 0,
  activeConversationID: null,
  messages: [],
  historyEnded: true,
  loadingHistory: false,
  groupMembers: [],
  loadingGroupMembers: false,
  groupAction: null,
  restoring: false,
  uploadProgressByClientMsgID: {},
  error: null
};

export const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
export const MAX_FILE_BYTES = 100 * 1024 * 1024;
const SUPPORTED_IMAGE_TYPES = new Set(["image/jpeg", "image/png", "image/gif", "image/webp"]);

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null) {
    const value = error as { errCode?: unknown; errMsg?: unknown };
    if (typeof value.errMsg === "string") return typeof value.errCode === "number" ? `${value.errCode}: ${value.errMsg}` : value.errMsg;
  }
  return "OpenIM operation failed";
}

function supportedConversations(items: ConversationItem[]): ConversationItem[] {
  return items
    .filter((item) => item.conversationType === SessionType.Single ||
      (item.conversationType === SessionType.Group && !item.isNotInGroup))
    .sort((left, right) => Number(right.isPinned) - Number(left.isPinned) || right.latestMsgSendTime - left.latestMsgSendTime);
}

export function filterConversations(items: ConversationItem[], query: string): ConversationItem[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return items;
  return items.filter((item) => [item.showName, item.userID, item.groupID]
    .some((value) => value?.toLowerCase().includes(normalized)));
}

export function canInviteGroupMembers(members: GroupMemberItem[], selfUserID: string): boolean {
  const role = members.find((member) => member.userID === selfUserID)?.roleLevel;
  return role === GroupMemberRole.Owner || role === GroupMemberRole.Admin;
}

export function canRemoveGroupMember(members: GroupMemberItem[], selfUserID: string, targetUserID: string): boolean {
  if (!targetUserID || targetUserID === selfUserID) return false;
  const self = members.find((member) => member.userID === selfUserID);
  const target = members.find((member) => member.userID === targetUserID);
  if (!self || !target || target.roleLevel === GroupMemberRole.Owner) return false;
  if (self.roleLevel === GroupMemberRole.Owner) return true;
  return self.roleLevel === GroupMemberRole.Admin && target.roleLevel === GroupMemberRole.Normal;
}

function supportedMessages(items: MessageItem[]): MessageItem[] {
  return items.filter((item) =>
    (item.sessionType === SessionType.Single || item.sessionType === SessionType.Group) &&
    (item.contentType === MessageType.TextMessage || item.contentType === MessageType.PictureMessage || item.contentType === MessageType.FileMessage)
  );
}

function validateFileName(file: File): void {
  if (!file.name.trim()) throw new Error("文件名不能为空");
  if (file.name.length > 255) throw new Error("文件名不能超过 255 个字符");
  if (file.size <= 0) throw new Error("不能发送空文件");
}

export function validateImage(file: File): void {
  validateFileName(file);
  if (!SUPPORTED_IMAGE_TYPES.has(file.type.toLowerCase())) throw new Error("仅支持 JPEG、PNG、GIF 和 WebP 图片");
  if (file.size > MAX_IMAGE_BYTES) throw new Error("图片不能超过 20 MiB");
}

export function validateFile(file: File): void {
  validateFileName(file);
  if (file.size > MAX_FILE_BYTES) throw new Error("文件不能超过 100 MiB");
}

function mergeMessages(current: MessageItem[], incoming: MessageItem[]): MessageItem[] {
  const merged = new Map(current.map((message) => [message.clientMsgID, message]));
  for (const message of incoming) {
    const previous = merged.get(message.clientMsgID);
    merged.set(message.clientMsgID, previous ? { ...previous, ...message } : message);
  }
  return [...merged.values()].sort((left, right) => (left.sendTime || left.createTime) - (right.sendTime || right.createTime));
}

function upsertConversations(current: ConversationItem[], incoming: ConversationItem[]): ConversationItem[] {
  const merged = new Map(current.map((item) => [item.conversationID, item]));
  for (const item of supportedConversations(incoming)) merged.set(item.conversationID, item);
  return supportedConversations([...merged.values()]);
}

export class ConversationController {
  private state: ChatState = initialChatState;
  private listeners = new Set<(state: ChatState) => void>();
  private unsubscribePort: (() => void) | null = null;
  private selfUserID = "";
  private groupMemberRequest = 0;
  private unavailableGroupIDs = new Set<string>();

  constructor(private readonly port: ChatPort) {}

  getState = (): ChatState => this.state;

  visibleConversations = (): ConversationItem[] => filterConversations(this.state.conversations, this.state.conversationQuery);

  subscribe = (listener: (state: ChatState) => void): (() => void) => {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  };

  async start(selfUserID: string): Promise<void> {
    if (!selfUserID) throw new Error("OpenIM user identity is required");
    this.selfUserID = selfUserID;
    this.unsubscribePort?.();
    this.unsubscribePort = this.port.subscribe({
      conversationsChanged: (items) => this.mergeConversationEvents(items),
      totalUnreadChanged: (count) => this.update({ totalUnread: count }),
      messagesReceived: (items) => void this.receive(items),
      uploadProgress: (clientMsgID, progress) => this.applyUploadProgress(clientMsgID, progress),
      groupMembersChanged: (groupID) => this.handleGroupMembersChanged(groupID),
      groupUnavailable: (groupID) => this.clearUnavailableGroup(groupID)
    });
    await this.restore();
  }

  stop(): void {
    this.unsubscribePort?.();
    this.unsubscribePort = null;
  }

  async restore(): Promise<void> {
    this.update({ restoring: true, error: null });
    try {
      const [conversations, totalUnread] = await Promise.all([this.port.listConversations(), this.port.totalUnread()]);
      const active = this.state.activeConversationID;
      const priorActive = this.state.conversations.find((item) => item.conversationID === active);
      const available = conversations.filter((item) => !item.groupID || !this.unavailableGroupIDs.has(item.groupID));
      this.update({ conversations: priorActive ? upsertConversations([priorActive], available) : supportedConversations(available), totalUnread });
      if (active) {
        await this.loadHistory(active, false);
        await this.markRead(active);
      }
      this.update({ restoring: false });
    } catch (error) {
      this.fail(error, { restoring: false });
      throw error;
    }
  }

  async openDirect(userID: string): Promise<void> {
    const target = userID.trim();
    if (!target || target === this.selfUserID) throw new Error("a different OpenIM user ID is required");
    try {
      const conversation = await this.port.oneConversation(target, SessionType.Single);
      this.update({ conversations: upsertConversations(this.state.conversations, [conversation]) });
      await this.select(conversation.conversationID);
    } catch (error) {
      this.fail(error);
      throw error;
    }
  }

  async select(conversationID: string, loadExistingHistory = true): Promise<void> {
    const conversation = this.state.conversations.find((item) => item.conversationID === conversationID);
    if (!conversation || (conversation.conversationType !== SessionType.Single && conversation.conversationType !== SessionType.Group)) {
      throw new Error("conversation is unavailable");
    }
    this.groupMemberRequest += 1;
    this.update({ activeConversationID: conversationID, messages: [], uploadProgressByClientMsgID: {}, historyEnded: !loadExistingHistory, groupMembers: [], loadingGroupMembers: false, error: null });
    if (loadExistingHistory) await this.loadHistory(conversationID, false);
    await this.markRead(conversationID);
    if (conversation.conversationType === SessionType.Group) await this.loadGroupMembers(conversation.groupID);
  }

  setConversationQuery(query: string): void {
    this.update({ conversationQuery: query });
  }

  async setPinned(conversationID: string, isPinned: boolean): Promise<void> {
    await this.updateConversationSetting(conversationID, "pin", { isPinned });
  }

  async setMuted(conversationID: string, isMuted: boolean): Promise<void> {
    const conversation = this.state.conversations.find((item) => item.conversationID === conversationID);
    if (conversation?.recvMsgOpt === MessageReceiveOptType.NotReceive) {
      const error = new Error("当前会话处于不接收消息状态，不能在此切换免打扰");
      this.fail(error);
      throw error;
    }
    await this.updateConversationSetting(conversationID, "mute", {
      recvMsgOpt: isMuted ? MessageReceiveOptType.NotNotify : MessageReceiveOptType.Normal
    });
  }

  async createGroup(name: string, memberUserIDs: string[]): Promise<void> {
    const groupName = name.trim();
    const members = [...new Set(memberUserIDs.map((value) => value.trim()).filter((value) => value && value !== this.selfUserID))];
    if (!groupName) throw new Error("group name is required");
    if (groupName.length > 60) throw new Error("group name exceeds 60 characters");
    if (members.length < 1) throw new Error("at least one other OpenIM user is required");
    try {
      const conversation = await this.port.createGroup(groupName, members);
      this.unavailableGroupIDs.delete(conversation.groupID);
      this.update({ conversations: upsertConversations(this.state.conversations, [conversation]), error: null });
      await this.select(conversation.conversationID, false);
    } catch (error) {
      this.fail(error);
      throw error;
    }
  }

  async refreshGroupMembers(): Promise<void> {
    const conversation = this.activeConversation();
    if (!conversation || conversation.conversationType !== SessionType.Group) return;
    await this.loadGroupMembers(conversation.groupID);
  }

  async inviteGroupMembers(userIDs: string[]): Promise<void> {
    const group = this.activeGroup();
    if (!group) this.reject("邀请成员前请选择群聊");
    if (!canInviteGroupMembers(this.state.groupMembers, this.selfUserID)) this.reject("当前角色不能邀请群成员");
    const existing = new Set(this.state.groupMembers.map((member) => member.userID));
    const candidates = [...new Set(userIDs.map((userID) => userID.trim()).filter((userID) => userID && userID !== this.selfUserID && !existing.has(userID)))];
    if (candidates.length === 0) this.reject("请选择尚未入群的成员");
    await this.runGroupAction({ groupID: group.groupID, kind: "invite" }, async () => {
      await this.port.inviteGroupMembers(group.groupID, candidates);
      await this.loadGroupMembers(group.groupID);
    });
  }

  async removeGroupMember(userID: string): Promise<void> {
    const group = this.activeGroup();
    if (!group) this.reject("移除成员前请选择群聊");
    if (!canRemoveGroupMember(this.state.groupMembers, this.selfUserID, userID)) this.reject("当前角色不能移除该成员");
    await this.runGroupAction({ groupID: group.groupID, kind: "remove", userID }, async () => {
      await this.port.removeGroupMember(group.groupID, userID);
      await this.loadGroupMembers(group.groupID);
    });
  }

  async leaveActiveGroup(): Promise<void> {
    const group = this.activeGroup();
    if (!group) this.reject("退出前请选择群聊");
    const self = this.state.groupMembers.find((member) => member.userID === this.selfUserID);
    if (!self) this.reject("当前用户不在群成员列表中");
    if (self.roleLevel === GroupMemberRole.Owner) this.reject("群主不能退出群聊，请解散群聊");
    await this.runGroupAction({ groupID: group.groupID, kind: "leave" }, async () => {
      await this.port.leaveGroup(group.groupID);
      this.clearUnavailableGroup(group.groupID);
    });
  }

  async dismissActiveGroup(): Promise<void> {
    const group = this.activeGroup();
    if (!group) this.reject("解散前请选择群聊");
    const self = this.state.groupMembers.find((member) => member.userID === this.selfUserID);
    if (self?.roleLevel !== GroupMemberRole.Owner) this.reject("只有群主可以解散群聊");
    await this.runGroupAction({ groupID: group.groupID, kind: "dismiss" }, async () => {
      await this.port.dismissGroup(group.groupID);
      this.clearUnavailableGroup(group.groupID);
    });
  }

  async loadOlder(): Promise<void> {
    const active = this.state.activeConversationID;
    if (!active || this.state.historyEnded || this.state.loadingHistory) return;
    await this.loadHistory(active, true);
  }

  async sendText(text: string): Promise<void> {
    const content = text.trim();
    const conversation = this.activeConversation();
    if (!conversation) throw new Error("select a conversation before sending");
    if (!content) throw new Error("message text is required");
    if (content.length > 6000) throw new Error("message text exceeds 6000 characters");

    let draft: MessageItem;
    try {
      draft = await this.port.createText(content);
    } catch (error) {
      this.fail(error);
      throw error;
    }
    await this.sendDraft(conversation, draft, false);
  }

  async sendImage(file: File): Promise<void> {
    const conversation = this.activeConversation();
    if (!conversation) throw new Error("发送图片前请选择会话");
    let draft: MessageItem;
    try {
      validateImage(file);
      draft = await this.port.createImage(file);
    } catch (error) {
      this.fail(error);
      throw error;
    }
    await this.sendDraft(conversation, draft, true);
  }

  async sendFile(file: File): Promise<void> {
    const conversation = this.activeConversation();
    if (!conversation) throw new Error("发送文件前请选择会话");
    let draft: MessageItem;
    try {
      validateFile(file);
      draft = await this.port.createFile(file);
    } catch (error) {
      this.fail(error);
      throw error;
    }
    await this.sendDraft(conversation, draft, true);
  }

  clearError(): void {
    this.update({ error: null });
  }

  private async loadHistory(conversationID: string, older: boolean): Promise<void> {
    this.update({ loadingHistory: true, error: null });
    const start = older ? this.state.messages[0]?.clientMsgID ?? "" : "";
    try {
      const result = await this.port.history(conversationID, start);
      if (this.state.activeConversationID !== conversationID) return;
      const incoming = supportedMessages(result.messageList);
      this.update({
        messages: older ? mergeMessages(incoming, this.state.messages) : mergeMessages([], incoming),
        uploadProgressByClientMsgID: older ? this.state.uploadProgressByClientMsgID : {},
        historyEnded: result.isEnd,
        loadingHistory: false
      });
    } catch (error) {
      this.fail(error, { loadingHistory: false });
      throw error;
    }
  }

  private async receive(items: MessageItem[]): Promise<void> {
    const active = this.activeConversation();
    const incoming = supportedMessages(items).filter((message) => {
      if (!active) return false;
      if (active.conversationType === SessionType.Group) return message.groupID === active.groupID;
      return (message.sendID === active.userID && message.recvID === this.selfUserID) ||
        (message.sendID === this.selfUserID && message.recvID === active.userID);
    });
    if (incoming.length === 0 || !active) return;
    this.update({ messages: mergeMessages(this.state.messages, incoming) });
    if (incoming.some((message) => message.sendID !== this.selfUserID)) await this.markRead(active.conversationID);
  }

  private async loadGroupMembers(groupID: string): Promise<void> {
    const request = ++this.groupMemberRequest;
    this.update({ loadingGroupMembers: true, error: null });
    try {
      const members = await this.port.groupMembers(groupID);
      if (request !== this.groupMemberRequest) return;
      const active = this.activeConversation();
      if (!active || active.groupID !== groupID) return;
      this.update({ groupMembers: members, loadingGroupMembers: false });
    } catch (error) {
      if (request !== this.groupMemberRequest) return;
      this.fail(new Error(`加载群成员失败：${errorMessage(error)}`), { loadingGroupMembers: false });
      throw error;
    }
  }

  private async markRead(conversationID: string): Promise<void> {
    try {
      await this.port.markRead(conversationID);
      const totalUnread = await this.port.totalUnread();
      this.update({
        conversations: this.state.conversations.map((item) => item.conversationID === conversationID ? { ...item, unreadCount: 0 } : item),
        totalUnread
      });
    } catch (error) {
      this.fail(new Error(`标记已读失败：${errorMessage(error)}`));
      throw error;
    }
  }

  private activeConversation(): ConversationItem | null {
    return this.state.conversations.find((item) => item.conversationID === this.state.activeConversationID) ?? null;
  }

  private activeGroup(): ConversationItem | null {
    const active = this.activeConversation();
    return active?.conversationType === SessionType.Group ? active : null;
  }

  private async runGroupAction(
    action: { groupID: string; kind: "invite" | "remove" | "leave" | "dismiss"; userID?: string },
    operation: () => Promise<void>
  ): Promise<void> {
    if (this.state.groupAction) {
      const error = new Error("群聊操作正在执行");
      this.fail(error);
      throw error;
    }
    this.update({ groupAction: action, error: null });
    try {
      await operation();
    } catch (error) {
      this.fail(error);
      throw error;
    } finally {
      this.update({ groupAction: null });
    }
  }

  private handleGroupMembersChanged(groupID: string): void {
    const active = this.activeGroup();
    if (!active || active.groupID !== groupID) return;
    void this.loadGroupMembers(groupID).catch(() => undefined);
  }

  private mergeConversationEvents(items: ConversationItem[]): void {
    const available = items.filter((item) => !item.groupID || !this.unavailableGroupIDs.has(item.groupID));
    this.update({ conversations: upsertConversations(this.state.conversations, available) });
  }

  private clearUnavailableGroup(groupID: string): void {
    this.unavailableGroupIDs.add(groupID);
    const active = this.activeGroup();
    const clearsActive = active?.groupID === groupID;
    if (clearsActive) this.groupMemberRequest += 1;
    this.update({
      conversations: this.state.conversations.filter((item) => item.groupID !== groupID),
      ...(clearsActive ? {
        activeConversationID: null,
        messages: [],
        groupMembers: [],
        loadingGroupMembers: false,
        uploadProgressByClientMsgID: {},
        historyEnded: true
      } : {})
    });
  }

  private async updateConversationSetting(
    conversationID: string,
    action: "pin" | "mute",
    patch: { isPinned?: boolean; recvMsgOpt?: MessageReceiveOptType }
  ): Promise<void> {
    const conversation = this.state.conversations.find((item) => item.conversationID === conversationID);
    if (!conversation) {
      const error = new Error("会话不可用");
      this.fail(error);
      throw error;
    }
    if (this.state.conversationActionByID[conversationID]) {
      const error = new Error("该会话设置正在更新");
      this.fail(error);
      throw error;
    }
    this.update({
      conversationActionByID: { ...this.state.conversationActionByID, [conversationID]: action },
      error: null
    });
    try {
      await this.port.setConversation(conversationID, patch);
      const current = this.state.conversations.find((item) => item.conversationID === conversationID) ?? conversation;
      this.update({ conversations: upsertConversations(this.state.conversations, [{ ...current, ...patch }]) });
    } catch (error) {
      this.fail(error);
      throw error;
    } finally {
      const next = { ...this.state.conversationActionByID };
      delete next[conversationID];
      this.update({ conversationActionByID: next });
    }
  }

  private async sendDraft(conversation: ConversationItem, message: MessageItem, tracksUpload: boolean): Promise<void> {
    const draft = { ...message, status: MessageStatus.Sending };
    const progress = tracksUpload
      ? { ...this.state.uploadProgressByClientMsgID, [draft.clientMsgID]: 0 }
      : this.state.uploadProgressByClientMsgID;
    this.update({ messages: mergeMessages(this.state.messages, [draft]), uploadProgressByClientMsgID: progress, error: null });
    try {
      const sent = await this.port.send(conversation, draft);
      const nextProgress = { ...this.state.uploadProgressByClientMsgID };
      delete nextProgress[draft.clientMsgID];
      this.update({
        messages: mergeMessages(this.state.messages, [{ ...sent, status: MessageStatus.Succeed }]),
        uploadProgressByClientMsgID: nextProgress
      });
    } catch (error) {
      this.update({
        messages: mergeMessages(this.state.messages, [{ ...draft, status: MessageStatus.Failed }]),
        error: `发送失败：${errorMessage(error)}`
      });
      throw error;
    }
  }

  private applyUploadProgress(clientMsgID: string, progress: number): void {
    if (!Number.isFinite(progress)) return;
    const message = this.state.messages.find((item) => item.clientMsgID === clientMsgID);
    if (!message || message.status !== MessageStatus.Sending ||
      (message.contentType !== MessageType.PictureMessage && message.contentType !== MessageType.FileMessage)) return;
    this.update({
      uploadProgressByClientMsgID: {
        ...this.state.uploadProgressByClientMsgID,
        [clientMsgID]: Math.max(0, Math.min(100, Math.round(progress)))
      }
    });
  }

  private fail(error: unknown, patch: Partial<ChatState> = {}): void {
    this.update({ ...patch, error: errorMessage(error) });
  }

  private reject(message: string): never {
    const error = new Error(message);
    this.fail(error);
    throw error;
  }

  private update(patch: Partial<ChatState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

export function createOpenIMChatPort(): ChatPort {
  const sdk = getSDK();
  const localPreviewURLs = new Map<string, string>();
  return {
    listConversations: async () => (await sdk.getConversationListSplit({ offset: 0, count: 200 })).data,
    setConversation: async (conversationID, patch) => { await sdk.setConversation({ conversationID, ...patch }); },
    totalUnread: async () => (await sdk.getTotalUnreadMsgCount()).data,
    oneConversation: async (sourceID, sessionType) => (await sdk.getOneConversation({ sourceID, sessionType })).data,
    history: async (conversationID, startClientMsgID) => {
      const { data } = await sdk.getAdvancedHistoryMessageList({
        conversationID,
        startClientMsgID,
        count: 30,
        viewType: ViewType.History
      });
      return data;
    },
    createText: async (text) => (await sdk.createTextMessage(text)).data,
    createImage: async (file) => {
      const { width, height } = await readImageDimensions(file);
      const previewURL = URL.createObjectURL(file);
      const base = {
        uuid: secureUUID(),
        type: file.type,
        size: file.size,
        width,
        height,
        url: previewURL
      };
      try {
        const message = (await sdk.createImageMessageByFile({
          sourcePicture: { ...base },
          bigPicture: { ...base },
          snapshotPicture: { ...base },
          sourcePath: "",
          file
        })).data;
        localPreviewURLs.set(message.clientMsgID, previewURL);
        return message;
      } catch (error) {
        URL.revokeObjectURL(previewURL);
        throw error;
      }
    },
    createFile: async (file) => (await sdk.createFileMessageByFile({
      filePath: "",
      fileName: file.name,
      uuid: secureUUID(),
      sourceUrl: "",
      fileSize: file.size,
      fileType: file.type || "application/octet-stream",
      file
    })).data,
    send: async (conversation, message) => {
      const sent = (await sdk.sendMessage({
        recvID: conversation.conversationType === SessionType.Single ? conversation.userID : "",
        groupID: conversation.conversationType === SessionType.Group ? conversation.groupID : "",
        message
      })).data;
      const previewURL = localPreviewURLs.get(message.clientMsgID);
      if (previewURL) {
        URL.revokeObjectURL(previewURL);
        localPreviewURLs.delete(message.clientMsgID);
      }
      return sent;
    },
    createGroup: async (name, memberUserIDs) => {
      let expectedGroupID = "";
      const observed = new Map<string, ConversationItem>();
      let resolveReady: ((conversation: ConversationItem) => void) | null = null;
      const ready = new Promise<ConversationItem>((resolve) => { resolveReady = resolve; });
      const observe = ({ data }: WSEvent<ConversationItem[]>) => {
        for (const conversation of data) {
          if (conversation.conversationType !== SessionType.Group) continue;
          observed.set(conversation.groupID, conversation);
          if (conversation.groupID === expectedGroupID) resolveReady?.(conversation);
        }
      };
      sdk.on(CbEvents.OnNewConversation, observe);
      sdk.on(CbEvents.OnConversationChanged, observe);
      let timeoutID = 0;
      try {
        const group = (await sdk.createGroup({
          groupInfo: { groupName: name, groupType: GroupType.WorkingGroup },
          memberUserIDs,
          adminUserIDs: []
        })).data;
        expectedGroupID = group.groupID;
        const alreadyObserved = observed.get(expectedGroupID);
        if (alreadyObserved) return alreadyObserved;
        return await Promise.race([
          ready,
          new Promise<never>((_, reject) => {
            timeoutID = window.setTimeout(() => reject(new Error("OpenIM group conversation synchronization timed out")), 15_000);
          })
        ]);
      } finally {
        window.clearTimeout(timeoutID);
        sdk.off(CbEvents.OnNewConversation, observe);
        sdk.off(CbEvents.OnConversationChanged, observe);
      }
    },
    groupMembers: async (groupID) => (await sdk.getGroupMemberList({
      groupID,
      filter: GroupMemberFilter.All,
      offset: 0,
      count: 200
    })).data,
    inviteGroupMembers: async (groupID, userIDs) => { await sdk.inviteUserToGroup({ groupID, reason: "", userIDList: userIDs }); },
    removeGroupMember: async (groupID, userID) => { await sdk.kickGroupMember({ groupID, reason: "", userIDList: [userID] }); },
    leaveGroup: async (groupID) => { await sdk.quitGroup(groupID); },
    dismissGroup: async (groupID) => { await sdk.dismissGroup(groupID); },
    markRead: async (conversationID) => { await sdk.markConversationMessageAsRead(conversationID); },
    subscribe: (events) => {
      const conversationsChanged = ({ data }: WSEvent<ConversationItem[]>) => events.conversationsChanged(data);
      const newConversation = ({ data }: WSEvent<ConversationItem[]>) => events.conversationsChanged(data);
      const totalUnreadChanged = ({ data }: WSEvent<number>) => events.totalUnreadChanged(data);
      const messagesReceived = ({ data }: WSEvent<MessageItem[]>) => events.messagesReceived(data);
      const uploadProgress = ({ data }: WSEvent<{ progress: number; clientMsgID: string }>) => events.uploadProgress(data.clientMsgID, data.progress);
      const groupMemberChanged = ({ data }: WSEvent<GroupMemberItem>) => events.groupMembersChanged(data.groupID);
      const groupUnavailable = ({ data }: WSEvent<GroupItem>) => events.groupUnavailable(data.groupID);
      sdk.on(CbEvents.OnConversationChanged, conversationsChanged);
      sdk.on(CbEvents.OnNewConversation, newConversation);
      sdk.on(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChanged);
      sdk.on(CbEvents.OnRecvNewMessages, messagesReceived);
      sdk.on(CbEvents.OnProgress, uploadProgress);
      sdk.on(CbEvents.OnGroupMemberAdded, groupMemberChanged);
      sdk.on(CbEvents.OnGroupMemberDeleted, groupMemberChanged);
      sdk.on(CbEvents.OnGroupMemberInfoChanged, groupMemberChanged);
      sdk.on(CbEvents.OnJoinedGroupDeleted, groupUnavailable);
      sdk.on(CbEvents.OnGroupDismissed, groupUnavailable);
      return () => {
        sdk.off(CbEvents.OnConversationChanged, conversationsChanged);
        sdk.off(CbEvents.OnNewConversation, newConversation);
        sdk.off(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChanged);
        sdk.off(CbEvents.OnRecvNewMessages, messagesReceived);
        sdk.off(CbEvents.OnProgress, uploadProgress);
        sdk.off(CbEvents.OnGroupMemberAdded, groupMemberChanged);
        sdk.off(CbEvents.OnGroupMemberDeleted, groupMemberChanged);
        sdk.off(CbEvents.OnGroupMemberInfoChanged, groupMemberChanged);
        sdk.off(CbEvents.OnJoinedGroupDeleted, groupUnavailable);
        sdk.off(CbEvents.OnGroupDismissed, groupUnavailable);
        for (const previewURL of localPreviewURLs.values()) URL.revokeObjectURL(previewURL);
        localPreviewURLs.clear();
      };
    }
  };
}

function secureUUID(): string {
  if (!globalThis.crypto?.randomUUID) throw new Error("浏览器不支持安全的媒体标识生成");
  return globalThis.crypto.randomUUID();
}

function readImageDimensions(file: File): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const source = URL.createObjectURL(file);
    const image = new Image();
    const release = () => URL.revokeObjectURL(source);
    image.onload = () => {
      const width = image.naturalWidth;
      const height = image.naturalHeight;
      release();
      if (width <= 0 || height <= 0) reject(new Error("无法读取图片尺寸"));
      else resolve({ width, height });
    };
    image.onerror = () => {
      release();
      reject(new Error("图片无法解码"));
    };
    image.src = source;
  });
}
