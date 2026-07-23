#!/usr/bin/env bash
set -euo pipefail

proxy_drop_in="${1:-/etc/systemd/system/docker.service.d/http-proxy.conf}"
backup_root="${2:-/home/qsyy0921/MFL/staging/docker-proxy-backup}"
stale_endpoint="127.0.0.1:17890"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ -f "$proxy_drop_in" ]] || {
  echo "docker_proxy=already_absent"
  exit 0
}
grep -Fq "$stale_endpoint" "$proxy_drop_in" || {
  echo "Docker proxy does not match the known stale endpoint" >&2
  exit 1
}
if ss -lntH | awk '{print $4}' | grep -Eq '^(127\.0\.0\.1|\[::1\]):17890$'; then
  echo "Docker proxy endpoint is listening; refusing to disable it" >&2
  exit 1
fi

http_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --connect-timeout 10 https://docker.m.daocloud.io/v2/)"
[[ "$http_status" == "401" ]] || {
  echo "direct registry mirror readiness failed: HTTP $http_status" >&2
  exit 1
}

install -d -m 0700 "$backup_root"
backup="$backup_root/http-proxy.conf.$(date -u +%Y%m%dT%H%M%SZ)"
install -m 0600 "$proxy_drop_in" "$backup"
rm -f "$proxy_drop_in"
systemctl daemon-reload
systemctl restart docker.service
for _ in $(seq 1 60); do
  if systemctl is-active --quiet docker.service && docker info >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
systemctl is-active --quiet docker.service
docker info >/dev/null

echo "docker_proxy=disabled_stale_endpoint"
echo "docker_proxy_backup=$backup"
