#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
release_root="${2:-/home/ubuntu/MFL/releases/d663256}"
bin_dir="$release_root/linux-amd64"
wheel="$(find "$release_root/python" -maxdepth 1 -type f -name 'openim_intelligence_worker-*.whl' -print -quit)"
venv=/home/ubuntu/MFL/venvs/intelligence-worker
config_dir=/etc/openim-platform
credential_file="$config_dir/credentials/deepseek-api-key"
action_env="$config_dir/action-executor.env"
postgres_container=openim-platform-local-postgres-1

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$(ps -p 1 -o comm=)" == systemd ]] || {
  echo "systemd must be PID 1" >&2
  exit 1
}
for path in "$wheel" "$bin_dir/agent-runtime" "$bin_dir/action-executor" "$config_dir/platform.env"; do
  [[ -e "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done

chmod 0755 "$bin_dir/agent-runtime" "$bin_dir/action-executor"
install -d -m 0750 -o root -g ubuntu "$config_dir" "$config_dir/credentials"
install -d -m 0750 -o ubuntu -g ubuntu "$(dirname "$venv")"

if [[ ! -x "$venv/bin/python" ]]; then
  python3 -m venv "$venv"
fi
if ! "$venv/bin/python" -m pip --version >/dev/null 2>&1; then
  python3 -m venv --upgrade "$venv"
fi
set -a
. /etc/openim/proxy.env
set +a
"$venv/bin/python" -m pip install --disable-pip-version-check --force-reinstall --no-deps "$wheel"
chown -R ubuntu:ubuntu "$venv"

cat >"$config_dir/intelligence.env" <<'EOF'
INTELLIGENCE_HTTP_HOST=127.0.0.1
INTELLIGENCE_HTTP_PORT=18082
INTELLIGENCE_DEEPSEEK_BASE_URL=https://api.deepseek.com
INTELLIGENCE_DEEPSEEK_MODEL=deepseek-v4-pro
INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS=90
INTELLIGENCE_DEEPSEEK_MAX_TOKENS=1024
EOF
chown root:ubuntu "$config_dir/intelligence.env"
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
chown root:ubuntu "$action_env"
chmod 0640 "$action_env"

cat >/etc/systemd/system/openim-intelligence-worker.service <<EOF
[Unit]
Description=OpenIM Intelligence Worker
After=network-online.target

[Service]
Type=simple
User=ubuntu
Group=ubuntu
EnvironmentFile=$config_dir/intelligence.env
LoadCredential=deepseek_api_key:$credential_file
ExecStart=/bin/sh -ec 'export INTELLIGENCE_DEEPSEEK_API_KEY="\$(cat "\$CREDENTIALS_DIRECTORY/deepseek_api_key")"; exec $venv/bin/python -m uvicorn intelligence_worker.app:app --host "\$INTELLIGENCE_HTTP_HOST" --port "\$INTELLIGENCE_HTTP_PORT"'
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
Requires=docker.service openim-intelligence-worker.service
After=docker.service openim-intelligence-worker.service

[Service]
Type=simple
User=ubuntu
Group=ubuntu
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
User=ubuntu
Group=ubuntu
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

systemctl daemon-reload
systemctl enable openim-action-executor.service
systemctl restart openim-action-executor.service
systemctl is-active openim-action-executor.service

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

if [[ -f "$credential_file" ]]; then
  systemctl enable openim-intelligence-worker.service openim-agent-runtime.service
  systemctl restart openim-intelligence-worker.service openim-agent-runtime.service
  systemctl is-active openim-intelligence-worker.service openim-agent-runtime.service
  assert_running_binary openim-agent-runtime.service "$bin_dir/agent-runtime"
  echo "deepseek_credential=present"
else
  echo "deepseek_credential=required"
fi
echo "agent_runtime_binaries=verified"
echo "node2_agent_runtime=installed"
