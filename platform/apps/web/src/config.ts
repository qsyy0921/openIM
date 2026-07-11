export type WebConfig = {
  oidcAuthority: string;
  oidcClientID: string;
  oidcRedirectURI: string;
  oidcPostLogoutRedirectURI: string;
  platformAPIBaseURL: string;
  deviceID: string;
  openIMAPIURL: string;
  openIMWSURL: string;
};

type Environment = Record<string, string | boolean | undefined>;

function required(env: Environment, key: string): string {
  const raw = env[key];
  const value = typeof raw === "string" ? raw.trim() : "";
  if (!value) {
    throw new Error(`required web configuration ${key} is missing`);
  }
  return value;
}

function absoluteURL(value: string, key: string, schemes: string[]): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${key} must be an absolute URL`);
  }
  if (!schemes.includes(parsed.protocol)) {
    throw new Error(`${key} uses unsupported scheme ${parsed.protocol}`);
  }
  return value.replace(/\/$/, "");
}

function APIBase(value: string): string {
  if (value.startsWith("/") && !value.startsWith("//")) {
    return value.replace(/\/$/, "");
  }
  return absoluteURL(value, "VITE_PLATFORM_API_BASE_URL", ["http:", "https:"]);
}

export function loadConfig(env: Environment): WebConfig {
  return {
    oidcAuthority: absoluteURL(required(env, "VITE_OIDC_AUTHORITY"), "VITE_OIDC_AUTHORITY", ["http:", "https:"]),
    oidcClientID: required(env, "VITE_OIDC_CLIENT_ID"),
    oidcRedirectURI: absoluteURL(required(env, "VITE_OIDC_REDIRECT_URI"), "VITE_OIDC_REDIRECT_URI", ["http:", "https:"]),
    oidcPostLogoutRedirectURI: absoluteURL(
      required(env, "VITE_OIDC_POST_LOGOUT_REDIRECT_URI"),
      "VITE_OIDC_POST_LOGOUT_REDIRECT_URI",
      ["http:", "https:"]
    ),
    platformAPIBaseURL: APIBase(required(env, "VITE_PLATFORM_API_BASE_URL")),
    deviceID: required(env, "VITE_DEVICE_ID"),
    openIMAPIURL: absoluteURL(required(env, "VITE_OPENIM_API_URL"), "VITE_OPENIM_API_URL", ["http:", "https:"]),
    openIMWSURL: absoluteURL(required(env, "VITE_OPENIM_WS_URL"), "VITE_OPENIM_WS_URL", ["ws:", "wss:"])
  };
}
