#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
release_root="${2:-/home/ubuntu/MFL/releases/agent-catalog-v1}"
admin="$release_root/linux-amd64/agent-catalog-admin"
openim_env="$deploy_root/openim/.env"
postgres_container=openim-platform-local-postgres-1
tenant_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
actor_member_id=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb
spec_file="$(mktemp)"
rollback_needed=false
agent_id=""
baseline_version_id=""

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 -U platform -d platform -c "$1"
}

activate() {
  local version_id="$1"
  local revision="$2"
  "$admin" \
    -operation activate \
    -tenant-id "$tenant_id" \
    -agent-id "$agent_id" \
    -actor-member-id "$actor_member_id" \
    -version-id "$version_id" \
    -expected-revision "$revision" >/dev/null
}

cleanup() {
  local exit_code=$?
  rm -f "$spec_file"
  if [[ "$rollback_needed" == true && -n "$agent_id" && -n "$baseline_version_id" ]]; then
    local current current_version revision
    current="$(psql_value "select active_version_id::text,revision from agent.deployments where tenant_id='$tenant_id' and agent_id='$agent_id' and slot='production'" || true)"
    IFS='|' read -r current_version revision <<<"$current"
    if [[ -n "$current_version" && "$current_version" != "$baseline_version_id" && -n "$revision" ]]; then
      activate "$baseline_version_id" "$revision" || echo "automatic rollback failed" >&2
    fi
  fi
  exit "$exit_code"
}
trap cleanup EXIT

[[ -x "$admin" && -f "$openim_env" ]] || {
  echo "Agent Catalog admin binary or OpenIM environment is missing" >&2
  exit 1
}
set -a
. /etc/openim-platform/platform.env
set +a

catalog_row="$(psql_value "
select d.id::text,v.id::text,v.version_number,dep.revision,l.openim_user_id,v.spec->>'model_route'
from agent.definitions d
join agent.deployments dep on dep.tenant_id=d.tenant_id and dep.agent_id=d.id and dep.slot='production'
join agent.versions v on v.tenant_id=d.tenant_id and v.agent_id=d.id and v.id=dep.active_version_id
join identity.identity_links l on l.tenant_id=d.tenant_id and l.member_id='$actor_member_id' and l.provisioning_state='ready'
where d.tenant_id='$tenant_id' and d.slug='knowledge-agent'")"
IFS='|' read -r agent_id baseline_version_id baseline_version_number revision sender baseline_model <<<"$catalog_row"
[[ -n "$agent_id" && -n "$baseline_version_id" && "$baseline_version_number" =~ ^[1-9][0-9]*$ && \
   -n "$revision" && -n "$sender" && "$baseline_model" == gpt-5.6-luna ]] || {
  echo "seed Agent Catalog or sender identity is missing" >&2
  exit 1
}

cat >"$spec_file" <<'JSON'
{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and identify this execution as catalog validation version two.","model_route":"gpt-5.6-luna","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}
JSON
expected_checksum="$(SPEC_FILE="$spec_file" python3 - <<'PY'
import hashlib
import os

raw = open(os.environ["SPEC_FILE"], "rb").read().strip()
print("sha256:" + hashlib.sha256(raw).hexdigest())
PY
)"

acceptance_row="$(psql_value "select id::text,spec_checksum,version_number from agent.versions where tenant_id='$tenant_id' and agent_id='$agent_id' and spec_checksum='$expected_checksum'")"
if [[ -z "$acceptance_row" ]]; then
  expected_version="$(psql_value "select max(version_number)+1 from agent.versions where tenant_id='$tenant_id' and agent_id='$agent_id'")"
  publish_result="$("$admin" \
    -operation publish \
    -tenant-id "$tenant_id" \
    -agent-id "$agent_id" \
    -actor-member-id "$actor_member_id" \
    -expected-version "$expected_version" \
    -spec-file "$spec_file")"
  acceptance_version_id="$(RESULT="$publish_result" python3 -c 'import json,os; print(json.loads(os.environ["RESULT"])["version_id"])')"
  acceptance_checksum="$(RESULT="$publish_result" python3 -c 'import json,os; print(json.loads(os.environ["RESULT"])["spec_checksum"])')"
else
  IFS='|' read -r acceptance_version_id acceptance_checksum expected_version <<<"$acceptance_row"
fi
[[ "$acceptance_checksum" == "$expected_checksum" && "$expected_version" =~ ^[1-9][0-9]*$ ]] || {
  echo "existing acceptance version does not match the specification" >&2
  exit 1
}

current_row="$(psql_value "select active_version_id::text,revision from agent.deployments where tenant_id='$tenant_id' and agent_id='$agent_id' and slot='production'")"
IFS='|' read -r current_version revision <<<"$current_row"
if [[ "$current_version" != "$baseline_version_id" ]]; then
  echo "acceptance requires production to remain on the captured baseline" >&2
  exit 1
fi
activate "$acceptance_version_id" "$revision"
rollback_needed=true

secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
admin_response="$(curl -fsS --max-time 10 -X POST \
  http://127.0.0.1:12002/auth/get_admin_token \
  -H 'Content-Type: application/json' \
  -H "operationID: node2-catalog-admin-$(date +%s%N)" \
  --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
admin_token="$(RESPONSE="$admin_response" python3 -c 'import json,os; print(json.loads(os.environ["RESPONSE"])["data"]["token"])')"
unset admin_response secret

send_and_wait() {
  local case_name="$1"
  local expected_version="$2"
  local expected_version_id="$3"
  local request_file response server_msg_id row
  request_file="$(mktemp)"
  CASE_NAME="$case_name" SENDER="$sender" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Local Member",
    "senderPlatformID": 5,
    "content": {"content": "@Agent 第三方安全评估管理制度：受理确认和正常完成时限分别是多少？请仅依据授权证据回答并标注引用。 " + os.environ["CASE_NAME"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": "node2-agent-catalog:" + os.environ["CASE_NAME"],
}, separators=(",", ":"), ensure_ascii=False))
PY
  response="$(curl -fsS --max-time 15 -X POST \
    http://127.0.0.1:12002/msg/send_msg \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: node2-catalog-$case_name-$(date +%s%N)" \
    --data-binary "@$request_file")"
  rm -f "$request_file"
  server_msg_id="$(RESPONSE="$response" python3 -c 'import json,os; p=json.loads(os.environ["RESPONSE"]); assert p.get("errCode")==0,p; print(p["data"]["serverMsgID"])')"
  row=""
  for _ in $(seq 1 90); do
    row="$(psql_value "
select r.id::text,r.state,r.agent_version_id::text,v.version_number,r.agent_spec_checksum,
       coalesce(r.model,''),coalesce(r.reply_server_msg_id,''),
       (select count(*) from agent.run_citations c where c.run_id=r.id)
from integration.ingress_messages i
join agent.runs r on r.source_event_id=i.event_id
join agent.versions v on v.tenant_id=r.tenant_id and v.agent_id=r.agent_id and v.id=r.agent_version_id
where i.server_msg_id='$server_msg_id'")"
    [[ "$row" == *"|succeeded|"* || "$row" == *"|failed|"* ]] && break
    sleep 2
  done
  IFS='|' read -r run_id state pinned_version_id version_number checksum model reply_id citation_count <<<"$row"
  [[ "$state" == succeeded && "$pinned_version_id" == "$expected_version_id" && "$version_number" -eq "$expected_version" && -n "$reply_id" && "$citation_count" -ge 1 ]] || {
    echo "$case_name did not complete on expected version: $row" >&2
    return 1
  }
  printf '%s|%s|%s|%s\n' "$run_id" "$version_number" "$checksum" "$model"
}

nonce="$(date +%s%N)"
acceptance_run="$(send_and_wait "candidate-$nonce" "$expected_version" "$acceptance_version_id")"

current_revision="$(psql_value "select revision from agent.deployments where tenant_id='$tenant_id' and agent_id='$agent_id' and slot='production'")"
activate "$baseline_version_id" "$current_revision"
rollback_needed=false
baseline_run="$(send_and_wait "rollback-baseline-$nonce" "$baseline_version_number" "$baseline_version_id")"

IFS='|' read -r old_run_id old_run_version _ _ <<<"$acceptance_run"
old_run_still_pinned="$(psql_value "select v.version_number from agent.runs r join agent.versions v on v.id=r.agent_version_id and v.agent_id=r.agent_id and v.tenant_id=r.tenant_id where r.id='$old_run_id'")"
[[ "$old_run_version" -eq "$expected_version" && "$old_run_still_pinned" -eq "$expected_version" ]] || {
  echo "rollback mutated the historical acceptance Run" >&2
  exit 1
}

printf 'acceptance_version_run=%s\n' "$acceptance_run"
printf 'rollback_baseline_run=%s\n' "$baseline_run"
printf 'historical_run_version=%s\n' "$expected_version"
printf 'production_version=%s\n' "$baseline_version_number"
unset admin_token
