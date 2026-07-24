#!/usr/bin/env bash
set -euo pipefail

deploy_root="${1:-/home/ubuntu/MFL/deploy/node2-20260711}"
release_root="${2:-/home/ubuntu/MFL/releases/d663256}"
bin_dir="$release_root/linux-amd64"
python_dir="$release_root/python"
runtime_user="${OPENIM_PLATFORM_RUNTIME_USER:-qsyy0921}"
runtime_group="${OPENIM_PLATFORM_RUNTIME_GROUP:-$runtime_user}"
mfl_root="${OPENIM_PLATFORM_MFL_ROOT:-$(dirname "$(dirname "$release_root")")}"
retrieval_venv="${OPENIM_RETRIEVAL_VENV:-$mfl_root/venvs/intelligence-worker-retrieval}"
config_dir=/etc/openim-platform
telegram_credential_file="$config_dir/credentials/telegram-bot-token"
action_env="$config_dir/action-executor.env"
retrieval_env="$config_dir/retrieval-worker.env"
postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
intelligence_ssh_target="${OPENIM_INTELLIGENCE_SSH_TARGET:-10495@172.31.50.1}"
windows_embedding_forward_port="${OPENIM_INTELLIGENCE_WINDOWS_EMBEDDING_FORWARD_PORT:-11435}"
embedding_model="${OPENIM_INTELLIGENCE_EMBEDDING_MODEL:-qwen3-embedding:4b}"
embedding_dimension="${OPENIM_INTELLIGENCE_EMBEDDING_DIMENSION:-2560}"
projection_revision="document-title-content-v1"
embedding_timeout="${OPENIM_INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS:-180}"
knowledge_index_batch_size="${OPENIM_KNOWLEDGE_INDEX_BATCH_SIZE:-4}"
knowledge_index_embedding_workers="${OPENIM_KNOWLEDGE_INDEX_EMBEDDING_WORKERS:-2}"
reranker_model="${OPENIM_INTELLIGENCE_RERANKER_MODEL:-BAAI/bge-reranker-v2-m3}"
reranker_revision="${OPENIM_INTELLIGENCE_RERANKER_REVISION:-953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e}"
reranker_path="${OPENIM_INTELLIGENCE_RERANKER_PATH:-$mfl_root/models/bge-reranker-v2-m3-${reranker_revision:0:12}}"
telegram_proxy_url="${OPENIM_TELEGRAM_PROXY_URL:-http://127.0.0.1:7893}"
telegram_no_proxy="${OPENIM_TELEGRAM_NO_PROXY:-127.0.0.1,localhost,172.31.50.0/24,192.168.0.0/24}"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
[[ "$(ps -p 1 -o comm=)" == systemd ]] || {
  echo "systemd must be PID 1" >&2
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
command -v runuser >/dev/null 2>&1 || {
  echo "runuser is required" >&2
  exit 1
}
[[ -d "$python_dir" ]] || {
  echo "release Python directory is missing: $python_dir" >&2
  exit 1
}
mapfile -t intelligence_wheels < <(
  find "$python_dir" -maxdepth 1 -type f -name 'openim_intelligence_worker-*.whl' -print |
    sort
)
(( ${#intelligence_wheels[@]} == 1 )) || {
  echo "release must contain exactly one intelligence worker wheel" >&2
  exit 1
}
intelligence_wheel="${intelligence_wheels[0]}"
[[ -x "$retrieval_venv/bin/python" ]] || {
  echo "pre-provisioned retrieval Python runtime is missing: $retrieval_venv" >&2
  exit 1
}
[[ "$knowledge_index_batch_size" =~ ^[0-9]+$ ]] && \
  ((knowledge_index_batch_size >= 1 && knowledge_index_batch_size <= 128)) || {
  echo "knowledge index batch size must be between 1 and 128" >&2
  exit 1
}
[[ "$knowledge_index_embedding_workers" =~ ^[0-9]+$ ]] && \
  ((knowledge_index_embedding_workers >= 1 && knowledge_index_embedding_workers <= 8)) || {
  echo "knowledge index embedding workers must be between 1 and 8" >&2
  exit 1
}
[[ "$embedding_model" == "qwen3-embedding:4b" && "$embedding_dimension" == "2560" ]] || {
  echo "retrieval embedding model contract is invalid" >&2
  exit 1
}
[[ "$projection_revision" == "document-title-content-v1" ]] || {
  echo "retrieval projection revision contract is invalid" >&2
  exit 1
}
[[ "$reranker_model" == "BAAI/bge-reranker-v2-m3" && \
   "$reranker_revision" == "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e" ]] || {
  echo "retrieval reranker model contract is invalid" >&2
  exit 1
}
[[ -f "$reranker_path/openim-model-manifest.json" ]] || {
  echo "locked reranker manifest is missing: $reranker_path" >&2
  exit 1
}
[[ "$telegram_proxy_url" =~ ^http://127\.0\.0\.1:[0-9]{1,5}$ ]] || {
  echo "Telegram proxy URL must be a loopback HTTP proxy" >&2
  exit 1
}
[[ "$telegram_no_proxy" =~ ^[A-Za-z0-9.,:/-]+$ ]] || {
  echo "Telegram NO_PROXY value is malformed" >&2
  exit 1
}
[[ "$intelligence_ssh_target" =~ ^[A-Za-z0-9._-]+@[A-Za-z0-9.:-]+$ ]] || {
  echo "intelligence SSH target is malformed" >&2
  exit 1
}
[[ "$windows_embedding_forward_port" =~ ^[0-9]+$ ]] && \
  ((windows_embedding_forward_port >= 1024 && windows_embedding_forward_port <= 65535)) || {
  echo "Windows embedding forward port must be between 1024 and 65535" >&2
  exit 1
}
command -v ssh >/dev/null 2>&1 || {
  echo "OpenSSH client is required for the intelligence tunnel" >&2
  exit 1
}
runtime_binaries=(
  agent-catalog-admin
  agent-runtime
  agent-delivery
  capability-admin
  group-memory-admin
  knowledge-ingestion
  knowledge-rag-admin
  mcp-admin
  member-grant-admin
  proactive-admin
  telegram-ingress
  telegram-admin
  memory-extractor
  memory-projector
  proactive-runtime
  role-admin
  runtime-control-admin
  skill-admin
  action-executor
)
for path in "$intelligence_wheel" "$config_dir/platform.env"; do
  [[ -e "$path" ]] || {
    echo "required path is missing: $path" >&2
    exit 1
  }
done
for binary in "${runtime_binaries[@]}"; do
  [[ -e "$bin_dir/$binary" ]] || {
    echo "required path is missing: $bin_dir/$binary" >&2
    exit 1
  }
done

for binary in "${runtime_binaries[@]}"; do
  chmod 0755 "$bin_dir/$binary"
done
install -d -m 0750 -o root -g "$runtime_group" "$config_dir" "$config_dir/credentials"
if [[ ! -e "$telegram_credential_file" ]]; then
  install -m 0400 -o root -g root /dev/null "$telegram_credential_file"
fi
"$retrieval_venv/bin/python" -m pip install \
  --disable-pip-version-check \
  --force-reinstall \
  --no-deps \
  --no-index \
  "$intelligence_wheel"
"$retrieval_venv/bin/python" - <<'PY'
from importlib import metadata

expected = {
    "fastapi": "0.136.0",
    "huggingface-hub": "0.36.0",
    "httpx": "0.28.1",
    "pydantic": "2.12.5",
    "prometheus-client": "0.25.0",
    "PyYAML": "6.0.3",
    "safetensors": "0.7.0",
    "torch": "2.11.0+cpu",
    "transformers": "4.57.1",
    "uvicorn": "0.44.0",
}
for distribution, version in expected.items():
    actual = metadata.version(distribution)
    if actual != version:
        raise SystemExit(
            f"retrieval dependency mismatch: {distribution}={actual}, expected {version}"
        )
PY

cat >"$retrieval_env" <<EOF
INTELLIGENCE_HTTP_PORT=18083
INTELLIGENCE_EMBEDDING_BASE_URL=http://127.0.0.1:11434/v1
INTELLIGENCE_EMBEDDING_MODEL=$embedding_model
INTELLIGENCE_EMBEDDING_DIMENSION=$embedding_dimension
INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS=$embedding_timeout
INTELLIGENCE_RERANKER_MODEL=$reranker_model
INTELLIGENCE_RERANKER_REVISION=$reranker_revision
INTELLIGENCE_RERANKER_PATH=$reranker_path
INTELLIGENCE_RERANKER_DEVICE=cpu
INTELLIGENCE_RERANKER_MAX_LENGTH=512
INTELLIGENCE_RERANKER_BATCH_SIZE=16
TRANSFORMERS_OFFLINE=1
HF_HUB_OFFLINE=1
TOKENIZERS_PARALLELISM=false
OMP_NUM_THREADS=36
MKL_NUM_THREADS=36
EOF
chown root:"$runtime_group" "$retrieval_env"
chmod 0640 "$retrieval_env"
rm -f "$config_dir/intelligence.env"

if [[ ! -f "$action_env" ]]; then
  action_password="$(openssl rand -hex 24)"
else
  action_url="$(sed -n 's/^ACTION_DATABASE_URL=//p' "$action_env")"
  action_password="${action_url#*platform_action_executor:}"
  action_password="${action_password%@127.0.0.1:*}"
fi
[[ "$action_password" =~ ^[0-9a-f]{48}$ ]] || {
  echo "Action Executor password is missing or malformed" >&2
  exit 1
}
escaped_action_password="$(printf '%s' "$action_password" | sed "s/'/''/g")"
docker exec -i "$postgres_container" psql -v ON_ERROR_STOP=1 -U platform -d platform >/dev/null <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platform_action_executor') THEN
    CREATE ROLE platform_action_executor LOGIN PASSWORD '$escaped_action_password';
  ELSE
    ALTER ROLE platform_action_executor PASSWORD '$escaped_action_password';
  END IF;
END
\$\$;
ALTER ROLE platform_action_executor NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
REVOKE ALL ON SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA action, collaboration, audit, agent, identity FROM platform_action_executor;
GRANT USAGE ON SCHEMA action, collaboration, audit, agent, identity TO platform_action_executor;
GRANT SELECT, UPDATE ON action.intents, action.executions TO platform_action_executor;
GRANT SELECT, INSERT ON collaboration.tickets TO platform_action_executor;
GRANT INSERT ON audit.action_events TO platform_action_executor;
GRANT USAGE, SELECT ON SEQUENCE audit.action_events_id_seq TO platform_action_executor;
GRANT SELECT, UPDATE ON agent.runs TO platform_action_executor;
SQL
umask 027
printf 'ACTION_DATABASE_URL=postgres://platform_action_executor:%s@127.0.0.1:15432/platform?sslmode=disable\n' "$action_password" >"$action_env"
printf 'ACTION_POLL_INTERVAL=500ms\nACTION_LEASE=30s\nACTION_DEPENDENCY_TIMEOUT=20s\nACTION_MAX_ATTEMPTS=3\n' >>"$action_env"
unset action_password escaped_action_password action_url
chown root:"$runtime_group" "$action_env"
chmod 0640 "$action_env"

cat >/etc/systemd/system/openim-intelligence-tunnel.service <<EOF
[Unit]
Description=OpenIM loopback-only candidate-generation tunnel to Windows node1
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
ExecStart=/usr/bin/ssh -NT -o BatchMode=yes -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -L 127.0.0.1:18082:127.0.0.1:18082 -R 127.0.0.1:$windows_embedding_forward_port:127.0.0.1:11434 $intelligence_ssh_target
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/openim-retrieval-worker.service <<EOF
[Unit]
Description=OpenIM loopback-only enterprise retrieval worker
Requires=ollama.service
After=network-online.target ollama.service
Wants=network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$retrieval_env
Environment=PYTHONUNBUFFERED=1
Environment=HTTP_PROXY=
Environment=HTTPS_PROXY=
Environment=ALL_PROXY=
Environment=http_proxy=
Environment=https_proxy=
Environment=all_proxy=
Environment=NO_PROXY=127.0.0.1,localhost
ExecStart=$retrieval_venv/bin/python -m intelligence_worker.retrieval_bootstrap
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/openim-agent-runtime.service <<EOF
[Unit]
Description=OpenIM governed Agent Runtime
Requires=docker.service openim-intelligence-tunnel.service openim-retrieval-worker.service
After=docker.service openim-intelligence-tunnel.service openim-retrieval-worker.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
ExecStart=$bin_dir/agent-runtime
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/openim-action-executor.service <<EOF
[Unit]
Description=OpenIM approved Action Executor
Requires=docker.service
After=docker.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$action_env
ExecStart=$bin_dir/action-executor
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

write_platform_worker_unit() {
  local service="$1"
  local description="$2"
  local binary="$3"
  local requires="$4"
  local after="$5"
  cat >"/etc/systemd/system/$service.service" <<EOF
[Unit]
Description=$description
Requires=$requires
After=$after

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
ExecStart=$bin_dir/$binary
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
}

write_telegram_worker_unit() {
  local service="$1"
  local description="$2"
  local binary="$3"
  cat >"/etc/systemd/system/$service.service" <<EOF
[Unit]
Description=$description
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
Environment=HTTP_PROXY=$telegram_proxy_url
Environment=HTTPS_PROXY=$telegram_proxy_url
Environment=NO_PROXY=$telegram_no_proxy
LoadCredential=telegram_bot_token:$telegram_credential_file
ExecStart=/bin/sh -ec 'export PLATFORM_TELEGRAM_BOT_TOKEN="\$(cat "\$CREDENTIALS_DIRECTORY/telegram_bot_token")"; exec $bin_dir/$binary'
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
}

write_delivery_worker_unit() {
  cat >"/etc/systemd/system/openim-agent-delivery.service" <<EOF
[Unit]
Description=OpenIM Agent channel delivery
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
Environment=HTTP_PROXY=$telegram_proxy_url
Environment=HTTPS_PROXY=$telegram_proxy_url
Environment=NO_PROXY=$telegram_no_proxy
LoadCredential=telegram_bot_token:$telegram_credential_file
ExecStart=/bin/sh -ec 'if [ -s "\$CREDENTIALS_DIRECTORY/telegram_bot_token" ]; then export PLATFORM_TELEGRAM_DELIVERY_ENABLED=true; export PLATFORM_TELEGRAM_BOT_TOKEN="\$(cat "\$CREDENTIALS_DIRECTORY/telegram_bot_token")"; else export PLATFORM_TELEGRAM_DELIVERY_ENABLED=false; unset PLATFORM_TELEGRAM_BOT_TOKEN; fi; exec $bin_dir/agent-delivery'
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
}

write_platform_worker_unit \
  openim-memory-projector "OpenIM Agent Memory projector" memory-projector \
  docker.service docker.service
write_platform_worker_unit \
  openim-memory-extractor "OpenIM Agent Memory extractor" memory-extractor \
  "docker.service openim-intelligence-tunnel.service" \
  "docker.service openim-intelligence-tunnel.service"
write_platform_worker_unit \
  openim-proactive-runtime "OpenIM proactive Agent Runtime" proactive-runtime \
  "docker.service openim-intelligence-tunnel.service" \
  "docker.service openim-intelligence-tunnel.service"
install -d -m 0750 -o "$runtime_user" -g "$runtime_group" /var/lib/openim-platform/knowledge-ingestion
cat >/etc/systemd/system/openim-knowledge-ingestion.service <<EOF
[Unit]
Description=OpenIM enterprise knowledge ingestion
Requires=docker.service openim-retrieval-worker.service
After=docker.service openim-retrieval-worker.service

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
EnvironmentFile=$config_dir/platform.env
ExecStart=$bin_dir/knowledge-ingestion
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/lib/openim-platform/knowledge-ingestion
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
write_telegram_worker_unit \
  openim-telegram-ingress "OpenIM Telegram ingress" telegram-ingress
write_delivery_worker_unit

systemctl daemon-reload
systemctl enable openim-action-executor.service openim-memory-projector.service
systemctl restart openim-action-executor.service openim-memory-projector.service
systemctl is-active openim-action-executor.service openim-memory-projector.service

assert_running_binary() {
  local service="$1"
  local expected="$2"
  local pid actual
  expected="$(readlink -f "$expected")"
  actual=""
  for _ in $(seq 1 30); do
    pid="$(systemctl show -p MainPID --value "$service")"
    if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
      actual="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
      [[ "$actual" == "$expected" ]] && return 0
    fi
    sleep 1
  done
  echo "$service executable mismatch: expected $expected, running ${actual:-unavailable}" >&2
  exit 1
}

assert_running_binary openim-action-executor.service "$bin_dir/action-executor"
assert_running_binary openim-memory-projector.service "$bin_dir/memory-projector"

systemctl disable --now openim-intelligence-worker.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/openim-intelligence-worker.service
systemctl daemon-reload
systemctl enable openim-intelligence-tunnel.service openim-retrieval-worker.service \
  openim-agent-runtime.service \
  openim-memory-extractor.service openim-proactive-runtime.service \
  openim-knowledge-ingestion.service
systemctl stop openim-agent-runtime.service openim-memory-extractor.service \
  openim-proactive-runtime.service openim-knowledge-ingestion.service
systemctl restart openim-intelligence-tunnel.service openim-retrieval-worker.service
systemctl is-active openim-intelligence-tunnel.service openim-retrieval-worker.service
for endpoint in http://127.0.0.1:18082/healthz http://127.0.0.1:18083/healthz; do
  for _ in $(seq 1 60); do
    if curl --fail --silent --show-error "$endpoint" >/dev/null; then
      break
    fi
    sleep 2
  done
  curl --fail --silent --show-error "$endpoint" >/dev/null
done
assert_running_binary openim-retrieval-worker.service "$retrieval_venv/bin/python"

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

embedding_probe="$(mktemp)"
trap 'rm -f "$embedding_probe"' EXIT
curl --fail --silent --show-error --max-time "$((embedding_timeout + 5))" \
  -H 'Content-Type: application/json' \
  --data '{"texts":["node2 deployment readiness"]}' \
  http://127.0.0.1:18083/v1/embeddings >"$embedding_probe"
"$retrieval_venv/bin/python" - "$embedding_probe" "$embedding_model" "$embedding_dimension" <<'PY'
import json
import sys

path, expected_model, expected_dimension_raw = sys.argv[1:]
expected_dimension = int(expected_dimension_raw)
with open(path, encoding="utf-8") as handle:
    body = json.load(handle)

vectors = body.get("vectors")
if body.get("model") != expected_model:
    raise SystemExit("embedding probe returned an unexpected model")
if body.get("dimension") != expected_dimension:
    raise SystemExit("embedding probe returned an unexpected dimension")
if not isinstance(vectors, list) or len(vectors) != 1:
    raise SystemExit("embedding probe returned an unexpected vector count")
if not isinstance(vectors[0], list) or len(vectors[0]) != expected_dimension:
    raise SystemExit("embedding probe returned an unexpected vector length")
if not all(isinstance(value, (int, float)) for value in vectors[0]):
    raise SystemExit("embedding probe returned a non-numeric vector")
PY
rm -f "$embedding_probe"
trap - EXIT

reranker_probe="$(mktemp)"
trap 'rm -f "$reranker_probe"' EXIT
curl --fail --silent --show-error --max-time 180 \
  -H 'Content-Type: application/json' \
  --data '{"query":"enterprise security policy","candidates":[{"candidate_id":"probe-1","content":"The enterprise security policy requires access review."}]}' \
  http://127.0.0.1:18083/v1/rerank >"$reranker_probe"
"$retrieval_venv/bin/python" - "$reranker_probe" "$reranker_model" "$reranker_revision" <<'PY'
import json
import math
import sys

path, expected_model, expected_revision = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    body = json.load(handle)
scores = body.get("scores")
if body.get("model") != expected_model or body.get("revision") != expected_revision:
    raise SystemExit("reranker probe returned an unexpected locked model")
if (
    not isinstance(scores, list)
    or len(scores) != 1
    or scores[0].get("candidate_id") != "probe-1"
    or not isinstance(scores[0].get("score"), (int, float))
    or not math.isfinite(scores[0]["score"])
):
    raise SystemExit("reranker probe returned an invalid score")
PY
rm -f "$reranker_probe"
trap - EXIT

database_url="$(sed -n 's/^PLATFORM_DATABASE_URL=//p' "$config_dir/platform.env" | tail -1 | tr -d '\r')"
[[ "$database_url" =~ ^postgres(ql)?://[^[:space:]]+$ ]] || {
  unset database_url
  echo "platform database URL is missing or malformed" >&2
  exit 1
}

knowledge_tenant_output="$(
  docker exec "$postgres_container" psql -At -U platform -d platform -c "
SELECT DISTINCT document.tenant_id::text
FROM knowledge.documents AS document
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
WHERE document.status = 'active'
  AND version.status = 'published'
ORDER BY document.tenant_id::text"
)"
mapfile -t knowledge_tenant_ids < <(printf '%s' "$knowledge_tenant_output")
unset knowledge_tenant_output

index_report="$(mktemp)"
trap 'rm -f "$index_report"' EXIT
knowledge_indexed_total=0
for tenant_id in "${knowledge_tenant_ids[@]}"; do
  [[ "$tenant_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] || {
    echo "knowledge index tenant ID is malformed" >&2
    exit 1
  }
  runuser -u "$runtime_user" -- env \
    PLATFORM_DATABASE_URL="$database_url" \
    PLATFORM_RETRIEVAL_INTELLIGENCE_URL=http://127.0.0.1:18083 \
    PLATFORM_RETRIEVAL_EMBEDDING_MODEL="$embedding_model" \
    PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION="$embedding_dimension" \
    PLATFORM_RETRIEVAL_PROJECTION_REVISION="$projection_revision" \
    "$bin_dir/knowledge-rag-admin" \
      -mode index \
      -tenant-id "$tenant_id" \
      -intelligence-url http://127.0.0.1:18083 \
      -model "$embedding_model" \
      -projection-revision "$projection_revision" \
      -dimension "$embedding_dimension" \
      -batch-size "$knowledge_index_batch_size" \
      -embedding-workers "$knowledge_index_embedding_workers" \
      -timeout "$((embedding_timeout * 2))s" >"$index_report"
  read -r indexed expected < <(
    "$retrieval_venv/bin/python" - "$index_report" "$projection_revision" <<'PY'
import json
import sys
import uuid

path, expected_projection = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    body = json.load(handle)
try:
    uuid.UUID(body.get("generation_id", ""), version=4)
except (AttributeError, TypeError, ValueError):
    raise SystemExit("knowledge index generation ID is invalid")
expected = body.get("expected_chunks")
indexed = body.get("indexed_chunks")
if (
    body.get("projection_revision") != expected_projection
    or body.get("state") != "active"
    or body.get("activated") is not True
    or not isinstance(expected, int)
    or expected < 1
    or indexed != expected
):
    raise SystemExit("knowledge embedding index report violates the activation contract")
print(indexed, expected)
PY
  )
  knowledge_indexed_total=$((knowledge_indexed_total + indexed))
done
unset database_url
rm -f "$index_report"
trap - EXIT
echo "knowledge_index_tenants=${#knowledge_tenant_ids[@]}"
echo "knowledge_embeddings_indexed=$knowledge_indexed_total"

systemctl restart openim-agent-runtime.service openim-memory-extractor.service \
  openim-proactive-runtime.service openim-knowledge-ingestion.service
systemctl is-active openim-intelligence-tunnel.service openim-retrieval-worker.service \
  openim-agent-runtime.service openim-memory-extractor.service \
  openim-proactive-runtime.service openim-knowledge-ingestion.service
assert_running_binary openim-agent-runtime.service "$bin_dir/agent-runtime"
assert_running_binary openim-memory-extractor.service "$bin_dir/memory-extractor"
assert_running_binary openim-proactive-runtime.service "$bin_dir/proactive-runtime"
assert_running_binary openim-knowledge-ingestion.service "$bin_dir/knowledge-ingestion"
echo "candidate_generation_tunnel=active"
echo "retrieval_worker=active"
systemctl enable openim-agent-delivery.service
systemctl restart openim-agent-delivery.service
systemctl is-active openim-agent-delivery.service
assert_running_binary openim-agent-delivery.service "$bin_dir/agent-delivery"
if [[ -s "$telegram_credential_file" ]]; then
  systemctl enable openim-telegram-ingress.service
  systemctl restart openim-telegram-ingress.service
  systemctl is-active openim-telegram-ingress.service
  assert_running_binary openim-telegram-ingress.service "$bin_dir/telegram-ingress"
  echo "telegram_credential=present"
else
  systemctl disable --now openim-telegram-ingress.service >/dev/null 2>&1 || true
  echo "telegram_delivery=disabled"
  echo "telegram_credential=required"
fi
echo "agent_runtime_binaries=verified"
echo "node2_agent_runtime=installed"
