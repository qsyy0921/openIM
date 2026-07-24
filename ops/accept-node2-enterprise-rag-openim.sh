#!/usr/bin/env bash
set -euo pipefail

phase="${1:-}"
state_path="${2:-}"
manifest_path="${3:-}"
tenant_id="${4:-}"
authorized_member_id="${5:-}"
denied_member_id="${6:-}"
deploy_root="${7:-/home/qsyy0921/MFL/deploy/node2-native}"
result_path="${8:-}"

postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
openim_env="$deploy_root/openim/.env"

usage() {
  cat >&2 <<'EOF'
usage:
  accept-node2-enterprise-rag-openim.sh PHASE STATE_JSON MANIFEST_JSON TENANT_ID AUTHORIZED_MEMBER_ID DENIED_MEMBER_ID DEPLOY_ROOT RESULT_JSON

PHASE is one of: authorized, denied, revoked, version
EOF
  exit 2
}

valid_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_batch_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9-]{7,63}$ ]]
}

[[ "$phase" =~ ^(authorized|denied|revoked|version)$ ]] || usage
[[ -f "$state_path" && -f "$manifest_path" && -f "$openim_env" ]] || usage
[[ "$result_path" == /* && -d "$(dirname "$result_path")" ]] || usage
valid_uuid "$tenant_id" && valid_uuid "$authorized_member_id" && valid_uuid "$denied_member_id" || usage
docker inspect "$postgres_container" >/dev/null

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 \
    -U platform -d platform -c "$1"
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
contract_parser="$script_dir/enterprise-rag-channel-contract.py"
[[ -f "$contract_parser" ]] || {
  echo "shared enterprise RAG channel contract parser is missing" >&2
  exit 1
}
contract_file="$(mktemp)"
trap 'rm -f -- "$contract_file"' EXIT
python3 "$contract_parser" openim "$phase" "$state_path" "$manifest_path" >"$contract_file"

IFS='|' read -r batch_id prompt markdown_document_id text_document_id pdf_document_id \
  docx_document_id markdown_initial_version_id markdown_next_version_id \
  markdown_marker markdown_next_marker <"$contract_file"
valid_batch_id "$batch_id" || usage
for value in "$markdown_document_id" "$text_document_id" "$pdf_document_id" \
  "$docx_document_id" "$markdown_initial_version_id"; do
  valid_uuid "$value" || usage
done
if [[ "$phase" == version ]]; then
  valid_uuid "$markdown_next_version_id" || usage
elif [[ -n "$markdown_next_version_id" ]]; then
  valid_uuid "$markdown_next_version_id" || usage
fi
[[ "$markdown_marker" =~ ^[A-Za-z0-9-]{8,96}$ && \
   "$markdown_next_marker" =~ ^[A-Za-z0-9-]{8,96}$ ]] || usage

case "$phase" in
  authorized|revoked|version) member_id="$authorized_member_id" ;;
  denied) member_id="$denied_member_id" ;;
esac

sender="$(psql_value "
SELECT openim_user_id
FROM identity.identity_links
WHERE tenant_id='$tenant_id'::uuid
  AND member_id='$member_id'::uuid
  AND provisioning_state='ready'")"
[[ -n "$sender" && "$sender" != *"|"* && "$sender" != *$'\n'* ]] || {
  echo "OpenIM identity is missing or ambiguous for the acceptance member" >&2
  exit 1
}

target_ids_sql="'$markdown_document_id'::uuid,'$text_document_id'::uuid,'$pdf_document_id'::uuid,'$docx_document_id'::uuid"

find_existing_run() {
  psql_value "
SELECT id::text,state,COALESCE(reply_server_msg_id,'')
FROM agent.runs
WHERE tenant_id='$tenant_id'::uuid
  AND principal_member_id='$member_id'::uuid
  AND source_channel='openim'
  AND prompt=\$prompt\$$prompt\$prompt\$
ORDER BY created_at,id
LIMIT 2"
}

existing="$(find_existing_run)"
existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
[[ "$existing_count" -le 1 ]] || {
  echo "OpenIM acceptance prompt already has duplicate Runs; no message was sent" >&2
  exit 1
}

attempt_state="$(RESULT_PATH="$result_path" BATCH_ID="$batch_id" PHASE="$phase" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
if not path.exists():
    print("missing|")
    raise SystemExit
value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("OpenIM result file belongs to another acceptance batch")
record = value.get("openim", {}).get(os.environ["PHASE"])
if record is None:
    print("missing|")
elif record.get("state") == "prepared" and not record.get("run_id"):
    print("prepared|")
elif isinstance(record.get("run_id"), str):
    print("completed|" + record["run_id"])
else:
    raise SystemExit("OpenIM phase result is malformed")
PY
)"
IFS='|' read -r recorded_state recorded_run_id <<<"$attempt_state"
if [[ "$recorded_state" == completed ]]; then
  [[ "$existing_count" -eq 1 && "${existing%%|*}" == "$recorded_run_id" ]] || {
    echo "recorded OpenIM acceptance Run cannot be reconciled; no message was sent" >&2
    exit 1
  }
fi
if [[ "$recorded_state" == prepared && "$existing_count" -eq 0 ]]; then
  for _ in $(seq 1 15); do
    existing="$(find_existing_run)"
    existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
    [[ "$existing_count" -gt 0 ]] && break
    sleep 2
  done
  [[ "$existing_count" -eq 1 ]] || {
    echo "a prior OpenIM send attempt remains uncertain; the script did not retry" >&2
    exit 1
  }
fi

source_server_msg_id=""
if [[ "$existing_count" -eq 0 ]]; then
  secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
  [[ -n "$secret" ]] || {
    echo "OpenIM server secret is missing" >&2
    exit 1
  }
  admin_response="$(curl -fsS --max-time 10 -X POST \
    http://127.0.0.1:12002/auth/get_admin_token \
    -H 'Content-Type: application/json' \
    -H "operationID: enterprise-rag-admin-$batch_id" \
    --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
  admin_token="$(RESPONSE="$admin_response" python3 -c \
    'import json,os; value=json.loads(os.environ["RESPONSE"]); assert value.get("errCode")==0,value; print(value["data"]["token"])')"
  unset admin_response secret

  request_file="$(mktemp)"
  trap 'rm -f -- "$contract_file" "${request_file:-}"; unset admin_token' EXIT
  PHASE="$phase" PROMPT="$prompt" SENDER="$sender" BATCH_ID="$batch_id" python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Enterprise RAG E2E",
    "senderPlatformID": 5,
    "clientMsgID": f"enterprise-rag-{os.environ['BATCH_ID']}-{os.environ['PHASE']}",
    "content": {"content": "@Agent " + os.environ["PROMPT"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": f"enterprise-rag-e2e:{os.environ['BATCH_ID']}:{os.environ['PHASE']}",
}, separators=(",", ":"), ensure_ascii=False))
PY

  RESULT_PATH="$result_path" BATCH_ID="$batch_id" PHASE="$phase" \
    CLIENT_MSG_ID="enterprise-rag-$batch_id-$phase" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
value = {"schema_version": 1, "batch_id": os.environ["BATCH_ID"], "openim": {}}
if path.exists():
    value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("OpenIM result file belongs to another acceptance batch")
channel = value.setdefault("openim", {})
if os.environ["PHASE"] in channel:
    raise SystemExit("OpenIM phase already has an acceptance record")
channel[os.environ["PHASE"]] = {
    "state": "prepared",
    "client_msg_id": os.environ["CLIENT_MSG_ID"],
}
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
PY

  set +e
  response="$(curl -fsS --max-time 15 -X POST \
    http://127.0.0.1:12002/msg/send_msg \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: enterprise-rag-$batch_id-$phase" \
    --data-binary "@$request_file")"
  curl_status=$?
  set -e
  rm -f -- "$request_file"
  request_file=""
  unset admin_token

  if [[ "$curl_status" -eq 0 ]]; then
    source_server_msg_id="$(RESPONSE="$response" python3 -c \
      'import json,os; value=json.loads(os.environ["RESPONSE"]); assert value.get("errCode")==0,value; print(value["data"]["serverMsgID"])')"
  else
    unset response
    for _ in $(seq 1 15); do
      existing="$(find_existing_run)"
      existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
      [[ "$existing_count" -gt 0 ]] && break
      sleep 2
    done
    [[ "$existing_count" -eq 1 ]] || {
      echo "OpenIM send outcome is uncertain and no durable Run was found; the script did not retry" >&2
      exit 1
    }
  fi
  unset response
fi

run_row=""
for _ in $(seq 1 180); do
  run_rows="$(find_existing_run)"
  run_count="$(printf '%s\n' "$run_rows" | sed '/^$/d' | wc -l)"
  [[ "$run_count" -le 1 ]] || {
    echo "OpenIM acceptance prompt produced duplicate Runs" >&2
    exit 1
  }
  if [[ "$run_count" -eq 1 ]]; then
    run_row="$run_rows"
    [[ "$run_row" == *"|succeeded|"* || "$run_row" == *"|failed|"* ]] && break
  fi
  sleep 2
done
[[ -n "$run_row" ]] || {
  echo "OpenIM acceptance message did not create a terminal Agent Run" >&2
  exit 1
}
IFS='|' read -r run_id run_state reply_server_msg_id <<<"$run_row"
[[ "$run_state" == succeeded && -n "$reply_server_msg_id" ]] || {
  echo "OpenIM acceptance Run failed closed: run_id=$run_id state=$run_state" >&2
  exit 1
}

run_contract="$(psql_value "
SELECT model,(provider_response_id IS NOT NULL)::text,
       (SELECT count(*) FROM agent.run_citations citation
         WHERE citation.run_id=run.id
           AND citation.document_id IN ($target_ids_sql)),
       (SELECT count(*)
          FROM agent.tool_calls call
          CROSS JOIN LATERAL jsonb_array_elements(COALESCE(call.result,'[]'::jsonb)) item
         WHERE call.run_id=run.id
           AND item->>'document_id' IN (
             '$markdown_document_id','$text_document_id',
             '$pdf_document_id','$docx_document_id'))
FROM agent.runs run
WHERE run.id='$run_id'::uuid")"
IFS='|' read -r model has_provider target_citations target_results <<<"$run_contract"

case "$phase" in
  authorized)
    coverage="$(psql_value "
SELECT count(DISTINCT document_id),
       count(*) FILTER (
         WHERE version_id NOT IN (
           '$markdown_initial_version_id'::uuid,
           (SELECT id FROM knowledge.document_versions WHERE document_id='$text_document_id'::uuid AND version_number=1),
           (SELECT id FROM knowledge.document_versions WHERE document_id='$pdf_document_id'::uuid AND version_number=1),
           (SELECT id FROM knowledge.document_versions WHERE document_id='$docx_document_id'::uuid AND version_number=1)))
FROM agent.run_citations
WHERE run_id='$run_id'::uuid AND document_id IN ($target_ids_sql)")"
    [[ "$model" == gpt-5.6-terra && "$has_provider" == true && \
       "$coverage" == "4|0" && "$target_citations" -ge 4 ]] || {
      echo "authorized OpenIM Run did not return all four current documents: $run_contract coverage=$coverage" >&2
      exit 1
    }
    ;;
  denied|revoked)
    [[ "$target_citations" -eq 0 && "$target_results" -eq 0 ]] || {
      echo "$phase OpenIM Run exposed target evidence: $run_contract" >&2
      exit 1
    }
    ;;
  version)
    version_contract="$(psql_value "
SELECT
  count(*) FILTER (WHERE citation.version_id='$markdown_next_version_id'::uuid),
  count(*) FILTER (WHERE citation.version_id='$markdown_initial_version_id'::uuid),
  (position('$markdown_next_marker' in COALESCE(run.candidate_text,''))>0)::text,
  (position('$markdown_marker' in COALESCE(run.candidate_text,''))>0)::text
FROM agent.runs run
LEFT JOIN agent.run_citations citation ON citation.run_id=run.id
WHERE run.id='$run_id'::uuid
GROUP BY run.candidate_text")"
    [[ "$model" == gpt-5.6-terra && "$has_provider" == true && \
       "$version_contract" =~ ^[1-9][0-9]*\|0\|true\|false$ ]] || {
      echo "versioned OpenIM Run retained old evidence or omitted current evidence: $version_contract" >&2
      exit 1
    }
    ;;
esac

PHASE="$phase" RUN_ID="$run_id" SOURCE_MSG_ID="$source_server_msg_id" \
  REPLY_MSG_ID="$reply_server_msg_id" RESULT_PATH="$result_path" BATCH_ID="$batch_id" \
  python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
value = {"schema_version": 1, "batch_id": os.environ["BATCH_ID"], "openim": {}}
if path.exists():
    value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("OpenIM result file belongs to another acceptance batch")
channel = value.setdefault("openim", {})
record = {
    "state": "completed",
    "run_id": os.environ["RUN_ID"],
    "reply_server_msg_id": os.environ["REPLY_MSG_ID"],
}
if os.environ["SOURCE_MSG_ID"]:
    record["source_server_msg_id"] = os.environ["SOURCE_MSG_ID"]
existing = channel.get(os.environ["PHASE"])
if existing is not None and existing.get("run_id") not in (None, record["run_id"]):
    raise SystemExit("OpenIM phase already records another Run")
channel[os.environ["PHASE"]] = record
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
PY

echo "enterprise_rag_openim=$phase run_id=$run_id reply_server_msg_id=$reply_server_msg_id"
