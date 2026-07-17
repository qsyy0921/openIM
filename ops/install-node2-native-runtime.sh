#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/qsyy0921/MFL/deploy/node2-native}"
release_root="${2:?release root is required}"
runtime_user="${3:-qsyy0921}"
public_host="${4:?public host is required}"
runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-$runtime_user}"
bin_dir="$release_root/linux-amd64"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
oidc_issuer="http://$public_host:18081/realms/platform"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$(ps -p 1 -o comm=)" == systemd ]] || {
  echo "systemd must be PID 1" >&2
  exit 1
}
if grep -qi microsoft /proc/sys/kernel/osrelease; then
  echo "native Ubuntu is required; WSL is not an accepted target" >&2
  exit 1
fi
[[ "$public_host" =~ ^[A-Za-z0-9.-]+$ ]] || {
  echo "public host is malformed" >&2
  exit 1
}
id "$runtime_user" >/dev/null 2>&1 || {
  echo "runtime user does not exist: $runtime_user" >&2
  exit 1
}
getent group "$runtime_group" >/dev/null 2>&1 || {
  echo "runtime group does not exist: $runtime_group" >&2
  exit 1
}

required_paths=(
  "$deploy_root/platform/.env"
  "$deploy_root/openim/.env"
  "$deploy_root/platform/seed-node2-native-identity.sql"
  "$deploy_root/native-ubuntu/nginx-openim-platform.conf"
  "$deploy_root/ops/deploy-node2-platform-runtime.sh"
  "$deploy_root/ops/deploy-node2-agent-runtime.sh"
  "$deploy_root/ops/deploy-node2-native-web.sh"
  "$release_root/SHA256SUMS"
  "$release_root/datasets/enterprise-knowledge/v1/postgres_import.sql"
  "$bin_dir/platform-migrate"
)
for path in "${required_paths[@]}"; do
  [[ -e "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done
command -v nginx >/dev/null 2>&1 || {
  echo "nginx is required" >&2
  exit 1
}
docker inspect "$postgres_container" >/dev/null 2>&1 || {
  echo "PostgreSQL container is missing: $postgres_container" >&2
  exit 1
}
docker exec "$postgres_container" pg_isready -U platform -d platform >/dev/null

(
  cd "$release_root"
  sha256sum -c SHA256SUMS
)
chmod 0755 "$bin_dir"/*

postgres_password="$(sed -n 's/^PLATFORM_LOCAL_POSTGRES_PASSWORD=//p' "$deploy_root/platform/.env" | tail -1 | tr -d '\r')"
[[ "$postgres_password" =~ ^[0-9a-f]{48}$ ]] || {
  echo "host-local PostgreSQL password is missing or malformed" >&2
  exit 1
}
database_url="postgres://platform:$postgres_password@127.0.0.1:15432/platform?sslmode=disable"
PLATFORM_DATABASE_URL="$database_url" \
PLATFORM_DEPENDENCY_TIMEOUT=30s \
  "$bin_dir/platform-migrate"

docker exec -i "$postgres_container" \
  psql -v ON_ERROR_STOP=1 \
  -v "platform_oidc_issuer=$oidc_issuer" \
  -U platform -d platform \
  <"$deploy_root/platform/seed-node2-native-identity.sql"

docker exec -i "$postgres_container" \
  psql -v ON_ERROR_STOP=1 -U platform -d platform \
  <"$release_root/datasets/enterprise-knowledge/v1/postgres_import.sql"

read -r documents versions chunks grants catalog <<<"$(docker exec "$postgres_container" \
  psql -At -F ' ' -U platform -d platform -c \
  "select (select count(*) from knowledge.documents), (select count(*) from knowledge.document_versions), (select count(*) from knowledge.chunks), (select count(*) from authz.document_grants), (select count(*) from agent.deployments where slot='production');")"
[[ "$documents" == "520" && "$versions" == "624" && "$chunks" == "3224" ]] || {
  echo "enterprise dataset counts are invalid: documents=$documents versions=$versions chunks=$chunks" >&2
  exit 1
}
[[ "$grants" == "520" && "$catalog" == "1" ]] || {
  echo "authorization or Agent Catalog counts are invalid: grants=$grants catalog=$catalog" >&2
  exit 1
}

export OPENIM_PLATFORM_RUNTIME_USER="$runtime_user"
export OPENIM_PLATFORM_RUNTIME_GROUP="$runtime_group"
export OPENIM_PLATFORM_MFL_ROOT="$(dirname "$(dirname "$release_root")")"
export OPENIM_PLATFORM_OIDC_ISSUER="$oidc_issuer"
export OPENIM_PLATFORM_OPENIM_WS_URL="ws://$public_host:12001"
export OPENIM_INTELLIGENCE_INSTALL_DEPENDENCIES=true

bash "$deploy_root/ops/deploy-node2-platform-runtime.sh" "$deploy_root" "$release_root"
bash "$deploy_root/ops/deploy-node2-agent-runtime.sh" "$deploy_root" "$release_root"
bash "$deploy_root/ops/deploy-node2-native-web.sh" \
  "$release_root" \
  "$deploy_root/native-ubuntu/nginx-openim-platform.conf"

echo "dataset=documents:$documents,versions:$versions,chunks:$chunks,grants:$grants"
echo "node2_native_runtime=installed"
