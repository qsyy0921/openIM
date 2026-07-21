#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
shift || true
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
platform_env="${OPENIM_PLATFORM_ENV:-/etc/openim-platform/platform.env}"
ingress_unit="${OPENIM_TELEGRAM_INGRESS_UNIT:-openim-telegram-ingress.service}"
delivery_unit="${OPENIM_AGENT_DELIVERY_UNIT:-openim-agent-delivery.service}"

usage() {
  cat >&2 <<'EOF'
usage:
  accept-node2-telegram.sh snapshot
  accept-node2-telegram.sh bind BASELINE_UPDATE_ID TENANT_ID MEMBER_ID
  accept-node2-telegram.sh verify BASELINE_UPDATE_ID TENANT_ID MEMBER_ID [MIN_CITATIONS]
  accept-node2-telegram.sh cleanup TELEGRAM_USER_ID TELEGRAM_CHAT_ID TENANT_ID MEMBER_ID
EOF
  exit 2
}

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 \
    -U platform -d platform -c "$1"
}

require_root() {
  [[ "$(id -u)" -eq 0 ]] || {
    echo "run this mode as root" >&2
    exit 1
  }
}

require_runtime() {
  systemctl is-active --quiet "$ingress_unit"
  systemctl is-active --quiet "$delivery_unit"
  docker inspect "$postgres_container" >/dev/null
}

valid_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_nonnegative_integer() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

valid_telegram_user_id() {
  [[ "$1" =~ ^[1-9][0-9]{4,19}$ ]]
}

valid_telegram_chat_id() {
  [[ "$1" =~ ^-?[1-9][0-9]{4,19}$ ]]
}

snapshot() {
  require_runtime
  local row
  row="$(psql_value "SELECT telegram_bot_id,next_update_id FROM channel.telegram_offsets")"
  [[ "$row" =~ ^[0-9]+\|[0-9]+$ ]] || {
    echo "Telegram offset is missing or ambiguous" >&2
    exit 1
  }
  echo "telegram_offset=$row"
}

bind_latest_unbound() {
  require_root
  require_runtime
  local baseline="$1" tenant_id="$2" member_id="$3"
  valid_nonnegative_integer "$baseline" || usage
  valid_uuid "$tenant_id" || usage
  valid_uuid "$member_id" || usage
  [[ -r "$platform_env" ]] || {
    echo "platform environment is not readable" >&2
    exit 1
  }

  local rows="" count=0
  for _ in $(seq 1 90); do
    rows="$(psql_value "
SELECT source_offset,server_msg_id,sender_id
FROM integration.ingress_rejections
WHERE source_topic='telegram.getUpdates'
  AND reason='telegram_unbound_identity'
  AND source_offset >= $baseline
ORDER BY source_offset
LIMIT 2")"
    count="$(printf '%s\n' "$rows" | sed '/^$/d' | wc -l)"
    if [[ "$count" -ge 1 ]]; then
      break
    fi
    sleep 2
  done
  [[ "$count" -eq 1 ]] || {
    echo "expected exactly one unbound Telegram update after baseline, found $count" >&2
    exit 1
  }

  local update_id server_msg_id sender_id chat_id user_id message_id
  IFS='|' read -r update_id server_msg_id sender_id <<<"$rows"
  IFS=':' read -r _ chat_id message_id <<<"$server_msg_id"
  IFS=':' read -r _ user_id <<<"$sender_id"
  valid_nonnegative_integer "$update_id" || {
    echo "Telegram update ID is malformed" >&2
    exit 1
  }
  valid_telegram_user_id "$user_id" || {
    echo "Telegram user ID is malformed" >&2
    exit 1
  }
  valid_telegram_chat_id "$chat_id" || {
    echo "Telegram chat ID is malformed" >&2
    exit 1
  }
  valid_nonnegative_integer "$message_id" || {
    echo "Telegram message ID is malformed" >&2
    exit 1
  }

  local pid binary bin_dir runtime_user database_url dependency_timeout
  pid="$(systemctl show -p MainPID --value "$ingress_unit")"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || {
    echo "Telegram ingress has no main process" >&2
    exit 1
  }
  binary="$(readlink -f "/proc/$pid/exe")"
  bin_dir="$(dirname "$binary")"
  [[ -x "$bin_dir/telegram-admin" ]] || {
    echo "telegram-admin is missing from the active release" >&2
    exit 1
  }
  runtime_user="$(systemctl show -p User --value "$ingress_unit")"
  id "$runtime_user" >/dev/null 2>&1 || {
    echo "Telegram runtime user is invalid" >&2
    exit 1
  }
  database_url="$(sed -n 's/^PLATFORM_DATABASE_URL=//p' "$platform_env" | tail -1 | tr -d '\r')"
  dependency_timeout="$(sed -n 's/^PLATFORM_DEPENDENCY_TIMEOUT=//p' "$platform_env" | tail -1 | tr -d '\r')"
  [[ "$database_url" =~ ^postgres(ql)?://[^[:space:]]+$ && -n "$dependency_timeout" ]] || {
    unset database_url
    echo "Telegram admin dependencies are missing" >&2
    exit 1
  }

  runuser -u "$runtime_user" -- env \
    PLATFORM_DATABASE_URL="$database_url" \
    PLATFORM_DEPENDENCY_TIMEOUT="$dependency_timeout" \
    "$bin_dir/telegram-admin" \
      -user-id "$user_id" -chat-id "$chat_id" \
      -tenant-id "$tenant_id" -member-id "$member_id" -session-type 1
  unset database_url

  local next_offset
  next_offset="$(psql_value "SELECT next_update_id FROM channel.telegram_offsets")"
  valid_nonnegative_integer "$next_offset" || {
    echo "Telegram offset did not advance after binding bootstrap" >&2
    exit 1
  }
  echo "telegram_binding=ready"
  echo "telegram_user_id=$user_id"
  echo "telegram_chat_id=$chat_id"
  echo "telegram_verify_baseline=$next_offset"
}

verify_round_trip() {
  require_runtime
  local baseline="$1" tenant_id="$2" member_id="$3" minimum_citations="${4:-0}"
  valid_nonnegative_integer "$baseline" || usage
  valid_uuid "$tenant_id" || usage
  valid_uuid "$member_id" || usage
  valid_nonnegative_integer "$minimum_citations" || usage

  local row=""
  for _ in $(seq 1 150); do
    row="$(psql_value "
SELECT i.source_offset,i.event_id::text,o.state,
       COALESCE(r.id::text,''),COALESCE(r.state,''),COALESCE(r.model,''),
       COALESCE(r.provider_response_id,''),COALESCE(d.id::text,''),
       COALESCE(d.state,''),COALESCE(d.external_message_id,''),
       COALESCE((SELECT count(*) FROM agent.run_citations c WHERE c.run_id=r.id),0)
FROM integration.ingress_messages i
JOIN integration.outbox_events o ON o.event_id=i.event_id
LEFT JOIN agent.runs r ON r.source_event_id=i.event_id::text
LEFT JOIN agent.deliveries d ON d.run_id=r.id
WHERE i.source_channel='telegram'
  AND i.principal_member_id='$member_id'::uuid
  AND i.tenant_id='$tenant_id'::uuid
  AND i.source_offset >= $baseline
ORDER BY i.source_offset
LIMIT 1")"
    if [[ "$row" == *"|failed|"* || "$row" == *"|uncertain|"* ]]; then
      echo "Telegram Agent Run or delivery entered a terminal failure: $row" >&2
      exit 1
    fi
    if [[ "$row" == *"|published|"*"|succeeded|"*"|sent|"* ]]; then
      break
    fi
    sleep 2
  done
  [[ -n "$row" ]] || {
    echo "Telegram update was not durably accepted" >&2
    exit 1
  }

  local update_id event_id outbox_state run_id run_state model provider_id
  local delivery_id delivery_state external_message_id citation_count
  IFS='|' read -r update_id event_id outbox_state run_id run_state model provider_id \
    delivery_id delivery_state external_message_id citation_count <<<"$row"
  [[ "$outbox_state" == published && "$run_state" == succeeded && \
     "$model" == gpt-5.6-luna && -n "$provider_id" && \
     "$delivery_state" == sent && -n "$external_message_id" && \
     "$citation_count" -ge "$minimum_citations" ]] || {
    echo "Telegram round trip did not meet the acceptance contract: $row" >&2
    exit 1
  }

  local cardinality
  cardinality="$(psql_value "
SELECT (SELECT count(*) FROM integration.ingress_messages WHERE event_id='$event_id'),
       (SELECT count(*) FROM agent.runs WHERE source_event_id='$event_id'),
       (SELECT count(*) FROM agent.deliveries WHERE run_id='$run_id'::uuid)")"
  [[ "$cardinality" == "1|1|1" ]] || {
    echo "Telegram idempotency cardinality is invalid: $cardinality" >&2
    exit 1
  }
  echo "telegram_ingress=accepted update_id=$update_id"
  echo "telegram_outbox=published event_id=$event_id"
  echo "telegram_agent=succeeded run_id=$run_id citations=$citation_count"
  echo "telegram_delivery=sent delivery_id=$delivery_id external_message_id=$external_message_id"
  echo "telegram_idempotency=1|1|1"
}

cleanup_binding() {
  require_root
  require_runtime
  local user_id="$1" chat_id="$2" tenant_id="$3" member_id="$4"
  valid_telegram_user_id "$user_id" || usage
  valid_telegram_chat_id "$chat_id" || usage
  valid_uuid "$tenant_id" || usage
  valid_uuid "$member_id" || usage

  local fixture
  fixture="$(psql_value "
SELECT (SELECT count(*) FROM channel.telegram_principals
         WHERE telegram_user_id=$user_id AND tenant_id='$tenant_id'::uuid
           AND member_id='$member_id'::uuid),
       (SELECT count(*) FROM channel.telegram_chats
         WHERE telegram_chat_id=$chat_id AND tenant_id='$tenant_id'::uuid
           AND session_type=1)")"
  [[ "$fixture" == "1|1" ]] || {
    echo "Telegram binding does not match the isolated fixture: $fixture" >&2
    exit 1
  }
  psql_value "
BEGIN;
DELETE FROM channel.telegram_principals
 WHERE telegram_user_id=$user_id AND tenant_id='$tenant_id'::uuid
   AND member_id='$member_id'::uuid;
DELETE FROM channel.telegram_chats
 WHERE telegram_chat_id=$chat_id AND tenant_id='$tenant_id'::uuid
   AND session_type=1;
COMMIT;" >/dev/null
  echo "telegram_binding=cleaned"
}

case "$mode" in
  snapshot)
    [[ "$#" -eq 0 ]] || usage
    snapshot
    ;;
  bind)
    [[ "$#" -eq 3 ]] || usage
    bind_latest_unbound "$@"
    ;;
  verify)
    [[ "$#" -ge 3 && "$#" -le 4 ]] || usage
    verify_round_trip "$@"
    ;;
  cleanup)
    [[ "$#" -eq 4 ]] || usage
    cleanup_binding "$@"
    ;;
  *) usage ;;
esac
