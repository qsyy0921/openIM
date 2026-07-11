#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"
postgres_container=openim-platform-local-postgres-1
tenant_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
member_id=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb
document_id=dddddddd-dddd-4ddd-8ddd-dddddddddddd
grant_removed=false

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 -U platform -d platform -c "$1"
}

restore_grant() {
  if [[ "$grant_removed" == true ]]; then
    psql_value "INSERT INTO authz.document_grants(tenant_id,document_id,member_id,permission) VALUES('$tenant_id','$document_id','$member_id','read') ON CONFLICT DO NOTHING" >/dev/null
  fi
}
trap restore_grant EXIT

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
sender="$(psql_value "select openim_user_id from identity.identity_links where member_id='$member_id' and provisioning_state='ready'")"
[[ -n "$secret" && -n "$sender" ]] || {
  echo "OpenIM secret or provisioned sender is missing" >&2
  exit 1
}

admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-agent-admin-$(date +%s)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

send_and_wait() {
  local case_name="$1"
  local prompt="$2"
  local request_file response server_msg_id row
  request_file="$(mktemp)"
  CASE_NAME="$case_name" PROMPT="$prompt" SENDER="$sender" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Local Member",
    "senderPlatformID": 5,
    "content": {"content": "@Agent " + os.environ["PROMPT"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-agent-acl:" + os.environ["CASE_NAME"],
}, separators=(",", ":"), ensure_ascii=False))
PY
  response="$(curl -fsS --max-time 15 -X POST \
    http://127.0.0.1:12002/msg/send_msg \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: node2-agent-$case_name-$(date +%s%N)" \
    --data-binary "@$request_file")"
  rm -f "$request_file"
  server_msg_id="$(RESPONSE="$response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["serverMsgID"])')"

  row=""
  for _ in $(seq 1 90); do
    row="$(psql_value "select r.id::text,r.state,coalesce(r.model,''),coalesce(r.provider_response_id,''),coalesce(r.reply_server_msg_id,''),(select count(*) from agent.run_citations c where c.run_id=r.id) from integration.ingress_messages i join agent.runs r on r.source_event_id=i.event_id where i.server_msg_id='$server_msg_id'")"
    if [[ "$row" == *"|succeeded|"* || "$row" == *"|failed|"* ]]; then
      break
    fi
    sleep 2
  done
  [[ -n "$row" ]] || {
    echo "$case_name did not create an Agent Run" >&2
    return 1
  }
  IFS='|' read -r run_id state model provider_response_id reply_server_msg_id citation_count <<<"$row"
  [[ "$state" == succeeded && -n "$reply_server_msg_id" ]] || {
    echo "$case_name failed: run=$run_id state=$state" >&2
    return 1
  }
  printf '%s|%s|%s|%s\n' "$run_id" "$model" "$provider_response_id" "$citation_count"
}

nonce="$(date +%s%N)"
valid="$(send_and_wait "valid-$nonce" 'OpenIM 平台 本机优先开发 PostgreSQL Keycloak')"
IFS='|' read -r valid_run valid_model valid_provider valid_citations <<<"$valid"
[[ "$valid_model" == deepseek-v4-pro && -n "$valid_provider" && "$valid_citations" -ge 1 ]] || {
  echo "authorized evidence was not processed by DeepSeek: $valid" >&2
  exit 1
}
echo "authorized_acl_rag=accepted run_id=$valid_run citations=$valid_citations"

psql_value "DELETE FROM authz.document_grants WHERE tenant_id='$tenant_id' AND document_id='$document_id' AND member_id='$member_id' AND permission='read'" >/dev/null
grant_removed=true
revoked="$(send_and_wait "revoked-$nonce" 'OpenIM 平台 本机优先开发 PostgreSQL Keycloak')"
IFS='|' read -r revoked_run revoked_model revoked_provider revoked_citations <<<"$revoked"
[[ "$revoked_model" == runtime-policy && "$revoked_provider" == no-evidence:* && "$revoked_citations" -eq 0 ]] || {
  echo "revoked evidence escaped authorization: $revoked" >&2
  exit 1
}
echo "revoked_acl=denied run_id=$revoked_run citations=0"

restore_grant
grant_removed=false
no_match="$(send_and_wait "no-match-$nonce" "不存在的验收词条 $nonce")"
IFS='|' read -r no_match_run no_match_model no_match_provider no_match_citations <<<"$no_match"
[[ "$no_match_model" == runtime-policy && "$no_match_provider" == no-evidence:* && "$no_match_citations" -eq 0 ]] || {
  echo "no-match unexpectedly produced evidence: $no_match" >&2
  exit 1
}
echo "no_match=explicit_abstention run_id=$no_match_run citations=0"

unset admin_token
