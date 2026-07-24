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
                *)
                  shift
                  ;;
              esac
            done
            printf '%s\\n' "$mode" >> "$FAKE_CALL_LOG"
            python3 - "$mode" "$output" "$application_commit" <<'PY'
            import json
            import sys

            mode, output, application_commit = sys.argv[1:]
            if mode == "evaluate":
                report = {
                    "schema_version": 5,
                    "projection_revision": "document-title-content-v1",
                    "cases": 1120,
                    "acl_denied_cases": 1120,
                    "recall_at_5": 0.90,
                    "recall_at_10": 0.95,
                    "mrr": 0.80,
                    "provenance_integrity": 1.0,
                    "checksum_integrity": 1.0,
                    "acl_leakage_rate": 0,
                    "stale_version_leakage_rate": 0,
                }
            elif mode == "evaluate-generation":
                report = {
                    "schema_version": 1,
                    "model": "gpt-5.6-terra",
                    "cases": 120,
                    "answerable_cases": 60,
                    "unanswerable_cases": 60,
                    "production_gate_evaluated": True,
                    "production_gate_passed": True,
                }
            elif mode == "finalize":
                report = {
                    "schema_version": 2,
                    "application_commit": application_commit,
                    "embedding_revision": "qwen3-embedding:4b",
                    "projection_revision": "document-title-content-v1",
                    "reranker_revision":
                        "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e",
                    "generation_model": "gpt-5.6-terra",
                    "passed": True,
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
            "acl_denied_cases": 1120,
            "recall_at_5": 0.84,
            "recall_at_10": 0.95,
            "mrr": 0.80,
            "provenance_integrity": 1.0,
            "checksum_integrity": 1.0,
            "acl_leakage_rate": 0,
            "stale_version_leakage_rate": 0,
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


if __name__ == "__main__":
    unittest.main()
