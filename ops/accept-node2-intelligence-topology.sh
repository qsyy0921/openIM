#!/usr/bin/env bash
set -euo pipefail

worker_base="${1:-http://127.0.0.1:18082}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

curl --fail --silent --show-error --max-time 10 "$worker_base/healthz" >"$work_dir/health.json"
curl --fail --silent --show-error --max-time 190 \
  --header 'Content-Type: application/json' \
  --data '{"texts":["bidirectional loopback topology acceptance"]}' \
  "$worker_base/v1/embeddings" >"$work_dir/embedding.json"

python3 - "$work_dir/health.json" "$work_dir/embedding.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    health = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    embedding = json.load(stream)

assert health == {"status": "ok"}, health
assert embedding.get("model") == "qwen3-embedding:4b", embedding.get("model")
assert embedding.get("dimension") == 2560, embedding.get("dimension")
vectors = embedding.get("vectors")
assert isinstance(vectors, list) and len(vectors) == 1, "unexpected embedding vector count"
assert isinstance(vectors[0], list) and len(vectors[0]) == 2560, "unexpected embedding dimension"
assert all(isinstance(value, (int, float)) for value in vectors[0]), "embedding contains non-numeric values"

print("bidirectional_loopback_topology=accepted")
print("embedding_model=qwen3-embedding:4b")
print("embedding_dimension=2560")
PY
