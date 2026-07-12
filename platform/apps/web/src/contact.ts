import {
  CbEvents,
  getSDK,
  type FriendApplicationItem,
  type FriendUserItem,
  type PublicUserItem
} from "@openim/wasm-client-sdk";

export type ContactEvents = {
  changed: () => void;
};

export type ContactPort = {
  friends: () => Promise<FriendUserItem[]>;
  incomingApplications: () => Promise<FriendApplicationItem[]>;
  outgoingApplications: () => Promise<FriendApplicationItem[]>;
  lookupUser: (userID: string) => Promise<PublicUserItem | null>;
  addFriend: (userID: string, message: string) => Promise<void>;
  acceptApplication: (userID: string, message: string) => Promise<void>;
  rejectApplication: (userID: string, message: string) => Promise<void>;
  subscribe: (events: ContactEvents) => () => void;
};

export type ContactState = {
  friends: FriendUserItem[];
  incomingApplications: FriendApplicationItem[];
  outgoingApplications: FriendApplicationItem[];
  lookupResult: PublicUserItem | null;
  lookedUpUsers: PublicUserItem[];
  lookupAttempted: boolean;
  loading: boolean;
  refreshing: boolean;
  searching: boolean;
  pendingOperations: string[];
  error: string | null;
};

export const initialContactState: ContactState = {
  friends: [],
  incomingApplications: [],
  outgoingApplications: [],
  lookupResult: null,
  lookedUpUsers: [],
  lookupAttempted: false,
  loading: false,
  refreshing: false,
  searching: false,
  pendingOperations: [],
  error: null
};

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected contacts failure";
}

function uniqueByUserID<T extends { userID: string }>(items: T[]): T[] {
  const values = new Map<string, T>();
  for (const item of items) values.set(item.userID, item);
  return [...values.values()].sort((left, right) => {
    const leftName = "remark" in left && typeof left.remark === "string" && left.remark ? left.remark : "nickname" in left ? String(left.nickname) : left.userID;
    const rightName = "remark" in right && typeof right.remark === "string" && right.remark ? right.remark : "nickname" in right ? String(right.nickname) : right.userID;
    return leftName.localeCompare(rightName);
  });
}

function uniqueApplications(items: FriendApplicationItem[]): FriendApplicationItem[] {
  const values = new Map<string, FriendApplicationItem>();
  for (const item of items) values.set(`${item.fromUserID}\u0000${item.toUserID}`, item);
  return [...values.values()].sort((left, right) => right.createTime - left.createTime);
}

function normalizedUserID(value: string): string {
  const userID = value.trim();
  if (!userID) throw new Error("OpenIM 用户 ID 不能为空");
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(userID)) throw new Error("OpenIM 用户 ID 格式无效");
  return userID;
}

function normalizedMessage(value: string, defaultMessage: string): string {
  const message = value.trim() || defaultMessage;
  if (message.length > 200) throw new Error("申请说明不能超过 200 个字符");
  return message;
}

export class ContactController {
  private state: ContactState = initialContactState;
  private listeners = new Set<(state: ContactState) => void>();
  private detach: (() => void) | null = null;
  private selfUserID = "";
  private generation = 0;
  private lookupRequest = 0;
  private refreshPromise: Promise<void> | null = null;
  private refreshQueued = false;
  private pending = new Set<string>();

  constructor(private readonly port: ContactPort) {}

  getState(): ContactState {
    return this.state;
  }

  subscribe(listener: (state: ContactState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  async start(selfUserID: string): Promise<void> {
    this.stop();
    this.selfUserID = normalizedUserID(selfUserID);
    const generation = ++this.generation;
    this.update({ ...initialContactState, loading: true });
    this.detach = this.port.subscribe({ changed: () => this.refreshFromEvent() });
    try {
      await this.refresh(generation);
    } catch (error) {
      this.fail(error, { loading: false, refreshing: false });
      throw error;
    }
  }

  stop(): void {
    this.generation += 1;
    this.lookupRequest += 1;
    this.detach?.();
    this.detach = null;
    this.refreshPromise = null;
    this.refreshQueued = false;
    this.pending.clear();
    this.selfUserID = "";
  }

  async restore(): Promise<void> {
    if (!this.selfUserID) return;
    await this.refresh(this.generation);
  }

  async refresh(expectedGeneration = this.generation): Promise<void> {
    if (!this.selfUserID || expectedGeneration !== this.generation) return;
    if (this.refreshPromise) {
      this.refreshQueued = true;
      return this.refreshPromise;
    }
    const initial = this.state.loading;
    this.update({ refreshing: !initial, error: null });
    const refresh = this.refreshLoop(expectedGeneration);
    this.refreshPromise = refresh;
    try {
      await refresh;
    } finally {
      if (this.refreshPromise === refresh) this.refreshPromise = null;
    }
  }

  async lookup(value: string): Promise<PublicUserItem | null> {
    const request = ++this.lookupRequest;
    let userID: string;
    try {
      userID = normalizedUserID(value);
    } catch (error) {
      this.fail(error, { searching: false, lookupResult: null, lookupAttempted: true });
      throw error;
    }
    if (userID === this.selfUserID) {
      const error = new Error("不能搜索当前登录用户");
      this.fail(error, { searching: false, lookupResult: null, lookupAttempted: true });
      throw error;
    }
    const generation = this.generation;
    this.update({ searching: true, lookupResult: null, lookupAttempted: false, error: null });
    try {
      const result = await this.port.lookupUser(userID);
      if (request !== this.lookupRequest || generation !== this.generation) return null;
      if (!result || result.userID !== userID) {
        const error = new Error(`未找到 OpenIM 用户 ${userID}`);
        this.fail(error, { searching: false, lookupResult: null, lookupAttempted: true });
        throw error;
      }
      this.update({
        searching: false,
        lookupResult: result,
        lookedUpUsers: uniqueByUserID([...this.state.lookedUpUsers, result]),
        lookupAttempted: true
      });
      return result;
    } catch (error) {
      if (request !== this.lookupRequest || generation !== this.generation) return null;
      if (!this.state.error) this.fail(error, { searching: false, lookupResult: null, lookupAttempted: true });
      throw error;
    }
  }

  clearLookup(): void {
    this.lookupRequest += 1;
    this.update({ lookupResult: null, lookupAttempted: false, searching: false });
  }

  addFriend(userID: string, message: string): Promise<void> {
    const target = normalizedUserID(userID);
    if (target === this.selfUserID) return Promise.reject(new Error("不能添加当前登录用户"));
    return this.mutate(`add:${target}`, () => this.port.addFriend(target, normalizedMessage(message, "申请添加好友")));
  }

  acceptApplication(userID: string, message = ""): Promise<void> {
    const target = normalizedUserID(userID);
    return this.mutate(`accept:${target}`, () => this.port.acceptApplication(target, normalizedMessage(message, "已同意")));
  }

  rejectApplication(userID: string, message = ""): Promise<void> {
    const target = normalizedUserID(userID);
    return this.mutate(`reject:${target}`, () => this.port.rejectApplication(target, normalizedMessage(message, "已拒绝")));
  }

  clearError(): void {
    this.update({ error: null });
  }

  private async refreshLoop(expectedGeneration: number): Promise<void> {
    try {
      do {
        this.refreshQueued = false;
        const [friends, incomingApplications, outgoingApplications] = await Promise.all([
          this.port.friends(),
          this.port.incomingApplications(),
          this.port.outgoingApplications()
        ]);
        if (expectedGeneration !== this.generation) return;
        this.update({
          friends: uniqueByUserID(friends.filter((friend) => friend.userID !== this.selfUserID)),
          incomingApplications: uniqueApplications(incomingApplications),
          outgoingApplications: uniqueApplications(outgoingApplications),
          loading: false,
          refreshing: false,
          error: null
        });
      } while (this.refreshQueued && expectedGeneration === this.generation);
    } catch (error) {
      if (expectedGeneration === this.generation) this.fail(error, { loading: false, refreshing: false });
      throw error;
    }
  }

  private refreshFromEvent(): void {
    void this.refresh().catch(() => {
      // refresh() has already published the authoritative SDK failure to state.
    });
  }

  private async mutate(key: string, action: () => Promise<void>): Promise<void> {
    if (this.pending.has(key)) throw new Error("该操作正在处理中");
    const generation = this.generation;
    this.pending.add(key);
    this.publishPending(null);
    try {
      await action();
      await this.refresh();
    } catch (error) {
      if (generation === this.generation) this.fail(error);
      throw error;
    } finally {
      this.pending.delete(key);
      if (generation === this.generation) this.publishPending(this.state.error);
    }
  }

  private publishPending(error: string | null): void {
    this.update({ pendingOperations: [...this.pending].sort(), error });
  }

  private fail(error: unknown, patch: Partial<ContactState> = {}): void {
    this.update({ ...patch, error: errorMessage(error) });
  }

  private update(patch: Partial<ContactState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

export function createOpenIMContactPort(): ContactPort {
  const sdk = getSDK();
  return {
    friends: async () => (await sdk.getFriendList(true)).data,
    incomingApplications: async () => (await sdk.getFriendApplicationListAsRecipient()).data,
    outgoingApplications: async () => (await sdk.getFriendApplicationListAsApplicant()).data,
    lookupUser: async (userID) => {
      const users = (await sdk.getUsersInfo([userID])).data;
      return users.find((user) => user.userID === userID) ?? null;
    },
    addFriend: async (toUserID, reqMsg) => { await sdk.addFriend({ toUserID, reqMsg }); },
    acceptApplication: async (toUserID, handleMsg) => { await sdk.acceptFriendApplication({ toUserID, handleMsg }); },
    rejectApplication: async (toUserID, handleMsg) => { await sdk.refuseFriendApplication({ toUserID, handleMsg }); },
    subscribe: (events) => {
      const changed = () => events.changed();
      const eventNames = [
        CbEvents.OnFriendAdded,
        CbEvents.OnFriendDeleted,
        CbEvents.OnFriendInfoChanged,
        CbEvents.OnFriendApplicationAdded,
        CbEvents.OnFriendApplicationDeleted,
        CbEvents.OnFriendApplicationAccepted,
        CbEvents.OnFriendApplicationRejected
      ];
      for (const event of eventNames) sdk.on(event, changed);
      return () => {
        for (const event of eventNames) sdk.off(event, changed);
      };
    }
  };
}
