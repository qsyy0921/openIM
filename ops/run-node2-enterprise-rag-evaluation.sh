#!/usr/bin/env bash
set -euo pipefail

evaluation_root="${1:-/home/qsyy0921/MFL/eval/enterprise-rag}"
application_commit="${2:-}"
tenant_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
member_id="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
retrieval_url="http://127.0.0.1:18083"
generation_url="http://127.0.0.1:18082"
embedding_model="qwen3-embedding:4b"
generation_model="gpt-5.6-terra"
reranker_revision="953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"

[[ "$evaluation_root" == /* && "$evaluation_root" != "/" ]] || {
  echo "evaluation root must be an absolute non-root path" >&2
  exit 1
}
[[ "$application_commit" =~ ^[0-9a-f]{40}$ ]] || {
  echo "evaluated application commit must be a full lowercase Git commit" >&2
  exit 1
}
[[ "$retrieval_url" != "$generation_url" ]] || {
  echo "retrieval and generation endpoints must remain separate" >&2
  exit 1
}

admin="$evaluation_root/knowledge-rag-admin"
database_runner="$evaluation_root/with_eval_db.sh"
qa="$evaluation_root/qa.jsonl"
retrieval_report="$evaluation_root/retrieval-report.json"
generation_report="$evaluation_root/generation-report.json"
final_report="$evaluation_root/final-report.json"
for path in "$admin" "$database_runner" "$qa" "$retrieval_report"; do
  [[ -e "$path" ]] || {
    echo "required evaluation input is missing: $path" >&2
    exit 1
  }
done

validate_retrieval() {
  python3 - "$retrieval_report" <<'PY'
import json
import math
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
expected = {
    "schema_version": 4,
    "cases": 1120,
    "acl_denied_cases": 1120,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"retrieval report violates {field}")
thresholds = {
    "recall_at_5": 0.85,
    "recall_at_10": 0.928846,
    "mrr": 0.70,
    "provenance_integrity": 1.0,
    "checksum_integrity": 1.0,
}
for field, minimum in thresholds.items():
    measured = report.get(field)
    if not isinstance(measured, (int, float)) or not math.isfinite(measured) or measured < minimum:
        raise SystemExit(f"retrieval gate failed: {field}")
for field in ("acl_leakage_rate", "stale_version_leakage_rate"):
    if report.get(field) != 0:
        raise SystemExit(f"retrieval gate failed: {field}")
print("retrieval_gate=passed")
PY
}

validate_generation() {
  python3 - "$generation_report" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if (
    report.get("schema_version") != 1
    or report.get("model") != "gpt-5.6-terra"
    or report.get("cases") != 120
    or report.get("answerable_cases") != 60
    or report.get("unanswerable_cases") != 60
    or report.get("production_gate_evaluated") is not True
    or report.get("production_gate_passed") is not True
):
    raise SystemExit("generation evaluation gate failed")
print("generation_gate=passed")
PY
}

validate_final() {
  python3 - "$final_report" "$application_commit" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if (
    report.get("schema_version") != 1
    or report.get("application_commit") != sys.argv[2]
    or report.get("embedding_revision") != "qwen3-embedding:4b"
    or report.get("reranker_revision")
    != "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"
    or report.get("generation_model") != "gpt-5.6-terra"
    or report.get("passed") is not True
):
    raise SystemExit("final production evaluation gate failed")
print("enterprise_rag_evaluation=passed")
PY
}

validate_retrieval
if [[ -f "$final_report" ]]; then
  validate_generation
  validate_final
  exit 0
fi

if [[ -f "$generation_report" ]]; then
  validate_generation
else
  generation_tmp="$(mktemp "$evaluation_root/.generation-report.XXXXXXXX")"
  trap 'rm -f -- "${generation_tmp:-}" "${final_tmp:-}"' EXIT
  "$database_runner" "$admin" \
    -mode evaluate-generation \
    -tenant-id "$tenant_id" \
    -member-id "$member_id" \
    -intelligence-url "$retrieval_url" \
    -generation-url "$generation_url" \
    -model "$embedding_model" \
    -generation-model "$generation_model" \
    -generation-answerable 60 \
    -generation-unanswerable 60 \
    -qa "$qa" \
    -limit 8 \
    -timeout 300s \
    -output "$generation_tmp" >/dev/null
  mv -- "$generation_tmp" "$generation_report"
  generation_tmp=""
  validate_generation
fi

final_tmp="$(mktemp "$evaluation_root/.final-report.XXXXXXXX")"
trap 'rm -f -- "${generation_tmp:-}" "${final_tmp:-}"' EXIT
"$database_runner" "$admin" \
  -mode finalize \
  -tenant-id "$tenant_id" \
  -retrieval-report "$retrieval_report" \
  -generation-report "$generation_report" \
  -application-commit "$application_commit" \
  -dataset-revision enterprise-knowledge/v1 \
  -model "$embedding_model" \
  -reranker-revision "$reranker_revision" \
  -generation-model "$generation_model" \
  -qa "$qa" \
  -output "$final_tmp" >/dev/null
mv -- "$final_tmp" "$final_report"
final_tmp=""
validate_final
