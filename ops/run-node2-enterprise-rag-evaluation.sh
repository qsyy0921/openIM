#!/usr/bin/env bash
set -euo pipefail

evaluation_root="${1:-/home/qsyy0921/MFL/eval/enterprise-rag}"
application_commit="${2:-}"
tenant_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
member_id="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
denied_member_id="cccccccc-cccc-4ccc-8ccc-cccccccccccc"
retrieval_url="http://127.0.0.1:18083"
generation_url="http://127.0.0.1:18082"
embedding_model="qwen3-embedding:4b"
projection_revision="document-title-content-v1"
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
for path in "$admin" "$database_runner" "$qa"; do
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
    "schema_version": 5,
    "projection_revision": "document-title-content-v1",
    "cases": 1120,
    "answerable_cases": 1040,
    "unanswerable_cases": 80,
    "acl_denied_cases": 1120,
    "generation_abstention_evaluated": False,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"retrieval report violates {field}")
rate_fields = (
    "recall_at_5",
    "recall_at_10",
    "recall_at_k",
    "mrr",
    "ndcg_at_10",
    "precision_at_5",
    "precision_at_10",
    "retrieval_precision_at_k",
    "unanswerable_retrieval_empty_rate",
    "acl_leakage_rate",
    "stale_version_leakage_rate",
    "provenance_integrity",
    "checksum_integrity",
)
for field in rate_fields:
    measured = report.get(field)
    if (
        isinstance(measured, bool)
        or not isinstance(measured, (int, float))
        or not math.isfinite(measured)
        or measured < 0
        or measured > 1
    ):
        raise SystemExit(f"retrieval report has invalid metric: {field}")
if not math.isclose(report["recall_at_k"], report["recall_at_10"], abs_tol=1e-12):
    raise SystemExit("retrieval report recall aliases disagree")
if not math.isclose(
    report["retrieval_precision_at_k"],
    report["precision_at_10"],
    abs_tol=1e-12,
):
    raise SystemExit("retrieval report precision aliases disagree")
if not isinstance(report.get("failures"), list):
    raise SystemExit("retrieval report failures are missing")
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
import math
import re
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
expected = {
    "schema_version": 1,
    "evaluator_version": "grounded-generation-v1",
    "model": "gpt-5.6-terra",
    "cases": 120,
    "answerable_cases": 60,
    "unanswerable_cases": 60,
    "production_gate_evaluated": True,
    "production_gate_passed": True,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"generation report violates {field}")
if not re.fullmatch(r"sha256:[0-9a-f]{64}", report.get("sample_digest", "")):
    raise SystemExit("generation report sample digest is invalid")
count_fields = (
    "model_calls",
    "provider_failures",
    "retrieval_misses",
    "generated_candidates",
    "required_fact_matches",
    "required_fact_total",
    "correct_generated_citations",
    "generated_citations",
    "cited_expected_chunks",
    "expected_citation_chunks",
)
for field in count_fields:
    measured = report.get(field)
    if isinstance(measured, bool) or not isinstance(measured, int) or measured < 0:
        raise SystemExit(f"generation report has invalid count: {field}")
rate_fields = (
    "candidate_contract_success_rate",
    "grounding_decision_accuracy",
    "abstention_accuracy",
    "required_fact_coverage",
    "generated_citation_precision",
    "generated_citation_recall",
    "citation_checksum_integrity",
    "answer_correctness",
    "faithfulness",
    "citation_syntax_integrity",
    "end_to_end_success_rate",
)
for field in rate_fields:
    measured = report.get(field)
    if (
        isinstance(measured, bool)
        or not isinstance(measured, (int, float))
        or not math.isfinite(measured)
        or measured < 0
        or measured > 1
    ):
        raise SystemExit(f"generation report has invalid metric: {field}")
thresholds = {
    "candidate_contract_success_rate": 1.0,
    "abstention_accuracy": 0.95,
    "generated_citation_precision": 0.95,
    "citation_checksum_integrity": 1.0,
    "faithfulness": 0.95,
}
for field, minimum in thresholds.items():
    if report[field] < minimum:
        raise SystemExit(f"generation gate failed: {field}")
if not isinstance(report.get("failures"), list):
    raise SystemExit("generation report failures are missing")
print("generation_gate=passed")
PY
}

validate_final() {
  python3 - \
    "$final_report" \
    "$application_commit" \
    "$retrieval_report" \
    "$generation_report" <<'PY'
import json
import re
import sys
import uuid

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
with open(sys.argv[3], encoding="utf-8") as stream:
    retrieval = json.load(stream)
with open(sys.argv[4], encoding="utf-8") as stream:
    generation = json.load(stream)
expected = {
    "schema_version": 2,
    "tenant_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    "dataset_revision": "enterprise-knowledge/v1",
    "application_commit": sys.argv[2],
    "embedding_revision": "qwen3-embedding:4b",
    "projection_revision": "document-title-content-v1",
    "reranker_revision": "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e",
    "generation_model": "gpt-5.6-terra",
    "passed": True,
}
for field, value in expected.items():
    if report.get(field) != value:
        raise SystemExit(f"final production report violates {field}")
if not re.fullmatch(r"sha256:[0-9a-f]{64}", report.get("dataset_digest", "")):
    raise SystemExit("final production report dataset digest is invalid")
try:
    evaluation_run_id = uuid.UUID(report.get("evaluation_run_id", ""))
except (ValueError, AttributeError) as exc:
    raise SystemExit("final production report run ID is invalid") from exc
if evaluation_run_id.version != 5:
    raise SystemExit("final production report run ID is not deterministic")
thresholds = {
    "minimum_retrieval_cases": 1120,
    "minimum_generation_cases": 120,
    "minimum_recall_at_5": 0.85,
    "minimum_recall_at_10_baseline_comparison": 0.928846,
    "minimum_mrr": 0.70,
    "maximum_acl_leakage_rate": 0,
    "maximum_stale_version_leakage_rate": 0,
    "minimum_provenance_integrity": 1,
    "minimum_checksum_integrity": 1,
    "minimum_candidate_contract_success_rate": 1,
    "minimum_abstention_accuracy": 0.95,
    "minimum_citation_precision": 0.95,
    "minimum_citation_checksum_integrity": 1,
    "minimum_faithfulness": 0.95,
}
if report.get("thresholds") != thresholds:
    raise SystemExit("final production report thresholds are not locked")
if report.get("retrieval") != retrieval or report.get("generation") != generation:
    raise SystemExit("final production report does not embed its source evidence")
failure_ids = {
    failure.get("qa_id")
    for source in (retrieval, generation)
    for failure in source.get("failures", [])
    if isinstance(failure, dict) and failure.get("qa_id")
}
if report.get("failure_case_ids") != sorted(failure_ids):
    raise SystemExit("final production report failure IDs disagree with evidence")
print("enterprise_rag_evaluation=passed")
PY
}

if [[ -f "$retrieval_report" ]]; then
  validate_retrieval
else
  retrieval_tmp="$(mktemp "$evaluation_root/.retrieval-report.XXXXXXXX")"
  trap 'rm -f -- "${retrieval_tmp:-}" "${generation_tmp:-}" "${final_tmp:-}"' EXIT
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
    -output "$retrieval_tmp" >/dev/null
  mv -- "$retrieval_tmp" "$retrieval_report"
  retrieval_tmp=""
  validate_retrieval
fi

if [[ -f "$final_report" ]]; then
  validate_generation
  validate_final
  exit 0
fi

if [[ -f "$generation_report" ]]; then
  validate_generation
else
  generation_tmp="$(mktemp "$evaluation_root/.generation-report.XXXXXXXX")"
  trap 'rm -f -- "${retrieval_tmp:-}" "${generation_tmp:-}" "${final_tmp:-}"' EXIT
  "$database_runner" "$admin" \
    -mode evaluate-generation \
    -tenant-id "$tenant_id" \
    -member-id "$member_id" \
    -intelligence-url "$retrieval_url" \
    -generation-url "$generation_url" \
    -model "$embedding_model" \
    -projection-revision "$projection_revision" \
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
trap 'rm -f -- "${retrieval_tmp:-}" "${generation_tmp:-}" "${final_tmp:-}"' EXIT
"$database_runner" "$admin" \
  -mode finalize \
  -tenant-id "$tenant_id" \
  -retrieval-report "$retrieval_report" \
  -generation-report "$generation_report" \
  -application-commit "$application_commit" \
  -dataset-revision enterprise-knowledge/v1 \
  -model "$embedding_model" \
  -projection-revision "$projection_revision" \
  -reranker-revision "$reranker_revision" \
  -generation-model "$generation_model" \
  -qa "$qa" \
  -output "$final_tmp" >/dev/null
mv -- "$final_tmp" "$final_report"
final_tmp=""
validate_final
