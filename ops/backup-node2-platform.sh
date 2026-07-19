#!/usr/bin/env bash
set -euo pipefail

backup="${1:?usage: backup-node2-platform.sh <backup-path> [postgres-container]}"
postgres_container="${2:-openim-platform-local-postgres-1}"

install -d -m 0700 "$(dirname "$backup")"
docker exec "$postgres_container" pg_dump -U platform -d platform -Fc >"$backup"
chmod 0600 "$backup"
sha256sum "$backup"
stat -c 'backup_bytes=%s' "$backup"
docker exec -i "$postgres_container" psql -At -U platform -d platform <<'SQL'
SELECT count(*) || '|' || COALESCE(max(name), '')
FROM platform_meta.schema_migrations;
SQL
