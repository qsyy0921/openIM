#!/usr/bin/env bash
set -euo pipefail

release_root="${1:?release root is required}"
nginx_config="${2:?nginx configuration path is required}"
version="$(basename "$release_root")"
source_root="$release_root/web"
web_root=/srv/openim-platform/web
destination="$web_root/$version"
health_url="${OPENIM_PLATFORM_WEB_HEALTH_URL:-https://127.0.0.1:3443/}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
command -v nginx >/dev/null 2>&1 || {
  echo "nginx is required" >&2
  exit 1
}
for path in "$source_root/index.html" "$nginx_config"; do
  [[ -f "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done
[[ ! -e "$destination" ]] || {
  echo "immutable Web release already exists: $destination" >&2
  exit 1
}

install -d -m 0755 "$web_root"
staging="$(mktemp -d "$web_root/.staging-$version-XXXXXX")"
trap 'rm -rf "$staging"' EXIT
cp -a "$source_root/." "$staging/"
find "$staging" -type d -exec chmod 0755 {} +
find "$staging" -type f -exec chmod 0644 {} +
mv "$staging" "$destination"
trap - EXIT

ln -sfn "$destination" "$web_root/.current-$version"
mv -Tf "$web_root/.current-$version" "$web_root/current"
install -m 0644 "$nginx_config" /etc/nginx/sites-available/openim-platform
ln -sfn /etc/nginx/sites-available/openim-platform /etc/nginx/sites-enabled/openim-platform
if [[ -L /etc/nginx/sites-enabled/default ]]; then
  unlink /etc/nginx/sites-enabled/default
fi
nginx -t
systemctl enable nginx.service
systemctl restart nginx.service
curl -fsS --max-time 5 "$health_url" >/dev/null
echo "native_web=ready"
