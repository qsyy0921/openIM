#!/usr/bin/env bash
set -euo pipefail

platform_env="${1:?platform environment path is required}"
public_origin="${2:?public origin is required}"
keycloak_container="${OPENIM_PLATFORM_KEYCLOAK_CONTAINER:-openim-platform-local-keycloak-1}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$public_origin" =~ ^https://[A-Za-z0-9.-]+:[0-9]+$ ]] || {
  echo "public origin must be an explicit HTTPS origin" >&2
  exit 1
}
[[ -r "$platform_env" ]] || {
  echo "platform environment is not readable: $platform_env" >&2
  exit 1
}
docker inspect "$keycloak_container" >/dev/null 2>&1 || {
  echo "Keycloak container is missing: $keycloak_container" >&2
  exit 1
}

admin_password="$(sed -n 's/^PLATFORM_LOCAL_KEYCLOAK_ADMIN_PASSWORD=//p' "$platform_env" | tail -1 | tr -d '\r' | sed -E 's/[[:space:]]+#.*$//')"
[[ "$admin_password" =~ ^[0-9a-f]{48}$ ]] || {
  unset admin_password
  echo "host-local Keycloak admin password is missing or malformed" >&2
  exit 1
}

for _ in $(seq 1 90); do
  if curl -fsS --max-time 3 http://127.0.0.1:18081/auth/realms/platform/.well-known/openid-configuration >/dev/null; then
    break
  fi
  sleep 2
done
curl -fsS --max-time 5 http://127.0.0.1:18081/auth/realms/platform/.well-known/openid-configuration >/dev/null

printf '%s\n' "$admin_password" | docker exec \
  -e "OPENIM_PUBLIC_ORIGIN=$public_origin" \
  -i "$keycloak_container" sh -ec '
    IFS= read -r admin_password
    config=/tmp/openim-node2-kcadm.config
    rm -f "$config"
    /opt/keycloak/bin/kcadm.sh config credentials \
      --config "$config" \
      --server http://127.0.0.1:8080/auth \
      --realm master \
      --user local-admin \
      --password "$admin_password" >/dev/null
    unset admin_password
    client_id="$(/opt/keycloak/bin/kcadm.sh get clients \
      --config "$config" \
      -r platform \
      -q clientId=platform-api \
      --fields id \
      --format csv \
      --noquotes | tail -1)"
    test -n "$client_id"
    /opt/keycloak/bin/kcadm.sh update "clients/$client_id" \
      --config "$config" \
      -r platform \
      -s "redirectUris=[\"$OPENIM_PUBLIC_ORIGIN/*\"]" \
      -s "webOrigins=[\"$OPENIM_PUBLIC_ORIGIN\"]" >/dev/null
    rm -f "$config"
  '
unset admin_password

echo "keycloak_https_client=ready"
