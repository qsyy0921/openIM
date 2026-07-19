#!/usr/bin/env bash
set -euo pipefail

archive="${1:?usage: install-node2-ollama.sh <archive> [runtime-user] [install-root]}"
runtime_user="${2:-qsyy0921}"
install_root="${3:-/home/$runtime_user/MFL/ollama}"
version="${OPENIM_OLLAMA_VERSION:-v0.32.1}"
archive_sha256="${OPENIM_OLLAMA_ARCHIVE_SHA256:-83b1f22841eb7f6c4900c6797f960ebaa09466874442ea5b8ae3da6980d3914c}"
embedding_model="${OPENIM_OLLAMA_EMBEDDING_MODEL:-qwen3-embedding:4b}"
embedding_dimension="${OPENIM_OLLAMA_EMBEDDING_DIMENSION:-2560}"
version_root="$install_root/$version"
current_root="$install_root/current"
models_root="$install_root/models"
state_root="$install_root/state"

[[ "$(id -u)" -eq 0 ]] || {
  echo "run as root" >&2
  exit 1
}
id "$runtime_user" >/dev/null 2>&1 || {
  echo "runtime user does not exist: $runtime_user" >&2
  exit 1
}
[[ -f "$archive" ]] || {
  echo "Ollama archive is missing: $archive" >&2
  exit 1
}
[[ "$archive_sha256" =~ ^[0-9a-f]{64}$ ]] || {
  echo "Ollama archive digest is malformed" >&2
  exit 1
}
[[ "$embedding_dimension" =~ ^[0-9]+$ ]] || {
  echo "embedding dimension is malformed" >&2
  exit 1
}

actual_sha256="$(sha256sum "$archive" | awk '{print $1}')"
[[ "$actual_sha256" == "$archive_sha256" ]] || {
  echo "Ollama archive digest mismatch" >&2
  exit 1
}

runtime_group="$(id -gn "$runtime_user")"
install -d -m 0755 -o "$runtime_user" -g "$runtime_group" \
  "$install_root" "$models_root" "$state_root"
if [[ ! -x "$version_root/bin/ollama" ]]; then
  install -d -m 0755 -o "$runtime_user" -g "$runtime_group" "$version_root"
  zstd -dc "$archive" | tar -xf - -C "$version_root"
  chown -R "$runtime_user:$runtime_group" "$version_root"
fi
ln -sfn "$version_root" "$current_root"
chown -h "$runtime_user:$runtime_group" "$current_root"

cat >/etc/systemd/system/ollama.service <<EOF
[Unit]
Description=Ollama local embedding runtime
After=network-online.target

[Service]
Type=simple
User=$runtime_user
Group=$runtime_group
Environment=OLLAMA_HOST=127.0.0.1:11434
Environment=OLLAMA_MODELS=$models_root
Environment=OLLAMA_KEEP_ALIVE=5m
Environment=HOME=$state_root
ExecStart=$current_root/bin/ollama serve
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$models_root $state_root
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now ollama.service
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error http://127.0.0.1:11434/api/version >/dev/null; then
    break
  fi
  sleep 2
done
curl --fail --silent --show-error http://127.0.0.1:11434/api/version >/dev/null

pull_log="$(mktemp)"
pull_complete=false
for attempt in $(seq 1 5); do
  if runuser -u "$runtime_user" -- env \
    OLLAMA_HOST=http://127.0.0.1:11434 \
    OLLAMA_MODELS="$models_root" \
    "$current_root/bin/ollama" pull "$embedding_model" >"$pull_log" 2>&1; then
    pull_complete=true
    break
  fi
  echo "Ollama model pull attempt $attempt failed" >&2
  sleep 5
done
if [[ "$pull_complete" != "true" ]]; then
  tail -n 20 "$pull_log" >&2
  rm -f "$pull_log"
  echo "Ollama model pull failed after 5 attempts" >&2
  exit 1
fi
rm -f "$pull_log"

embedding_probe="$(mktemp)"
trap 'rm -f "$embedding_probe"' EXIT
curl --fail --silent --show-error --max-time 120 \
  -H 'Content-Type: application/json' \
  --data "{\"model\":\"$embedding_model\",\"input\":[\"node2 ollama readiness\"]}" \
  http://127.0.0.1:11434/v1/embeddings >"$embedding_probe"
python3 - "$embedding_probe" "$embedding_dimension" <<'PY'
import json
import sys

path, expected_dimension_raw = sys.argv[1:]
expected_dimension = int(expected_dimension_raw)
with open(path, encoding="utf-8") as handle:
    body = json.load(handle)

data = body.get("data")
if not isinstance(data, list) or len(data) != 1:
    raise SystemExit("Ollama embedding probe returned an unexpected item count")
vector = data[0].get("embedding") if isinstance(data[0], dict) else None
if not isinstance(vector, list) or len(vector) != expected_dimension:
    raise SystemExit("Ollama embedding probe returned an unexpected vector length")
if not all(isinstance(value, (int, float)) for value in vector):
    raise SystemExit("Ollama embedding probe returned a non-numeric vector")
PY
rm -f "$embedding_probe"
trap - EXIT

systemctl is-active --quiet ollama.service
echo "ollama_version=$version"
echo "embedding_model=$embedding_model"
echo "embedding_dimension=$embedding_dimension"
echo "node2_ollama=installed"
