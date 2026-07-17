#!/usr/bin/env bash
set -euo pipefail

action="${1:-}"
web_user_id="${2:-}"
deploy_root="${3:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$action" == "accept" || "$action" == "reset" ]] || {
  echo "friend E2E action must be accept or reset" >&2
  exit 1
}
[[ "$web_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || {
  echo "Web OpenIM user ID is invalid" >&2
  exit 1
}
[[ -r "$openim_env" ]] || {
  echo "node2 OpenIM environment is missing" >&2
  exit 1
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-friend-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["token"])')"
unset admin_response secret

respond() {
  curl -fsS --max-time 15 -X POST \
    http://127.0.0.1:12002/friend/add_friend_response \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: node2-web-e2e-friend-accept-$(date +%s%N)" \
    --data "{\"fromUserID\":\"$web_user_id\",\"toUserID\":\"imAdmin\",\"handleResult\":1,\"handleMsg\":\"Node2 E2E accepted\"}"
}

if [[ "$action" == "accept" ]]; then
  response="$(respond)"
  RESPONSE="$response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'
  echo "node2_e2e_friend=accepted"
  echo "web_user_id=$web_user_id"
  exit 0
fi

# Normalize any pending request first. A nonzero result means there was no
# unprocessed request; import/delete below still establishes the final state.
respond >/dev/null 2>&1 || true

import_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/friend/import_friend \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-friend-import-$(date +%s%N)" \
  --data "{\"ownerUserID\":\"$web_user_id\",\"friendUserIDs\":[\"imAdmin\"]}")"
RESPONSE="$import_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'

delete_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/friend/delete_friend \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-friend-delete-$(date +%s%N)" \
  --data "{\"ownerUserID\":\"$web_user_id\",\"friendUserID\":\"imAdmin\"}")"
unset admin_token
RESPONSE="$delete_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'
echo "node2_e2e_friend=reset"
echo "web_user_id=$web_user_id"
