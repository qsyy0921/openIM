#!/usr/bin/env bash
set -euo pipefail

env_file="${1:-/home/qsyy0921/MFL/deploy/node2-native/platform/.env}"

[[ -f "$env_file" ]] || {
  echo "platform environment is missing: $env_file" >&2
  exit 1
}

current="$(sed -n 's/^PLATFORM_LOCAL_GRAFANA_ADMIN_PASSWORD=//p' "$env_file" | tail -1 | tr -d '\r')"
if [[ -n "$current" ]]; then
  echo "grafana_admin_password=preserved"
  exit 0
fi

password="$(openssl rand -hex 24)"
[[ "$password" =~ ^[0-9a-f]{48}$ ]] || {
  echo "failed to generate Grafana administrator password" >&2
  exit 1
}
printf '\nPLATFORM_LOCAL_GRAFANA_ADMIN_PASSWORD=%s\n' "$password" >>"$env_file"
unset password
chmod 0600 "$env_file"
echo "grafana_admin_password=generated"
