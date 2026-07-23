import { User } from "oidc-client-ts";
import { describe, expect, it, vi } from "vitest";

import {
  EnterpriseSessionController,
  EnterpriseSessionError,
  type EnterpriseSessionEvents,
  type EnterpriseSessionManager,
  restoreEnterpriseIdentity
} from "./enterprise-session";

const now = Date.UTC(2026, 6, 23, 12, 0, 0);
const nowSeconds = Math.floor(now / 1000);

function user(overrides: {
  token?: string;
  refreshToken?: string;
  subject?: string;
  tenantID?: string;
  accessExpiry?: number;
  idExpiry?: number;
} = {}): User {
  return new User({
    id_token: overrides.token ?? "id-token-current",
    access_token: `access-${overrides.token ?? "current"}`,
    refresh_token: overrides.refreshToken === undefined ? "refresh-current" : overrides.refreshToken || undefined,
    token_type: "Bearer",
    expires_at: overrides.accessExpiry ?? nowSeconds + 300,
    profile: {
      iss: "https://identity.example.test/realms/platform",
      aud: "platform-api",
      iat: nowSeconds - 30,
      sub: overrides.subject ?? "subject-1",
      tenant_id: overrides.tenantID ?? "tenant-1",
      exp: overrides.idExpiry ?? nowSeconds + 300
    }
  });
}

class FakeEvents implements EnterpriseSessionEvents {
  readonly loaded = new Set<(value: User) => Promise<void> | void>();
  readonly renewalErrors = new Set<(error: Error) => Promise<void> | void>();
  readonly expired = new Set<() => Promise<void> | void>();
  readonly unloaded = new Set<() => Promise<void> | void>();

  addUserLoaded(callback: (value: User) => Promise<void> | void): () => void {
    this.loaded.add(callback);
    return () => this.loaded.delete(callback);
  }
  addSilentRenewError(callback: (error: Error) => Promise<void> | void): () => void {
    this.renewalErrors.add(callback);
    return () => this.renewalErrors.delete(callback);
  }
  addAccessTokenExpired(callback: () => Promise<void> | void): () => void {
    this.expired.add(callback);
    return () => this.expired.delete(callback);
  }
  addUserUnloaded(callback: () => Promise<void> | void): () => void {
    this.unloaded.add(callback);
    return () => this.unloaded.delete(callback);
  }

  async emitLoaded(value: User): Promise<void> {
    await Promise.all([...this.loaded].map((callback) => callback(value)));
  }
  async emitRenewalError(): Promise<void> {
    await Promise.all([...this.renewalErrors].map((callback) => callback(new Error("provider details must stay private"))));
  }
  async emitExpired(): Promise<void> {
    await Promise.all([...this.expired].map((callback) => callback()));
  }
}

function manager(stored: User | null, renewed: User | null = null) {
  const events = new FakeEvents();
  const value: EnterpriseSessionManager = {
    events,
    getUser: vi.fn(async () => stored),
    signinSilent: vi.fn(async () => renewed),
    removeUser: vi.fn(async () => undefined),
    startSilentRenew: vi.fn(),
    stopSilentRenew: vi.fn()
  };
  return { value, events };
}

describe("restoreEnterpriseIdentity", () => {
  it("returns a current renewable identity without a refresh request", async () => {
    const current = user();
    const fixture = manager(current);

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).resolves.toBe(current);
    expect(fixture.value.signinSilent).not.toHaveBeenCalled();
  });

  it("refreshes one expired stored identity and pins subject and tenant", async () => {
    const expired = user({ accessExpiry: nowSeconds - 1, idExpiry: nowSeconds - 1 });
    const renewed = user({ token: "id-token-renewed", idExpiry: nowSeconds + 600 });
    const fixture = manager(expired, renewed);

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).resolves.toBe(renewed);
    expect(fixture.value.signinSilent).toHaveBeenCalledOnce();
    expect(fixture.value.removeUser).not.toHaveBeenCalled();
  });

  it("clears a user without renewal material", async () => {
    const fixture = manager(user({ refreshToken: "" }));

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "renewal-unavailable" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });

  it("clears a stored identity without the required tenant claim", async () => {
    const invalid = user();
    invalid.profile.tenant_id = "";
    const fixture = manager(invalid);

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "identity-invalid" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
    expect(fixture.value.signinSilent).not.toHaveBeenCalled();
  });

  it("fails closed when renewal returns no user", async () => {
    const fixture = manager(user({ accessExpiry: nowSeconds - 1, idExpiry: nowSeconds - 1 }), null);

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "renewal-failed" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });

  it("rejects a renewed identity with an expired ID Token", async () => {
    const fixture = manager(
      user({ accessExpiry: nowSeconds - 1, idExpiry: nowSeconds - 1 }),
      user({ token: "stale-id-token", idExpiry: nowSeconds - 1 })
    );

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "identity-expired" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });

  it("rejects a refresh response that preserves the previous ID Token", async () => {
    const fixture = manager(
      user({ accessExpiry: nowSeconds - 1, idExpiry: nowSeconds - 1 }),
      user({ token: "id-token-current", idExpiry: nowSeconds + 600 })
    );

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "renewal-failed" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });

  it.each([
    ["subject", user({ subject: "subject-2" })],
    ["tenant", user({ tenantID: "tenant-2" })]
  ])("rejects a renewed %s mismatch", async (_name, renewed) => {
    const fixture = manager(user({ accessExpiry: nowSeconds - 1, idExpiry: nowSeconds - 1 }), renewed);

    await expect(restoreEnterpriseIdentity(fixture.value, () => now)).rejects.toMatchObject({ code: "identity-changed" });
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });
});

describe("EnterpriseSessionController", () => {
  it("rotates the current ID Token without terminating the session", async () => {
    const fixture = manager(user());
    const changed = vi.fn();
    const terminal = vi.fn();
    const controller = new EnterpriseSessionController(
      fixture.value,
      user(),
      { onUserChanged: changed, onTerminal: terminal },
      () => now
    );
    controller.start();

    expect(fixture.value.startSilentRenew).toHaveBeenCalledOnce();

    await fixture.events.emitLoaded(user({ token: "id-token-rotated", idExpiry: nowSeconds + 600 }));

    expect(controller.idToken()).toBe("id-token-rotated");
    expect(changed).toHaveBeenCalledOnce();
    expect(terminal).not.toHaveBeenCalled();
  });

  it("admits one terminal transition for duplicate failure events", async () => {
    const fixture = manager(user());
    const terminal = vi.fn();
    const controller = new EnterpriseSessionController(
      fixture.value,
      user(),
      { onUserChanged: vi.fn(), onTerminal: terminal },
      () => now
    );
    controller.start();

    await Promise.all([fixture.events.emitRenewalError(), fixture.events.emitExpired()]);

    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
    expect(terminal).toHaveBeenCalledOnce();
    expect(terminal).toHaveBeenCalledWith("renewal-failed");
    expect(() => controller.idToken()).toThrow(EnterpriseSessionError);
  });

  it("terminates when a refresh changes the pinned identity", async () => {
    const fixture = manager(user());
    const terminal = vi.fn();
    const controller = new EnterpriseSessionController(
      fixture.value,
      user(),
      { onUserChanged: vi.fn(), onTerminal: terminal },
      () => now
    );
    controller.start();

    await fixture.events.emitLoaded(user({ tenantID: "tenant-2" }));

    expect(terminal).toHaveBeenCalledWith("identity-changed");
    expect(fixture.value.removeUser).toHaveBeenCalledOnce();
  });

  it("removes every listener and ignores stale callbacks after close", async () => {
    const fixture = manager(user());
    const changed = vi.fn();
    const terminal = vi.fn();
    const controller = new EnterpriseSessionController(
      fixture.value,
      user(),
      { onUserChanged: changed, onTerminal: terminal },
      () => now
    );
    controller.start();
    controller.close();

    expect(fixture.events.loaded.size).toBe(0);
    expect(fixture.events.renewalErrors.size).toBe(0);
    expect(fixture.events.expired.size).toBe(0);
    expect(fixture.events.unloaded.size).toBe(0);
    expect(fixture.value.stopSilentRenew).toHaveBeenCalledOnce();
    await fixture.events.emitLoaded(user({ token: "stale-token" }));
    await fixture.events.emitExpired();
    expect(changed).not.toHaveBeenCalled();
    expect(terminal).not.toHaveBeenCalled();
  });
});
