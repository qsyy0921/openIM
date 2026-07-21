#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
release_root="${2:-/home/ubuntu/MFL/releases/d663256}"
bin_dir="$release_root/linux-amd64"
wheel="$(find "$release_root/python" -maxdepth 1 -type f -name 'openim_intelligence_worker-*.whl' -print -quit)"
runtime_user="${OPENIM_PLATFORM_RUNTIME_USER:-ubuntu}"
runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-$runtime_user}"
mfl_root="${OPENIM_PLATFORM_MFL_ROOT:-$(dirname "$(dirname "$release_root")")}"
venv="${OPENIM_INTELLIGENCE_VENV:-$mfl_root/venvs/intelligence-worker}"
config_dir=/etc/openim-platform
telegram_credential_file="$config_dir/credentials/telegram-bot-token"
action_env="$config_dir/action-executor.env"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
install_dependencies="${OPENIM_INTELLIGENCE_INSTALL_DEPENDENCIES:-false}"
proxy_env="${OPENIM_PLATFORM_PROXY_ENV:-/etc/openim/proxy.env}"
intelligence_ssh_target="${OPENIM_INTELLIGENCE_SSH_TARGET:-10495@172.31.50.1}"
embedding_base_url="${OPENIM_INTELLIGENCE_EMBEDDING_BASE_URL:-http://127.0.0.1:11434/v1}"
embedding_api_key="${OPENIM_INTELLIGENCE_EMBEDDING_API_KEY:-local-only}"
embedding_model="${OPENIM_INTELLIGENCE_EMBEDDING_MODEL:-qwen3-embedding:4b}"
embedding_dimension="${OPENIM_INTELLIGENCE_EMBEDDING_DIMENSION:-2560}"
embedding_timeout="${OPENIM_INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS:-180}"
knowledge_index_batch_size="${OPENIM_KNOWLEDGE_INDEX_BATCH_SIZE:-32}"
routing_dense_min_similarity="${OPENIM_INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY:-0.2}"
telegram_proxy_url="${OPENIM_TELEGRAM_PROXY_URL:-http://127.0.0.1:7893}"
telegram_no_proxy="${OPENIM_TELEGRAM_NO_PROXY:-127.0.0.1,localhost,172.31.50.0/24,192.168.0.0/24}"

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
command -v runuser >/dev/null 2>&1 || {
  echo "runuser is required" >&2
  exit 1
}
[[ "$install_dependencies" == "true" || "$install_dependencies" == "false" ]] || {
  echo "OPENIM_INTELLIGENCE_INSTALL_DEPENDENCIES must be true or false" >&2
  exit 1
}
[[ "$knowledge_index_batch_size" =~ ^[0-9]+$ ]] && \
  ((knowledge_index_batch_size >= 1 && knowledge_index_batch_size <= 128)) || {
  echo "knowledge index batch size must be between 1 and 128" >&2
  exit 1
}
[[ "$telegram_proxy_url" =~ ^http://127\.0\.0\.1:[0-9]{1,5}$ ]] || {
  echo "Telegram proxy URL must be a loopback HTTP proxy" >&2
  exit 1
}
[[ "$telegram_no_proxy" =~ ^[A-Za-z0-9.,:/-]+$ ]] || {
  echo "Telegram NO_PROXY value is malformed" >&2
  exit 1
}
[[ "$intelligence_ssh_target" =~ ^[A-Za-z0-9._-]+@[A-Za-z0-9.:-]+$ ]] || {
  echo "intelligence SSH target is malformed" >&2
  exit 1
}
command -v ssh >/dev/null 2>&1 || {
  echo "OpenSSH client is required for the intelligence tunnel" >&2
  exit 1
}
runtime_binaries=(
  agent-catalog-admin
  agent-runtime
  agent-delivery
  capability-admin
  group-memory-admin
  knowledge-rag-admin
  mcp-admin
  member-grant-admin
  proactive-admin
  telegram-ingress
  telegram-admin
  memory-extractor
  memory-projector
  proactive-runtime
  role-admin
  runtime-control-admin
  skill-admin
  action-executor
)
for path in "$wheel" "$config_dir/platform.env"; do
  [[ -e "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done
for binary in "${runtime_binaries[@]}"; do
  [[ -e "$bin_dir/$binary" ]] || {
    echo "required path is missing: $bin_dir/$binary" >&2
    exit 1
  }
done

for binary in "${runtime_binaries[@]}"; do
  chmod 0755 "$bin_dir/$binary"
done
install -d -m 0750 -o root -g "$runtime_group" "$config_dir" "$config_dir/credentials"
if [[ ! -e "$telegram_credential_file" ]]; then
  install -m 0400 -o root -g root /dev/null "$telegram_credential_file"
fi
install -d -m 0750 -o "$runtime_user" -g "$runtime_group" "$(dirname "$venv")"

if [[ ! -x "$venv/bin/python" ]]; then
  python3 -m venv "$venv"
fi
if ! "$venv/bin/python" -m pip --version >/dev/null 2>&1; then
  python3 -m venv --upgrade "$venv"
fi
if [[ -r "$proxy_env" ]]; then
  set -a
  . "$proxy_env"
  set +a
fi
pip_args=(install --disable-pip-version-check --force-reinstall)
if [[ "$install_dependencies" == "false" ]]; then
  pip_args+=(--no-deps)
fi
"$venv/bin/python" -m pip "${pip_args[@]}" "$wheel"
chown -R "$runtime_user:$runtime_group" "$venv"

cat >"$config_dir/intelligence.env" <<EOF
INTELLIGENCE_HTTP_HOST=127.0.0.1
INTELLIGENCE_HTTP_PORT=18082
INTELLIGENCE_EMBEDDING_BASE_URL=$embedding_base_url
INTELLIGENCE_EMBEDDING_API_KEY=$embedding_api_key
INTELLIGENCE_EMBEDDING_MODEL=$embedding_model
INTELLIGENCE_EMBEDDING_DIMENSION=$embedding_dimension
INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS=$embedding_timeout
INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY=$routing_dense_min_similarity
EOF
chown root:"$runtime_group" "$config_dir/intelligence.env"
chmod 0640 "$config_dir/intelligence.env"

if [[ ! -f "$action_env" ]]; then
  action_password="$(openssl rand -hex 24)"
else
  action_url="$(sed -n 's/^ACTION_DATABASE_URL=//p' "$action_env")"
  action_password="${action_url#*platform_action_executor:}"
  action_password="${action_password%@127.0.0.1:*}"
fi
[[ "$action_password" =~ ^[0-9a-f]{48}$ ]] || {
  echo "Action Executor password is missing or malformed" >&2
  exit 1
}
escaped_action_password="$(printf '%s' "$action_password" | sed "s/'/''/g")"
docker exec -i "$postgres_container" psql -v ON_ERROR_STOP=1 -U platform -d platform >/dev/null <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platform_action_executor') THEN
    CREATE ROLE platform_action_executor LOGIN PASSWORD '$escaped_action_password';
  ELSE
    ALTER ROLE platform_action_executor PASSWORD '$escaped_action_password';
  END IF;
END
\$\$;
ALTER ROLE platform_action_executor NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
REVOKE ALL ON SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
GRANT USAGE ON SCHEMA action, collaboration, audit, agent, identity TO platform_action_executor;
GRANT SELECT, UPDATE ON action.intents, action.executions TO platform_action_executor;
GRANT SELECT, INSERT ON collaboration.tickets TO platform_action_executor;
GRANT INSERT ON audit.action_events TO platform_action_executor;
GRANT USAGE, SELECT ON SEQUENCE audit.action_events_id_seq TO platform_action_executor;
GRANT SELECT, UPDATE ON agent.runs TO platform_action_executor;
SQL
umask 027
printf 'ACTION_DATABASE_URL=postgres://platform_action_executor:%s@127.0.0.1:15432/platform?sslmode=disable\n' "$action_password" >"$action_env"
printf 'ACTION_POLL_INTERVAL=500ms\nACTION_LEASE=30s\nACTION_DEPENDENCY_TIMEOUT=20s\nACTION_MAX_ATTEMPTS=3\n' >>"$action_env"
unset action_password escaped_action_password action_url
chown root:"$runtime_group" "$action_env"
chmod 0640 "$action_env"

cat >/etc/systemd/system/openim-intelligence-tunnel.service <<EOF
[Unit]
Description=OpenIM loopback-only intelligence tunnel to Windows node1
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
ExecStart=/usr/bin/ssh -NT -o BatchMode=yes -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -L 127.0.0.1:18082:127.0.0.1:18082 -R 127.0.0.1:11434:127.0.0.1:11434 $intelligence_ssh_target
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

cat >/etc/systemd/system/openim-agent-runtime.service <<EOF
[Unit]
Description=OpenIM governed Agent Runtime
Requires=docker.service openim-intelligence-tunnel.service
After=docker.service openim-intelligence-tunnel.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
ExecStart=$bin_dir/agent-runtime
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

cat >/etc/systemd/system/openim-action-executor.service <<EOF
[Unit]
Description=OpenIM approved Action Executor
Requires=docker.service
After=docker.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$action_env
ExecStart=$bin_dir/action-executor
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

write_platform_worker_unit() {
  local service="$1"
  local description="$2"
  local binary="$3"
  local requires="$4"
  local after="$5"
  cat >"/etc/systemd/system/$service.service" <<EOF
[Unit]
Description=$description
Requires=$requires
After=$after

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
ExecStart=$bin_dir/$binary
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
}

write_telegram_worker_unit() {
  local service="$1"
  local description="$2"
  local binary="$3"
  cat >"/etc/systemd/system/$service.service" <<EOF
[Unit]
Description=$description
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
Environment=HTTP_PROXY=$telegram_proxy_url
Environment=HTTPS_PROXY=$telegram_proxy_url
Environment=NO_PROXY=$telegram_no_proxy
LoadCredential=telegram_bot_token:$telegram_credential_file
ExecStart=/bin/sh -ec 'export PLATFORM_TELEGRAM_BOT_TOKEN="\$(cat "\$CREDENTIALS_DIRECTORY/telegram_bot_token")"; exec $bin_dir/$binary'
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
}

write_delivery_worker_unit() {
  cat >"/etc/systemd/system/openim-agent-delivery.service" <<EOF
[Unit]
Description=OpenIM Agent channel delivery
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
Environment=HTTP_PROXY=$telegram_proxy_url
Environment=HTTPS_PROXY=$telegram_proxy_url
Environment=NO_PROXY=$telegram_no_proxy
LoadCredential=telegram_bot_token:$telegram_credential_file
ExecStart=/bin/sh -ec 'if [ -s "\$CREDENTIALS_DIRECTORY/telegram_bot_token" ]; then export PLATFORM_TELEGRAM_DELIVERY_ENABLED=true; export PLATFORM_TELEGRAM_BOT_TOKEN="\$(cat "\$CREDENTIALS_DIRECTORY/telegram_bot_token")"; else export PLATFORM_TELEGRAM_DELIVERY_ENABLED=false; unset PLATFORM_TELEGRAM_BOT_TOKEN; fi; exec $bin_dir/agent-delivery'
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
}

write_platform_worker_unit \
  openim-memory-projector "OpenIM Agent Memory projector" memory-projector \
  docker.service docker.service
write_platform_worker_unit \
  openim-memory-extractor "OpenIM Agent Memory extractor" memory-extractor \
  "docker.service openim-intelligence-tunnel.service" \
  "docker.service openim-intelligence-tunnel.service"
write_platform_worker_unit \
  openim-proactive-runtime "OpenIM proactive Agent Runtime" proactive-runtime \
  "docker.service openim-intelligence-tunnel.service" \
  "docker.service openim-intelligence-tunnel.service"
write_telegram_worker_unit \
  openim-telegram-ingress "OpenIM Telegram ingress" telegram-ingress
write_delivery_worker_unit

systemctl daemon-reload
systemctl enable openim-action-executor.service openim-memory-projector.service
systemctl restart openim-action-executor.service openim-memory-projector.service
systemctl is-active openim-action-executor.service openim-memory-projector.service

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

assert_running_binary openim-action-executor.service "$bin_dir/action-executor"
assert_running_binary openim-memory-projector.service "$bin_dir/memory-projector"

systemctl disable --now openim-intelligence-worker.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/openim-intelligence-worker.service
systemctl daemon-reload
systemctl enable openim-intelligence-tunnel.service openim-agent-runtime.service \
  openim-memory-extractor.service openim-proactive-runtime.service
systemctl stop openim-agent-runtime.service openim-memory-extractor.service openim-proactive-runtime.service
systemctl restart openim-intelligence-tunnel.service
  for _ in $(seq 1 60); do
    if curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null; then
      break
    fi
    sleep 2
  done
  curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null
  embedding_probe="$(mktemp)"
  trap 'rm -f "$embedding_probe"' EXIT
  curl --fail --silent --show-error --max-time "$((embedding_timeout + 5))" \
    -H 'Content-Type: application/json' \
    --data '{"texts":["node2 deployment readiness"]}' \
    http://127.0.0.1:18082/v1/embeddings >"$embedding_probe"
  "$venv/bin/python" - "$embedding_probe" "$embedding_model" "$embedding_dimension" <<'PY'
import json
import sys

path, expected_model, expected_dimension_raw = sys.argv[1:]
expected_dimension = int(expected_dimension_raw)
with open(path, encoding="utf-8") as handle:
    body = json.load(handle)

vectors = body.get("vectors")
if body.get("model") != expected_model:
    raise SystemExit("embedding probe returned an unexpected model")
if body.get("dimension") != expected_dimension:
    raise SystemExit("embedding probe returned an unexpected dimension")
if not isinstance(vectors, list) or len(vectors) != 1:
    raise SystemExit("embedding probe returned an unexpected vector count")
if not isinstance(vectors[0], list) or len(vectors[0]) != expected_dimension:
    raise SystemExit("embedding probe returned an unexpected vector length")
if not all(isinstance(value, (int, float)) for value in vectors[0]):
    raise SystemExit("embedding probe returned a non-numeric vector")
PY
  rm -f "$embedding_probe"
  trap - EXIT
  database_url="$(sed -n 's/^PLATFORM_DATABASE_URL=//p' "$config_dir/platform.env" | tail -1 | tr -d '\r')"
  [[ "$database_url" =~ ^postgres(ql)?://[^[:space:]]+$ ]] || {
    unset database_url
    echo "platform database URL is missing or malformed" >&2
    exit 1
  }
  index_report="$(mktemp)"
  trap 'rm -f "$index_report"' EXIT
  runuser -u "$runtime_user" -- env \
    PLATFORM_DATABASE_URL="$database_url" \
    PLATFORM_INTELLIGENCE_URL=http://127.0.0.1:18082 \
    PLATFORM_RETRIEVAL_EMBEDDING_MODEL="$embedding_model" \
    PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION="$embedding_dimension" \
    "$bin_dir/knowledge-rag-admin" \
      -mode index -batch-size "$knowledge_index_batch_size" \
      -timeout "$((embedding_timeout * 2))s" >"$index_report"
  unset database_url
  "$venv/bin/python" - "$index_report" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    body = json.load(handle)
indexed = body.get("indexed")
if not isinstance(indexed, int) or indexed < 0:
    raise SystemExit("knowledge embedding index report is invalid")
print(f"knowledge_embeddings_indexed={indexed}")
PY
  rm -f "$index_report"
  trap - EXIT
  systemctl restart openim-agent-runtime.service openim-memory-extractor.service openim-proactive-runtime.service
  systemctl is-active openim-intelligence-tunnel.service openim-agent-runtime.service \
    openim-memory-extractor.service openim-proactive-runtime.service
  assert_running_binary openim-agent-runtime.service "$bin_dir/agent-runtime"
  assert_running_binary openim-memory-extractor.service "$bin_dir/memory-extractor"
  assert_running_binary openim-proactive-runtime.service "$bin_dir/proactive-runtime"
echo "intelligence_tunnel=active"
systemctl enable openim-agent-delivery.service
systemctl restart openim-agent-delivery.service
systemctl is-active openim-agent-delivery.service
assert_running_binary openim-agent-delivery.service "$bin_dir/agent-delivery"
if [[ -s "$telegram_credential_file" ]]; then
  systemctl enable openim-telegram-ingress.service
  systemctl restart openim-telegram-ingress.service
  systemctl is-active openim-telegram-ingress.service
  assert_running_binary openim-telegram-ingress.service "$bin_dir/telegram-ingress"
  echo "telegram_credential=present"
else
  systemctl disable --now openim-telegram-ingress.service >/dev/null 2>&1 || true
  echo "telegram_delivery=disabled"
  echo "telegram_credential=required"
fi
echo "agent_runtime_binaries=verified"
echo "node2_agent_runtime=installed"
