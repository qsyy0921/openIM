#!/usr/bin/env bash
set -euo pipefail

credential_dir=/etc/openim-platform/credentials
credential_file="$credential_dir/deepseek-api-key"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}

IFS= read -r key
key="${key%$'\r'}"
[[ "$key" =~ ^sk-[A-Za-z0-9_-]{20,}$ ]] || {
  unset key
  echo "DeepSeek credential is missing or malformed" >&2
  exit 1
}

install -d -m 0750 -o root -g root "$credential_dir"
umask 077
printf '%s' "$key" >"$credential_file"
unset key
chown root:root "$credential_file"
chmod 0400 "$credential_file"

systemctl enable openim-intelligence-worker.service openim-agent-runtime.service >/dev/null
systemctl restart openim-intelligence-worker.service

for _ in $(seq 1 60); do
  if curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null; then
    break
  fi
  sleep 2
done
curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null

systemctl restart openim-agent-runtime.service
systemctl is-active --quiet openim-intelligence-worker.service
systemctl is-active --quiet openim-agent-runtime.service
echo "intelligence_worker=active"
echo "agent_runtime=active"
