#!/usr/bin/env bash
set -euo pipefail

peer_user_id="${1:-lifecyclePeer}"
deploy_root="${2:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"

[[ "$peer_user_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || { echo "peer user ID is invalid" >&2; exit 1; }
[[ -r "$openim_env" ]] || { echo "node2 OpenIM environment is missing" >&2; exit 1; }

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-web-e2e-peer-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["token"])')"
unset admin_response secret

token_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_user_token \
  -H 'Content-Type: application/json' \
  -H "token: $admin_token" \
  -H "operationID: node2-web-e2e-peer-token-$(date +%s%N)" \
  --data "{\"platformID\":5,\"userID\":\"$peer_user_id\"}")"
unset admin_token
user_token="$(RESPONSE="$token_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["token"])')"
unset token_response
printf 'user_token=%s\n' "$user_token"
unset user_token
