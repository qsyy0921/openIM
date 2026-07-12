---
unit: im-client-media
status: verified
depends_on:
  - web-client
  - im-client-foundation
  - im-client-contacts
  - openim-adapter
---

# IM Client Media Messages

## Scope

Add the first media-message slice to the verified Web IM client: send and receive image and file messages in existing single and working-group conversations, expose real SDK upload progress and failure, render synchronized history and real-time delivery, open an image preview, and provide a file download/open action.

The slice uses only the pinned `@openim/wasm-client-sdk@3.8.3-patch.13` message/upload path and the existing OpenIM third RPC plus MinIO deployment. It does not add a Platform API upload endpoint, browser object store, media proxy, alternate transport, voice/video messages, clipboard paste, drag/drop, bulk upload, cloud drive, document editing, cancellation, or resumable UI.

## Responsibilities and non-goals

`ConversationController` owns media validation, optimistic message state, progress keyed by `clientMsgID`, explicit failure state, history/realtime merging, and cleanup through the existing SDK subscription lifecycle. The OpenIM port owns creation of SDK image/file messages and the one authoritative `sendMessage` call. `ChatWorkspace` owns file-input controls, image/file presentation, preview state, and accessible open/download commands.

OpenIM owns message IDs, upload execution, MinIO object URLs, persistence, synchronization, and authorization. The Web client does not upload directly with `fetch`, synthesize progress, copy media facts into PostgreSQL, or replace failed SDK results with local success.

## Contracts and dependencies

The pinned SDK contract is:

- `createImageMessageByFile(...)` registers the browser `File` in the WASM file map and creates a `PictureMessage` draft;
- `createFileMessageByFile(...)` registers the browser `File` and creates a `FileMessage` draft;
- `sendMessage(...)` performs the real upload through OpenIM third RPC before accepting the message;
- `CbEvents.OnProgress` emits `{ clientMsgID, progress }` for the exact sending message; in the pinned Web implementation the callback is completion-granular for the normal upload path, so the client must not fabricate smoother intermediate percentages;
- `pictureElem.sourcePicture`, `bigPicture`, and `snapshotPicture` contain synchronized image metadata/URLs;
- `fileElem.sourceUrl`, `fileName`, `fileSize`, and `fileType` contain synchronized file metadata;
- existing history and receive APIs return text, picture, and file messages in one ordered stream.

Node2 OpenIM uses `object.enable: minio`; no service, schema, token, or public API contract changes are required.

## Product policy

- Accepted images: JPEG, PNG, GIF, and WebP with a nonzero size up to 20 MiB.
- Accepted files: any nonzero file up to 100 MiB with a nonempty name of at most 255 characters.
- SVG is not admitted as an image because active content and external references make inline image treatment unsafe; it may be sent only as a normal file.
- One media item is sent per user action. Multi-select and batching are later product decisions.
- The current slice retains a failed media message and explicit error; cancel and retry controls are not claimed.

## Invariants

- One selected conversation is required and determines `recvID` versus `groupID` exactly as text send already does.
- A media message enters the timeline as `sending`, becomes `succeeded` only from the SDK result, and becomes `failed` on any create/upload/send error.
- Progress is clamped to 0-100 and accepted only for a currently known sending message with the exact `clientMsgID`.
- History and real-time events accept only Text, Picture, and File message types and remain deduplicated by `clientMsgID`.
- Image dimensions come from browser decoding before SDK creation; invalid or undecodable image input fails before upload.
- Remote render/open URLs must use HTTP or HTTPS. Local optimistic image preview may use a browser `blob:` URL that is revoked after success or controller teardown.
- The client never logs file bytes, object URLs containing credentials, tokens, or message payloads.
- Upload failure never switches to REST upload, embeds bytes into the message, or reports success.

## Runtime flow

1. The user selects an existing single or group conversation and chooses the image or file command.
2. The controller validates type, size, name, and selected conversation.
3. Image creation decodes dimensions, creates one temporary local preview URL, and calls `createImageMessageByFile`; file creation calls `createFileMessageByFile`.
4. The SDK draft is merged into the timeline with `sending` status and progress zero.
5. `sendMessage` reads the browser file through the WASM file map and uploads through OpenIM third RPC to MinIO.
6. `OnProgress` updates the exact draft without changing its order or identity.
7. The authoritative SDK result replaces the draft and supplies the remote object URL; create/upload/send failure leaves a failed item and visible error.
8. OpenIM message callbacks, history reload, and reconnect restore text/image/file messages through the existing synchronization path.
9. The UI renders a bounded image thumbnail or file row; image preview and file open/download use only validated remote URLs.

## Data ownership and state

OpenIM Server owns message and object metadata; MinIO owns bytes; the SDK local database owns synchronized message history. React state adds only `uploadProgressByClientMsgID` and the currently previewed image. Native `File` objects and temporary `blob:` URLs remain process-local and are never serialized to application storage.

## Failure handling

No active conversation, unsupported image type, empty/oversized input, image decode failure, SDK create failure, upload failure, send rejection, missing media element, and unsafe/missing remote URL are explicit non-success states. A failed draft remains distinguishable from a synchronized message. Reload relies on OpenIM facts rather than preserving browser `File` objects.

## Security

The browser receives no Admin Token or MinIO credential. Upload authorization remains inside the OpenIM user-token path. Filenames render as React text; URLs are parsed and restricted by protocol before use. Image elements use object URLs only for the selected local file and HTTP(S) for synchronized data. File content is never interpreted as HTML.

## Observability

The UI exposes validation, current SDK-reported upload percentage, sending, succeeded, and failed states. The optimistic draft starts at zero; the pinned SDK normally advances the callback at upload completion rather than emitting browser byte-stream increments. Existing OpenIM operation IDs, third-service logs, and MinIO/server metrics remain authoritative. The client does not fabricate throughput or completion metrics.

## Acceptance criteria

- Single and group conversations send one real image and one real file through the pinned SDK path.
- The timeline displays real upload progress events associated with the correct draft ID and never applies stale/unknown progress.
- Image/file messages render from history and real-time callbacks without duplicate entries.
- Image preview and file open/download work with the synchronized HTTP(S) object URL.
- Validation rejects unsupported, empty, oversized, and undecodable inputs before SDK send.
- SDK create/upload/send failure remains explicit and does not generate local success or another upload path.
- Unit tests cover validation, message filtering, progress scoping/clamping, image/file success, failure, and subscription cleanup.
- Node2 E2E performs real Web uploads, reuses their real MinIO URLs for an `imAdmin` inbound image/file message, verifies single/group presentation and reload, and preserves existing Agent/contact/text regression.
- Desktop and mobile screenshots show bounded media, progress/file controls, preview, and composer controls without overlap or horizontal overflow.

## Planned follow-up slices

1. Explicit media retry/cancel semantics after SDK support and product behavior are agreed.
2. Conversation pin, mute, drafts, and message search.
3. Group administration and member mutation.
4. Voice/video message and calling evaluation as separate slices.

## Open questions

- Production media retention, antivirus scanning, DLP, and download audit policy require an enterprise file-governance design.
- Whether large-file upload should expose cancellation or resume requires an accepted SDK contract rather than a second uploader.

## Source evidence

- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/sdk/index.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/entity.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/types/eventData.d.ts`
- `platform/apps/web/node_modules/@openim/wasm-client-sdk/lib/index.es.js`
- `openim-sdk-core/internal/conversation_msg/api.go`
- `openim-sdk-core/internal/conversation_msg/progress.go`
- `openim-sdk-core/internal/third/file/file_js.go`
- `open-im-server/pkg/apistruct/msg.go`
- `open-im-server/config/openim-rpc-third.yml`
- `platform/apps/web/src/chat.ts`
- `platform/apps/web/src/ChatWorkspace.tsx`
- `platform/apps/web/e2e/node2-foundation.spec.ts`
- `ops/send-node2-web-e2e-media.ps1`
- `ops/send-node2-web-e2e-media.sh`

## Verification evidence

- `npm run test` passed 38 Vitest tests across seven files. The 17 `ConversationController` tests include image/file history filtering, validation-before-create, exact progress scoping and clamping, single/group routing, explicit failed media, and subscription cleanup.
- `npm run typecheck` and `npm run build` passed with `@openim/wasm-client-sdk@3.8.3-patch.13` unchanged.
- `npm run test:e2e:node2` passed both serial real-runtime scenarios in 42.1 seconds. The foundation scenario rejected an undecodable PNG before message creation; uploaded a real image and file from Web to `imAdmin`; downloaded and byte-checked the real file URL; reused the resulting HTTP(S) MinIO URLs in real `imAdmin` picture/file messages; created a real group and uploaded a group image and file; then verified synchronized history after reload.
- The same E2E preserved PKCE login, `/v1/im/session`, single/group text, contacts, group cleanup, reconnect, unread/read state, and the Agent cited-answer plus approved idempotent-action regression. It observed no HTTP failures, no unexpected console errors, no localStorage session/token entries, and no horizontal overflow.
- Desktop `1280x720` and mobile `390x844` media timelines and image previews were visually inspected. Images remain bounded, file names truncate inside stable rows, attachment controls stay inside the composer, and preview controls do not overlap the media.
- `python ops/validate-repository.py`, strict SDD validation, `git diff --check`, and the final source/secret/fallback audit passed.
