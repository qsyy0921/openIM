#!/usr/bin/env bash
set -euo pipefail

runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-qsyy0921}"
container_name="${OPENIM_KNOWLEDGE_MINIO_CONTAINER:-openim-platform-knowledge-minio}"
image_ref="minio/minio@sha256:796f75ea413b883cec953cb5f0bd2b6050615e36123f36e39aa5555096a07bc1"
listen_port="${OPENIM_KNOWLEDGE_MINIO_PORT:-12015}"
data_dir="${OPENIM_KNOWLEDGE_MINIO_DATA_DIR:-/home/qsyy0921/MFL/data/openim-platform-knowledge-minio}"
credential_env="${OPENIM_KNOWLEDGE_MINIO_CREDENTIAL_ENV:-/etc/openim-platform/knowledge-minio.env}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
getent group "$runtime_group" >/dev/null 2>&1 || {
  echo "runtime group does not exist: $runtime_group" >&2
  exit 1
}
[[ "$listen_port" =~ ^[0-9]+$ ]] && ((listen_port >= 1024 && listen_port <= 65535)) || {
  echo "knowledge MinIO port is invalid" >&2
  exit 1
}
command -v docker >/dev/null 2>&1 || {
  echo "Docker is required" >&2
  exit 1
}
command -v curl >/dev/null 2>&1 || {
  echo "curl is required" >&2
  exit 1
}
command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required" >&2
  exit 1
}
docker image inspect "$image_ref" >/dev/null 2>&1 || {
  echo "pinned knowledge MinIO image is not present" >&2
  exit 1
}

install -d -m 0750 -o root -g "$runtime_group" /etc/openim-platform
install -d -m 0700 -o root -g root "$data_dir"
if [[ ! -e "$credential_env" ]]; then
  access_key="knowledge$(openssl rand -hex 12)"
  secret_key="$(openssl rand -base64 48 | tr -d '\r\n+/=' | cut -c1-48)"
  [[ "$access_key" =~ ^knowledge[0-9a-f]{24}$ && "$secret_key" =~ ^[A-Za-z0-9]{48}$ ]] || {
    echo "failed to generate knowledge MinIO credentials" >&2
    exit 1
  }
  umask 077
  {
    printf 'MINIO_ROOT_USER=%s\n' "$access_key"
    printf 'MINIO_ROOT_PASSWORD=%s\n' "$secret_key"
    printf 'MINIO_BROWSER=off\n'
  } >"$credential_env"
fi
[[ "$(stat -c '%U:%G:%a' "$credential_env")" == "root:root:600" ]] || {
  echo "knowledge MinIO credential file ownership or mode is invalid" >&2
  exit 1
}

expected_image_id="$(docker image inspect "$image_ref" --format '{{.Id}}')"
if docker container inspect "$container_name" >/dev/null 2>&1; then
  actual_image_id="$(docker container inspect "$container_name" --format '{{.Image}}')"
  actual_mount="$(docker container inspect "$container_name" --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Source}}{{end}}{{end}}')"
  actual_binding="$(docker container inspect "$container_name" --format '{{(index (index .HostConfig.PortBindings "9000/tcp") 0).HostIp}}:{{(index (index .HostConfig.PortBindings "9000/tcp") 0).HostPort}}')"
  [[ "$actual_image_id" == "$expected_image_id" &&
      "$actual_mount" == "$data_dir" &&
      "$actual_binding" == "127.0.0.1:$listen_port" ]] || {
    echo "existing knowledge MinIO container violates the locked topology" >&2
    exit 1
  }
  docker start "$container_name" >/dev/null
else
  docker run --detach \
    --name "$container_name" \
    --restart unless-stopped \
    --security-opt no-new-privileges:true \
    --cap-drop ALL \
    --pids-limit 256 \
    --env-file "$credential_env" \
    --publish "127.0.0.1:$listen_port:9000" \
    --mount "type=bind,source=$data_dir,target=/data" \
    "$image_ref" server /data --address ":9000" >/dev/null
fi

for _ in $(seq 1 60); do
  if curl --silent --fail --max-time 2 \
    "http://127.0.0.1:$listen_port/minio/health/ready" >/dev/null; then
    echo "knowledge_minio=ready"
    exit 0
  fi
  sleep 1
done

echo "knowledge MinIO did not become ready" >&2
exit 1
