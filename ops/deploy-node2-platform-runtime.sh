#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
release_root="${2:-/home/ubuntu/MFL/releases/goal-final}"
bin_dir="$release_root/linux-amd64"
runtime_env=/etc/openim-platform/platform.env
release_version="$(basename "$release_root")"
runtime_user="${OPENIM_PLATFORM_RUNTIME_USER:-ubuntu}"
runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-$runtime_user}"
platform_http_addr="${OPENIM_PLATFORM_HTTP_ADDR:-0.0.0.0:18080}"
oidc_issuer="${OPENIM_PLATFORM_OIDC_ISSUER:-http://172.31.50.2:18081/realms/platform}"
openim_api_url="${OPENIM_PLATFORM_OPENIM_API_URL:-http://127.0.0.1:12002}"
openim_ws_url="${OPENIM_PLATFORM_OPENIM_WS_URL:-ws://172.31.50.2:12001}"
kafka_brokers="${OPENIM_PLATFORM_KAFKA_BROKERS:-127.0.0.1:19094}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$(ps -p 1 -o comm=)" == systemd ]] || {
  echo "systemd must be PID 1" >&2
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

for path in \
  "$deploy_root/platform/.env" \
  "$deploy_root/openim/.env" \
  "$bin_dir/platform-api" \
  "$bin_dir/platform-ingress"; do
  [[ -e "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done

postgres_password="$(sed -n 's/^PLATFORM_LOCAL_POSTGRES_PASSWORD=//p' "$deploy_root/platform/.env" | tr -d '\r')"
openim_secret="$(sed -n 's/^OPENIM_SECRET=//p' "$deploy_root/openim/.env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
[[ -n "$postgres_password" && -n "$openim_secret" ]] || {
  echo "host-local PostgreSQL or OpenIM secret is empty" >&2
  exit 1
}

chmod 0755 "$bin_dir/platform-api" "$bin_dir/platform-ingress"
install -d -m 0750 -o root -g "$runtime_group" /etc/openim-platform
umask 027
{
  printf 'PLATFORM_HTTP_ADDR=%s\n' "$platform_http_addr"
  printf 'PLATFORM_VERSION=%s\n' "$release_version"
  printf 'PLATFORM_SHUTDOWN_TIMEOUT=15s\n'
  printf 'PLATFORM_DEPENDENCY_TIMEOUT=20s\n'
  printf 'PLATFORM_DATABASE_URL=postgres://platform:%s@127.0.0.1:15432/platform?sslmode=disable\n' "$postgres_password"
  printf 'PLATFORM_OIDC_ISSUER=%s\n' "$oidc_issuer"
  printf 'PLATFORM_OIDC_AUDIENCE=platform-api\n'
  printf 'PLATFORM_OPENIM_API_URL=%s\n' "$openim_api_url"
  printf 'PLATFORM_OPENIM_WS_URL=%s\n' "$openim_ws_url"
  printf 'PLATFORM_OPENIM_SECRET=%s\n' "$openim_secret"
  printf 'PLATFORM_OPENIM_ADMIN_USER_ID=imAdmin\n'
  printf 'PLATFORM_KAFKA_BROKERS=%s\n' "$kafka_brokers"
  printf 'PLATFORM_OPENIM_INGRESS_TOPIC=toRedis\n'
  printf 'PLATFORM_OPENIM_INGRESS_GROUP=platform-ingress-node2-v1\n'
  printf 'PLATFORM_EVENT_TOPIC=platform.im.message.accepted.v1\n'
  printf 'PLATFORM_OUTBOX_POLL_INTERVAL=500ms\n'
  printf 'PLATFORM_OUTBOX_LEASE=15s\n'
  printf 'PLATFORM_OUTBOX_BATCH=100\n'
  printf 'PLATFORM_KAFKA_TLS_ENABLED=false\n'
  printf 'PLATFORM_AGENT_CONSUMER_GROUP=agent-runtime-node2-v1\n'
  printf 'PLATFORM_AGENT_POLL_INTERVAL=500ms\n'
  printf 'PLATFORM_AGENT_LEASE=30s\n'
  printf 'PLATFORM_AGENT_MAX_ATTEMPTS=3\n'
  printf 'PLATFORM_INTELLIGENCE_URL=http://127.0.0.1:18082\n'
} >"$runtime_env"
chown root:"$runtime_group" "$runtime_env"
chmod 0640 "$runtime_env"

cat >/etc/systemd/system/openim-platform-api.service <<EOF
[Unit]
Description=OpenIM Intelligent Collaboration Platform API
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$runtime_env
ExecStart=$bin_dir/platform-api
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/openim-platform-ingress.service <<EOF
[Unit]
Description=OpenIM durable message ingress
Requires=docker.service
After=docker.service network-online.target openim-platform-api.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$runtime_env
ExecStart=$bin_dir/platform-ingress
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable openim-platform-api.service openim-platform-ingress.service
systemctl restart openim-platform-api.service openim-platform-ingress.service

health_file="$(mktemp)"
trap 'rm -f "$health_file"' EXIT
for _ in $(seq 1 30); do
  if curl -fsS --max-time 3 http://127.0.0.1:18080/healthz >"$health_file"; then
    break
  fi
  sleep 2
done
python3 - "$health_file" "$release_version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
assert payload.get("status") == "ready", payload
assert payload.get("version") == sys.argv[2], payload
print("platform_api=ready")
PY
systemctl is-active openim-platform-api.service openim-platform-ingress.service

assert_running_binary() {
  local service="$1"
  local expected="$2"
  local pid actual
  expected="$(readlink -f "$expected")"
  actual=""
  for _ in $(seq 1 30); do
    pid="$(systemctl show -p MainPID --value "$service")"
    if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
      actual="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
      [[ "$actual" == "$expected" ]] && return 0
    fi
    sleep 1
  done
  echo "$service executable mismatch: expected $expected, running ${actual:-unavailable}" >&2
  exit 1
}

assert_running_binary openim-platform-api.service "$bin_dir/platform-api"
assert_running_binary openim-platform-ingress.service "$bin_dir/platform-ingress"
echo "platform_runtime_binaries=verified"
