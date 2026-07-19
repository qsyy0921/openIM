#!/usr/bin/env python3
"""Validate structure, provenance, balance, and reproducibility metadata."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any


VALIDATOR_VERSION = "1.0.0"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DATASET = ROOT / "datasets" / "enterprise-knowledge" / "v1"
EXPECTED_DOMAINS = 13
EXPECTED_TOPICS = 104
EXPECTED_DOCUMENTS = 520
EXPECTED_VERSIONS = 624
EXPECTED_CHUNKS = 3224
EXPECTED_QA = 1120


def read_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def read_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, 1):
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path.name}:{line_number}: {exc}") from exc
    return rows


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_text(value: str) -> str:
    return "sha256:" + sha256_bytes(value.encode("utf-8"))


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def unique_map(rows: list[dict[str, Any]], key: str, label: str, errors: list[str]) -> dict[str, dict[str, Any]]:
    result: dict[str, dict[str, Any]] = {}
    for row in rows:
        value = row.get(key)
        if not isinstance(value, str) or not value:
            errors.append(f"{label} has invalid {key}: {value!r}")
            continue
        if value in result:
            errors.append(f"duplicate {label} {key}: {value}")
        result[value] = row
    return result


def char_ngrams(value: str, n: int = 5) -> set[str]:
    compact = "".join(value.split())
    return {compact[index:index + n] for index in range(max(0, len(compact) - n + 1))}


def max_form_similarity(documents: list[dict[str, Any]], versions_by_id: dict[str, dict[str, Any]], dataset: Path) -> float:
    grouped: dict[str, list[set[str]]] = defaultdict(list)
    for document in documents:
        version = versions_by_id[document["current_version_id"]]
        body = (dataset / version["raw_path"]).read_text(encoding="utf-8")
        grouped[document["document_form"]].append(char_ngrams(body))
    maximum = 0.0
    for values in grouped.values():
        for left_index, left in enumerate(values):
            for right in values[left_index + 1:]:
                union = len(left | right)
                score = len(left & right) / union if union else 1.0
                maximum = max(maximum, score)
    return round(maximum, 6)


def validate(dataset: Path) -> tuple[dict[str, Any], list[str]]:
    errors: list[str] = []
    required_files = (
        "manifest.json", "company_profile.json", "canonical_facts.jsonl", "documents.jsonl",
        "document_versions.jsonl", "chunks.jsonl", "qa.jsonl", "postgres_import.sql",
        "statistics.json", "README.md",
    )
    for name in required_files:
        require((dataset / name).is_file(), f"missing required file: {name}", errors)
    if errors:
        return {}, errors

    manifest = read_json(dataset / "manifest.json")
    profile = read_json(dataset / "company_profile.json")
    topics = read_jsonl(dataset / "canonical_facts.jsonl")
    documents = read_jsonl(dataset / "documents.jsonl")
    versions = read_jsonl(dataset / "document_versions.jsonl")
    chunks = read_jsonl(dataset / "chunks.jsonl")
    qas = read_jsonl(dataset / "qa.jsonl")
    stats = read_json(dataset / "statistics.json")
    postgres_import = (dataset / "postgres_import.sql").read_text(encoding="utf-8")

    require(profile.get("dataset_notice", "").startswith("全部企业"), "synthetic data notice is missing", errors)
    require(profile.get("tenant_id") == "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "dataset tenant identity is invalid", errors)
    require(profile.get("local_member_id") == "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "dataset member identity is invalid", errors)
    tenant_insert = postgres_import.find("INSERT INTO identity.tenants")
    member_insert = postgres_import.find("INSERT INTO identity.members")
    document_insert = postgres_import.find("INSERT INTO knowledge.documents")
    grant_insert = postgres_import.find("INSERT INTO authz.document_grants")
    require(0 <= tenant_insert < member_insert < document_insert < grant_insert, "PostgreSQL import must seed synthetic identity before knowledge and grants", errors)
    require(manifest.get("synthetic") is True, "manifest must mark the dataset synthetic", errors)
    require(len(topics) == EXPECTED_TOPICS, f"expected {EXPECTED_TOPICS} topics, got {len(topics)}", errors)
    require(len({row.get('domain_code') for row in topics}) == EXPECTED_DOMAINS, f"expected {EXPECTED_DOMAINS} domains", errors)
    require(len(documents) == EXPECTED_DOCUMENTS, f"expected {EXPECTED_DOCUMENTS} documents, got {len(documents)}", errors)
    require(len(versions) == EXPECTED_VERSIONS, f"expected {EXPECTED_VERSIONS} versions, got {len(versions)}", errors)
    require(len(chunks) == EXPECTED_CHUNKS, f"expected {EXPECTED_CHUNKS} chunks, got {len(chunks)}", errors)
    require(len(qas) == EXPECTED_QA, f"expected {EXPECTED_QA} QA cases, got {len(qas)}", errors)

    document_map = unique_map(documents, "id", "document", errors)
    version_map = unique_map(versions, "id", "version", errors)
    chunk_map = unique_map(chunks, "id", "chunk", errors)
    unique_map(qas, "qa_id", "qa", errors)

    require(len({row["source_uri"] for row in documents}) == len(documents), "document source_uri values are not unique", errors)
    require(len({row["question"] for row in qas}) == len(qas), "QA questions are not unique", errors)
    domain_counts = Counter(row["domain_code"] for row in documents)
    form_counts = Counter(row["document_form"] for row in documents)
    require(set(domain_counts.values()) == {40}, f"domain balance must be 40 documents each: {dict(domain_counts)}", errors)
    require(set(form_counts.values()) == {104}, f"form balance must be 104 documents each: {dict(form_counts)}", errors)

    versions_by_document: dict[str, list[dict[str, Any]]] = defaultdict(list)
    chunks_by_version: dict[str, list[dict[str, Any]]] = defaultdict(list)
    raw_hashes: set[str] = set()
    for version in versions:
        document_id = version.get("document_id")
        require(document_id in document_map, f"version {version.get('id')} references unknown document", errors)
        versions_by_document[document_id].append(version)
        raw_path = dataset / version.get("raw_path", "")
        require(raw_path.is_file(), f"version {version.get('id')} raw file missing: {version.get('raw_path')}", errors)
        if raw_path.is_file():
            body = raw_path.read_text(encoding="utf-8")
            require(version.get("checksum") == sha256_text(body), f"version checksum mismatch: {version.get('id')}", errors)
            require(len(body) >= 650, f"document version is too short for realistic retrieval: {version.get('id')} ({len(body)})", errors)
            raw_hashes.add(sha256_bytes(body.encode("utf-8")))
    require(len(raw_hashes) == len(versions), "raw document bodies contain exact duplicates", errors)

    for chunk in chunks:
        version_id = chunk.get("version_id")
        document_id = chunk.get("document_id")
        require(version_id in version_map, f"chunk {chunk.get('id')} references unknown version", errors)
        require(document_id in document_map, f"chunk {chunk.get('id')} references unknown document", errors)
        if version_id in version_map:
            require(version_map[version_id]["document_id"] == document_id, f"chunk {chunk.get('id')} document/version mismatch", errors)
        content = chunk.get("content", "")
        require(1 <= len(content) <= 16000, f"chunk {chunk.get('id')} has invalid length", errors)
        require(chunk.get("checksum") == sha256_text(content), f"chunk checksum mismatch: {chunk.get('id')}", errors)
        chunks_by_version[version_id].append(chunk)

    current_version_ids = {row["current_version_id"] for row in documents}
    for document in documents:
        current_id = document.get("current_version_id")
        require(current_id in version_map, f"document {document['id']} current version missing", errors)
        if current_id in version_map:
            current = version_map[current_id]
            require(current["document_id"] == document["id"], f"document {document['id']} current version belongs elsewhere", errors)
            require(current["status"] == "published", f"document {document['id']} current version is not published", errors)
        expected_versions = 2 if document["document_form"] == "policy" else 1
        owned_versions = versions_by_document[document["id"]]
        require(len(owned_versions) == expected_versions, f"document {document['id']} expected {expected_versions} versions", errors)
        if expected_versions == 2:
            require(Counter(row["status"] for row in owned_versions) == Counter({"published": 1, "superseded": 1}), f"policy {document['id']} version states invalid", errors)

    for version_id, owned_chunks in chunks_by_version.items():
        ordinals = sorted(row["ordinal"] for row in owned_chunks)
        require(ordinals == list(range(len(ordinals))), f"version {version_id} chunk ordinals are not contiguous", errors)
        require(len(owned_chunks) >= 5, f"version {version_id} has fewer than five semantic chunks", errors)

    qa_type_counts = Counter(row["type"] for row in qas)
    expected_qa_types = {
        "single_document": 416,
        "numeric": 104,
        "procedure": 104,
        "multi_document": 208,
        "decision_trace": 104,
        "version_awareness": 104,
        "unanswerable": 80,
    }
    require(dict(qa_type_counts) == expected_qa_types, f"QA type distribution differs: {dict(qa_type_counts)}", errors)
    for qa in qas:
        evidence = qa.get("evidence", [])
        if qa.get("answerable"):
            require(bool(evidence), f"answerable QA has no evidence: {qa.get('qa_id')}", errors)
            evidence_content: list[str] = []
            cited_documents: set[str] = set()
            for item in evidence:
                chunk = chunk_map.get(item.get("chunk_id"))
                require(chunk is not None, f"QA {qa.get('qa_id')} references unknown chunk", errors)
                if chunk is None:
                    continue
                require(item.get("document_id") == chunk["document_id"], f"QA {qa.get('qa_id')} evidence document mismatch", errors)
                require(item.get("version_id") == chunk["version_id"], f"QA {qa.get('qa_id')} evidence version mismatch", errors)
                require(item.get("version_id") in current_version_ids, f"QA {qa.get('qa_id')} cites a non-current version", errors)
                require(item.get("quote", "") in chunk["content"], f"QA {qa.get('qa_id')} quote is not verbatim evidence", errors)
                evidence_content.append(chunk["content"])
                cited_documents.add(chunk["document_id"])
            joined = "\n".join(evidence_content)
            for fact in qa.get("required_facts", []):
                require(fact in joined, f"QA {qa.get('qa_id')} required fact absent from evidence: {fact}", errors)
            if qa.get("type") == "multi_document":
                require(len(cited_documents) >= 2, f"multi-document QA cites fewer than two documents: {qa.get('qa_id')}", errors)
            if qa.get("type") == "version_awareness":
                negatives = qa.get("negative_version_ids", [])
                require(len(negatives) == 1 and version_map.get(negatives[0], {}).get("status") == "superseded", f"version QA has invalid negative version: {qa.get('qa_id')}", errors)
        else:
            require(qa.get("type") == "unanswerable", f"non-answerable QA has unexpected type: {qa.get('qa_id')}", errors)
            require(not evidence, f"unanswerable QA contains evidence: {qa.get('qa_id')}", errors)
            require(not qa.get("required_facts"), f"unanswerable QA contains required facts: {qa.get('qa_id')}", errors)

    manifest_files = manifest.get("files", {})
    actual_manifest_files = {
        path.relative_to(dataset).as_posix(): sha256_bytes(path.read_bytes())
        for path in sorted(dataset.rglob("*"))
        if path.is_file() and path.name not in {"manifest.json", "validation-report.json"}
    }
    require(manifest_files == actual_manifest_files, "manifest file hashes do not match release files", errors)
    expected_release_hash = sha256_bytes(json.dumps(manifest_files, sort_keys=True).encode("utf-8"))
    require(manifest.get("release_hash") == expected_release_hash, "manifest release_hash is invalid", errors)
    require(stats.get("documents") == len(documents) and stats.get("qa_cases") == len(qas), "statistics counts are inconsistent", errors)

    similarity = max_form_similarity(documents, version_map, dataset)
    require(similarity < 0.88, f"document templates are too similar; max same-form 5-gram Jaccard={similarity}", errors)
    report = {
        "validator_version": VALIDATOR_VERSION,
        "dataset_version": manifest.get("dataset_version"),
        "release_hash": manifest.get("release_hash"),
        "passed": not errors,
        "counts": {"topics": len(topics), "documents": len(documents), "versions": len(versions), "chunks": len(chunks), "qa_cases": len(qas)},
        "quality": {
            "unique_document_body_ratio": len(raw_hashes) / len(versions) if versions else 0,
            "unique_question_ratio": len({row['question'] for row in qas}) / len(qas) if qas else 0,
            "max_same_form_5gram_jaccard": similarity,
            "answerable_cases": sum(1 for row in qas if row["answerable"]),
            "unanswerable_cases": sum(1 for row in qas if not row["answerable"]),
        },
        "errors": errors,
    }
    return report, errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    args = parser.parse_args()
    dataset = args.dataset.resolve()
    report, errors = validate(dataset)
    if report:
        (dataset / "validation-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        print(f"dataset_validation=failed errors={len(errors)}")
        return 1
    counts = report["counts"]
    print("dataset_validation=passed")
    print(f"release_hash={report['release_hash']}")
    print(f"documents={counts['documents']} versions={counts['versions']} chunks={counts['chunks']} qa_cases={counts['qa_cases']}")
    print(f"max_same_form_5gram_jaccard={report['quality']['max_same_form_5gram_jaccard']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
