#!/usr/bin/env bash
set -euo pipefail

action="${1:-}"
group_id="${2:-}"
old_owner_user_id="${3:-}"
peer_user_id="${4:-lifecyclePeer}"
deploy_root="${5:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$action" == "ensure-peer" || "$action" == "transfer-owner" ]] || { echo "lifecycle action is invalid" >&2; exit 1; }
[[ "$peer_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || { echo "peer user ID is invalid" >&2; exit 1; }
if [[ "$action" == "transfer-owner" ]]; then
  [[ "$group_id" =~ ^[A-Za-z0-9_-]{1,128}$ && "$old_owner_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || {
    echo "group or owner user ID is invalid" >&2
    exit 1
  }
fi
[[ -r "$openim_env" ]] || { echo "node2 OpenIM environment is missing" >&2; exit 1; }

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-lifecycle-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["token"])')"
unset admin_response secret

if [[ "$action" == "ensure-peer" ]]; then
  check_file="$(mktemp)"
  register_file="$(mktemp)"
  trap 'rm -f "$check_file" "$register_file"' EXIT
  PEER_USER_ID="$peer_user_id" python3 - <<'PY' >"$check_file"
import json
import os
print(json.dumps({"checkUserIDs": [os.environ["PEER_USER_ID"]]}, separators=(",", ":")))
PY
  check_response="$(curl -fsS --max-time 10 -X POST \
    http://127.0.0.1:12002/user/account_check \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: node2-web-e2e-lifecycle-check-$(date +%s%N)" \
    --data-binary "@$check_file")"
  account_status="$(RESPONSE="$check_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["results"][0]["accountStatus"])')"
  if [[ "$account_status" == "0" ]]; then
    PEER_USER_ID="$peer_user_id" python3 - <<'PY' >"$register_file"
import json
import os
print(json.dumps({"users": [{"userID": os.environ["PEER_USER_ID"], "nickname": "Lifecycle Peer", "faceURL": ""}]}, separators=(",", ":")))
PY
    register_response="$(curl -fsS --max-time 10 -X POST \
      http://127.0.0.1:12002/user/user_register \
      -H 'Content-Type: application/json' \
      -H "token: $admin_token" \
      -H "operationID: node2-web-e2e-lifecycle-register-$(date +%s%N)" \
      --data-binary "@$register_file")"
    RESPONSE="$register_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'
  elif [[ "$account_status" != "1" ]]; then
    echo "unexpected peer account status" >&2
    exit 1
  fi
  unset check_response admin_token
  echo "node2_e2e_lifecycle_peer=ready"
  echo "peer_user_id=$peer_user_id"
  exit 0
fi

transfer_file="$(mktemp)"
trap 'rm -f "$transfer_file"' EXIT
GROUP_ID="$group_id" OLD_OWNER_USER_ID="$old_owner_user_id" python3 - <<'PY' >"$transfer_file"
import json
import os
print(json.dumps({
    "groupID": os.environ["GROUP_ID"],
    "oldOwnerUserID": os.environ["OLD_OWNER_USER_ID"],
    "newOwnerUserID": "imAdmin",
}, separators=(",", ":")))
PY
transfer_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/group/transfer_group \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-lifecycle-transfer-$(date +%s%N)" \
  --data-binary "@$transfer_file")"
unset admin_token
RESPONSE="$transfer_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'
echo "node2_e2e_group_owner=transferred"
echo "group_id=$group_id"
