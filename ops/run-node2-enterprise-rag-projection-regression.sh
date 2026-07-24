#!/usr/bin/env bash
set -euo pipefail

evaluation_root="${1:-/home/qsyy0921/MFL/eval/enterprise-rag-projection-0033}"
tenant_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
member_id="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
denied_member_id="cccccccc-cccc-4ccc-8ccc-cccccccccccc"
retrieval_url="http://127.0.0.1:18083"
embedding_model="qwen3-embedding:4b"
projection_revision="document-title-content-v1"
expected_cases=158

[[ "$evaluation_root" == /* && "$evaluation_root" != "/" ]] || {
  echo "evaluation root must be an absolute non-root path" >&2
  exit 1
}

admin="$evaluation_root/knowledge-rag-admin"
database_runner="$evaluation_root/with_eval_db.sh"
qa="$evaluation_root/failed-158.jsonl"
index_report="$evaluation_root/index-report.json"
regression_report="$evaluation_root/retrieval-regression-report.json"
for path in "$admin" "$database_runner" "$qa"; do
  [[ -f "$path" ]] || {
    echo "required projection-regression input is missing: $path" >&2
    exit 1
  }
done

validate_index() {
  python3 - "$index_report" "$projection_revision" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
expected = {
    "projection_revision": sys.argv[2],
    "state": "active",
    "expected_chunks": 2704,
    "indexed_chunks": 2704,
    "activated": True,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"projection index report violates {field}")
print("projection_index=passed")
PY
}

validate_regression() {
  python3 - "$regression_report" "$projection_revision" "$expected_cases" <<'PY'
import json
import math
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
case_count = int(sys.argv[3])
expected = {
    "schema_version": 5,
    "projection_revision": sys.argv[2],
    "cases": case_count,
    "answerable_cases": case_count,
    "unanswerable_cases": 0,
    "acl_denied_cases": case_count,
    "acl_leakage_rate": 0,
    "stale_version_leakage_rate": 0,
    "provenance_integrity": 1,
    "checksum_integrity": 1,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"projection regression violates {field}")

# The failed-set gate asks whether title-aware projection recovers enough of the
# 158 previous misses to make a full rerun economically justified. The old full
# report needs at least 85 additional Top-5 and 84 additional Top-10 successes
# to cross its recall gates, so 60% provides a bounded margin without claiming
# that the full-set MRR gate has passed.
for field in ("recall_at_5", "recall_at_10"):
    measured = report.get(field)
    if (
        not isinstance(measured, (int, float))
        or not math.isfinite(measured)
        or measured < 0.60
    ):
        raise SystemExit(f"projection regression gate failed: {field}")

print(
    "projection_regression=passed "
    f"recall_at_5={report['recall_at_5']:.6f} "
    f"recall_at_10={report['recall_at_10']:.6f} "
    f"mrr={report['mrr']:.6f}"
)
PY
}

if [[ -f "$index_report" ]]; then
  validate_index
else
  index_tmp="$(mktemp "$evaluation_root/.index-report.XXXXXXXX")"
  trap 'rm -f -- "${index_tmp:-}" "${regression_tmp:-}"' EXIT
  "$database_runner" "$admin" \
    -mode index \
    -tenant-id "$tenant_id" \
    -intelligence-url "$retrieval_url" \
    -model "$embedding_model" \
    -projection-revision "$projection_revision" \
    -dimension 2560 \
    -batch-size 4 \
    -embedding-workers 2 \
    -timeout 300s \
    -output "$index_tmp" >/dev/null
  mv -- "$index_tmp" "$index_report"
  index_tmp=""
  validate_index
fi

if [[ -f "$regression_report" ]]; then
  validate_regression
  exit 0
fi

regression_tmp="$(mktemp "$evaluation_root/.retrieval-regression.XXXXXXXX")"
trap 'rm -f -- "${index_tmp:-}" "${regression_tmp:-}"' EXIT
"$database_runner" "$admin" \
  -mode evaluate \
  -tenant-id "$tenant_id" \
  -member-id "$member_id" \
  -denied-member-id "$denied_member_id" \
  -intelligence-url "$retrieval_url" \
  -model "$embedding_model" \
  -projection-revision "$projection_revision" \
  -dimension 2560 \
  -dense-minimum 0.45 \
  -max-candidates 32 \
  -qa "$qa" \
  -limit 8 \
  -timeout 300s \
  -output "$regression_tmp" >/dev/null
mv -- "$regression_tmp" "$regression_report"
regression_tmp=""
validate_regression
