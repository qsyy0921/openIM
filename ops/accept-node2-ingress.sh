#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"
postgres_container=openim-platform-local-postgres-1

[[ -r "$openim_env" ]] || {
  echo "OpenIM environment is not readable: $openim_env" >&2
  exit 1
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
sender="$(docker exec "$postgres_container" psql -At -U platform -d platform -c \
  "select openim_user_id from identity.identity_links where member_id='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' and provisioning_state='ready'")"
[[ -n "$secret" && -n "$sender" ]] || {
  echo "OpenIM secret or provisioned sender is missing" >&2
  exit 1
}

admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-ingress-admin-$(date +%s)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c \
  'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"

run_id="node2-migration-$(date +%s%N)"
request_file="$(mktemp)"
trap 'rm -f "$request_file"' EXIT
SENDER="$sender" RUN_ID="$run_id" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Local Member",
    "senderPlatformID": 5,
    "content": {"content": "@Agent node2 ingress migration acceptance " + os.environ["RUN_ID"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-migration-acceptance:" + os.environ["RUN_ID"],
}, separators=(",", ":")))
PY

send_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/msg/send_msg \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: $run_id" \
  --data-binary "@$request_file")"
server_msg_id="$(RESPONSE="$send_response" python3 -c \
  'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p.get("errMsg"); print(p["data"]["serverMsgID"])')"
unset admin_token admin_response secret send_response

row=""
for _ in $(seq 1 40); do
  row="$(docker exec "$postgres_container" psql -At -F '|' -U platform -d platform -c \
    "select i.event_id::text,o.state from integration.ingress_messages i join integration.outbox_events o on o.event_id=i.event_id where i.server_msg_id='$server_msg_id'")"
  if [[ -n "$row" ]] && grep -q '|published$' <<<"$row"; then
    break
  fi
  sleep 2
done

[[ -n "$row" ]] || {
  echo "message was not persisted by platform ingress" >&2
  exit 1
}
grep -q '|published$' <<<"$row" || {
  echo "ingress Outbox event was not published" >&2
  exit 1
}
echo 'openim_message_send=accepted'
echo 'platform_ingress=accepted'
echo 'platform_outbox=published'
