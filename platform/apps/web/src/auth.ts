import { UserManager, WebStorageStateStore } from "oidc-client-ts";

import type { WebConfig } from "./config";

export function createUserManager(config: WebConfig): UserManager {
  return new UserManager({
    authority: config.oidcAuthority,
    client_id: config.oidcClientID,
    redirect_uri: config.oidcRedirectURI,
    post_logout_redirect_uri: config.oidcPostLogoutRedirectURI,
    response_type: "code",
    scope: "openid profile email",
    userStore: new WebStorageStateStore({ store: window.sessionStorage }),
    stateStore: new WebStorageStateStore({ store: window.sessionStorage }),
    automaticSilentRenew: false,
    monitorSession: false
  });
}
