#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/qsyy0921/MFL/deploy/node2-native}"
release_root="${2:-/home/qsyy0921/MFL/releases/akashic-node2-20260719}"
release_version="$(basename "$release_root")"
mfl_root="${OPENIM_PLATFORM_MFL_ROOT:-$(dirname "$(dirname "$release_root")")}"
retrieval_venv="${OPENIM_RETRIEVAL_VENV:-$mfl_root/venvs/intelligence-worker-retrieval}"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
knowledge_minio_container="${OPENIM_KNOWLEDGE_MINIO_CONTAINER:-openim-platform-knowledge-minio}"
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
  openim-intelligence-tunnel.service
  openim-retrieval-worker.service
  openim-agent-runtime.service
  openim-agent-delivery.service
  openim-action-executor.service
  openim-memory-projector.service
  openim-memory-extractor.service
  openim-proactive-runtime.service
  openim-knowledge-ingestion.service
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
assert_running_binary openim-retrieval-worker.service "$retrieval_venv/bin/python"
assert_running_binary openim-agent-runtime.service "$release_root/linux-amd64/agent-runtime"
assert_running_binary openim-agent-delivery.service "$release_root/linux-amd64/agent-delivery"
assert_running_binary openim-action-executor.service "$release_root/linux-amd64/action-executor"
assert_running_binary openim-memory-projector.service "$release_root/linux-amd64/memory-projector"
assert_running_binary openim-memory-extractor.service "$release_root/linux-amd64/memory-extractor"
assert_running_binary openim-proactive-runtime.service "$release_root/linux-amd64/proactive-runtime"
assert_running_binary openim-knowledge-ingestion.service "$release_root/linux-amd64/knowledge-ingestion"

database_state="$(docker exec "$postgres_container" psql -At -F '|' -U platform -d platform -c \
  "SELECT (SELECT count(*) FROM platform_meta.schema_migrations),
          (SELECT max(name) FROM platform_meta.schema_migrations),
          (SELECT extversion FROM pg_extension WHERE extname = 'vector'),
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
          (SELECT count(*) FROM knowledge.index_generations
            WHERE state = 'active'
              AND model_revision = 'qwen3-embedding:4b'
              AND projection_revision = 'document-title-content-v1'
              AND dimension = 2560),
          (SELECT count(*) FROM knowledge.chunk_search_indexes AS search
             JOIN knowledge.index_generations AS generation
               ON generation.id = search.generation_id
              AND generation.tenant_id = search.tenant_id
             JOIN knowledge.chunks AS chunk
               ON chunk.id = search.chunk_id
              AND chunk.tenant_id = search.tenant_id
             JOIN knowledge.document_versions AS version
               ON version.id = chunk.version_id
              AND version.tenant_id = chunk.tenant_id
             JOIN knowledge.documents AS document
               ON document.id = chunk.document_id
              AND document.tenant_id = chunk.tenant_id
              AND document.current_version_id = version.id
            WHERE generation.state = 'active'
              AND document.status = 'active'
              AND version.status = 'published'
              AND search.model_revision = 'qwen3-embedding:4b'
              AND search.projection_revision = 'document-title-content-v1'
              AND search.dimension = 2560
              AND search.normalized
              AND search.content_checksum = chunk.checksum);")"
[[ "$database_state" == "33|sql/0033_knowledge_projection_revision.sql|0.8.5|520|624|3224|520|2704|1|2704" ]] || {
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

knowledge_minio_state="$(docker inspect --format '{{.State.Status}}' "$knowledge_minio_container")"
knowledge_minio_binding="$(docker inspect "$knowledge_minio_container" --format '{{(index (index .HostConfig.PortBindings "9000/tcp") 0).HostIp}}:{{(index (index .HostConfig.PortBindings "9000/tcp") 0).HostPort}}')"
[[ "$knowledge_minio_state" == "running" && "$knowledge_minio_binding" == "127.0.0.1:12015" ]] || {
  echo "dedicated knowledge MinIO topology is invalid" >&2
  exit 1
}

curl --fail --silent --show-error http://127.0.0.1:18080/healthz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:18083/healthz >/dev/null
curl --fail --silent --show-error http://127.0.0.1:11434/api/version >/dev/null
curl --fail --silent --show-error http://127.0.0.1:12015/minio/health/ready >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" "$public_origin/" >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" "$public_origin/platform-api/healthz" >/dev/null
curl --fail --silent --show-error --cacert "$ca_file" \
  "$public_origin/auth/realms/platform/.well-known/openid-configuration" >/dev/null

curl --fail --silent --show-error --max-time 120 \
  -H 'Content-Type: application/json' \
  --data '{"texts":["node2 runtime acceptance"]}' \
  http://127.0.0.1:18083/v1/embeddings >"$work_dir/embedding.json"
"$retrieval_venv/bin/python" - "$work_dir/embedding.json" <<'PY'
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

curl --fail --silent --show-error --max-time 180 \
  -H 'Content-Type: application/json' \
  --data '{"query":"enterprise security policy","candidates":[{"candidate_id":"acceptance-1","content":"The enterprise security policy requires access review."}]}' \
  http://127.0.0.1:18083/v1/rerank >"$work_dir/reranker.json"
"$retrieval_venv/bin/python" - "$work_dir/reranker.json" <<'PY'
import json
import math
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    body = json.load(handle)
scores = body.get("scores")
if body.get("model") != "BAAI/bge-reranker-v2-m3":
    raise SystemExit("unexpected reranker model")
if body.get("revision") != "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e":
    raise SystemExit("unexpected reranker revision")
if (
    not isinstance(scores, list)
    or len(scores) != 1
    or scores[0].get("candidate_id") != "acceptance-1"
    or not isinstance(scores[0].get("score"), (int, float))
    or not math.isfinite(scores[0]["score"])
):
    raise SystemExit("invalid reranker score")
PY

retrieval_candidate_status="$(
  curl --silent --output /dev/null --write-out '%{http_code}' \
    -H 'Content-Type: application/json' \
    --data '{}' \
    http://127.0.0.1:18083/v1/candidates
)"
[[ "$retrieval_candidate_status" == "404" ]] || {
  echo "retrieval worker unexpectedly exposes candidate generation" >&2
  exit 1
}

echo "node2_database=33|sql/0033_knowledge_projection_revision.sql|pgvector:0.8.5|520|624|3224|520|2704|active-generations:1|indexed:2704"
[[ "$release_version" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "release version is malformed" >&2
  exit 1
}
echo "node2_release=$release_version"
echo "node2_embedding=qwen3-embedding:4b|2560"
echo "node2_reranker=BAAI/bge-reranker-v2-m3|953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"
echo "node2_retrieval_worker=loopback|18083|retrieval-only"
echo "node1_candidate_generation=loopback-tunnel|18082"
echo "node2_knowledge_minio=loopback|12015|dedicated"
echo "node2_runtime=accepted"
