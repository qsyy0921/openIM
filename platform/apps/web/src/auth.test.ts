import { describe, expect, it } from "vitest";

import { createUserManagerSettings } from "./auth";
import type { WebConfig } from "./config";

const config: WebConfig = {
  oidcAuthority: "https://identity.example.test/realms/platform",
  oidcClientID: "platform-api",
  oidcRedirectURI: "https://workspace.example.test/auth/callback",
  oidcPostLogoutRedirectURI: "https://workspace.example.test/",
  platformAPIBaseURL: "/platform-api",
  deviceID: "browser-device",
  openIMAPIURL: "https://workspace.example.test/openim-api",
  openIMWSURL: "wss://workspace.example.test/openim-msggateway"
};

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, value); }
  };
}

describe("createUserManagerSettings", () => {
  it("defers bounded refresh-token renewal until identity restoration", () => {
    const settings = createUserManagerSettings(config, memoryStorage());

    expect(settings.response_type).toBe("code");
    expect(settings.automaticSilentRenew).toBe(false);
    expect(settings.validateSubOnSilentRenew).toBe(true);
    expect(settings.includeIdTokenInSilentRenew).toBe(true);
    expect(settings.accessTokenExpiringNotificationTimeInSeconds).toBe(60);
    expect(settings.maxSilentRenewTimeoutRetries).toBe(1);
    expect(settings.monitorSession).toBe(false);
    expect(settings.silent_redirect_uri).toBeUndefined();
  });
});
