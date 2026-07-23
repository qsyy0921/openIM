import type { TelegramLinkChallenge, TelegramLinkStatus } from "./telegram-link-api";

export type TelegramLinkState = {
  status: "idle" | TelegramLinkStatus["state"];
  expiresAt: string | null;
  challenge: TelegramLinkChallenge | null;
  loading: boolean;
  issuing: boolean;
  error: string | null;
};

export const initialTelegramLinkState: TelegramLinkState = {
  status: "idle",
  expiresAt: null,
  challenge: null,
  loading: false,
  issuing: false,
  error: null
};

export type TelegramLinkDataPort = {
  status: () => Promise<TelegramLinkStatus>;
  issue: () => Promise<TelegramLinkChallenge>;
};

type TimerHandle = ReturnType<typeof setTimeout>;
type TimerPort = {
  set: (callback: () => void, delay: number) => TimerHandle;
  clear: (handle: TimerHandle) => void;
};

const defaultTimers: TimerPort = {
  set: (callback, delay) => setTimeout(callback, delay),
  clear: (handle) => clearTimeout(handle)
};

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "Telegram 连接操作失败";
}

export class TelegramLinkController {
  private state = initialTelegramLinkState;
  private listeners = new Set<(state: TelegramLinkState) => void>();
  private requestVersion = 0;
  private timer: TimerHandle | null = null;
  private closed = false;

  constructor(
    private readonly data: TelegramLinkDataPort,
    private readonly timers: TimerPort = defaultTimers,
    private readonly now: () => number = Date.now
  ) {}

  subscribe(listener: (state: TelegramLinkState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  getState(): TelegramLinkState { return this.state; }

  async start(): Promise<void> {
    this.closed = false;
    await this.refresh();
  }

  close(): void {
    this.closed = true;
    this.requestVersion += 1;
    this.clearTimer();
    this.listeners.clear();
  }

  async refresh(): Promise<void> {
    const version = ++this.requestVersion;
    this.clearTimer();
    this.update({ loading: true, error: null });
    try {
      const status = await this.data.status();
      if (this.closed || version !== this.requestVersion) return;
      const expiresAt = status.state === "pending" ? status.expires_at ?? null : null;
      const challenge = status.state === "pending" && this.state.challenge?.expires_at === expiresAt ? this.state.challenge : null;
      this.update({ status: status.state, expiresAt, challenge, loading: false });
      this.schedule(status);
    } catch (error) {
      if (this.closed || version !== this.requestVersion) return;
      this.update({ loading: false, error: messageOf(error) });
      throw error;
    }
  }

  async issue(): Promise<void> {
    if (this.state.issuing) throw new Error("Telegram 绑定码正在生成");
    const version = ++this.requestVersion;
    this.clearTimer();
    this.update({ issuing: true, error: null, challenge: null });
    try {
      const challenge = await this.data.issue();
      if (this.closed || version !== this.requestVersion) return;
      this.update({ status: "pending", expiresAt: challenge.expires_at, challenge, issuing: false, loading: false });
      this.schedule(challenge);
    } catch (error) {
      if (this.closed || version !== this.requestVersion) return;
      this.update({ issuing: false, error: messageOf(error) });
      throw error;
    }
  }

  clearError(): void { this.update({ error: null }); }

  private schedule(status: TelegramLinkStatus): void {
    if (this.closed || status.state !== "pending" || !status.expires_at) return;
    const remaining = Date.parse(status.expires_at) - this.now();
    const delay = remaining <= 0 ? 250 : Math.min(2_000, remaining + 50);
    this.timer = this.timers.set(() => {
      this.timer = null;
      void this.refresh().catch(() => undefined);
    }, delay);
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      this.timers.clear(this.timer);
      this.timer = null;
    }
  }

  private update(patch: Partial<TelegramLinkState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}
