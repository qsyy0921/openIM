#!/usr/bin/env bash
set -euo pipefail

group_id="${1:-}"
deploy_root="${2:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$group_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || {
  echo "E2E group ID is invalid" >&2
  exit 1
}
[[ -r "$openim_env" ]] || {
  echo "node2 OpenIM environment is missing" >&2
  exit 1
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-cleanup-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

dismiss_response="$(curl -fsS --max-time 15 -X POST \
  http://127.0.0.1:12002/group/dismiss_group \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-dismiss-$(date +%s%N)" \
  --data "{\"groupID\":\"$group_id\",\"deleteMember\":true}")"
unset admin_token
RESPONSE="$dismiss_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p'
echo "node2_e2e_group=dismissed"
echo "group_id=$group_id"
