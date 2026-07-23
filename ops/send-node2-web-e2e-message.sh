#!/usr/bin/env bash
set -euo pipefail

target_user_id="${1:-}"
content_base64="${2:-}"
deploy_root="${3:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$target_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || {
  echo "target OpenIM user ID is invalid" >&2
  exit 1
}
[[ -r "$openim_env" && -n "$content_base64" ]] || {
  echo "node2 OpenIM environment or message content is missing" >&2
  exit 1
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
content="$(CONTENT_BASE64="$content_base64" python3 - <<'PY'
import base64
import os

value = base64.b64decode(os.environ["CONTENT_BASE64"], validate=True).decode("utf-8")
if not 1 <= len(value) <= 6000:
    raise SystemExit("message content length is invalid")
print(value, end="")
PY
)"

admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

request_file="$(mktemp)"
trap 'rm -f "$request_file"' EXIT
TARGET_USER_ID="$target_user_id" CONTENT="$content" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": os.environ["TARGET_USER_ID"],
    "sendID": "imAdmin",
    "groupID": "",
    "senderNickname": "Node2 E2E Peer",
    "senderPlatformID": 5,
    "content": {"content": os.environ["CONTENT"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-web-single-chat-e2e",
}, separators=(",", ":"), ensure_ascii=False))
PY

send_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/msg/send_msg \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-send-$(date +%s%N)" \
  --data-binary "@$request_file")"
unset admin_token content
server_msg_id="$(RESPONSE="$send_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["serverMsgID"])')"
echo "node2_e2e_message=accepted"
echo "server_msg_id=$server_msg_id"
