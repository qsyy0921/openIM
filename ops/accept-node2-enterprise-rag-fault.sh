#!/usr/bin/env bash
set -euo pipefail

phase="${1:-}"
state_path="${2:-}"
manifest_path="${3:-}"
tenant_id="${4:-}"
authorized_member_id="${5:-}"
deploy_root="${6:-}"
result_path="${7:-}"

postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
platform_env="${OPENIM_PLATFORM_ENV:-/etc/openim-platform/platform.env}"
runtime_unit="openim-agent-runtime.service"
proxy_unit="openim-enterprise-rag-fault-proxy.service"
proxy_unit_path="/run/systemd/system/$proxy_unit"
dropin_dir="/run/systemd/system/$runtime_unit.d"
dropin_path="$dropin_dir/99-enterprise-rag-fault.conf"
marker_path="/run/openim-enterprise-rag-fault.json"
proxy_script="$deploy_root/ops/enterprise-rag-fault-proxy.py"
openim_env="$deploy_root/openim/.env"
temporary_contract=""
request_file=""
restore_on_exit=true
fault_active=false

usage() {
  cat >&2 <<'EOF'
usage:
  accept-node2-enterprise-rag-fault.sh PHASE STATE_JSON MANIFEST_JSON TENANT_ID AUTHORIZED_MEMBER_ID DEPLOY_ROOT RESULT_JSON

PHASE is one of: embedding, reranker, model
The script must run as root on Node2 after the Web bootstrap phase.
EOF
  exit 2
}

valid_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_batch_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9-]{7,63}$ ]]
}

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 \
    -U platform -d platform -c "$1"
}

platform_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$platform_env" | tail -1 | tr -d '\r'
}

marker_matches() {
  [[ -f "$marker_path" ]] || return 1
  MARKER_PATH="$marker_path" BATCH_ID="$batch_id" PHASE="$phase" \
    FAULT_ID="$fault_id" python3 - <<'PY'
import json
import os
from pathlib import Path

value = json.loads(Path(os.environ["MARKER_PATH"]).read_text(encoding="utf-8"))
expected = {
    "schema_version": 1,
    "batch_id": os.environ["BATCH_ID"],
    "phase": os.environ["PHASE"],
    "fault_id": os.environ["FAULT_ID"],
}
if any(value.get(key) != item for key, item in expected.items()):
    raise SystemExit("another fault-injection lease owns the Node2 runtime")
PY
}

runtime_environment_has() {
  local expected="$1"
  local pid
  pid="$(systemctl show "$runtime_unit" -p MainPID --value)"
  [[ "$pid" =~ ^[1-9][0-9]*$ && -r "/proc/$pid/environ" ]] || return 1
  tr '\0' '\n' <"/proc/$pid/environ" | grep -qxF "$expected"
}

wait_active() {
  local unit="$1"
  for _ in $(seq 1 30); do
    systemctl is-active --quiet "$unit" && return 0
    sleep 1
  done
  return 1
}

verify_direct_runtime() {
  wait_active "$runtime_unit" || {
    echo "Agent Runtime did not return active after fault restoration" >&2
    return 1
  }
  runtime_environment_has "PLATFORM_INTELLIGENCE_URL=http://127.0.0.1:18082" || {
    echo "Agent Runtime generation URL was not restored" >&2
    return 1
  }
  runtime_environment_has "PLATFORM_RETRIEVAL_INTELLIGENCE_URL=http://127.0.0.1:18083" || {
    echo "Agent Runtime retrieval URL was not restored" >&2
    return 1
  }
  curl -fsS --max-time 5 http://127.0.0.1:18082/healthz >/dev/null
  curl -fsS --max-time 5 http://127.0.0.1:18083/healthz >/dev/null
}

verify_fault_runtime() {
  marker_matches
  [[ -f "$proxy_unit_path" && -f "$dropin_path" ]] || {
    echo "fault-injection systemd files are incomplete" >&2
    return 1
  }
  wait_active "$proxy_unit" || {
    echo "fault proxy did not become active" >&2
    return 1
  }
  wait_active "$runtime_unit" || {
    echo "Agent Runtime did not become active with the fault proxy" >&2
    return 1
  }
  runtime_environment_has "$runtime_override" || {
    echo "Agent Runtime did not load the expected fault proxy URL" >&2
    return 1
  }
  curl -fsS --max-time 5 "http://127.0.0.1:$proxy_port/__fault/healthz" >/dev/null
  curl -fsS --max-time 5 "$upstream_url/healthz" >/dev/null
}

start_fault_runtime() {
  if [[ -e "$marker_path" || -e "$proxy_unit_path" || -e "$dropin_path" ]]; then
    marker_matches || {
      echo "Node2 contains an unowned fault-injection artifact" >&2
      return 1
    }
    fault_active=true
    verify_fault_runtime
    return
  fi

  local runtime_user runtime_group
  runtime_user="$(systemctl show "$runtime_unit" -p User --value)"
  runtime_group="$(systemctl show "$runtime_unit" -p Group --value)"
  [[ "$runtime_user" =~ ^[A-Za-z_][A-Za-z0-9_.-]*$ ]] || {
    echo "Agent Runtime service user is malformed" >&2
    return 1
  }
  [[ -n "$runtime_group" ]] || runtime_group="$runtime_user"
  [[ "$runtime_group" =~ ^[A-Za-z_][A-Za-z0-9_.-]*$ ]] || {
    echo "Agent Runtime service group is malformed" >&2
    return 1
  }

  install -d -m 0755 "$dropin_dir"
  BATCH_ID="$batch_id" PHASE="$phase" FAULT_ID="$fault_id" \
    python3 - <<'PY' >"$marker_path"
import json
import os

print(json.dumps({
    "schema_version": 1,
    "batch_id": os.environ["BATCH_ID"],
    "phase": os.environ["PHASE"],
    "fault_id": os.environ["FAULT_ID"],
}, separators=(",", ":"), sort_keys=True))
PY
  chmod 0600 "$marker_path"
  fault_active=true

  cat >"$proxy_unit_path" <<EOF
[Unit]
Description=OpenIM enterprise RAG scoped fault proxy
After=network.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
Environment=PYTHONDONTWRITEBYTECODE=1
ExecStart=/usr/bin/python3 $proxy_script --listen-port $proxy_port --upstream $upstream_url --fail-path $fail_path --match-text $fault_marker --fault-id $fault_id --timeout-seconds 30
Restart=no
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
IPAddressDeny=any
IPAddressAllow=localhost
UMask=0077
EOF

  cat >"$dropin_path" <<EOF
[Service]
Environment=$runtime_override
EOF

  systemctl daemon-reload
  systemctl start "$proxy_unit"
  for _ in $(seq 1 30); do
    curl -fsS --max-time 2 "http://127.0.0.1:$proxy_port/__fault/healthz" >/dev/null 2>&1 && break
    sleep 1
  done
  curl -fsS --max-time 2 "http://127.0.0.1:$proxy_port/__fault/healthz" >/dev/null
  systemctl restart "$runtime_unit"
  verify_fault_runtime
}

restore_fault_runtime() {
  marker_matches || {
    echo "refusing to restore an unowned fault-injection runtime" >&2
    return 1
  }
  rm -f -- "$dropin_path"
  rmdir "$dropin_dir" 2>/dev/null || true
  systemctl daemon-reload
  systemctl restart "$runtime_unit"
  verify_direct_runtime
  systemctl stop "$proxy_unit"
  rm -f -- "$proxy_unit_path" "$marker_path"
  systemctl daemon-reload
  systemctl reset-failed "$proxy_unit" >/dev/null 2>&1 || true
  fault_active=false
}

on_exit() {
  local status=$?
  set +e
  [[ -n "$temporary_contract" ]] && rm -f -- "$temporary_contract"
  [[ -n "$request_file" ]] && rm -f -- "$request_file"
  if [[ "$fault_active" == true && "$restore_on_exit" == true ]]; then
    restore_fault_runtime
    local restore_status=$?
    if [[ "$restore_status" -ne 0 ]]; then
      echo "fault runtime restoration requires operator attention" >&2
      status=1
    fi
  elif [[ "$fault_active" == true ]]; then
    echo "fault proxy remains marker-scoped because the external message outcome is not terminal" >&2
  fi
  exit "$status"
}
trap on_exit EXIT

read_result_state() {
  RESULT_PATH="$result_path" BATCH_ID="$batch_id" PHASE="$phase" \
    MARKER_DIGEST="$marker_digest" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
if not path.exists():
    print("missing|")
    raise SystemExit
value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("fault result belongs to another acceptance batch")
record = value.get("faults", {}).get(os.environ["PHASE"])
if record is None:
    print("missing|")
    raise SystemExit
if record.get("marker_sha256") != os.environ["MARKER_DIGEST"]:
    raise SystemExit("fault result marker differs from the current contract")
state = record.get("state")
if state not in {"prepared", "active", "send_prepared", "completed"}:
    raise SystemExit("fault result state is malformed")
run_id = record.get("run_id", "")
if run_id is not None and not isinstance(run_id, str):
    raise SystemExit("fault result Run ID is malformed")
print(f"{state}|{run_id}")
PY
}

write_result_state() {
  local next_state="$1"
  local run_id="${2:-}"
  local source_server_msg_id="${3:-}"
  RESULT_PATH="$result_path" BATCH_ID="$batch_id" PHASE="$phase" \
    NEXT_STATE="$next_state" RUN_ID="$run_id" SOURCE_MSG_ID="$source_server_msg_id" \
    MARKER_DIGEST="$marker_digest" FAIL_PATH="$fail_path" \
    python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
value = {"schema_version": 1, "batch_id": os.environ["BATCH_ID"], "faults": {}}
ownership = None
if path.exists():
    ownership = path.stat()
    value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("fault result belongs to another acceptance batch")
records = value.setdefault("faults", {})
record = records.get(os.environ["PHASE"])
order = {"prepared": 0, "active": 1, "send_prepared": 2, "completed": 3}
next_state = os.environ["NEXT_STATE"]
if next_state not in order:
    raise SystemExit("unsupported fault result state")
if record is not None:
    current = record.get("state")
    if current not in order or order[next_state] < order[current]:
        raise SystemExit("fault result state cannot move backwards")
    if record.get("marker_sha256") != os.environ["MARKER_DIGEST"]:
        raise SystemExit("fault result marker differs from the current contract")
else:
    record = {
        "marker_sha256": os.environ["MARKER_DIGEST"],
        "fail_path": os.environ["FAIL_PATH"],
    }
if os.environ["RUN_ID"]:
    prior_run = record.get("run_id")
    if prior_run not in (None, "", os.environ["RUN_ID"]):
        raise SystemExit("fault result already records another Run")
    record["run_id"] = os.environ["RUN_ID"]
if os.environ["SOURCE_MSG_ID"]:
    record["source_server_msg_id"] = os.environ["SOURCE_MSG_ID"]
record["state"] = next_state
records[os.environ["PHASE"]] = record
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
if ownership is not None:
    os.chown(temporary, ownership.st_uid, ownership.st_gid)
temporary.replace(path)
PY
}

find_existing_run() {
  psql_value "
SELECT id::text,state
FROM agent.runs
WHERE tenant_id='$tenant_id'::uuid
  AND principal_member_id='$authorized_member_id'::uuid
  AND source_channel='openim'
  AND prompt=\$prompt\$$prompt\$prompt\$
ORDER BY created_at,id
LIMIT 2"
}

verify_failed_run() {
  local run_id="$1"
  local row
  row="$(psql_value "
SELECT state,COALESCE(route_status,''),COALESCE(route_operation_id,''),
       model_attempts,(candidate_text IS NULL)::text,(model IS NULL)::text,
       (provider_response_id IS NULL)::text,COALESCE(last_error,''),
       (reply_server_msg_id IS NULL)::text,
       (SELECT count(*) FROM agent.run_citations WHERE run_id=run.id),
       (SELECT count(*) FROM agent.deliveries WHERE run_id=run.id)
FROM agent.runs run
WHERE id='$run_id'::uuid")"
  local state route_status route_operation model_attempts candidate_null model_null
  local provider_null last_error reply_null citation_count delivery_count
  IFS='|' read -r state route_status route_operation model_attempts candidate_null model_null \
    provider_null last_error reply_null citation_count delivery_count <<<"$row"
  [[ "$state" == failed && "$route_status" == selected && \
     "$route_operation" == enterprise.knowledge.search && "$model_attempts" -eq 3 && \
     "$candidate_null" == true && "$model_null" == true && "$provider_null" == true && \
     "$last_error" == "$expected_error" && "$reply_null" == true && \
     "$citation_count" -eq 0 && "$delivery_count" -eq 0 ]] || {
    echo "$phase fault Run did not fail closed under the exact contract: $row" >&2
    return 1
  }

  local events cardinality
  events="$(psql_value "
SELECT count(*) FILTER (WHERE event_type='retry_scheduled'),
       count(*) FILTER (WHERE event_type='failed')
FROM audit.agent_run_events
WHERE run_id='$run_id'::uuid")"
  cardinality="$(psql_value "
SELECT
  (SELECT count(*) FROM agent.runs WHERE id='$run_id'::uuid),
  (SELECT count(*) FROM integration.ingress_messages
    WHERE event_id=(SELECT source_event_id FROM agent.runs WHERE id='$run_id'::uuid)),
  (SELECT count(*) FROM integration.outbox_events
    WHERE event_id=(SELECT source_event_id FROM agent.runs WHERE id='$run_id'::uuid)
      AND state='published')")"
  [[ "$events" == "2|1" && "$cardinality" == "1|1|1" ]] || {
    echo "$phase fault Run retry or ingress cardinality is invalid: events=$events cardinality=$cardinality" >&2
    return 1
  }
}

[[ "$#" -eq 7 && "$phase" =~ ^(embedding|reranker|model)$ ]] || usage
[[ "$(id -u)" -eq 0 ]] || {
  echo "enterprise RAG fault acceptance must run as root" >&2
  exit 1
}
valid_uuid "$tenant_id" && valid_uuid "$authorized_member_id" || usage
[[ "$state_path" == /* && -f "$state_path" && \
   "$manifest_path" == /* && -f "$manifest_path" && \
   "$deploy_root" =~ ^/[A-Za-z0-9_./-]+$ && -r "$platform_env" && \
   "$result_path" == /* && -d "$(dirname "$result_path")" ]] || usage
deploy_root="$(realpath -e -- "$deploy_root")"
[[ "$deploy_root" == /home/qsyy0921/MFL/deploy/* ]] || {
  echo "deployment root is outside the locked Node2 deployment directory" >&2
  exit 1
}
proxy_script="$deploy_root/ops/enterprise-rag-fault-proxy.py"
openim_env="$deploy_root/openim/.env"
[[ -f "$proxy_script" && -f "$openim_env" ]] || usage
docker inspect "$postgres_container" >/dev/null
systemctl cat "$runtime_unit" >/dev/null

[[ "$(platform_value PLATFORM_INTELLIGENCE_URL)" == "http://127.0.0.1:18082" && \
   "$(platform_value PLATFORM_RETRIEVAL_INTELLIGENCE_URL)" == "http://127.0.0.1:18083" ]] || {
  echo "Node2 platform URLs differ from the locked loopback topology" >&2
  exit 1
}

temporary_contract="$(mktemp)"
python3 - "$state_path" "$manifest_path" "$phase" >"$temporary_contract" <<'PY'
import json
import re
import sys
import uuid
from pathlib import Path

state = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
manifest = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
phase = sys.argv[3]

def fail(message: str) -> None:
    raise SystemExit(message)

def uuid_text(value: object, label: str) -> str:
    if not isinstance(value, str):
        fail(f"{label} is not a UUID")
    try:
        parsed = uuid.UUID(value)
    except ValueError:
        fail(f"{label} is not a UUID")
    if parsed.version not in {1, 2, 3, 4, 5}:
        fail(f"{label} uses an unsupported UUID version")
    return str(parsed)

batch_id = manifest.get("batch_id")
if not isinstance(batch_id, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]{7,63}", batch_id):
    fail("manifest batch ID is malformed")
if state.get("schema_version") != 1 or manifest.get("schema_version") != 1 or state.get("batch_id") != batch_id:
    fail("state and manifest contract mismatch")
manifest_documents = manifest.get("documents")
state_documents = state.get("documents")
if not isinstance(manifest_documents, list) or not isinstance(state_documents, list) or \
   len(manifest_documents) != 4 or len(state_documents) != 4:
    fail("fault acceptance requires four bootstrap documents")
manifest_by_format = {item.get("format"): item for item in manifest_documents if isinstance(item, dict)}
state_by_format = {item.get("format"): item for item in state_documents if isinstance(item, dict)}
formats = ("markdown", "text", "pdf", "docx")
if set(manifest_by_format) != set(formats) or set(state_by_format) != set(formats):
    fail("four-format state is missing or duplicated")
values = [batch_id]
for fmt in formats:
    expected, actual = manifest_by_format[fmt], state_by_format[fmt]
    if any(actual.get(key) != expected.get(key) for key in ("title", "filename", "marker")):
        fail(f"{fmt} state differs from the fixture manifest")
    values.extend((
        uuid_text(actual.get("document_id"), f"{fmt} document"),
        uuid_text(actual.get("version_id"), f"{fmt} initial version"),
    ))
web = state.get("web")
if not isinstance(web, dict):
    fail("Web bootstrap state is missing")
values.append(uuid_text(web.get("bootstrap_run_id"), "Web bootstrap Run"))
question = manifest.get("question")
if not isinstance(question, str) or not question.strip() or "\x00" in question:
    fail("fixture question is malformed")
marker = f"RAG-FAULT-{phase}-{batch_id}"
prompt = f"{question.strip()} [{marker}]"
if len(prompt.encode("utf-8")) > 2000:
    fail("fault prompt exceeds the production query bound")
values.extend((marker, prompt))
if any("|" in item or "\r" in item or "\n" in item or "$prompt$" in item for item in values):
    fail("fault acceptance contract contains an unsafe delimiter")
print("|".join(values))
PY

IFS='|' read -r batch_id markdown_document_id markdown_version_id \
  text_document_id text_version_id pdf_document_id pdf_version_id \
  docx_document_id docx_version_id bootstrap_run_id fault_marker prompt \
  <"$temporary_contract"
valid_batch_id "$batch_id" || usage
for value in "$markdown_document_id" "$markdown_version_id" "$text_document_id" \
  "$text_version_id" "$pdf_document_id" "$pdf_version_id" \
  "$docx_document_id" "$docx_version_id" "$bootstrap_run_id"; do
  valid_uuid "$value" || usage
done
[[ "$fault_marker" =~ ^[A-Za-z0-9-]{8,95}$ ]] || usage

case "$phase" in
  embedding)
    proxy_port=18084
    upstream_url="http://127.0.0.1:18083"
    fail_path="/v1/embeddings"
    runtime_override="PLATFORM_RETRIEVAL_INTELLIGENCE_URL=http://127.0.0.1:$proxy_port"
    expected_error="retrieve authorized knowledge: embed retrieval query: embedding service returned HTTP 503"
    ;;
  reranker)
    proxy_port=18084
    upstream_url="http://127.0.0.1:18083"
    fail_path="/v1/rerank"
    runtime_override="PLATFORM_RETRIEVAL_INTELLIGENCE_URL=http://127.0.0.1:$proxy_port"
    expected_error="retrieve authorized knowledge: rerank authorized knowledge evidence: required reranker returned HTTP 503"
    ;;
  model)
    proxy_port=18085
    upstream_url="http://127.0.0.1:18082"
    fail_path="/v1/candidates"
    runtime_override="PLATFORM_INTELLIGENCE_URL=http://127.0.0.1:$proxy_port"
    expected_error="intelligence worker returned status 503"
    ;;
esac
fault_id="rag-fault-$phase-$batch_id"
marker_digest="$(printf '%s' "$fault_marker" | sha256sum | awk '{print $1}')"

target_ids_sql="'$markdown_document_id'::uuid,'$text_document_id'::uuid,'$pdf_document_id'::uuid,'$docx_document_id'::uuid"
preflight="$(psql_value "
SELECT
  (SELECT count(*) FROM knowledge.documents
    WHERE tenant_id='$tenant_id'::uuid
      AND id IN ($target_ids_sql)
      AND status='active'
      AND current_version_id IN (
        '$markdown_version_id'::uuid,'$text_version_id'::uuid,
        '$pdf_version_id'::uuid,'$docx_version_id'::uuid)),
  (SELECT count(*) FROM knowledge.document_versions
    WHERE tenant_id='$tenant_id'::uuid
      AND id IN (
        '$markdown_version_id'::uuid,'$text_version_id'::uuid,
        '$pdf_version_id'::uuid,'$docx_version_id'::uuid)
      AND status='published' AND ingestion_state='indexed'),
  (SELECT count(*) FROM authz.document_grants
    WHERE tenant_id='$tenant_id'::uuid
      AND member_id='$authorized_member_id'::uuid
      AND document_id IN ($target_ids_sql)),
  (SELECT count(*) FROM identity.identity_links
    WHERE tenant_id='$tenant_id'::uuid
      AND member_id='$authorized_member_id'::uuid
      AND provisioning_state='ready'),
  (SELECT count(*) FROM agent.runs
    WHERE id='$bootstrap_run_id'::uuid
      AND tenant_id='$tenant_id'::uuid
      AND principal_member_id='$authorized_member_id'::uuid
      AND state='succeeded'
      AND model='gpt-5.6-terra'),
  (SELECT count(DISTINCT document_id) FROM agent.run_citations
    WHERE run_id='$bootstrap_run_id'::uuid
      AND document_id IN ($target_ids_sql))")"
[[ "$preflight" == "4|4|4|1|1|4" ]] || {
  echo "fault acceptance requires the verified four-document Web bootstrap state: $preflight" >&2
  exit 1
}

sender="$(psql_value "
SELECT openim_user_id
FROM identity.identity_links
WHERE tenant_id='$tenant_id'::uuid
  AND member_id='$authorized_member_id'::uuid
  AND provisioning_state='ready'")"
[[ -n "$sender" && "$sender" != *"|"* && "$sender" != *$'\n'* ]] || {
  echo "authorized OpenIM identity is missing or ambiguous" >&2
  exit 1
}

result_row="$(read_result_state)"
IFS='|' read -r result_state recorded_run_id <<<"$result_row"
if [[ "$result_state" == completed ]]; then
  valid_uuid "$recorded_run_id" || {
    echo "completed fault result has no valid Run ID" >&2
    exit 1
  }
  if [[ -e "$marker_path" || -e "$proxy_unit_path" || -e "$dropin_path" ]]; then
    marker_matches
    fault_active=true
    verify_fault_runtime
    restore_fault_runtime
  fi
  verify_direct_runtime
  verify_failed_run "$recorded_run_id"
  echo "enterprise_rag_fault=$phase run_id=$recorded_run_id state=failed_closed"
  exit 0
fi
if [[ "$result_state" == missing ]]; then
  write_result_state prepared
  result_state=prepared
fi

start_fault_runtime
if [[ "$result_state" == prepared ]]; then
  write_result_state active
  result_state=active
fi

existing="$(find_existing_run)"
existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
[[ "$existing_count" -le 1 ]] || {
  echo "fault prompt already has duplicate Runs; no message was sent" >&2
  exit 1
}
if [[ "$result_state" == active && "$existing_count" -ne 0 ]]; then
  echo "fault Run exists without a persisted send-prepared intent; no message was sent" >&2
  exit 1
fi

source_server_msg_id=""
if [[ "$result_state" == send_prepared && "$existing_count" -eq 0 ]]; then
  restore_on_exit=false
  for _ in $(seq 1 30); do
    existing="$(find_existing_run)"
    existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
    [[ "$existing_count" -gt 0 ]] && break
    sleep 2
  done
  [[ "$existing_count" -eq 1 ]] || {
    echo "a prior OpenIM fault send remains uncertain; the script did not retry" >&2
    exit 1
  }
fi

if [[ "$existing_count" -eq 0 ]]; then
  secret="$(sed -n 's/^OPENIM_SECRET=//p' "$openim_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
  [[ -n "$secret" ]] || {
    echo "OpenIM server secret is missing" >&2
    exit 1
  }
  admin_response="$(curl -fsS --max-time 10 -X POST \
    http://127.0.0.1:12002/auth/get_admin_token \
    -H 'Content-Type: application/json' \
    -H "operationID: enterprise-rag-fault-admin-$batch_id" \
    --data "{\"secret\":\"$secret\",\"userID\":\"imAdmin\"}")"
  admin_token="$(RESPONSE="$admin_response" python3 -c \
    'import json,os; value=json.loads(os.environ["RESPONSE"]); assert value.get("errCode")==0,value; print(value["data"]["token"])')"
  unset admin_response secret

  request_file="$(mktemp)"
  PROMPT="$prompt" SENDER="$sender" BATCH_ID="$batch_id" PHASE="$phase" \
    python3 - <<'PY' >"$request_file"
import json
import os

print(json.dumps({
    "recvID": "imAdmin",
    "sendID": os.environ["SENDER"],
    "groupID": "",
    "senderNickname": "Enterprise RAG Fault E2E",
    "senderPlatformID": 5,
    "clientMsgID": f"enterprise-rag-fault-{os.environ['BATCH_ID']}-{os.environ['PHASE']}",
    "content": {"content": "@Agent " + os.environ["PROMPT"]},
    "contentType": 101,
    "sessionType": 1,
    "notOfflinePush": True,
    "ex": f"enterprise-rag-fault:{os.environ['BATCH_ID']}:{os.environ['PHASE']}",
}, separators=(",", ":"), ensure_ascii=False))
PY

  # Persist uncertainty only after every local prerequisite has succeeded and
  # immediately before the external message side effect.
  write_result_state send_prepared
  result_state=send_prepared
  restore_on_exit=false

  set +e
  response="$(curl -fsS --max-time 15 -X POST \
    http://127.0.0.1:12002/msg/send_msg \
    -H 'Content-Type: application/json' \
    -H "token: $admin_token" \
    -H "operationID: enterprise-rag-fault-$batch_id-$phase" \
    --data-binary "@$request_file")"
  curl_status=$?
  set -e
  rm -f -- "$request_file"
  request_file=""
  unset admin_token

  if [[ "$curl_status" -eq 0 ]]; then
    source_server_msg_id="$(RESPONSE="$response" python3 -c \
      'import json,os; value=json.loads(os.environ["RESPONSE"]); assert value.get("errCode")==0,value; print(value["data"]["serverMsgID"])')"
  fi
  unset response

  if [[ "$curl_status" -ne 0 ]]; then
    for _ in $(seq 1 30); do
      existing="$(find_existing_run)"
      existing_count="$(printf '%s\n' "$existing" | sed '/^$/d' | wc -l)"
      [[ "$existing_count" -gt 0 ]] && break
      sleep 2
    done
    [[ "$existing_count" -eq 1 ]] || {
      echo "OpenIM fault send outcome is uncertain and no durable Run was found; the script did not retry" >&2
      exit 1
    }
  fi
fi

run_row=""
for _ in $(seq 1 360); do
  run_rows="$(find_existing_run)"
  run_count="$(printf '%s\n' "$run_rows" | sed '/^$/d' | wc -l)"
  [[ "$run_count" -le 1 ]] || {
    echo "fault prompt produced duplicate Runs" >&2
    exit 1
  }
  if [[ "$run_count" -eq 1 ]]; then
    run_row="$run_rows"
    [[ "$run_row" == *"|failed" || "$run_row" == *"|succeeded" ]] && break
  fi
  sleep 2
done
[[ -n "$run_row" ]] || {
  echo "fault message did not create a terminal Agent Run" >&2
  exit 1
}
IFS='|' read -r run_id run_state <<<"$run_row"
valid_uuid "$run_id" || {
  echo "fault Run ID is malformed" >&2
  exit 1
}
restore_on_exit=true
verify_failed_run "$run_id"

proxy_status="$(curl -fsS --max-time 5 "http://127.0.0.1:$proxy_port/__fault/status")"
proxy_counts="$(STATUS="$proxy_status" FAULT_ID="$fault_id" FAIL_PATH="$fail_path" \
  MARKER_DIGEST="$marker_digest" PHASE="$phase" python3 - <<'PY'
import json
import os

value = json.loads(os.environ["STATUS"])
if value.get("schema_version") != 1 or value.get("fault_id") != os.environ["FAULT_ID"] or \
   value.get("fail_path") != os.environ["FAIL_PATH"] or \
   value.get("match_sha256") != os.environ["MARKER_DIGEST"]:
    raise SystemExit("fault proxy status does not match the active contract")
rejected = value.get("rejected_requests")
forwarded = value.get("forwarded_requests")
upstream_failures = value.get("upstream_failures")
if rejected != 3 or upstream_failures != 0:
    raise SystemExit("fault proxy did not reject exactly the three bounded attempts")
expected_forwarded = {
    "embedding": 0,
    "reranker": 3,
    "model": 1,
}[os.environ["PHASE"]]
if forwarded != expected_forwarded:
    raise SystemExit("fault proxy forwarding count differs from the phase contract")
print(f"{rejected}|{forwarded}|{upstream_failures}")
PY
)"
unset proxy_status
[[ "$proxy_counts" =~ ^3\|(0|1|3)\|0$ ]] || {
  echo "fault proxy counts are malformed: $proxy_counts" >&2
  exit 1
}

write_result_state completed "$run_id" "$source_server_msg_id"
restore_fault_runtime
restore_on_exit=false

echo "enterprise_rag_fault=$phase run_id=$run_id state=failed_closed proxy=$proxy_counts"
