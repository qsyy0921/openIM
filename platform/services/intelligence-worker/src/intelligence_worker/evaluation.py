from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import math
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from .models import IntentView, OperationDiscovery, RouteRequest
from .routing import IntentRouter, _tokens


class RoutingEvalCase(BaseModel):
    model_config = ConfigDict(extra="forbid")

    case_id: str = Field(min_length=1, max_length=128)
    content: str = Field(min_length=1, max_length=16_000)
    intent_view: IntentView
    expected_status: str = Field(pattern=r"^(selected|clarify|no_tool)$")
    expected_operation_id: str | None = None


@dataclass(frozen=True)
class RoutingEvalReport:
    total: int
    passed: int
    status_accuracy: float
    selected_recall_at_1: float
    failures: tuple[dict[str, Any], ...]

    @property
    def gate_passed(self) -> bool:
        return self.total > 0 and self.passed == self.total

    def as_dict(self) -> dict[str, Any]:
        return {
            "schema_version": 1,
            "total": self.total,
            "passed": self.passed,
            "status_accuracy": self.status_accuracy,
            "selected_recall_at_1": self.selected_recall_at_1,
            "gate_passed": self.gate_passed,
            "failures": list(self.failures),
        }


class _FixtureIntentProvider:
    def __init__(self, view: IntentView, case_id: str):
        self._view = view
        self._case_id = case_id

    async def analyze_intent(self, _request: RouteRequest) -> tuple[IntentView, str]:
        return self._view, f"offline-eval:{self._case_id}"


class _DeterministicEmbeddingProvider:
    """Local feature hashing for deterministic router regression tests only."""

    def __init__(self, dimension: int = 256):
        self._dimension = dimension

    async def embed(self, texts: list[str]) -> list[list[float]]:
        return [self._vector(text) for text in texts]

    def _vector(self, text: str) -> list[float]:
        vector = [0.0] * self._dimension
        for token in sorted(_tokens(text)):
            digest = hashlib.sha256(token.encode("utf-8")).digest()
            index = int.from_bytes(digest[:4], "big") % self._dimension
            vector[index] += -1.0 if digest[4] & 1 else 1.0
        norm = math.sqrt(sum(value * value for value in vector))
        if norm == 0:
            raise ValueError("evaluation input produced an empty embedding")
        return [value / norm for value in vector]


def default_operations() -> list[OperationDiscovery]:
    return [
        OperationDiscovery(
            operation_id="enterprise.knowledge.search",
            name="企业知识检索",
            summary="在当前成员有权限的企业制度和流程中检索证据",
            parameter_terms=["企业知识", "制度", "流程", "报销", "knowledge", "policy"],
            examples=["查询差旅报销制度", "搜索入职流程"],
            output_kinds=["text", "data"],
        ),
        OperationDiscovery(
            operation_id="collaboration.ticket.create",
            name="创建协作工单",
            summary="创建需要审批且可审计的协作工单",
            parameter_terms=["工单", "任务", "ticket", "create"],
            examples=["创建工单：复核迁移计划"],
            output_kinds=["text", "data"],
        ),
        OperationDiscovery(
            operation_id="agent.delegate",
            name="委派后台 Agent",
            summary="将有边界的任务委派给当前平台已发布的另一个 Agent",
            parameter_terms=["委派", "后台 Agent", "子任务", "delegate"],
            examples=["委派知识助手汇总安全制度"],
            output_kinds=["text", "data"],
        ),
    ]


async def evaluate_routing(cases: list[RoutingEvalCase], operations: list[OperationDiscovery] | None = None) -> RoutingEvalReport:
    if not cases:
        raise ValueError("routing evaluation dataset is empty")
    operations = operations or default_operations()
    operation_ids = {operation.operation_id for operation in operations}
    if len(operations) > 64 or len(operation_ids) != len(operations):
        raise ValueError("routing evaluation catalog is invalid")
    for case in cases:
        if case.expected_operation_id is not None and case.expected_operation_id not in operation_ids:
            raise ValueError(f"routing evaluation case {case.case_id} expects an unknown operation")
    status_matches = 0
    selected_total = 0
    selected_matches = 0
    failures: list[dict[str, Any]] = []
    for case in cases:
        response = await IntentRouter(
            _FixtureIntentProvider(case.intent_view, case.case_id),
            _DeterministicEmbeddingProvider(),
            0.05,
        ).route(RouteRequest(
            run_id=f"eval:{case.case_id}",
            capability_snapshot_id="capability-v1:" + "0" * 64,
            content=case.content,
            operations=operations,
        ))
        status_ok = response.status == case.expected_status
        operation_ok = response.operation_id == case.expected_operation_id
        status_matches += int(status_ok)
        if case.expected_status == "selected":
            selected_total += 1
            selected_matches += int(operation_ok)
        if not status_ok or not operation_ok:
            failures.append({
                "case_id": case.case_id,
                "expected_status": case.expected_status,
                "actual_status": response.status,
                "expected_operation_id": case.expected_operation_id,
                "actual_operation_id": response.operation_id,
            })
    total = len(cases)
    return RoutingEvalReport(
        total=total,
        passed=total - len(failures),
        status_accuracy=status_matches / total,
        selected_recall_at_1=selected_matches / selected_total if selected_total else 1.0,
        failures=tuple(failures),
    )


def load_cases(path: Path) -> list[RoutingEvalCase]:
    result: list[RoutingEvalCase] = []
    with path.open("r", encoding="utf-8") as source:
        for line_number, raw in enumerate(source, start=1):
            if not raw.strip():
                continue
            try:
                result.append(RoutingEvalCase.model_validate_json(raw))
            except ValueError as exc:
                raise ValueError(f"invalid routing evaluation case at line {line_number}") from exc
    if not result:
        raise ValueError("routing evaluation dataset is empty")
    case_ids = [case.case_id for case in result]
    if len(case_ids) != len(set(case_ids)):
        raise ValueError("routing evaluation case IDs are not unique")
    return result


def load_operations(path: Path) -> list[OperationDiscovery]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError("routing evaluation catalog is invalid") from exc
    if not isinstance(payload, list) or not payload:
        raise ValueError("routing evaluation catalog is empty")
    try:
        operations = [OperationDiscovery.model_validate(item) for item in payload]
    except ValueError as exc:
        raise ValueError("routing evaluation catalog is invalid") from exc
    operation_ids = [operation.operation_id for operation in operations]
    if len(operations) > 64 or len(operation_ids) != len(set(operation_ids)):
        raise ValueError("routing evaluation catalog operation IDs are not unique")
    return operations


def main() -> None:
    parser = argparse.ArgumentParser(description="Run the deterministic OpenIM Agent routing regression gate")
    parser.add_argument(
        "--dataset",
        type=Path,
        default=Path(__file__).resolve().parents[2] / "eval" / "routing_cases.jsonl",
    )
    parser.add_argument(
        "--catalog",
        type=Path,
        default=Path(__file__).resolve().parents[2] / "eval" / "routing_catalog.json",
    )
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    report = asyncio.run(evaluate_routing(load_cases(args.dataset), load_operations(args.catalog)))
    encoded = json.dumps(report.as_dict(), ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")
    print(encoded, end="")
    if not report.gate_passed:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
