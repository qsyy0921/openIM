#!/usr/bin/env bash
set -euo pipefail

endpoint="${1:-http://127.0.0.1:18082/v1/candidates}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

run_id="node2-intelligence-$(date +%s)-$RANDOM"
request_file="$work_dir/request.json"
response_file="$work_dir/response.json"
status_file="$work_dir/status"

python3 - "$request_file" "$run_id" <<'PY'
import json
import sys

payload = {
    "run_id": sys.argv[2],
    "tenant_id": "node2-acceptance-tenant",
    "conversation_id": "node2-intelligence-acceptance",
    "sender_id": "node2-acceptance-user",
    "agent_id": "node2-acceptance-agent",
    "agent_version_id": "node2-acceptance-version-1",
    "agent_spec_checksum": "sha256:" + "a" * 64,
    "instructions": "Answer only from the supplied authorized evidence and cite it.",
    "model_route": "deepseek-v4-pro",
    "allowed_action_types": ["create_ticket"],
    "content": "根据证据说明本次迁移的验收范围。",
    "evidence": [
        {
            "citation_id": "C1",
            "document_id": "node2-acceptance-document",
            "version_id": "node2-acceptance-version",
            "chunk_id": "node2-acceptance-chunk",
            "title": "Node2 migration acceptance",
            "source_uri": "openim://acceptance/node2",
            "checksum": "node2-acceptance-checksum",
            "content": "本次验收仅覆盖 Windows node1 与原生 Ubuntu node2，不包含其他主机。",
        }
    ],
}
with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump(payload, stream, ensure_ascii=False)
PY

curl --silent --show-error \
  --output "$response_file" \
  --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data-binary "@$request_file" \
  "$endpoint" >"$status_file"

python3 - "$status_file" "$response_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = stream.read().strip()
with open(sys.argv[2], encoding="utf-8") as stream:
    payload = json.load(stream)
assert status == "200", {"status": status, "response": payload}
assert payload.get("citation_ids") == ["C1"], payload
assert "[C1]" in payload.get("text", ""), payload
assert isinstance(payload.get("model"), str) and payload["model"], payload
assert isinstance(payload.get("provider_response_id"), str) and payload["provider_response_id"], payload
assert payload.get("action_intent") is None, payload
print(f"deepseek_model={payload['model']}")
print("deepseek_real_call=accepted")
print("citation_validation=accepted")
PY
