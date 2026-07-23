import { UserManager, WebStorageStateStore, type UserManagerSettings } from "oidc-client-ts";

import type { WebConfig } from "./config";

export function createUserManagerSettings(config: WebConfig, sessionStorage: Storage): UserManagerSettings {
  return {
    authority: config.oidcAuthority,
    client_id: config.oidcClientID,
    redirect_uri: config.oidcRedirectURI,
    post_logout_redirect_uri: config.oidcPostLogoutRedirectURI,
    response_type: "code",
    scope: "openid profile email",
    userStore: new WebStorageStateStore({ store: sessionStorage }),
    stateStore: new WebStorageStateStore({ store: sessionStorage }),
    automaticSilentRenew: false,
    validateSubOnSilentRenew: true,
    includeIdTokenInSilentRenew: true,
    accessTokenExpiringNotificationTimeInSeconds: 60,
    maxSilentRenewTimeoutRetries: 1,
    monitorSession: false
  };
}

export function createUserManager(config: WebConfig): UserManager {
  return new UserManager(createUserManagerSettings(config, window.sessionStorage));
}
