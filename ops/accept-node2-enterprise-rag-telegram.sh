#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
shift || true

postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
platform_env="${OPENIM_PLATFORM_ENV:-/etc/openim-platform/platform.env}"
telegram_credential="${OPENIM_TELEGRAM_CREDENTIAL:-/etc/openim-platform/credentials/telegram-bot-token}"
telegram_proxy="${OPENIM_TELEGRAM_PROXY_URL:-http://127.0.0.1:7893}"

usage() {
  cat >&2 <<'EOF'
usage:
  accept-node2-enterprise-rag-telegram.sh prompt PHASE STATE_JSON MANIFEST_JSON
  accept-node2-enterprise-rag-telegram.sh record-bootstrap BATCH_ID CHAT_ID MESSAGE_ID RESULT_JSON
  accept-node2-enterprise-rag-telegram.sh verify PHASE BASELINE_UPDATE_ID STATE_JSON MANIFEST_JSON TENANT_ID AUTHORIZED_MEMBER_ID DENIED_MEMBER_ID RESULT_JSON
  accept-node2-enterprise-rag-telegram.sh cleanup-contract RESULT_JSON
  accept-node2-enterprise-rag-telegram.sh cleanup-messages RESULT_JSON

PHASE is one of: authorized, denied, revoked, version
EOF
  exit 2
}

valid_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_nonnegative_integer() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

valid_telegram_chat_id() {
  [[ "$1" =~ ^-?[1-9][0-9]{4,19}$ ]]
}

valid_telegram_message_id() {
  [[ "$1" =~ ^[1-9][0-9]{0,18}$ ]]
}

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 \
    -U platform -d platform -c "$1"
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
contract_parser="$script_dir/enterprise-rag-channel-contract.py"
telegram_cleanup="$script_dir/enterprise-rag-telegram-cleanup.py"

load_contract() {
  local phase="$1"
  local state_path="$2"
  local manifest_path="$3"
  [[ "$phase" =~ ^(authorized|denied|revoked|version)$ ]] || usage
  [[ -f "$state_path" && -f "$manifest_path" && -f "$contract_parser" ]] || usage
  python3 "$contract_parser" telegram "$phase" "$state_path" "$manifest_path"
}

case "$mode" in
  prompt)
    [[ "$#" -eq 3 ]] || usage
    contract="$(load_contract "$1" "$2" "$3")"
    IFS='|' read -r _ prompt _ <<<"$contract"
    printf '%s\n' "$prompt"
    exit 0
    ;;
  record-bootstrap)
    [[ "$#" -eq 4 ]] || usage
    batch_id="$1"
    chat_id="$2"
    message_id="$3"
    result_path="$4"
    [[ "$batch_id" =~ ^[A-Za-z0-9][A-Za-z0-9-]{7,63}$ ]] || usage
    valid_telegram_chat_id "$chat_id" && valid_telegram_message_id "$message_id" || usage
    [[ "$result_path" == /* && -d "$(dirname "$result_path")" ]] || usage
    RESULT_PATH="$result_path" BATCH_ID="$batch_id" CHAT_ID="$chat_id" \
      MESSAGE_ID="$message_id" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
value = {"schema_version": 1, "batch_id": os.environ["BATCH_ID"], "telegram": {}}
if path.exists():
    value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("Telegram result file belongs to another acceptance batch")
channel = value.setdefault("telegram", {})
bootstraps = channel.setdefault("bootstraps", [])
record = {
    "chat_id": int(os.environ["CHAT_ID"]),
    "message_id": int(os.environ["MESSAGE_ID"]),
}
if record not in bootstraps:
    if any(item.get("message_id") == record["message_id"] for item in bootstraps):
        raise SystemExit("Telegram bootstrap message ID is already bound to another chat")
    bootstraps.append(record)
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
PY
    echo "enterprise_rag_telegram_bootstrap=recorded message_id=$message_id"
    exit 0
    ;;
  verify)
    [[ "$#" -eq 8 ]] || usage
    ;;
  cleanup-contract)
    [[ "$#" -eq 1 && -f "$telegram_cleanup" ]] || usage
    result_path="$1"
    [[ "$result_path" == /* && -f "$result_path" ]] || usage
    python3 "$telegram_cleanup" contract "$result_path"
    exit 0
    ;;
  cleanup-messages)
    [[ "$#" -eq 1 ]] || usage
    [[ "$(id -u)" -eq 0 ]] || {
      echo "cleanup-messages must run as root" >&2
      exit 1
    }
    result_path="$1"
    [[ "$result_path" == /* && -f "$result_path" ]] || usage
    [[ -f "$telegram_cleanup" && -r "$platform_env" && -r "$telegram_credential" ]] || {
      echo "Telegram cleanup runtime configuration is missing" >&2
      exit 1
    }
    [[ "$telegram_proxy" =~ ^http://127\.0\.0\.1:[0-9]{1,5}$ ]] || {
      echo "Telegram cleanup proxy must be loopback-only" >&2
      exit 1
    }
    python3 "$telegram_cleanup" execute "$result_path" "$platform_env" \
      "$telegram_credential" "$telegram_proxy"
    exit 0
    ;;
  *)
    usage
    ;;
esac

phase="$1"
baseline="$2"
state_path="$3"
manifest_path="$4"
tenant_id="$5"
authorized_member_id="$6"
denied_member_id="$7"
result_path="$8"

valid_nonnegative_integer "$baseline" || usage
valid_uuid "$tenant_id" && valid_uuid "$authorized_member_id" && valid_uuid "$denied_member_id" || usage
[[ "$result_path" == /* && -d "$(dirname "$result_path")" ]] || usage
docker inspect "$postgres_container" >/dev/null

contract="$(load_contract "$phase" "$state_path" "$manifest_path")"
IFS='|' read -r batch_id prompt markdown_document_id text_document_id pdf_document_id \
  docx_document_id markdown_initial_version_id markdown_next_version_id \
  markdown_marker markdown_next_marker <<<"$contract"
for value in "$markdown_document_id" "$text_document_id" "$pdf_document_id" \
  "$docx_document_id" "$markdown_initial_version_id"; do
  valid_uuid "$value" || usage
done
if [[ "$phase" == version ]]; then
  valid_uuid "$markdown_next_version_id" || usage
elif [[ -n "$markdown_next_version_id" ]]; then
  valid_uuid "$markdown_next_version_id" || usage
fi

case "$phase" in
  authorized|revoked|version) member_id="$authorized_member_id" ;;
  denied) member_id="$denied_member_id" ;;
esac

target_ids_sql="'$markdown_document_id'::uuid,'$text_document_id'::uuid,'$pdf_document_id'::uuid,'$docx_document_id'::uuid"
row=""
for _ in $(seq 1 180); do
  rows="$(psql_value "
SELECT ingress.source_offset,ingress.server_msg_id,ingress.event_id,
       outbox.state,run.id::text,run.state,COALESCE(run.model,''),
       (run.provider_response_id IS NOT NULL)::text,
       delivery.id::text,delivery.state,COALESCE(delivery.external_message_id,'')
FROM integration.ingress_messages ingress
JOIN integration.outbox_events outbox ON outbox.event_id=ingress.event_id
JOIN agent.runs run ON run.source_event_id=ingress.event_id
JOIN agent.deliveries delivery ON delivery.run_id=run.id
WHERE ingress.source_channel='telegram'
  AND ingress.tenant_id='$tenant_id'::uuid
  AND ingress.principal_member_id='$member_id'::uuid
  AND ingress.source_offset >= $baseline
  AND run.prompt=\$prompt\$$prompt\$prompt\$
ORDER BY ingress.source_offset,run.id
LIMIT 2")"
  row_count="$(printf '%s\n' "$rows" | sed '/^$/d' | wc -l)"
  [[ "$row_count" -le 1 ]] || {
    echo "Telegram acceptance prompt produced duplicate durable Runs" >&2
    exit 1
  }
  if [[ "$row_count" -eq 1 ]]; then
    row="$rows"
    [[ "$row" == *"|failed|"* || "$row" == *"|uncertain|"* ]] && {
      echo "Telegram acceptance entered a terminal failure: $row" >&2
      exit 1
    }
    [[ "$row" == *"|published|"*"|succeeded|"*"|sent|"* ]] && break
  fi
  sleep 2
done
[[ -n "$row" ]] || {
  echo "the exact Telegram acceptance prompt was not durably processed" >&2
  exit 1
}

IFS='|' read -r update_id source_server_msg_id event_id outbox_state run_id run_state \
  model has_provider delivery_id delivery_state external_message_id <<<"$row"
[[ "$outbox_state" == published && "$run_state" == succeeded && \
   "$delivery_state" == sent && -n "$external_message_id" ]] || {
  echo "Telegram acceptance did not complete its real delivery round trip" >&2
  exit 1
}

cardinality="$(psql_value "
SELECT
  (SELECT count(*) FROM integration.ingress_messages WHERE event_id='$event_id'),
  (SELECT count(*) FROM integration.outbox_events WHERE event_id='$event_id'),
  (SELECT count(*) FROM agent.runs WHERE id='$run_id'::uuid),
  (SELECT count(*) FROM agent.deliveries WHERE run_id='$run_id'::uuid)")"
[[ "$cardinality" == "1|1|1|1" ]] || {
  echo "Telegram acceptance idempotency cardinality is invalid: $cardinality" >&2
  exit 1
}

evidence="$(psql_value "
SELECT
  (SELECT count(*) FROM agent.run_citations citation
    WHERE citation.run_id=run.id AND citation.document_id IN ($target_ids_sql)),
  (SELECT count(*)
     FROM agent.tool_calls call
     CROSS JOIN LATERAL jsonb_array_elements(COALESCE(call.result,'[]'::jsonb)) item
    WHERE call.run_id=run.id
      AND item->>'document_id' IN (
        '$markdown_document_id','$text_document_id',
        '$pdf_document_id','$docx_document_id'))
FROM agent.runs run
WHERE run.id='$run_id'::uuid")"
IFS='|' read -r target_citations target_results <<<"$evidence"

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
      echo "authorized Telegram Run did not cite all four initial documents: evidence=$evidence coverage=$coverage" >&2
      exit 1
    }
    ;;
  denied|revoked)
    [[ "$target_citations" -eq 0 && "$target_results" -eq 0 ]] || {
      echo "$phase Telegram Run exposed target evidence: $evidence" >&2
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
      echo "versioned Telegram Run retained old evidence or omitted current evidence: $version_contract" >&2
      exit 1
    }
    ;;
esac

RESULT_PATH="$result_path" BATCH_ID="$batch_id" PHASE="$phase" \
  UPDATE_ID="$update_id" SOURCE_MSG_ID="$source_server_msg_id" EVENT_ID="$event_id" \
  RUN_ID="$run_id" DELIVERY_ID="$delivery_id" EXTERNAL_MSG_ID="$external_message_id" \
  python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["RESULT_PATH"])
value = {"schema_version": 1, "batch_id": os.environ["BATCH_ID"], "telegram": {}}
if path.exists():
    value = json.loads(path.read_text(encoding="utf-8"))
if value.get("schema_version") != 1 or value.get("batch_id") != os.environ["BATCH_ID"]:
    raise SystemExit("Telegram result file belongs to another acceptance batch")
channel = value.setdefault("telegram", {})
record = {
    "update_id": int(os.environ["UPDATE_ID"]),
    "source_server_msg_id": os.environ["SOURCE_MSG_ID"],
    "event_id": os.environ["EVENT_ID"],
    "run_id": os.environ["RUN_ID"],
    "delivery_id": os.environ["DELIVERY_ID"],
    "external_message_id": os.environ["EXTERNAL_MSG_ID"],
}
existing = channel.get(os.environ["PHASE"])
if existing is not None and existing.get("run_id") != record["run_id"]:
    raise SystemExit("Telegram phase already records another Run")
channel[os.environ["PHASE"]] = record
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
PY

echo "enterprise_rag_telegram=$phase update_id=$update_id run_id=$run_id delivery_id=$delivery_id"
