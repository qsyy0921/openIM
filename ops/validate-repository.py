#!/usr/bin/env python3
"""Validate repository contracts and unit SDD lifecycle structure."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
ALLOWED_STATUSES = {
    "draft",
    "proposed",
    "approved",
    "implemented",
    "verified",
    "deprecated",
}
REQUIRED_SECTIONS = {
    "scope": ("scope", "目标与范围", "范围"),
    "responsibilities": ("responsibilities and non-goals", "职责与非目标", "职责和非目标", "职责"),
    "contracts": ("contracts and dependencies", "契约与依赖", "接口与依赖"),
    "invariants": ("invariants", "不变量", "设计不变量"),
    "runtime": ("runtime flow", "运行时流程", "调用链路", "执行流程"),
    "data": ("data ownership and state", "数据所有权与状态", "数据与状态"),
    "failure": ("failure handling", "失败处理", "异常与恢复", "错误处理"),
    "security": ("security", "安全", "安全设计"),
    "observability": ("observability", "可观测性", "监控与可观测性"),
    "acceptance": ("acceptance criteria", "验收标准", "验收条件"),
    "evidence": ("source evidence", "源码依据", "代码依据", "实现依据"),
    "questions": ("open questions", "未决问题", "待确认问题"),
}


def normalize_heading(value: str) -> str:
    value = re.sub(r"[`*_]", "", value).strip().lower()
    value = re.sub(r"^\d+(?:\.\d+)*[.)、：:]?\s*", "", value)
    return re.sub(r"\s+", " ", value).rstrip("：:")


def frontmatter(text: str) -> dict[str, str]:
    match = re.match(r"^---\s*\r?\n(.*?)\r?\n---\s*(?:\r?\n|$)", text, re.DOTALL)
    if not match:
        return {}
    values: dict[str, str] = {}
    for line in match.group(1).splitlines():
        scalar = re.match(r"^([A-Za-z][A-Za-z0-9_-]*):\s*([^#]*?)\s*$", line)
        if scalar and scalar.group(2):
            values[scalar.group(1).lower()] = scalar.group(2).strip().strip("'\"")
    return values


def validate_sdd(path: Path) -> list[str]:
    errors: list[str] = []
    text = path.read_text(encoding="utf-8-sig")
    metadata = frontmatter(text)
    if not metadata.get("unit"):
        errors.append("frontmatter field 'unit' is required")
    status = metadata.get("status", "").lower()
    if status not in ALLOWED_STATUSES:
        errors.append(f"invalid or missing status: {status!r}")
    headings = {
        normalize_heading(match.group(1))
        for match in re.finditer(r"^#{2,3}\s+(.+?)\s*$", text, re.MULTILINE)
    }
    for name, aliases in REQUIRED_SECTIONS.items():
        normalized = tuple(normalize_heading(alias) for alias in aliases)
        if not any(heading == alias or heading.startswith(alias + " ") for heading in headings for alias in normalized):
            errors.append(f"missing required section: {name}")
    return errors


def validate_contracts() -> list[str]:
    errors: list[str] = []
    for path in sorted((ROOT / "contracts").rglob("*.json")):
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
            if path.name.endswith(".schema.json") and "$schema" not in payload:
                errors.append(f"{path.relative_to(ROOT)}: JSON Schema is missing $schema")
        except (OSError, json.JSONDecodeError) as exc:
            errors.append(f"{path.relative_to(ROOT)}: {exc}")
    for path in sorted((ROOT / "contracts").rglob("*.yaml")):
        try:
            payload = yaml.safe_load(path.read_text(encoding="utf-8"))
            if path.parent.name == "openapi" and not isinstance(payload, dict):
                errors.append(f"{path.relative_to(ROOT)}: OpenAPI document must be an object")
            elif path.parent.name == "openapi" and "openapi" not in payload:
                errors.append(f"{path.relative_to(ROOT)}: missing openapi version")
        except (OSError, yaml.YAMLError) as exc:
            errors.append(f"{path.relative_to(ROOT)}: {exc}")
    return errors


def main() -> int:
    errors = validate_contracts()
    for path in sorted((ROOT / "docs" / "sdd").glob("*.md")):
        if path.name != "README.md":
            errors.extend(f"{path.relative_to(ROOT)}: {error}" for error in validate_sdd(path))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"repository validation failed with {len(errors)} error(s)")
        return 1
    print("repository_validation=passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
