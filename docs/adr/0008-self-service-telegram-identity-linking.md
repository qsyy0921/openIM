# ADR-0008: Link Telegram identities with member-issued one-time challenges

- Status: Accepted
- Date: 2026-07-23

## Context

Telegram ingress already requires an explicit enterprise member/principal/chat binding, but the only provisioning path is a root-operated host command that accepts raw Telegram and enterprise identifiers. That path is suitable for isolated operations acceptance, not for a real member product. Accepting Telegram identifiers from the browser would let an authenticated member claim an identity that the browser cannot prove. Treating the first Telegram message as implicit enrollment would bypass enterprise authentication.

## Decision

Use a two-channel proof ceremony:

1. an OIDC-authenticated member on an active enrolled device requests a short-lived one-time challenge from Platform API;
2. the member sends that challenge in a private chat with the Telegram Bot;
3. Telegram ingress obtains the user/chat identity from the signed Telegram update and atomically consumes the challenge into the existing binding tables.

Challenge plaintext is returned once and stored only as a SHA-256 digest. A new challenge revokes the previous active challenge. Existing bindings cannot be transferred or overwritten. Link commands are control-plane input and are intercepted before Agent ingress, retrieval, Memory, Tool, or model execution. V1 provides no direct Telegram acknowledgement; the authenticated Web client reads binding status.

The root host-admin binding command remains an explicit operations tool for isolated recovery and acceptance, not a production fallback and not a route reachable from the Web product.

## Consequences

- The browser proves enterprise identity while Telegram proves channel identity; neither side can assert the other side's identifiers.
- PostgreSQL needs durable challenge and audit records, but no OpenIM or Telegram message fact is duplicated.
- The poller gains a small control-command path before normal binding resolution.
- Account transfer, unlinking, group binding, deep links, and Bot acknowledgements require separate decisions.
- Real acceptance can be completed without root inserting the binding, while existing Agent channel semantics remain unchanged.
