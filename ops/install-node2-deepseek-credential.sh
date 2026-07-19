#!/usr/bin/env bash
set -euo pipefail

credential_dir=/etc/openim-platform/credentials
credential_file="$credential_dir/deepseek-api-key"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}

IFS= read -r key
key="${key%$'\r'}"
[[ "$key" =~ ^sk-[A-Za-z0-9_-]{20,}$ ]] || {
  unset key
  echo "DeepSeek credential is missing or malformed" >&2
  exit 1
}

install -d -m 0750 -o root -g root "$credential_dir"
umask 077
printf '%s' "$key" >"$credential_file"
unset key
chown root:root "$credential_file"
chmod 0400 "$credential_file"

runtime_services=(openim-agent-runtime.service)
for service in openim-memory-extractor.service openim-proactive-runtime.service; do
  if systemctl cat "$service" >/dev/null 2>&1; then
    runtime_services+=("$service")
  fi
done
systemctl enable openim-intelligence-worker.service "${runtime_services[@]}" >/dev/null
systemctl restart openim-intelligence-worker.service

for _ in $(seq 1 60); do
  if curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null; then
    break
  fi
  sleep 2
done
curl --fail --silent --show-error http://127.0.0.1:18082/healthz >/dev/null

intelligence_env=/etc/openim-platform/intelligence.env
[[ -r "$intelligence_env" ]] || {
  echo "required path is missing: $intelligence_env" >&2
  exit 1
}
embedding_model="$(sed -n 's/^INTELLIGENCE_EMBEDDING_MODEL=//p' "$intelligence_env")"
embedding_dimension="$(sed -n 's/^INTELLIGENCE_EMBEDDING_DIMENSION=//p' "$intelligence_env")"
embedding_timeout="$(sed -n 's/^INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS=//p' "$intelligence_env")"
[[ -n "$embedding_model" && "$embedding_dimension" =~ ^[0-9]+$ && "$embedding_timeout" =~ ^[0-9]+$ ]] || {
  echo "embedding configuration is missing or malformed" >&2
  exit 1
}
embedding_probe="$(mktemp)"
trap 'rm -f "$embedding_probe"' EXIT
curl --fail --silent --show-error --max-time "$((embedding_timeout + 5))" \
  -H 'Content-Type: application/json' \
  --data '{"texts":["node2 credential readiness"]}' \
  http://127.0.0.1:18082/v1/embeddings >"$embedding_probe"
python3 - "$embedding_probe" "$embedding_model" "$embedding_dimension" <<'PY'
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

systemctl restart "${runtime_services[@]}"
systemctl is-active --quiet openim-intelligence-worker.service
for service in "${runtime_services[@]}"; do
  systemctl is-active --quiet "$service"
done
echo "intelligence_worker=active"
echo "agent_runtime=active"
