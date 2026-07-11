import {
  CbEvents,
  getSDK,
  MessageStatus,
  MessageType,
  SessionType,
  ViewType,
  type ConversationItem,
  type MessageItem,
  type WSEvent
} from "@openim/wasm-client-sdk";

export type ChatState = {
  conversations: ConversationItem[];
  totalUnread: number;
  activeConversationID: string | null;
  messages: MessageItem[];
  historyEnded: boolean;
  loadingHistory: boolean;
  restoring: boolean;
  error: string | null;
};

export type ChatEvents = {
  conversationsChanged: (items: ConversationItem[]) => void;
  totalUnreadChanged: (count: number) => void;
  messagesReceived: (items: MessageItem[]) => void;
};

export type ChatPort = {
  listConversations: () => Promise<ConversationItem[]>;
  totalUnread: () => Promise<number>;
  oneConversation: (userID: string) => Promise<ConversationItem>;
  history: (conversationID: string, startClientMsgID: string) => Promise<{ isEnd: boolean; messageList: MessageItem[] }>;
  createText: (text: string) => Promise<MessageItem>;
  send: (receiverID: string, message: MessageItem) => Promise<MessageItem>;
  markRead: (conversationID: string) => Promise<void>;
  subscribe: (events: ChatEvents) => () => void;
};

export const initialChatState: ChatState = {
  conversations: [],
  totalUnread: 0,
  activeConversationID: null,
  messages: [],
  historyEnded: true,
  loadingHistory: false,
  restoring: false,
  error: null
};

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null) {
    const value = error as { errCode?: unknown; errMsg?: unknown };
    if (typeof value.errMsg === "string") return typeof value.errCode === "number" ? `${value.errCode}: ${value.errMsg}` : value.errMsg;
  }
  return "OpenIM operation failed";
}

function singleConversations(items: ConversationItem[]): ConversationItem[] {
  return items
    .filter((item) => item.conversationType === SessionType.Single)
    .sort((left, right) => Number(right.isPinned) - Number(left.isPinned) || right.latestMsgSendTime - left.latestMsgSendTime);
}

function textMessages(items: MessageItem[]): MessageItem[] {
  return items.filter((item) => item.sessionType === SessionType.Single && item.contentType === MessageType.TextMessage);
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
  for (const item of singleConversations(incoming)) merged.set(item.conversationID, item);
  return singleConversations([...merged.values()]);
}

export class SingleChatController {
  private state: ChatState = initialChatState;
  private listeners = new Set<(state: ChatState) => void>();
  private unsubscribePort: (() => void) | null = null;
  private selfUserID = "";

  constructor(private readonly port: ChatPort) {}

  getState = (): ChatState => this.state;

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
      conversationsChanged: (items) => this.update({ conversations: upsertConversations(this.state.conversations, items) }),
      totalUnreadChanged: (count) => this.update({ totalUnread: count }),
      messagesReceived: (items) => void this.receive(items)
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
      this.update({ conversations: priorActive ? upsertConversations([priorActive], conversations) : singleConversations(conversations), totalUnread });
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
      const conversation = await this.port.oneConversation(target);
      this.update({ conversations: upsertConversations(this.state.conversations, [conversation]) });
      await this.select(conversation.conversationID);
    } catch (error) {
      this.fail(error);
      throw error;
    }
  }

  async select(conversationID: string): Promise<void> {
    const conversation = this.state.conversations.find((item) => item.conversationID === conversationID);
    if (!conversation || conversation.conversationType !== SessionType.Single) throw new Error("single conversation is unavailable");
    this.update({ activeConversationID: conversationID, messages: [], historyEnded: false, error: null });
    await this.loadHistory(conversationID, false);
    await this.markRead(conversationID);
  }

  async loadOlder(): Promise<void> {
    const active = this.state.activeConversationID;
    if (!active || this.state.historyEnded || this.state.loadingHistory) return;
    await this.loadHistory(active, true);
  }

  async sendText(text: string): Promise<void> {
    const content = text.trim();
    const conversation = this.activeConversation();
    if (!conversation) throw new Error("select a single conversation before sending");
    if (!content) throw new Error("message text is required");
    if (content.length > 6000) throw new Error("message text exceeds 6000 characters");

    let draft: MessageItem;
    try {
      draft = await this.port.createText(content);
    } catch (error) {
      this.fail(error);
      throw error;
    }
    draft = { ...draft, status: MessageStatus.Sending };
    this.update({ messages: mergeMessages(this.state.messages, [draft]), error: null });
    try {
      const sent = await this.port.send(conversation.userID, draft);
      this.update({ messages: mergeMessages(this.state.messages, [{ ...sent, status: MessageStatus.Succeed }]) });
    } catch (error) {
      this.update({
        messages: mergeMessages(this.state.messages, [{ ...draft, status: MessageStatus.Failed }]),
        error: `发送失败：${errorMessage(error)}`
      });
      throw error;
    }
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
      const incoming = textMessages(result.messageList);
      this.update({
        messages: older ? mergeMessages(incoming, this.state.messages) : mergeMessages([], incoming),
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
    const incoming = textMessages(items).filter((message) => {
      if (!active) return false;
      return (message.sendID === active.userID && message.recvID === this.selfUserID) ||
        (message.sendID === this.selfUserID && message.recvID === active.userID);
    });
    if (incoming.length === 0 || !active) return;
    this.update({ messages: mergeMessages(this.state.messages, incoming) });
    if (incoming.some((message) => message.sendID === active.userID)) await this.markRead(active.conversationID);
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

  private fail(error: unknown, patch: Partial<ChatState> = {}): void {
    this.update({ ...patch, error: errorMessage(error) });
  }

  private update(patch: Partial<ChatState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

export function createOpenIMChatPort(): ChatPort {
  const sdk = getSDK();
  return {
    listConversations: async () => (await sdk.getConversationListSplit({ offset: 0, count: 200 })).data,
    totalUnread: async () => (await sdk.getTotalUnreadMsgCount()).data,
    oneConversation: async (userID) => (await sdk.getOneConversation({ sourceID: userID, sessionType: SessionType.Single })).data,
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
    send: async (receiverID, message) => (await sdk.sendMessage({ recvID: receiverID, groupID: "", message })).data,
    markRead: async (conversationID) => { await sdk.markConversationMessageAsRead(conversationID); },
    subscribe: (events) => {
      const conversationsChanged = ({ data }: WSEvent<ConversationItem[]>) => events.conversationsChanged(data);
      const newConversation = ({ data }: WSEvent<ConversationItem[]>) => events.conversationsChanged(data);
      const totalUnreadChanged = ({ data }: WSEvent<number>) => events.totalUnreadChanged(data);
      const messagesReceived = ({ data }: WSEvent<MessageItem[]>) => events.messagesReceived(data);
      sdk.on(CbEvents.OnConversationChanged, conversationsChanged);
      sdk.on(CbEvents.OnNewConversation, newConversation);
      sdk.on(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChanged);
      sdk.on(CbEvents.OnRecvNewMessages, messagesReceived);
      return () => {
        sdk.off(CbEvents.OnConversationChanged, conversationsChanged);
        sdk.off(CbEvents.OnNewConversation, newConversation);
        sdk.off(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChanged);
        sdk.off(CbEvents.OnRecvNewMessages, messagesReceived);
      };
    }
  };
}
