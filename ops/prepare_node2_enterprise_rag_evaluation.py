from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import shutil
import stat
import tempfile
import uuid
from dataclasses import dataclass
from pathlib import Path


PROJECTION_REVISION = "document-title-content-v1"
UNIT_NAME = "openim-rag-enterprise-evaluation.service"
SAFE_PATH = re.compile(r"^/[A-Za-z0-9._/-]+$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")


@dataclass(frozen=True)
class Inputs:
    source_root: Path
    target_root: Path
    runner_source: Path
    application_commit: str
    tooling_commit: str
    admin_sha256: str
    runner_sha256: str
    qa_sha256: str
    database_runner_sha256: str


class PreparationError(RuntimeError):
    pass


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_json(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PreparationError(f"invalid JSON evidence: {path}") from exc
    if not isinstance(value, dict):
        raise PreparationError(f"JSON evidence is not an object: {path}")
    return value


def validate_path(path: Path, label: str) -> None:
    raw = str(path)
    if not path.is_absolute() or raw == "/" or not SAFE_PATH.fullmatch(raw):
        raise PreparationError(f"{label} must be a safe absolute non-root path")


def validate_inputs(inputs: Inputs) -> None:
    validate_path(inputs.source_root, "source evaluation root")
    validate_path(inputs.target_root, "target evaluation root")
    validate_path(inputs.runner_source, "evaluation runner source")
    if inputs.source_root == inputs.target_root:
        raise PreparationError("source and target evaluation roots must differ")
    if not COMMIT.fullmatch(inputs.application_commit):
        raise PreparationError(
            "application commit must be a full lowercase Git commit"
        )
    if not COMMIT.fullmatch(inputs.tooling_commit):
        raise PreparationError(
            "tooling commit must be a full lowercase Git commit"
        )
    for digest in (
        inputs.admin_sha256,
        inputs.runner_sha256,
        inputs.qa_sha256,
        inputs.database_runner_sha256,
    ):
        if not DIGEST.fullmatch(digest):
            raise PreparationError("evaluation input digest is malformed")


def source_paths(inputs: Inputs) -> dict[str, Path]:
    return {
        "knowledge-rag-admin": inputs.source_root / "knowledge-rag-admin",
        "with_eval_db.sh": inputs.source_root / "with_eval_db.sh",
        "qa.jsonl": inputs.source_root / "qa.jsonl",
        "source-index-report.json": inputs.source_root / "index-report.json",
        "source-retrieval-regression-report.json":
            inputs.source_root / "retrieval-regression-report.json",
        "source-regression-manifest.json":
            inputs.source_root / "regression-manifest.json",
        "run-evaluation.sh": inputs.runner_source,
    }


def validate_evidence(
    inputs: Inputs,
    index_path: Path,
    regression_path: Path,
    source_manifest_path: Path,
) -> None:
    index = load_json(index_path)
    expected_index = {
        "projection_revision": PROJECTION_REVISION,
        "state": "active",
        "expected_chunks": 2704,
        "indexed_chunks": 2704,
        "activated": True,
    }
    for field, expected in expected_index.items():
        if index.get(field) != expected:
            raise PreparationError(f"source index report violates {field}")
    try:
        generation_id = uuid.UUID(index.get("generation_id", ""))
    except (ValueError, AttributeError) as exc:
        raise PreparationError(
            "source index generation ID is invalid"
        ) from exc
    if generation_id.version not in (1, 4, 5):
        raise PreparationError(
            "source index generation ID has an invalid version"
        )

    regression = load_json(regression_path)
    expected_regression = {
        "schema_version": 5,
        "projection_revision": PROJECTION_REVISION,
        "cases": 158,
        "answerable_cases": 158,
        "unanswerable_cases": 0,
        "acl_denied_cases": 158,
        "acl_leakage_rate": 0,
        "stale_version_leakage_rate": 0,
        "provenance_integrity": 1,
        "checksum_integrity": 1,
    }
    for field, expected in expected_regression.items():
        if regression.get(field) != expected:
            raise PreparationError(
                f"source projection regression violates {field}"
            )
    for field in ("recall_at_5", "recall_at_10"):
        measured = regression.get(field)
        if (
            isinstance(measured, bool)
            or not isinstance(measured, (int, float))
            or not math.isfinite(measured)
            or measured < 0.60
            or measured > 1
        ):
            raise PreparationError(
                f"source projection regression gate failed: {field}"
            )

    source_manifest = load_json(source_manifest_path)
    expected_source_manifest = {
        "schema_version": 1,
        "failed_cases": 158,
        "projection_revision": PROJECTION_REVISION,
        "knowledge_rag_admin_sha256":
            f"sha256:{inputs.admin_sha256}",
        "source_qa_sha256": f"sha256:{inputs.qa_sha256}",
    }
    for field, expected in expected_source_manifest.items():
        if source_manifest.get(field) != expected:
            raise PreparationError(
                f"source projection manifest violates {field}"
            )


def validate_source(inputs: Inputs) -> dict[str, Path]:
    paths = source_paths(inputs)
    for path in paths.values():
        if not path.is_file():
            raise PreparationError(
                f"required full-evaluation source is missing: {path}"
            )
    locked = {
        "knowledge-rag-admin": inputs.admin_sha256,
        "run-evaluation.sh": inputs.runner_sha256,
        "qa.jsonl": inputs.qa_sha256,
        "with_eval_db.sh": inputs.database_runner_sha256,
    }
    for name, expected in locked.items():
        if file_sha256(paths[name]) != expected:
            raise PreparationError(
                f"full-evaluation source digest changed: {name}"
            )
    validate_evidence(
        inputs,
        paths["source-index-report.json"],
        paths["source-retrieval-regression-report.json"],
        paths["source-regression-manifest.json"],
    )
    return paths


def service_contract(target_root: Path, application_commit: str) -> str:
    return f"""[Unit]
Description=OpenIM enterprise RAG full production evaluation
After=openim-rag-retrieval-eval.service
Requires=openim-rag-retrieval-eval.service

[Service]
Type=oneshot
WorkingDirectory={target_root}
ExecStart={target_root}/run-evaluation.sh {target_root} {application_commit}
RemainAfterExit=yes
NoNewPrivileges=true
PrivateTmp=true
UMask=0077

[Install]
WantedBy=default.target
"""


def expected_manifest(inputs: Inputs, root: Path) -> dict:
    index = load_json(root / "source-index-report.json")
    return {
        "schema_version": 1,
        "application_commit": inputs.application_commit,
        "tooling_commit": inputs.tooling_commit,
        "application_binary_sha256": f"sha256:{inputs.admin_sha256}",
        "evaluation_runner_sha256": f"sha256:{inputs.runner_sha256}",
        "qa_sha256": f"sha256:{inputs.qa_sha256}",
        "database_runner_sha256":
            f"sha256:{inputs.database_runner_sha256}",
        "projection_revision": PROJECTION_REVISION,
        "generation_id": index["generation_id"],
        "source_index_report_sha256":
            f"sha256:{file_sha256(root / 'source-index-report.json')}",
        "source_regression_report_sha256":
            "sha256:"
            + file_sha256(
                root / "source-retrieval-regression-report.json"
            ),
        "source_regression_manifest_sha256":
            "sha256:"
            + file_sha256(root / "source-regression-manifest.json"),
    }


def validate_prepared_locked_files(inputs: Inputs, root: Path) -> None:
    locked_files = {
        "knowledge-rag-admin": inputs.admin_sha256,
        "run-evaluation.sh": inputs.runner_sha256,
        "qa.jsonl": inputs.qa_sha256,
        "with_eval_db.sh": inputs.database_runner_sha256,
    }
    for name, expected in locked_files.items():
        if file_sha256(root / name) != expected:
            raise PreparationError(
                f"prepared evaluation file digest changed: {name}"
            )


def validate_target(inputs: Inputs) -> None:
    root = inputs.target_root
    if not root.is_dir() or root.is_symlink():
        raise PreparationError(
            "target evaluation root exists but is not a real directory"
        )
    manifest = load_json(root / "evaluation-manifest.json")
    if manifest != expected_manifest(inputs, root):
        raise PreparationError("prepared evaluation manifest changed")
    validate_prepared_locked_files(inputs, root)
    unit_path = root / UNIT_NAME
    if unit_path.read_text(encoding="utf-8") != service_contract(
        root, inputs.application_commit
    ):
        raise PreparationError("prepared evaluation service contract changed")

    expected_modes = {
        root: 0o700,
        root / "knowledge-rag-admin": 0o500,
        root / "with_eval_db.sh": 0o500,
        root / "run-evaluation.sh": 0o500,
        root / "qa.jsonl": 0o400,
        root / "source-index-report.json": 0o400,
        root / "source-retrieval-regression-report.json": 0o400,
        root / "source-regression-manifest.json": 0o400,
        root / "evaluation-manifest.json": 0o400,
        unit_path: 0o400,
    }
    for path, expected in expected_modes.items():
        measured = stat.S_IMODE(path.stat().st_mode)
        if measured != expected:
            raise PreparationError(
                f"prepared evaluation mode changed: {path.name}"
            )


def copy_with_mode(source: Path, target: Path, mode: int) -> None:
    shutil.copyfile(source, target)
    target.chmod(mode)


def prepare(inputs: Inputs) -> str:
    validate_inputs(inputs)
    paths = validate_source(inputs)
    if inputs.target_root.exists() or inputs.target_root.is_symlink():
        validate_target(inputs)
        return "already_prepared"

    parent = inputs.target_root.parent
    parent.mkdir(parents=True, exist_ok=True)
    staging = Path(
        tempfile.mkdtemp(
            prefix=f".{inputs.target_root.name}.staging.",
            dir=parent,
        )
    )
    staging.chmod(0o700)
    try:
        for name, mode in (
            ("knowledge-rag-admin", 0o500),
            ("with_eval_db.sh", 0o500),
            ("run-evaluation.sh", 0o500),
            ("qa.jsonl", 0o400),
            ("source-index-report.json", 0o400),
            ("source-retrieval-regression-report.json", 0o400),
            ("source-regression-manifest.json", 0o400),
        ):
            copy_with_mode(paths[name], staging / name, mode)
        validate_evidence(
            inputs,
            staging / "source-index-report.json",
            staging / "source-retrieval-regression-report.json",
            staging / "source-regression-manifest.json",
        )
        validate_prepared_locked_files(inputs, staging)
        manifest_path = staging / "evaluation-manifest.json"
        with manifest_path.open("x", encoding="utf-8") as stream:
            json.dump(
                expected_manifest(inputs, staging),
                stream,
                ensure_ascii=True,
                indent=2,
                sort_keys=True,
            )
            stream.write("\n")
        manifest_path.chmod(0o400)
        unit_path = staging / UNIT_NAME
        unit_path.write_text(
            service_contract(inputs.target_root, inputs.application_commit),
            encoding="utf-8",
        )
        unit_path.chmod(0o400)
        os.rename(staging, inputs.target_root)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    validate_target(inputs)
    return "prepared"


def parse_args() -> Inputs:
    parser = argparse.ArgumentParser(
        description="Prepare an immutable Node2 enterprise RAG evaluation root"
    )
    parser.add_argument("source_root", type=Path)
    parser.add_argument("target_root", type=Path)
    parser.add_argument("runner_source", type=Path)
    parser.add_argument("application_commit")
    parser.add_argument("tooling_commit")
    parser.add_argument("admin_sha256")
    parser.add_argument("runner_sha256")
    parser.add_argument("qa_sha256")
    parser.add_argument("database_runner_sha256")
    values = parser.parse_args()
    return Inputs(**vars(values))


def main() -> int:
    try:
        inputs = parse_args()
        state = prepare(inputs)
    except PreparationError as exc:
        print(str(exc), file=os.sys.stderr)
        return 1
    print(f"enterprise_rag_evaluation_root={state}")
    print(
        "enterprise_rag_evaluation_service="
        f"{inputs.target_root / UNIT_NAME}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
