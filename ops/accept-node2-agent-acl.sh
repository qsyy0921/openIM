#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/qsyy0921/MFL/deploy/node2-native}"
openim_env="$deploy_root/openim/.env"
postgres_container=openim-platform-local-postgres-1
tenant_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
member_id=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb
document_id=0c202ead-9feb-50b2-b52e-5322d1aeb679
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

[[ "$(psql_value "SELECT count(*) FROM knowledge.documents WHERE id='$document_id'::uuid AND title='第三方安全评估管理制度'")" == 1 ]] || {
  echo "ACL acceptance document fixture is missing or changed" >&2
  exit 1
}
[[ "$(psql_value "SELECT count(*) FROM knowledge.chunks WHERE content ILIKE '%量子咖啡机%'")" == 0 ]] || {
  echo "no-match acceptance term unexpectedly exists in the knowledge corpus" >&2
  exit 1
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
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
    row="$(psql_value "select r.id::text,r.state,coalesce(r.model,''),coalesce(r.provider_response_id,''),coalesce(r.reply_server_msg_id,''),(select count(*) from agent.run_citations c where c.run_id=r.id),coalesce((select event.evidence->>'grounding_status' from audit.agent_run_events event where event.run_id=r.id and event.event_type='candidate_persisted' order by event.id desc limit 1),'') from integration.ingress_messages i join agent.runs r on r.source_event_id=i.event_id where i.server_msg_id='$server_msg_id'")"
    if [[ "$row" == *"|succeeded|"* || "$row" == *"|failed|"* ]]; then
      break
    fi
    sleep 2
  done
  [[ -n "$row" ]] || {
    echo "$case_name did not create an Agent Run" >&2
    return 1
  }
  IFS='|' read -r run_id state model provider_response_id reply_server_msg_id citation_count grounding_status <<<"$row"
  [[ "$state" == succeeded && -n "$reply_server_msg_id" ]] || {
    echo "$case_name failed: run=$run_id state=$state" >&2
    return 1
  }
  printf '%s|%s|%s|%s|%s\n' "$run_id" "$model" "$provider_response_id" "$citation_count" "$grounding_status"
}

nonce="$(date +%s%N)"
valid="$(send_and_wait "valid-$nonce" '请在企业知识库中检索并总结《第三方安全评估管理制度》的核心要求')"
IFS='|' read -r valid_run valid_model valid_provider valid_citations valid_grounding <<<"$valid"
valid_target_citations="$(psql_value "SELECT count(*) FROM agent.run_citations WHERE run_id='$valid_run'::uuid AND document_id='$document_id'::uuid")"
[[ "$valid_model" == gpt-5.6-terra && -n "$valid_provider" && "$valid_citations" -ge 1 && \
   "$valid_grounding" == grounded && "$valid_target_citations" -ge 1 ]] || {
  echo "authorized evidence was not processed by the fixed generation model: $valid" >&2
  exit 1
}
echo "authorized_acl_rag=accepted run_id=$valid_run citations=$valid_citations"

psql_value "DELETE FROM authz.document_grants WHERE tenant_id='$tenant_id' AND document_id='$document_id' AND member_id='$member_id' AND permission='read'" >/dev/null
grant_removed=true
revoked="$(send_and_wait "revoked-$nonce" '请在企业知识库中检索并总结《第三方安全评估管理制度》的核心要求')"
IFS='|' read -r revoked_run revoked_model revoked_provider revoked_citations revoked_grounding <<<"$revoked"
revoked_target_results="$(psql_value "SELECT count(*) FROM agent.tool_calls call CROSS JOIN LATERAL jsonb_array_elements(call.result) item WHERE call.run_id='$revoked_run'::uuid AND call.call_id='knowledge-search' AND item->>'document_id'='$document_id'")"
revoked_target_citations="$(psql_value "SELECT count(*) FROM agent.run_citations WHERE run_id='$revoked_run'::uuid AND document_id='$document_id'::uuid")"
[[ "$revoked_target_results" -eq 0 && "$revoked_target_citations" -eq 0 ]] || {
  echo "revoked evidence escaped authorization: $revoked" >&2
  exit 1
}
if [[ "$revoked_citations" -eq 0 ]]; then
  [[ "$revoked_grounding" == insufficient_evidence ]] || {
    echo "revoked response has invalid abstention state: $revoked" >&2
    exit 1
  }
else
  [[ "$revoked_grounding" == grounded || "$revoked_grounding" == insufficient_evidence ]] || {
    echo "revoked response has invalid cited state: $revoked" >&2
    exit 1
  }
fi
echo "revoked_acl=denied run_id=$revoked_run target_results=0 target_citations=0 authorized_citations=$revoked_citations"

psql_value "INSERT INTO authz.document_grants(tenant_id,document_id,member_id,permission) VALUES('$tenant_id','$document_id','$member_id','read') ON CONFLICT DO NOTHING" >/dev/null
grant_removed=false
no_match="$(send_and_wait "no-match-$nonce" '请在企业知识库中检索并总结《月球基地量子咖啡机维护制度》的核心要求')"
IFS='|' read -r no_match_run no_match_model no_match_provider no_match_citations no_match_grounding <<<"$no_match"
[[ "$no_match_grounding" == insufficient_evidence ]] || {
  echo "no-match did not explicitly abstain: $no_match" >&2
  exit 1
}
echo "no_match=explicit_abstention run_id=$no_match_run authorized_citations=$no_match_citations"

unset admin_token
