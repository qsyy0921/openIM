from __future__ import annotations

import asyncio
from pathlib import Path

import pytest

from intelligence_worker.evaluation import evaluate_routing, load_cases, load_operations


DATASET = Path(__file__).resolve().parents[1] / "eval" / "routing_cases.jsonl"
CATALOG = Path(__file__).resolve().parents[1] / "eval" / "routing_catalog.json"


def test_routing_regression_dataset_passes_exact_gate() -> None:
    cases = load_cases(DATASET)
    operations = load_operations(CATALOG)
    assert len(cases) == 190
    assert len(operations) == 36
    report = asyncio.run(evaluate_routing(cases, operations))
    assert report.gate_passed
    assert report.status_accuracy == 1.0
    assert report.selected_recall_at_1 == 1.0
    assert report.failures == ()


def test_routing_dataset_rejects_duplicate_case_ids(tmp_path: Path) -> None:
    line = DATASET.read_text(encoding="utf-8").splitlines()[0]
    dataset = tmp_path / "duplicate.jsonl"
    dataset.write_text(f"{line}\n{line}\n", encoding="utf-8")
    with pytest.raises(ValueError, match="not unique"):
        load_cases(dataset)
