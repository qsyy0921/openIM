#!/usr/bin/env bash
set -euo pipefail

public_host="${1:?public host is required}"
runtime_user="${2:-qsyy0921}"
nginx_config="${3:?nginx configuration path is required}"
exported_ca="${4:?exported CA path is required}"
runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-$runtime_user}"
tls_dir=/etc/openim-platform/tls
ca_key="$tls_dir/lab-ca.key"
ca_cert="$tls_dir/lab-ca.crt"
server_key="$tls_dir/node2.key"
server_cert="$tls_dir/node2.crt"
host_marker="$tls_dir/public-host"
rotate="${OPENIM_PLATFORM_TLS_ROTATE:-false}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$public_host" =~ ^[A-Za-z0-9.-]+$ ]] || {
  echo "public host is malformed" >&2
  exit 1
}
[[ "$rotate" == "true" || "$rotate" == "false" ]] || {
  echo "OPENIM_PLATFORM_TLS_ROTATE must be true or false" >&2
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
for command_name in nginx openssl update-ca-certificates; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "required command is missing: $command_name" >&2
    exit 1
  }
done
[[ -f "$nginx_config" ]] || {
  echo "nginx configuration is missing: $nginx_config" >&2
  exit 1
}

install -d -m 0700 -o root -g root "$tls_dir"
if [[ "$rotate" == "true" ]]; then
  rm -f "$ca_key" "$ca_cert" "$tls_dir/lab-ca.srl" "$server_key" "$server_cert" "$host_marker"
fi
if [[ -f "$host_marker" && "$(cat "$host_marker")" != "$public_host" ]]; then
  echo "existing TLS identity belongs to another public host" >&2
  exit 1
fi

if [[ ! -f "$ca_key" || ! -f "$ca_cert" ]]; then
  openssl genrsa -out "$ca_key" 3072 >/dev/null 2>&1
  openssl req -x509 -new -sha256 -days 3650 \
    -key "$ca_key" \
    -subj '/CN=OpenIM Node2 Local Lab CA/O=OpenIM Local Lab' \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -addext 'subjectKeyIdentifier=hash' \
    -out "$ca_cert"
fi

if [[ ! -f "$server_key" || ! -f "$server_cert" ]]; then
  work_dir="$(mktemp -d)"
  trap 'rm -rf "$work_dir"' EXIT
  hostname_value="$(hostname -f 2>/dev/null || hostname)"
  if [[ "$public_host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    public_san="IP:$public_host"
  else
    public_san="DNS:$public_host"
  fi
  cat >"$work_dir/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
authorityKeyIdentifier=keyid,issuer
subjectAltName=$public_san,IP:172.31.50.2,IP:127.0.0.1,DNS:localhost,DNS:$hostname_value
EOF
  openssl genrsa -out "$server_key" 3072 >/dev/null 2>&1
  openssl req -new -sha256 \
    -key "$server_key" \
    -subj "/CN=$public_host/O=OpenIM Local Lab" \
    -out "$work_dir/server.csr"
  openssl x509 -req -sha256 -days 825 \
    -in "$work_dir/server.csr" \
    -CA "$ca_cert" \
    -CAkey "$ca_key" \
    -CAcreateserial \
    -extfile "$work_dir/server.ext" \
    -out "$server_cert" >/dev/null 2>&1
  rm -rf "$work_dir"
  trap - EXIT
fi

printf '%s\n' "$public_host" >"$host_marker"
chmod 0600 "$ca_key" "$server_key"
chmod 0644 "$ca_cert" "$server_cert" "$host_marker"
openssl verify -CAfile "$ca_cert" "$server_cert" >/dev/null
if [[ "$public_host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
  openssl x509 -in "$server_cert" -noout -checkip "$public_host" >/dev/null
else
  openssl x509 -in "$server_cert" -noout -checkhost "$public_host" >/dev/null
fi

install -m 0644 "$ca_cert" /usr/local/share/ca-certificates/openim-node2-lab-ca.crt
update-ca-certificates >/dev/null
install -d -m 0755 -o "$runtime_user" -g "$runtime_group" "$(dirname "$exported_ca")"
install -m 0644 -o "$runtime_user" -g "$runtime_group" "$ca_cert" "$exported_ca"

install -m 0644 "$nginx_config" /etc/nginx/sites-available/openim-platform
ln -sfn /etc/nginx/sites-available/openim-platform /etc/nginx/sites-enabled/openim-platform
if [[ -L /etc/nginx/sites-enabled/default ]]; then
  unlink /etc/nginx/sites-enabled/default
fi
nginx -t
systemctl enable nginx.service >/dev/null
systemctl restart nginx.service
openssl s_client \
  -connect 127.0.0.1:3443 \
  -servername "$public_host" \
  -CAfile "$ca_cert" \
  -verify_return_error </dev/null 2>/dev/null | grep -q 'Verify return code: 0 (ok)'
echo "native_tls=ready"
echo "native_ca=$exported_ca"
