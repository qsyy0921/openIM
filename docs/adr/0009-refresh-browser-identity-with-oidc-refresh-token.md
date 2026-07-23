# ADR-0009: Refresh browser identity with the OIDC refresh token

- Status: Accepted
- Date: 2026-07-23

## Context

The Web client uses Authorization Code with PKCE and stores the `oidc-client-ts` user in `sessionStorage`, but it disables automatic silent renewal. It also captures the initial ID Token in long-lived device, channel, and Agent controller closures. The local Keycloak realm uses its default short access-token lifetime, and the real Node2 browser therefore leaves the workspace after roughly five minutes even while the Keycloak SSO session remains valid.

OpenIM User Tokens and the enterprise OIDC session have separate authorities and lifecycles. Reissuing or reconnecting the OpenIM session for every enterprise Token rotation would interrupt a healthy IM connection and couple two independent credentials. Continuing to call Platform API with the captured ID Token would instead use an expired credential.

## Decision

Use the refresh token returned by the existing public-client Authorization Code + PKCE flow as the only non-interactive renewal mechanism.

1. The application starts the `oidc-client-ts` automatic silent-renew service only after startup restoration has completed and lifecycle listeners are installed, preventing two refreshes from racing on an expired stored user. The service permits one bounded timeout retry. No `silent_redirect_uri` is configured, so a missing refresh token cannot silently switch to iframe authentication.
2. A browser workspace is admitted only when the OIDC user has a non-expired access token, a current ID Token, a refresh token, a non-empty subject, and a non-empty `tenant_id` claim.
3. The initial `(sub, tenant_id)` pair is pinned for the lifetime of the mounted workspace. Every renewed user must retain both values and provide a different ID Token whose `exp` advances beyond the previous Token.
4. Long-lived Platform API adapters obtain the ID Token from a validated in-memory session accessor at request time. They do not capture the initial Token.
5. Successful enterprise renewal updates the accessor and UI identity without reconnecting a healthy OpenIM session. If OpenIM independently expires, its existing explicit retry path may request a new IM session using the current enterprise ID Token.
6. Missing renewal material, a failed automatic renewal after its bounded retry, an expired Token, an unloaded user, or an identity mismatch closes the workspace, disconnects OpenIM, clears the stored OIDC user, and requires interactive login. No alternate issuer, grant, iframe path, cached Token, or anonymous mode is used.

## Consequences

- A normal short Token rotation no longer destroys conversation or SDK synchronization state.
- Every Platform API request after rotation uses the renewed ID Token while tenant/member authorization remains server-derived.
- The browser continues to keep OIDC user and transaction state only in `sessionStorage`; OpenIM session material remains in process memory.
- A provider that omits a refresh token or a current ID Token cannot support this workspace and fails closed.
- Refresh-token rotation, revocation, reuse, and SSO maximum lifetime remain Keycloak concerns; the client only consumes the standards-based result.
- Device enrollment, organization authorization, cross-tab synchronization, and background service-worker authentication remain separate slices.
