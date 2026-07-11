#!/usr/bin/env bash
set -euo pipefail

: "${OPENIM_PROXY_URL:?set OPENIM_PROXY_URL to the Windows WSL-only proxy bridge}"

readonly docker_ce_version="${DOCKER_CE_VERSION:-5:29.6.1-1~ubuntu.24.04~noble}"
readonly docker_key_fingerprint="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi

if ! grep -qi microsoft /proc/sys/kernel/osrelease; then
  echo "this provisioner requires WSL2" >&2
  exit 1
fi

# shellcheck disable=SC1091
source /etc/os-release
if [[ "${ID:-}" != "ubuntu" || "${VERSION_CODENAME:-}" != "noble" ]]; then
  echo "expected Ubuntu 24.04 noble, got ${PRETTY_NAME:-unknown}" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
export HTTP_PROXY="$OPENIM_PROXY_URL"
export HTTPS_PROXY="$OPENIM_PROXY_URL"
export http_proxy="$OPENIM_PROXY_URL"
export https_proxy="$OPENIM_PROXY_URL"
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"

install -d -m 0755 /etc/apt/apt.conf.d /etc/apt/keyrings /etc/openim
cat >/etc/apt/apt.conf.d/90openim-proxy <<EOF
Acquire::http::Proxy "$OPENIM_PROXY_URL";
Acquire::https::Proxy "$OPENIM_PROXY_URL";
Acquire::Retries "3";
EOF

cat >/etc/openim/proxy.env <<EOF
HTTP_PROXY=$OPENIM_PROXY_URL
HTTPS_PROXY=$OPENIM_PROXY_URL
NO_PROXY=127.0.0.1,localhost
EOF
chmod 0600 /etc/openim/proxy.env

created_policy_rc=0
if [[ ! -e /usr/sbin/policy-rc.d ]]; then
  printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
  chmod 0755 /usr/sbin/policy-rc.d
  created_policy_rc=1
fi
cleanup() {
  if [[ "$created_policy_rc" -eq 1 ]]; then
    rm -f /usr/sbin/policy-rc.d
  fi
}
trap cleanup EXIT

apt-get update
apt-get install -y ca-certificates curl gnupg

for package in docker.io docker-compose docker-compose-v2 docker-doc podman-docker containerd runc; do
  apt-get remove -y "$package" >/dev/null 2>&1 || true
done

key_tmp="$(mktemp)"
curl --fail --show-error --silent --location \
  https://download.docker.com/linux/ubuntu/gpg \
  --output "$key_tmp"
actual_fingerprint="$(gpg --show-keys --with-colons "$key_tmp" | awk -F: '$1 == "fpr" { print $10; exit }')"
if [[ "$actual_fingerprint" != "$docker_key_fingerprint" ]]; then
  echo "Docker repository key fingerprint mismatch: $actual_fingerprint" >&2
  exit 1
fi
install -m 0644 "$key_tmp" /etc/apt/keyrings/docker.asc
rm -f "$key_tmp"

cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: noble
Components: stable
Architectures: amd64
Signed-By: /etc/apt/keyrings/docker.asc
EOF

apt-get update
if ! apt-cache madison docker-ce | awk '{print $3}' | grep -Fxq "$docker_ce_version"; then
  echo "required Docker CE version is unavailable: $docker_ce_version" >&2
  exit 1
fi

apt-get install -y \
  "docker-ce=$docker_ce_version" \
  "docker-ce-cli=$docker_ce_version" \
  containerd.io \
  docker-buildx-plugin \
  docker-compose-plugin

apt-mark hold docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# The inbox WSL2 5.10 kernel on node2 lacks the nftables addrtype support
# required by Docker 29's nft backend. Docker supports iptables-legacy.
update-alternatives --set iptables /usr/sbin/iptables-legacy
update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy

install -d -m 0755 /etc/docker
cat >/etc/docker/daemon.json <<'EOF'
{
  "data-root": "/var/lib/docker",
  "live-restore": true,
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "20m",
    "max-file": "5"
  }
}
EOF

install -d -m 0755 /etc/systemd/system/docker.service.d
cat >/etc/systemd/system/docker.service.d/openim-proxy.conf <<'EOF'
[Service]
EnvironmentFile=/etc/openim/proxy.env
EOF

cat >/usr/local/sbin/openim-start-docker <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

[[ "$(ps -p 1 -o comm=)" == "systemd" ]] || {
  echo "modern WSL systemd is required" >&2
  exit 1
}
systemctl daemon-reload
systemctl enable docker
systemctl restart docker
docker info >/dev/null
iptables -t nat -C OUTPUT -d 172.31.50.2/32 -p tcp --dport 18081 -j REDIRECT --to-ports 18081 2>/dev/null || \
  iptables -t nat -A OUTPUT -d 172.31.50.2/32 -p tcp --dport 18081 -j REDIRECT --to-ports 18081
EOF
chmod 0755 /usr/local/sbin/openim-start-docker

cleanup
trap - EXIT
/usr/local/sbin/openim-start-docker

docker version
docker compose version
docker info --format 'DockerRootDir={{.DockerRootDir}} Driver={{.Driver}} ServerVersion={{.ServerVersion}}'
