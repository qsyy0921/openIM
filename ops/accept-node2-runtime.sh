#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/qsyy0921/MFL/deploy/node2-native}"
release_root="${2:-/home/qsyy0921/MFL/releases/akashic-node2-20260719}"
release_version="$(basename "$release_root")"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
public_origin="${OPENIM_PLATFORM_PUBLIC_ORIGIN:-https://172.31.50.2:3443}"
ca_file="$deploy_root/native-ubuntu/openim-node2-lab-ca.crt"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

[[ -r "$ca_file" ]] || {
  echo "Node2 lab CA is not readable: $ca_file" >&2
  exit 1
}

services=(
  docker.service
  nginx.service
  ollama.service
  openim-platform-api.service
  openim-platform-ingress.service
  openim-intelligence-worker.service
  openim-agent-runtime.service
  openim-agent-delivery.service
  openim-action-executor.service
  openim-memory-projector.service
  openim-memory-extractor.service
  openim-proactive-runtime.service
)
for service in "${services[@]}"; do
  systemctl is-active --quiet "$service" || {
    echo "required service is not active: $service" >&2
    exit 1
  }
done

assert_running_binary() {
  local service="$1"
  local expected="$2"
  local pid actual
  expected="$(readlink -f "$expected")"
  pid="$(systemctl show -p MainPID --value "$service")"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || {
    echo "service has no main process: $service" >&2
    exit 1
  }
  actual="$(readlink -f "/proc/$pid/exe")"
  [[ "$actual" == "$expected" ]] || {
    echo "$service executable mismatch: expected $expected, running $actual" >&2
    exit 1
  }
}

assert_running_binary openim-platform-api.service "$release_root/linux-amd64/platform-api"
assert_running_binary openim-platform-ingress.service "$release_root/linux-amd64/platform-ingress"
assert_running_binary openim-agent-runtime.service "$release_root/linux-amd64/agent-runtime"
assert_running_binary openim-agent-delivery.service "$release_root/linux-amd64/agent-delivery"
assert_running_binary openim-action-executor.service "$release_root/linux-amd64/action-executor"
assert_running_binary openim-memory-projector.service "$release_root/linux-amd64/memory-projector"
assert_running_binary openim-memory-extractor.service "$release_root/linux-amd64/memory-extractor"
assert_running_binary openim-proactive-runtime.service "$release_root/linux-amd64/proactive-runtime"

database_state="$(docker exec "$postgres_container" psql -At -F '|' -U platform -d platform -c \
  "SELECT (SELECT count(*) FROM platform_meta.schema_migrations),
          (SELECT max(name) FROM platform_meta.schema_migrations),
          (SELECT count(*) FROM knowledge.documents),
          (SELECT count(*) FROM knowledge.document_versions),
          (SELECT count(*) FROM knowledge.chunks),
          (SELECT count(*) FROM authz.document_grants),
          (SELECT count(*) FROM knowledge.chunks AS c
             JOIN knowledge.document_versions AS v
               ON v.document_id = c.document_id AND v.id = c.version_id
             JOIN knowledge.documents AS d
               ON d.id = c.document_id AND d.current_version_id = v.id
            WHERE d.status = 'active' AND v.status = 'published'),
          (SELECT count(*) FROM knowledge.chunk_embeddings AS e
             JOIN knowledge.chunks AS c ON c.id = e.chunk_id
             JOIN knowledge.document_versions AS v
               ON v.document_id = c.document_id AND v.id = c.version_id
             JOIN knowledge.documents AS d
               ON d.id = c.document_id AND d.current_version_id = v.id
            WHERE d.status = 'active' AND v.status = 'published'
              AND e.model_revision = 'qwen3-embedding:4b'
              AND e.dimension = 2560 AND e.normalized
              AND e.content_checksum = c.checksum);")"
[[ "$database_state" == "28|sql/0028_remote_a2a.sql|520|624|3224|520|2704|2704" ]] || {
  echo "unexpected Node2 database state: $database_state" >&2
  exit 1
}

for container in openim-server openim-chat "$postgres_container"; do
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container")"
  [[ "$health" == "healthy" ]] || {
    echo "required container is not healthy: $container=$health" >&2
    exit 1
  }
done

curl --fail --silent --show-error http://127.0.0.1:18080/healthz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:11434/api/version >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" "$public_origin/" >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" "$public_origin/platform-api/healthz" >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" \
  "$public_origin/auth/realms/platform/.well-known/openid-configuration" >/dev/null

curl --fail --silent --show-error --max-time 120 \
  -H 'Content-Type: application/json' \
  --data '{"texts":["node2 runtime acceptance"]}' \
  http://127.0.0.1:18082/v1/embeddings >"$work_dir/embedding.json"
python3 - "$work_dir/embedding.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    body = json.load(handle)
vectors = body.get("vectors")
if body.get("model") != "qwen3-embedding:4b":
    raise SystemExit("unexpected embedding model")
if body.get("dimension") != 2560:
    raise SystemExit("unexpected embedding dimension")
if not isinstance(vectors, list) or len(vectors) != 1 or len(vectors[0]) != 2560:
    raise SystemExit("unexpected embedding vector shape")
if not all(isinstance(value, (int, float)) for value in vectors[0]):
    raise SystemExit("embedding vector contains non-numeric values")
PY

echo "node2_database=28|sql/0028_remote_a2a.sql|520|624|3224|520|2704|2704"
[[ "$release_version" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "release version is malformed" >&2
  exit 1
}
echo "node2_release=$release_version"
echo "node2_embedding=qwen3-embedding:4b|2560"
echo "node2_runtime=accepted"
