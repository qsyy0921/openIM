import type { User } from "oidc-client-ts";

export type EnterpriseSessionFailure =
  | "identity-invalid"
  | "identity-changed"
  | "identity-expired"
  | "identity-unloaded"
  | "renewal-unavailable"
  | "renewal-failed"
  | "session-clear-failed";

export class EnterpriseSessionError extends Error {
  readonly code: EnterpriseSessionFailure;

  constructor(code: EnterpriseSessionFailure) {
    super(code);
    this.name = "EnterpriseSessionError";
    this.code = code;
  }
}

export type EnterpriseIdentityPin = {
  subject: string;
  tenantID: string;
};

type UserLoadedCallback = (user: User) => Promise<void> | void;
type SilentRenewErrorCallback = (error: Error) => Promise<void> | void;
type AccessTokenCallback = () => Promise<void> | void;
type UserUnloadedCallback = () => Promise<void> | void;

export interface EnterpriseSessionEvents {
  addUserLoaded(callback: UserLoadedCallback): () => void;
  addSilentRenewError(callback: SilentRenewErrorCallback): () => void;
  addAccessTokenExpired(callback: AccessTokenCallback): () => void;
  addUserUnloaded(callback: UserUnloadedCallback): () => void;
}

export interface EnterpriseSessionManager {
  getUser(): Promise<User | null>;
  signinSilent(): Promise<User | null>;
  removeUser(): Promise<void>;
  startSilentRenew(): void;
  stopSilentRenew(): void;
  events: EnterpriseSessionEvents;
}

type EnterpriseSessionCallbacks = {
  onUserChanged(user: User): void;
  onTerminal(reason: EnterpriseSessionFailure): Promise<void> | void;
};

type Clock = () => number;

function requiredClaim(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

export function identityPin(user: User): EnterpriseIdentityPin {
  const subject = requiredClaim(user.profile.sub);
  const tenantID = requiredClaim(user.profile.tenant_id);
  if (!subject || !tenantID) {
    throw new EnterpriseSessionError("identity-invalid");
  }
  return { subject, tenantID };
}

function futureEpoch(value: unknown, nowSeconds: number): boolean {
  return typeof value === "number" && Number.isFinite(value) && value > nowSeconds;
}

export function assertCurrentEnterpriseUser(
  user: User,
  expected?: EnterpriseIdentityPin,
  now: Clock = Date.now
): EnterpriseIdentityPin {
  const pin = identityPin(user);
  if (expected && (pin.subject !== expected.subject || pin.tenantID !== expected.tenantID)) {
    throw new EnterpriseSessionError("identity-changed");
  }
  if (!user.id_token || !user.refresh_token) {
    throw new EnterpriseSessionError("renewal-unavailable");
  }
  const nowSeconds = Math.floor(now() / 1000);
  if (!futureEpoch(user.expires_at, nowSeconds) || !futureEpoch(user.profile.exp, nowSeconds)) {
    throw new EnterpriseSessionError("identity-expired");
  }
  return pin;
}

function assertRotatedIDToken(previous: User, renewed: User): void {
  if (
    renewed.id_token === previous.id_token ||
    typeof previous.profile.exp !== "number" ||
    typeof renewed.profile.exp !== "number" ||
    renewed.profile.exp <= previous.profile.exp
  ) {
    throw new EnterpriseSessionError("renewal-failed");
  }
}

async function clearUser(manager: EnterpriseSessionManager, failure: EnterpriseSessionFailure): Promise<never> {
  try {
    await manager.removeUser();
  } catch {
    throw new EnterpriseSessionError("session-clear-failed");
  }
  throw new EnterpriseSessionError(failure);
}

export async function admitCallbackIdentity(
  manager: EnterpriseSessionManager,
  user: User,
  now: Clock = Date.now
): Promise<User> {
  try {
    assertCurrentEnterpriseUser(user, undefined, now);
    return user;
  } catch (error) {
    const code = error instanceof EnterpriseSessionError ? error.code : "identity-invalid";
    return clearUser(manager, code);
  }
}

export async function restoreEnterpriseIdentity(
  manager: EnterpriseSessionManager,
  now: Clock = Date.now
): Promise<User | null> {
  const stored = await manager.getUser();
  if (!stored) return null;

  let pin: EnterpriseIdentityPin;
  try {
    pin = identityPin(stored);
  } catch (error) {
    const code = error instanceof EnterpriseSessionError ? error.code : "identity-invalid";
    return clearUser(manager, code);
  }
  if (!stored.refresh_token) {
    return clearUser(manager, "renewal-unavailable");
  }

  try {
    assertCurrentEnterpriseUser(stored, pin, now);
    return stored;
  } catch (error) {
    if (!(error instanceof EnterpriseSessionError) || error.code !== "identity-expired") {
      const code = error instanceof EnterpriseSessionError ? error.code : "identity-invalid";
      return clearUser(manager, code);
    }
  }

  let renewed: User | null;
  try {
    renewed = await manager.signinSilent();
  } catch {
    return clearUser(manager, "renewal-failed");
  }
  if (!renewed) {
    return clearUser(manager, "renewal-failed");
  }
  try {
    assertCurrentEnterpriseUser(renewed, pin, now);
    assertRotatedIDToken(stored, renewed);
    return renewed;
  } catch (error) {
    const code = error instanceof EnterpriseSessionError ? error.code : "identity-invalid";
    return clearUser(manager, code);
  }
}

export class EnterpriseSessionController {
  private readonly pin: EnterpriseIdentityPin;
  private current: User;
  private active = false;
  private terminal = false;
  private detach: Array<() => void> = [];

  constructor(
    private readonly manager: EnterpriseSessionManager,
    initialUser: User,
    private readonly callbacks: EnterpriseSessionCallbacks,
    private readonly now: Clock = Date.now
  ) {
    this.pin = assertCurrentEnterpriseUser(initialUser, undefined, now);
    this.current = initialUser;
  }

  start(): void {
    if (this.active) return;
    if (this.terminal) throw new EnterpriseSessionError("identity-unloaded");
    this.active = true;
    this.detach = [
      this.manager.events.addUserLoaded((user) => this.accept(user)),
      this.manager.events.addSilentRenewError(() => this.terminate("renewal-failed")),
      this.manager.events.addAccessTokenExpired(() => this.terminate("identity-expired")),
      this.manager.events.addUserUnloaded(() => this.terminate("identity-unloaded"))
    ];
    this.manager.startSilentRenew();
  }

  user(): User {
    if (!this.active || this.terminal) throw new EnterpriseSessionError("identity-unloaded");
    assertCurrentEnterpriseUser(this.current, this.pin, this.now);
    return this.current;
  }

  idToken(): string {
    const token = this.user().id_token;
    if (!token) throw new EnterpriseSessionError("renewal-unavailable");
    return token;
  }

  close(): void {
    if (!this.active) return;
    this.active = false;
    this.manager.stopSilentRenew();
    const callbacks = this.detach;
    this.detach = [];
    for (const callback of callbacks) callback();
  }

  private async accept(user: User): Promise<void> {
    if (!this.active || this.terminal) return;
    try {
      assertCurrentEnterpriseUser(user, this.pin, this.now);
      assertRotatedIDToken(this.current, user);
    } catch (error) {
      const code = error instanceof EnterpriseSessionError ? error.code : "identity-invalid";
      await this.terminate(code);
      return;
    }
    this.current = user;
    this.callbacks.onUserChanged(user);
  }

  private async terminate(reason: EnterpriseSessionFailure): Promise<void> {
    if (!this.active || this.terminal) return;
    this.terminal = true;
    this.close();
    let terminalReason = reason;
    try {
      await this.manager.removeUser();
    } catch {
      terminalReason = "session-clear-failed";
    }
    await this.callbacks.onTerminal(terminalReason);
  }
}
