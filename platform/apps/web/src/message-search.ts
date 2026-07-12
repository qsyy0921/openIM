import { getSDK, MessageType, type MessageItem, type SearchMessageResult } from "@openim/wasm-client-sdk";

export const MESSAGE_SEARCH_PAGE_SIZE = 20;

export type MessageSearchHit = {
  conversationID: string;
  conversationType: number;
  showName: string;
  message: MessageItem;
};

export type MessageSearchState = {
  query: string;
  submittedQuery: string;
  conversationID: string | null;
  page: number;
  totalCount: number;
  hits: MessageSearchHit[];
  loading: boolean;
  error: string | null;
};

export type MessageSearchPort = {
  search: (conversationID: string, query: string, page: number, count: number) => Promise<SearchMessageResult>;
};

export const initialMessageSearchState: MessageSearchState = {
  query: "",
  submittedQuery: "",
  conversationID: null,
  page: 1,
  totalCount: 0,
  hits: [],
  loading: false,
  error: null
};

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null) {
    const value = error as { errCode?: unknown; errMsg?: unknown };
    if (typeof value.errMsg === "string") return typeof value.errCode === "number" ? `${value.errCode}: ${value.errMsg}` : value.errMsg;
  }
  return "OpenIM message search failed";
}

function flatten(result: SearchMessageResult, conversationID: string): MessageSearchHit[] {
  const group = result.searchResultItems?.find((item) => item.conversationID === conversationID);
  if (!group) return [];
  return group.messageList
    .filter((message) => message.contentType === MessageType.TextMessage || message.contentType === MessageType.FileMessage)
    .map((message) => ({
      conversationID: group.conversationID,
      conversationType: group.conversationType,
      showName: group.showName,
      message
    }));
}

export class MessageSearchController {
  private state: MessageSearchState = initialMessageSearchState;
  private listeners = new Set<(state: MessageSearchState) => void>();
  private requestSequence = 0;

  constructor(private readonly port: MessageSearchPort) {}

  getState = (): MessageSearchState => this.state;

  subscribe = (listener: (state: MessageSearchState) => void): (() => void) => {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  };

  setQuery(query: string): void {
    this.update({ query, error: null });
  }

  async search(conversationID: string, query = this.state.query, page = 1): Promise<void> {
    const scope = conversationID.trim();
    const keyword = query.trim();
    if (!scope) return this.reject("搜索前请选择会话");
    if (!keyword) return this.reject("请输入搜索关键词");
    if (keyword.length > 100) return this.reject("搜索关键词不能超过 100 个字符");
    if (!Number.isInteger(page) || page < 1) return this.reject("搜索页码无效");
    const request = ++this.requestSequence;
    this.update({ query, submittedQuery: keyword, conversationID: scope, page, loading: true, error: null });
    try {
      const result = await this.port.search(scope, keyword, page, MESSAGE_SEARCH_PAGE_SIZE);
      if (request !== this.requestSequence) return;
      this.update({ hits: flatten(result, scope), totalCount: result.totalCount, loading: false });
    } catch (error) {
      if (request !== this.requestSequence) return;
      this.update({ hits: [], totalCount: 0, loading: false, error: errorMessage(error) });
      throw error;
    }
  }

  async nextPage(): Promise<void> {
    if (this.state.loading || !this.state.conversationID || !this.state.submittedQuery || this.state.page * MESSAGE_SEARCH_PAGE_SIZE >= this.state.totalCount) return;
    await this.search(this.state.conversationID, this.state.submittedQuery, this.state.page + 1);
  }

  async previousPage(): Promise<void> {
    if (this.state.loading || !this.state.conversationID || !this.state.submittedQuery || this.state.page <= 1) return;
    await this.search(this.state.conversationID, this.state.submittedQuery, this.state.page - 1);
  }

  close(): void {
    this.requestSequence += 1;
    this.state = initialMessageSearchState;
    this.publish();
  }

  private reject(message: string): never {
    const error = new Error(message);
    this.update({ loading: false, error: message });
    throw error;
  }

  private update(patch: Partial<MessageSearchState>): void {
    this.state = { ...this.state, ...patch };
    this.publish();
  }

  private publish(): void {
    for (const listener of this.listeners) listener(this.state);
  }
}

export function createOpenIMMessageSearchPort(): MessageSearchPort {
  const sdk = getSDK();
  return {
    search: async (conversationID, query, page, count) => (await sdk.searchLocalMessages({
      conversationID,
      keywordList: [query],
      keywordListMatchType: 0,
      senderUserIDList: [],
      messageTypeList: [MessageType.TextMessage, MessageType.FileMessage],
      searchTimePosition: 0,
      searchTimePeriod: 0,
      pageIndex: page,
      count
    })).data
  };
}
