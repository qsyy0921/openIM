#!/usr/bin/env python3
"""Validate and emit the shared OpenIM/Telegram RAG E2E phase contract."""

from __future__ import annotations

import argparse
import json
import re
import sys
import uuid
from dataclasses import dataclass
from pathlib import Path


BATCH_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{7,63}$")
MARKER_PATTERN = re.compile(r"^[A-Za-z0-9-]{8,96}$")
FORMATS = ("markdown", "text", "pdf", "docx")


class ContractError(ValueError):
    pass


@dataclass(frozen=True)
class ChannelContract:
    batch_id: str
    prompt: str
    document_ids: tuple[str, str, str, str]
    markdown_initial_version_id: str
    markdown_next_version_id: str
    markdown_marker: str
    markdown_next_marker: str

    def pipe_record(self) -> str:
        values = [
            self.batch_id,
            self.prompt,
            *self.document_ids,
            self.markdown_initial_version_id,
            self.markdown_next_version_id,
            self.markdown_marker,
            self.markdown_next_marker,
        ]
        if any(
            "|" in value
            or "\r" in value
            or "\n" in value
            or "$prompt$" in value
            for value in values
        ):
            raise ContractError("acceptance contract contains an unsafe delimiter")
        return "|".join(values)


def uuid_text(value: object, label: str) -> str:
    if not isinstance(value, str):
        raise ContractError(f"{label} is not a UUID")
    try:
        parsed = uuid.UUID(value)
    except ValueError as error:
        raise ContractError(f"{label} is not a UUID") from error
    if parsed.version not in {1, 2, 3, 4, 5}:
        raise ContractError(f"{label} uses an unsupported UUID version")
    return str(parsed)


def load_contract(
    channel: str,
    phase: str,
    state_path: Path,
    manifest_path: Path,
) -> ChannelContract:
    if channel not in {"openim", "telegram"}:
        raise ContractError("channel must be openim or telegram")
    if phase not in {"authorized", "denied", "revoked", "version"}:
        raise ContractError("acceptance phase is unsupported")
    state = json.loads(state_path.read_text(encoding="utf-8"))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

    batch_id = manifest.get("batch_id")
    if not isinstance(batch_id, str) or not BATCH_PATTERN.fullmatch(batch_id):
        raise ContractError("manifest batch ID is malformed")
    if (
        state.get("schema_version") != 1
        or manifest.get("schema_version") != 1
        or state.get("batch_id") != batch_id
    ):
        raise ContractError("state and manifest contract mismatch")

    manifest_documents = manifest.get("documents")
    state_documents = state.get("documents")
    if (
        not isinstance(manifest_documents, list)
        or not isinstance(state_documents, list)
        or len(manifest_documents) != 4
        or len(state_documents) != 4
    ):
        raise ContractError("four-format state is incomplete")
    manifest_by_format = {
        item.get("format"): item
        for item in manifest_documents
        if isinstance(item, dict)
    }
    state_by_format = {
        item.get("format"): item for item in state_documents if isinstance(item, dict)
    }
    if set(manifest_by_format) != set(FORMATS) or set(state_by_format) != set(
        FORMATS
    ):
        raise ContractError("four-format state is missing or duplicated")

    document_ids: list[str] = []
    for format_name in FORMATS:
        expected = manifest_by_format[format_name]
        actual = state_by_format[format_name]
        for key in ("title", "filename", "marker"):
            if actual.get(key) != expected.get(key):
                raise ContractError(
                    f"{format_name} state differs from the manifest"
                )
        if actual.get("version_number") != 1:
            raise ContractError(
                f"{format_name} initial version number must be 1"
            )
        document_ids.append(
            uuid_text(actual.get("document_id"), f"{format_name} document")
        )
        uuid_text(actual.get("version_id"), f"{format_name} initial version")

    markdown = state_by_format["markdown"]
    markdown_initial = uuid_text(
        markdown.get("version_id"), "Markdown initial version"
    )
    next_version_value = markdown.get("next_version_id")
    if phase == "version":
        markdown_next = uuid_text(next_version_value, "Markdown next version")
        if markdown.get("next_version_number") != 2:
            raise ContractError("Markdown next version number must be 2")
    elif next_version_value is not None:
        markdown_next = uuid_text(next_version_value, "Markdown next version")
    else:
        markdown_next = ""
    markdown_marker = markdown.get("marker")
    markdown_next_marker = markdown.get("version_marker")
    if not isinstance(markdown_marker, str) or not MARKER_PATTERN.fullmatch(
        markdown_marker
    ):
        raise ContractError("Markdown initial marker is malformed")
    if not isinstance(markdown_next_marker, str) or not MARKER_PATTERN.fullmatch(
        markdown_next_marker
    ):
        raise ContractError("Markdown next-version marker is malformed")

    question_key = "old_version_query" if phase == "version" else "question"
    question = manifest.get(question_key)
    if (
        not isinstance(question, str)
        or not question.strip()
        or "\x00" in question
    ):
        raise ContractError("acceptance question is malformed")
    channel_label = "OpenIM" if channel == "openim" else "Telegram"
    prompt = f"{question.strip()} [{channel_label} E2E {phase} {batch_id}]"
    if len(prompt.encode("utf-8")) > 2000:
        raise ContractError("acceptance prompt exceeds the production query bound")

    return ChannelContract(
        batch_id=batch_id,
        prompt=prompt,
        document_ids=tuple(document_ids),  # type: ignore[arg-type]
        markdown_initial_version_id=markdown_initial,
        markdown_next_version_id=markdown_next,
        markdown_marker=markdown_marker,
        markdown_next_marker=markdown_next_marker,
    )


def main(arguments: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("channel", choices=("openim", "telegram"))
    parser.add_argument(
        "phase", choices=("authorized", "denied", "revoked", "version")
    )
    parser.add_argument("state", type=Path)
    parser.add_argument("manifest", type=Path)
    parsed = parser.parse_args(arguments)
    try:
        contract = load_contract(
            parsed.channel, parsed.phase, parsed.state, parsed.manifest
        )
        print(contract.pipe_record())
    except (ContractError, json.JSONDecodeError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
