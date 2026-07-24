from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "run-node2-enterprise-rag-evaluation.sh"
APPLICATION_COMMIT = "3" * 40


class EnterpriseRAGEvaluationRunnerTest(unittest.TestCase):
    def setUp(self) -> None:
        if os.name == "nt" or shutil.which("bash") is None:
            self.skipTest("the Node2 evaluation runner requires a POSIX bash")
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.call_log = self.root / "calls.log"
        (self.root / "qa.jsonl").write_text("{}\n", encoding="utf-8")
        self._write_executable(
            "with_eval_db.sh",
            """
            #!/usr/bin/env bash
            set -euo pipefail
            exec "$@"
            """,
        )
        self._write_executable(
            "knowledge-rag-admin",
            """
            #!/usr/bin/env bash
            set -euo pipefail

            mode=""
            output=""
            application_commit=""
            retrieval_report=""
            generation_report=""
            while (($#)); do
              case "$1" in
                -mode)
                  mode="$2"
                  shift 2
                  ;;
                -output)
                  output="$2"
                  shift 2
                  ;;
                -application-commit)
                  application_commit="$2"
                  shift 2
                  ;;
                -retrieval-report)
                  retrieval_report="$2"
                  shift 2
                  ;;
                -generation-report)
                  generation_report="$2"
                  shift 2
                  ;;
                *)
                  shift
                  ;;
              esac
            done
            printf '%s\\n' "$mode" >> "$FAKE_CALL_LOG"
            python3 - \
              "$mode" \
              "$output" \
              "$application_commit" \
              "$retrieval_report" \
              "$generation_report" <<'PY'
            import json
            import sys
            import uuid

            (
                mode,
                output,
                application_commit,
                retrieval_report,
                generation_report,
            ) = sys.argv[1:]
            if mode == "evaluate":
                report = {
                    "schema_version": 5,
                    "projection_revision": "document-title-content-v1",
                    "cases": 1120,
                    "answerable_cases": 1040,
                    "unanswerable_cases": 80,
                    "acl_denied_cases": 1120,
                    "recall_at_5": 0.90,
                    "recall_at_10": 0.95,
                    "recall_at_k": 0.95,
                    "mrr": 0.80,
                    "ndcg_at_10": 0.85,
                    "precision_at_5": 0.20,
                    "precision_at_10": 0.10,
                    "retrieval_precision_at_k": 0.10,
                    "unanswerable_retrieval_empty_rate": 0.75,
                    "provenance_integrity": 1.0,
                    "checksum_integrity": 1.0,
                    "acl_leakage_rate": 0,
                    "stale_version_leakage_rate": 0,
                    "generation_abstention_evaluated": False,
                    "failures": [],
                }
            elif mode == "evaluate-generation":
                report = {
                    "schema_version": 1,
                    "evaluator_version": "grounded-generation-v1",
                    "model": "gpt-5.6-terra",
                    "sample_digest": f"sha256:{'4' * 64}",
                    "cases": 120,
                    "answerable_cases": 60,
                    "unanswerable_cases": 60,
                    "model_calls": 120,
                    "provider_failures": 0,
                    "retrieval_misses": 0,
                    "generated_candidates": 120,
                    "required_fact_matches": 120,
                    "required_fact_total": 120,
                    "correct_generated_citations": 60,
                    "generated_citations": 60,
                    "cited_expected_chunks": 60,
                    "expected_citation_chunks": 60,
                    "candidate_contract_success_rate": 1.0,
                    "grounding_decision_accuracy": 1.0,
                    "abstention_accuracy": 1.0,
                    "required_fact_coverage": 1.0,
                    "generated_citation_precision": 1.0,
                    "generated_citation_recall": 1.0,
                    "citation_checksum_integrity": 1.0,
                    "answer_correctness": 1.0,
                    "faithfulness": 1.0,
                    "citation_syntax_integrity": 1.0,
                    "end_to_end_success_rate": 1.0,
                    "production_gate_evaluated": True,
                    "production_gate_passed": True,
                    "failures": [],
                }
            elif mode == "finalize":
                with open(retrieval_report, encoding="utf-8") as stream:
                    retrieval = json.load(stream)
                with open(generation_report, encoding="utf-8") as stream:
                    generation = json.load(stream)
                report = {
                    "schema_version": 2,
                    "evaluation_run_id": str(
                        uuid.uuid5(uuid.NAMESPACE_OID, application_commit)
                    ),
                    "tenant_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
                    "dataset_revision": "enterprise-knowledge/v1",
                    "dataset_digest": f"sha256:{'5' * 64}",
                    "application_commit": application_commit,
                    "embedding_revision": "qwen3-embedding:4b",
                    "projection_revision": "document-title-content-v1",
                    "reranker_revision":
                        "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e",
                    "generation_model": "gpt-5.6-terra",
                    "thresholds": {
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
                    },
                    "retrieval": retrieval,
                    "generation": generation,
                    "passed": True,
                    "failure_case_ids": [],
                }
            else:
                raise SystemExit(f"unexpected mode: {mode}")
            with open(output, "w", encoding="utf-8") as stream:
                json.dump(report, stream)
            PY
            """,
        )

    def tearDown(self) -> None:
        if hasattr(self, "temp"):
            self.temp.cleanup()

    def _write_executable(self, name: str, body: str) -> None:
        path = self.root / name
        path.write_text(textwrap.dedent(body).lstrip(), encoding="utf-8")
        path.chmod(0o700)

    def _run(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", str(SCRIPT), str(self.root), APPLICATION_COMMIT],
            check=False,
            capture_output=True,
            text=True,
            env={**os.environ, "FAKE_CALL_LOG": str(self.call_log)},
        )

    def test_runs_three_stages_once_and_reuses_immutable_reports(self) -> None:
        first = self._run()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(
            self.call_log.read_text(encoding="utf-8").splitlines(),
            ["evaluate", "evaluate-generation", "finalize"],
        )
        self.assertIn("enterprise_rag_evaluation=passed", first.stdout)

        reports_before = {
            name: (self.root / name).read_bytes()
            for name in (
                "retrieval-report.json",
                "generation-report.json",
                "final-report.json",
            )
        }
        second = self._run()
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(
            self.call_log.read_text(encoding="utf-8").splitlines(),
            ["evaluate", "evaluate-generation", "finalize"],
        )
        for name, content in reports_before.items():
            self.assertEqual((self.root / name).read_bytes(), content)

    def test_failed_existing_retrieval_is_not_overwritten_or_advanced(self) -> None:
        failed_report = {
            "schema_version": 5,
            "projection_revision": "document-title-content-v1",
            "cases": 1120,
            "answerable_cases": 1040,
            "unanswerable_cases": 80,
            "acl_denied_cases": 1120,
            "recall_at_5": 0.84,
            "recall_at_10": 0.95,
            "recall_at_k": 0.95,
            "mrr": 0.80,
            "ndcg_at_10": 0.85,
            "precision_at_5": 0.20,
            "precision_at_10": 0.10,
            "retrieval_precision_at_k": 0.10,
            "unanswerable_retrieval_empty_rate": 0.75,
            "provenance_integrity": 1.0,
            "checksum_integrity": 1.0,
            "acl_leakage_rate": 0,
            "stale_version_leakage_rate": 0,
            "generation_abstention_evaluated": False,
            "failures": [],
        }
        retrieval_report = self.root / "retrieval-report.json"
        retrieval_report.write_text(
            json.dumps(failed_report, sort_keys=True), encoding="utf-8"
        )
        content_before = retrieval_report.read_bytes()

        result = self._run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("retrieval gate failed: recall_at_5", result.stderr)
        self.assertEqual(retrieval_report.read_bytes(), content_before)
        self.assertFalse(self.call_log.exists())
        self.assertFalse((self.root / "generation-report.json").exists())
        self.assertFalse((self.root / "final-report.json").exists())

    def test_tampered_final_report_cannot_bypass_source_evidence(self) -> None:
        first = self._run()
        self.assertEqual(first.returncode, 0, first.stderr)
        final_path = self.root / "final-report.json"
        final_report = json.loads(final_path.read_text(encoding="utf-8"))
        final_report["retrieval"]["recall_at_5"] = 1.0
        final_path.write_text(json.dumps(final_report), encoding="utf-8")

        second = self._run()
        self.assertNotEqual(second.returncode, 0)
        self.assertIn(
            "final production report does not embed its source evidence",
            second.stderr,
        )
        self.assertEqual(
            self.call_log.read_text(encoding="utf-8").splitlines(),
            ["evaluate", "evaluate-generation", "finalize"],
        )

    def test_missing_required_retrieval_metric_fails_closed(self) -> None:
        first = self._run()
        self.assertEqual(first.returncode, 0, first.stderr)
        retrieval_path = self.root / "retrieval-report.json"
        retrieval_report = json.loads(retrieval_path.read_text(encoding="utf-8"))
        del retrieval_report["ndcg_at_10"]
        retrieval_path.write_text(json.dumps(retrieval_report), encoding="utf-8")

        second = self._run()
        self.assertNotEqual(second.returncode, 0)
        self.assertIn(
            "retrieval report has invalid metric: ndcg_at_10",
            second.stderr,
        )
        self.assertEqual(
            self.call_log.read_text(encoding="utf-8").splitlines(),
            ["evaluate", "evaluate-generation", "finalize"],
        )

    def test_generation_pass_flag_cannot_override_measured_threshold(self) -> None:
        first = self._run()
        self.assertEqual(first.returncode, 0, first.stderr)
        (self.root / "final-report.json").unlink()
        generation_path = self.root / "generation-report.json"
        generation_report = json.loads(
            generation_path.read_text(encoding="utf-8")
        )
        generation_report["faithfulness"] = 0.94
        self.assertTrue(generation_report["production_gate_passed"])
        generation_path.write_text(
            json.dumps(generation_report), encoding="utf-8"
        )

        second = self._run()
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("generation gate failed: faithfulness", second.stderr)
        self.assertEqual(
            self.call_log.read_text(encoding="utf-8").splitlines(),
            ["evaluate", "evaluate-generation", "finalize"],
        )


if __name__ == "__main__":
    unittest.main()
