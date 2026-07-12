#!/usr/bin/env bash
set -euo pipefail

action="${1:-}"
device_id="${2:-}"
platform_id="${3:-3}"
postgres_container="openim-platform-local-postgres-1"

[[ "$action" == "add" || "$action" == "remove" ]] || { echo "device E2E action must be add or remove" >&2; exit 1; }
[[ "$device_id" =~ ^node2-e2e-[A-Za-z0-9_-]{1,96}$ ]] || { echo "device E2E ID is invalid" >&2; exit 1; }
[[ "$platform_id" =~ ^[0-9]+$ ]] && (( platform_id >= 1 && platform_id <= 11 && platform_id != 10 )) || { echo "platform ID is invalid" >&2; exit 1; }

member_id="$(docker exec "$postgres_container" psql -At -v ON_ERROR_STOP=1 -U platform -d platform -c \
  "SELECT member_id FROM identity.member_devices WHERE device_id = 'local-browser' AND platform_id = 5 AND status = 'active'")"
[[ "$member_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "active local-browser member was not found" >&2; exit 1; }

if [[ "$action" == "add" ]]; then
  docker exec "$postgres_container" psql -v ON_ERROR_STOP=1 -U platform -d platform -c \
    "INSERT INTO identity.member_devices(member_id, device_id, platform_id, status) VALUES ('$member_id'::uuid, '$device_id', $platform_id, 'active') ON CONFLICT (member_id, device_id, platform_id) DO UPDATE SET status = 'active', updated_at = now()" >/dev/null
  echo "node2_e2e_device=active"
else
  docker exec "$postgres_container" psql -v ON_ERROR_STOP=1 -U platform -d platform -c \
    "DELETE FROM identity.member_devices WHERE member_id = '$member_id'::uuid AND device_id = '$device_id' AND platform_id = $platform_id" >/dev/null
  echo "node2_e2e_device=removed"
fi
echo "device_id=$device_id"
echo "platform_id=$platform_id"
