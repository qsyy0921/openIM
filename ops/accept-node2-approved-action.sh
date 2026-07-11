#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
openim_env="$deploy_root/openim/.env"
postgres_container=openim-platform-local-postgres-1
member_id=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 -U platform -d platform -c "$1"
}

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r')"
sender="$(psql_value "select openim_user_id from identity.identity_links where member_id='$member_id' and provisioning_state='ready'")"
admin_response="$(curl -fsS --max-time 10 -X POST http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' -H "operationID: node2-action-admin-$(date +%s)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

nonce="$(date +%s%N)"
title="OpenIM 平台 Node2 审批验收 $nonce"
request_file="$(mktemp)"
response_file="$(mktemp)"
trap 'rm -f "$request_file" "$response_file"' EXIT
TITLE="$title" SENDER="$sender" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Local Member",
    "senderPlatformID": 5,
    "content": {"content": "@Agent 创建工单：" + os.environ["TITLE"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-approved-action",
}, separators=(",", ":"), ensure_ascii=False))
PY
send_response="$(curl -fsS --max-time 15 -X POST http://127.0.0.1:12002/msg/send_msg \
  -H 'Content-Type: application/json' -H "token: $admin_token" \
  -H "operationID: node2-action-$nonce" --data-binary "@$request_file")"
server_msg_id="$(RESPONSE="$send_response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["serverMsgID"])')"
unset admin_token send_response

intent_row=""
for _ in $(seq 1 90); do
  intent_row="$(psql_value "select r.id::text,i.id::text,i.payload_digest,r.state,(select count(*) from collaboration.tickets t where t.idempotency_key='intent:'||i.id::text) from integration.ingress_messages m join agent.runs r on r.source_event_id=m.event_id join action.intents i on i.run_id=r.id where m.server_msg_id='$server_msg_id'")"
  [[ "$intent_row" == *"|waiting_approval|0" ]] && break
  sleep 2
done
IFS='|' read -r run_id intent_id digest run_state ticket_count <<<"$intent_row"
[[ "$run_state" == waiting_approval && "$ticket_count" -eq 0 ]] || {
  echo "action did not stop before approval: $intent_row" >&2
  exit 1
}
echo "pre_approval_ticket_count=0 run_id=$run_id intent_id=$intent_id"

token_response="$(curl -fsS --max-time 15 -X POST \
  http://172.31.50.2:18081/realms/platform/protocol/openid-connect/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=password' \
  --data-urlencode 'client_id=platform-api' \
  --data-urlencode 'scope=openid' \
  --data-urlencode 'username=local-member' \
  --data-urlencode 'password=local-test-only')"
oidc_token="$(RESPONSE="$token_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["id_token"])')"
unset token_response

approve() {
  local approval_digest="$1"
  curl --silent --show-error --output "$response_file" --write-out '%{http_code}' \
    -X POST "http://127.0.0.1:18080/v1/agent/intents/$intent_id/approve" \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $oidc_token" \
    --data "{\"platform_id\":5,\"device_id\":\"local-browser\",\"payload_digest\":\"$approval_digest\"}"
}

wrong_digest="sha256:$(printf '0%.0s' $(seq 1 64))"
wrong_status="$(approve "$wrong_digest")"
[[ "$wrong_status" == 409 ]] || {
  echo "wrong digest was not rejected: HTTP $wrong_status" >&2
  exit 1
}
[[ "$(psql_value "select count(*) from action.approvals where intent_id='$intent_id'::uuid")" -eq 0 ]] || {
  echo "wrong digest created an approval" >&2
  exit 1
}
echo "wrong_digest=rejected"

first_status="$(approve "$digest")"
[[ "$first_status" == 202 ]] || {
  echo "exact digest approval failed: HTTP $first_status" >&2
  exit 1
}
first_execution="$(RESPONSE_FILE="$response_file" python3 -c 'import json,os; print(json.load(open(os.environ["RESPONSE_FILE"],encoding="utf-8"))["execution_id"])')"
second_status="$(approve "$digest")"
[[ "$second_status" == 202 ]] || {
  echo "duplicate approval failed: HTTP $second_status" >&2
  exit 1
}
second_execution="$(RESPONSE_FILE="$response_file" python3 -c 'import json,os; print(json.load(open(os.environ["RESPONSE_FILE"],encoding="utf-8"))["execution_id"])')"
unset oidc_token
[[ "$first_execution" == "$second_execution" ]] || {
  echo "duplicate approval returned another execution" >&2
  exit 1
}

final_row=""
for _ in $(seq 1 60); do
  final_row="$(psql_value "select e.state,i.state,r.state,e.idempotency_key,coalesce(e.ticket_id::text,''),(select count(*) from action.approvals a where a.intent_id=i.id),(select count(*) from collaboration.tickets t where t.idempotency_key=e.idempotency_key) from action.executions e join action.intents i on i.id=e.intent_id join agent.runs r on r.id=i.run_id where e.id='$first_execution'::uuid")"
  [[ "$final_row" == succeeded\|succeeded\|succeeded\|*\|*\|1\|1 ]] && break
  sleep 1
done
IFS='|' read -r execution_state intent_state run_state idempotency_key original_ticket approval_count ticket_count <<<"$final_row"
[[ "$execution_state" == succeeded && "$intent_state" == succeeded && "$run_state" == succeeded && "$approval_count" -eq 1 && "$ticket_count" -eq 1 && -n "$original_ticket" ]] || {
  echo "approved action did not converge: $final_row" >&2
  exit 1
}
echo "approval_idempotency=accepted execution_id=$first_execution ticket_id=$original_ticket"

psql_value "update agent.runs set state='waiting_approval',completed_at=null where id='$run_id'::uuid; update action.intents set state='approved' where id='$intent_id'::uuid; update action.executions set state='unknown',attempts=0,available_at=now(),lease_token=null,lease_until=null,ticket_id=null,receipt=null where id='$first_execution'::uuid" >/dev/null
for _ in $(seq 1 30); do
  reconcile_existing="$(psql_value "select state,coalesce(ticket_id::text,'') from action.executions where id='$first_execution'::uuid")"
  [[ "$reconcile_existing" == "succeeded|$original_ticket" ]] && break
  sleep 1
done
[[ "$reconcile_existing" == "succeeded|$original_ticket" ]] || {
  echo "UNKNOWN existing-ticket reconciliation failed: $reconcile_existing" >&2
  exit 1
}
echo "unknown_existing_ticket=reconciled"

psql_value "update agent.runs set state='waiting_approval',completed_at=null where id='$run_id'::uuid; update action.intents set state='approved' where id='$intent_id'::uuid; update action.executions set state='unknown',attempts=0,available_at=now(),lease_token=null,lease_until=null,ticket_id=null,receipt=null where id='$first_execution'::uuid; delete from collaboration.tickets where id='$original_ticket'::uuid" >/dev/null
absent_row=""
for _ in $(seq 1 40); do
  absent_row="$(psql_value "select e.state,coalesce(e.ticket_id::text,''),e.idempotency_key,(select count(*) from collaboration.tickets t where t.idempotency_key=e.idempotency_key) from action.executions e where e.id='$first_execution'::uuid")"
  [[ "$absent_row" == succeeded\|*\|"$idempotency_key"\|1 ]] && break
  sleep 1
done
IFS='|' read -r absent_state replacement_ticket replacement_key replacement_count <<<"$absent_row"
[[ "$absent_state" == succeeded && "$replacement_key" == "$idempotency_key" && "$replacement_count" -eq 1 && -n "$replacement_ticket" ]] || {
  echo "UNKNOWN absent-ticket reconciliation failed: $absent_row" >&2
  exit 1
}
echo "unknown_absent_ticket=safe_retry same_idempotency_key=true ticket_id=$replacement_ticket"
