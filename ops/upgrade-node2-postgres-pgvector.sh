#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/qsyy0921/MFL/deploy/node2-native}"
compose_file="$deploy_root/platform/compose.yaml"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
backup_root="${OPENIM_PLATFORM_BACKUP_ROOT:-/home/qsyy0921/MFL/backups}"
target_image='pgvector/pgvector:0.8.5-pg17-bookworm@sha256:d2ef61f42ef767baa5a1475393303cc235bcd92febd9d7014eddb48b41f3bad0'

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ -r "$compose_file" ]] || {
  echo "platform compose file is missing" >&2
  exit 1
}
docker inspect "$postgres_container" >/dev/null 2>&1 || {
  echo "platform PostgreSQL container is missing" >&2
  exit 1
}
docker image inspect "$target_image" >/dev/null 2>&1 || {
  echo "locked pgvector image is not pre-provisioned" >&2
  exit 1
}
docker exec "$postgres_container" pg_isready -U platform -d platform >/dev/null

server_major="$(docker exec "$postgres_container" psql -At -U platform -d platform \
  -c "SELECT current_setting('server_version_num')::integer / 10000")"
[[ "$server_major" == "17" ]] || {
  echo "in-place image switch requires PostgreSQL major 17" >&2
  exit 1
}

current_image="$(docker inspect --format '{{.Config.Image}}' "$postgres_container")"
if [[ "$current_image" == "$target_image" ]]; then
  available_version="$(docker exec "$postgres_container" psql -At -U platform -d platform \
    -c "SELECT default_version FROM pg_available_extensions WHERE name = 'vector'")"
  [[ "$available_version" == "0.8.5" ]] || {
    echo "locked pgvector extension is unavailable in the active image" >&2
    exit 1
  }
  echo "node2_postgres_pgvector_image=ready"
  exit 0
fi

install -d -m 0700 "$backup_root"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_file="$backup_root/platform-pre-pgvector-$timestamp.dump"
compose_backup="$(mktemp)"
cp -- "$compose_file" "$compose_backup"
chmod 0600 "$compose_backup"

runtime_services=(
  openim-platform-api.service
  openim-platform-ingress.service
  openim-agent-runtime.service
  openim-agent-delivery.service
  openim-action-executor.service
  openim-memory-projector.service
  openim-memory-extractor.service
  openim-proactive-runtime.service
  openim-knowledge-ingestion.service
)
active_services=()
for service in "${runtime_services[@]}"; do
  if systemctl is-active --quiet "$service"; then
    active_services+=("$service")
  fi
done

restore_previous_image() {
  cp -- "$compose_backup" "$compose_file"
  docker compose -f "$compose_file" up -d --no-deps --force-recreate --pull never postgres >/dev/null
  for _ in $(seq 1 60); do
    docker exec "$postgres_container" pg_isready -U platform -d platform >/dev/null 2>&1 && break
    sleep 2
  done
  if ((${#active_services[@]} > 0)); then
    systemctl restart "${active_services[@]}" || true
  fi
}

failed=1
cleanup() {
  if [[ "$failed" -ne 0 ]]; then
    echo "pgvector image activation failed; restoring the previous PostgreSQL image" >&2
    restore_previous_image
  fi
  rm -f -- "$compose_backup"
}
trap cleanup EXIT

docker exec "$postgres_container" pg_dump -Fc -U platform -d platform >"$backup_file"
chmod 0600 "$backup_file"
[[ -s "$backup_file" ]] || {
  echo "pre-pgvector PostgreSQL backup is empty" >&2
  exit 1
}

if ((${#active_services[@]} > 0)); then
  systemctl stop "${active_services[@]}"
fi

python3 - "$compose_file" "$target_image" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
target = sys.argv[2]
lines = path.read_text(encoding="utf-8").splitlines()
service_index = next((i for i, line in enumerate(lines) if line == "  postgres:"), None)
if service_index is None:
    raise SystemExit("postgres service is missing from platform compose")
image_indexes = [
    i
    for i in range(service_index + 1, min(len(lines), service_index + 12))
    if lines[i].startswith("    image: ")
]
if len(image_indexes) != 1:
    raise SystemExit("postgres image declaration is ambiguous")
lines[image_indexes[0]] = f"    image: {target}"
path.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY

docker compose -f "$compose_file" up -d --no-deps --force-recreate --pull never postgres >/dev/null
for _ in $(seq 1 60); do
  docker exec "$postgres_container" pg_isready -U platform -d platform >/dev/null 2>&1 && break
  sleep 2
done
docker exec "$postgres_container" pg_isready -U platform -d platform >/dev/null

active_image="$(docker inspect --format '{{.Config.Image}}' "$postgres_container")"
[[ "$active_image" == "$target_image" ]] || {
  echo "active PostgreSQL image does not match the locked pgvector image" >&2
  exit 1
}
available_version="$(docker exec "$postgres_container" psql -At -U platform -d platform \
  -c "SELECT default_version FROM pg_available_extensions WHERE name = 'vector'")"
[[ "$available_version" == "0.8.5" ]] || {
  echo "pgvector 0.8.5 is unavailable after image activation" >&2
  exit 1
}

if ((${#active_services[@]} > 0)); then
  systemctl restart "${active_services[@]}"
fi
failed=0
echo "node2_postgres_backup=$backup_file"
echo "node2_postgres_pgvector_image=ready"
