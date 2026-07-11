import { describe, expect, it } from "vitest";

import { loadConfig } from "./config";

const valid = {
  VITE_OIDC_AUTHORITY: "http://idp.test/realms/platform",
  VITE_OIDC_CLIENT_ID: "platform-api",
  VITE_OIDC_REDIRECT_URI: "http://127.0.0.1:3000/auth/callback",
  VITE_OIDC_POST_LOGOUT_REDIRECT_URI: "http://127.0.0.1:3000/",
  VITE_PLATFORM_API_BASE_URL: "/platform-api",
  VITE_DEVICE_ID: "local-browser",
  VITE_OPENIM_API_URL: "http://127.0.0.1:3000/openim-api",
  VITE_OPENIM_WS_URL: "ws://openim.test:10001"
};

describe("loadConfig", () => {
  it("loads explicit node endpoints", () => {
    expect(loadConfig(valid)).toMatchObject({
      oidcClientID: "platform-api",
      platformAPIBaseURL: "/platform-api",
      deviceID: "local-browser",
      openIMWSURL: "ws://openim.test:10001"
    });
  });

  it("fails closed when required identity configuration is absent", () => {
    expect(() => loadConfig({ ...valid, VITE_OIDC_AUTHORITY: "" })).toThrow(
      "required web configuration VITE_OIDC_AUTHORITY is missing"
    );
  });

  it("rejects unsafe endpoint schemes", () => {
    expect(() => loadConfig({ ...valid, VITE_OPENIM_WS_URL: "http://openim.test" })).toThrow(
      "unsupported scheme"
    );
  });
});
