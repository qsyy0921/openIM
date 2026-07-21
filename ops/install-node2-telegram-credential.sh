#!/usr/bin/env bash
set -euo pipefail

credential_dir=/etc/openim-platform/credentials
credential_file="$credential_dir/telegram-bot-token"
platform_env=/etc/openim-platform/platform.env
proxy_url="${OPENIM_TELEGRAM_PROXY_URL:-http://127.0.0.1:7893}"
no_proxy="${OPENIM_TELEGRAM_NO_PROXY:-127.0.0.1,localhost,172.31.50.0/24,192.168.0.0/24}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
for unit in openim-telegram-ingress.service openim-agent-delivery.service; do
  systemctl cat "$unit" >/dev/null 2>&1 || {
    echo "required service is not installed: $unit" >&2
    exit 1
  }
done
[[ -r "$platform_env" ]] || {
  echo "platform environment is missing" >&2
  exit 1
}
[[ "$proxy_url" =~ ^http://127\.0\.0\.1:[0-9]{1,5}$ ]] || {
  echo "Telegram proxy URL must be a loopback HTTP proxy" >&2
  exit 1
}
[[ "$no_proxy" =~ ^[A-Za-z0-9.,:/-]+$ ]] || {
  echo "Telegram NO_PROXY value is malformed" >&2
  exit 1
}

if [[ -t 0 ]]; then
  printf 'Telegram Bot Token: ' >/dev/tty
  IFS= read -rs token
  printf '\n' >/dev/tty
else
  IFS= read -r token
fi
token="${token%$'\r'}"
[[ "$token" =~ ^[0-9]{6,}:[A-Za-z0-9_-]{20,}$ ]] || {
  unset token
  echo "Telegram Bot Token is missing or malformed" >&2
  exit 1
}

temporary="$(mktemp)"
trap 'rm -f "$temporary"' EXIT
chmod 0600 "$temporary"
printf '%s' "$token" >"$temporary"
unset token
api_base="$(sed -n 's/^PLATFORM_TELEGRAM_API_BASE_URL=//p' "$platform_env" | tail -1 | tr -d '\r')"
[[ "$api_base" =~ ^https://[^/]+(/.*)?$ ]] || {
  echo "Telegram API base URL is missing or malformed" >&2
  exit 1
}
python3 - "$temporary" "$api_base" "$proxy_url" <<'PY'
import json
import pathlib
import sys
import urllib.request

token = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
request = urllib.request.Request(
    sys.argv[2].rstrip("/") + "/bot" + token + "/getMe",
    data=b"{}",
    headers={"Content-Type": "application/json"},
    method="POST",
)
opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({"http": sys.argv[3], "https": sys.argv[3]})
)
with opener.open(request, timeout=30) as response:
    payload = json.load(response)
if payload.get("ok") is not True or not payload.get("result", {}).get("id"):
    raise SystemExit("Telegram credential validation failed")
print("telegram_credential=validated")
PY

install -d -m 0750 -o root -g root "$credential_dir"
install -m 0400 -o root -g root "$temporary" "$credential_file"

for unit in openim-telegram-ingress.service openim-agent-delivery.service; do
  dropin="/etc/systemd/system/$unit.d"
  install -d -m 0755 -o root -g root "$dropin"
  cat >"$dropin/20-telegram-proxy.conf" <<EOF
[Service]
Environment=HTTP_PROXY=$proxy_url
Environment=HTTPS_PROXY=$proxy_url
Environment=NO_PROXY=$no_proxy
EOF
  chmod 0644 "$dropin/20-telegram-proxy.conf"
done

systemctl daemon-reload
systemctl enable openim-telegram-ingress.service openim-agent-delivery.service >/dev/null
systemctl restart openim-telegram-ingress.service openim-agent-delivery.service
sleep 3
systemctl is-active --quiet openim-telegram-ingress.service
systemctl is-active --quiet openim-agent-delivery.service
echo "telegram_ingress=active"
echo "agent_delivery=active"
