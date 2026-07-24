from __future__ import annotations

import hashlib
import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
import uuid
from pathlib import Path


SCRIPT = (
    Path(__file__).parents[1]
    / "prepare_node2_enterprise_rag_evaluation.py"
)
APPLICATION_COMMIT = "3" * 40
TOOLING_COMMIT = "6" * 40


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


class PrepareEnterpriseRAGEvaluationTest(unittest.TestCase):
    def setUp(self) -> None:
        if os.name == "nt" or shutil.which("python3") is None:
            self.skipTest("the Node2 preparation helper requires POSIX tools")
        self.temp = tempfile.TemporaryDirectory()
        self.temp_root = Path(self.temp.name)
        self.source = self.temp_root / "projection-source"
        self.target = self.temp_root / "full-evaluation"
        self.runner = self.temp_root / "run-evaluation.sh"
        self.source.mkdir(mode=0o700)

        self.admin = self.source / "knowledge-rag-admin"
        self.database_runner = self.source / "with_eval_db.sh"
        self.qa = self.source / "qa.jsonl"
        self.admin.write_bytes(b"commit-matched-application-binary\n")
        self.database_runner.write_text(
            "#!/usr/bin/env bash\nset -euo pipefail\nexec \"$@\"\n",
            encoding="utf-8",
        )
        self.qa.write_text('{"qa_id":"fixture"}\n', encoding="utf-8")
        self.runner.write_text(
            "#!/usr/bin/env bash\nset -euo pipefail\n",
            encoding="utf-8",
        )

        index_report = {
            "generation_id": str(uuid.uuid4()),
            "projection_revision": "document-title-content-v1",
            "state": "active",
            "expected_chunks": 2704,
            "indexed_chunks": 2704,
            "activated": True,
        }
        regression_report = {
            "schema_version": 5,
            "projection_revision": "document-title-content-v1",
            "cases": 158,
            "answerable_cases": 158,
            "unanswerable_cases": 0,
            "acl_denied_cases": 158,
            "recall_at_5": 0.70,
            "recall_at_10": 0.80,
            "mrr": 0.65,
            "acl_leakage_rate": 0,
            "stale_version_leakage_rate": 0,
            "provenance_integrity": 1,
            "checksum_integrity": 1,
        }
        (self.source / "index-report.json").write_text(
            json.dumps(index_report), encoding="utf-8"
        )
        (self.source / "retrieval-regression-report.json").write_text(
            json.dumps(regression_report), encoding="utf-8"
        )
        source_manifest = {
            "schema_version": 1,
            "failed_cases": 158,
            "projection_revision": "document-title-content-v1",
            "knowledge_rag_admin_sha256": f"sha256:{sha256(self.admin)}",
            "source_qa_sha256": f"sha256:{sha256(self.qa)}",
            "working_tree_artifact": True,
        }
        (self.source / "regression-manifest.json").write_text(
            json.dumps(source_manifest), encoding="utf-8"
        )

    def tearDown(self) -> None:
        if hasattr(self, "temp"):
            self.temp.cleanup()

    def run_prepare(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                str(self.source),
                str(self.target),
                str(self.runner),
                APPLICATION_COMMIT,
                TOOLING_COMMIT,
                sha256(self.admin),
                sha256(self.runner),
                sha256(self.qa),
                sha256(self.database_runner),
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_prepares_once_and_retries_as_read_only_verification(self) -> None:
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertIn("enterprise_rag_evaluation_root=prepared", first.stdout)
        manifest_path = self.target / "evaluation-manifest.json"
        manifest_before = manifest_path.read_bytes()
        manifest = json.loads(manifest_before)
        self.assertEqual(manifest["application_commit"], APPLICATION_COMMIT)
        self.assertEqual(manifest["tooling_commit"], TOOLING_COMMIT)
        self.assertEqual(
            manifest["application_binary_sha256"],
            f"sha256:{sha256(self.admin)}",
        )
        service = (
            self.target / "openim-rag-enterprise-evaluation.service"
        ).read_text(encoding="utf-8")
        self.assertIn(f"WorkingDirectory={self.target}", service)
        self.assertIn(APPLICATION_COMMIT, service)
        self.assertEqual(
            stat.S_IMODE((self.target / "knowledge-rag-admin").stat().st_mode),
            0o500,
        )
        self.assertEqual(
            stat.S_IMODE(manifest_path.stat().st_mode),
            0o400,
        )

        second = self.run_prepare()
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn(
            "enterprise_rag_evaluation_root=already_prepared",
            second.stdout,
        )
        self.assertEqual(manifest_path.read_bytes(), manifest_before)

    def test_failed_regression_cannot_create_target(self) -> None:
        report_path = self.source / "retrieval-regression-report.json"
        report = json.loads(report_path.read_text(encoding="utf-8"))
        report["recall_at_5"] = 0.59
        report_path.write_text(json.dumps(report), encoding="utf-8")

        result = self.run_prepare()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(
            "source projection regression gate failed: recall_at_5",
            result.stderr,
        )
        self.assertFalse(self.target.exists())

    def test_existing_tampered_target_is_rejected_without_repair(self) -> None:
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        prepared_admin = self.target / "knowledge-rag-admin"
        prepared_admin.chmod(0o700)
        with prepared_admin.open("ab") as stream:
            stream.write(b"tampered")
        content_before = prepared_admin.read_bytes()

        second = self.run_prepare()
        self.assertNotEqual(second.returncode, 0)
        self.assertIn(
            "prepared evaluation file digest changed: knowledge-rag-admin",
            second.stderr,
        )
        self.assertEqual(prepared_admin.read_bytes(), content_before)


if __name__ == "__main__":
    unittest.main()
