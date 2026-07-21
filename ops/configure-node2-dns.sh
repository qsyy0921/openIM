#!/usr/bin/env bash
set -euo pipefail

device="${1:-wls6}"
backup_root="${2:-/home/qsyy0921/MFL/staging/network-backup}"
primary_dns="${OPENIM_NODE2_PRIMARY_DNS:-223.5.5.5}"
secondary_dns="${OPENIM_NODE2_SECONDARY_DNS:-223.6.6.6}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
connection="$(nmcli -t -f NAME,DEVICE connection show --active | awk -F: -v device="$device" '$2 == device {print $1; exit}')"
[[ -n "$connection" ]] || {
  echo "no active NetworkManager connection for $device" >&2
  exit 1
}
for server in "$primary_dns" "$secondary_dns"; do
  udp_ready=false
  tcp_ready=false
  for _ in $(seq 1 5); do
    if dig +short +time=2 +tries=1 "@$server" m.daocloud.io A | grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then
      udp_ready=true
      break
    fi
    sleep 1
  done
  for _ in $(seq 1 3); do
    if dig +tcp +short +time=3 +tries=1 "@$server" m.daocloud.io A | grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then
      tcp_ready=true
      break
    fi
    sleep 1
  done
  [[ "$udp_ready" == true && "$tcp_ready" == true ]] || {
    echo "DNS server is not ready over UDP and TCP: $server" >&2
    exit 1
  }
done

install -d -m 0700 "$backup_root"
backup="$backup_root/${connection//\//_}.$(date -u +%Y%m%dT%H%M%SZ).txt"
nmcli -f connection.id,connection.interface-name,ipv4.method,ipv4.dns,ipv4.ignore-auto-dns,ipv6.method,ipv6.dns,ipv6.ignore-auto-dns \
  connection show "$connection" >"$backup"
chmod 0600 "$backup"

nmcli connection modify "$connection" \
  ipv4.ignore-auto-dns yes \
  ipv4.dns "$primary_dns,$secondary_dns" \
  ipv6.ignore-auto-dns yes \
  ipv6.dns ""
nmcli device reapply "$device"
resolvectl flush-caches
resolvectl dns "$device" | grep -F "$primary_dns" >/dev/null
resolvectl dns "$device" | grep -F "$secondary_dns" >/dev/null
resolution_ready=false
for _ in $(seq 1 10); do
  if getent ahostsv4 m.daocloud.io >/dev/null; then
    resolution_ready=true
    break
  fi
  sleep 1
done
[[ "$resolution_ready" == "true" ]] || {
  echo "Node2 DNS resolution did not become ready" >&2
  exit 1
}

echo "node2_dns=$primary_dns,$secondary_dns"
echo "network_backup=$backup"
