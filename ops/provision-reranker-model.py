#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile

from huggingface_hub import HfApi, snapshot_download

MODEL_ID = "BAAI/bge-reranker-v2-m3"
REVISION = "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"
ALLOWED_NAMES = {
    "config.json",
    "model.safetensors",
    "sentencepiece.bpe.model",
    "special_tokens_map.json",
    "tokenizer.json",
    "tokenizer_config.json",
}


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _verify_existing(destination: Path) -> bool:
    manifest_path = destination / "openim-model-manifest.json"
    if not manifest_path.is_file() or manifest_path.is_symlink():
        return False
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return False
    if (
        manifest.get("schema_version") != 1
        or manifest.get("model_id") != MODEL_ID
        or manifest.get("revision") != REVISION
        or not isinstance(manifest.get("files"), list)
    ):
        return False
    for item in manifest["files"]:
        if not isinstance(item, dict) or set(item) != {"path", "size", "sha256"}:
            return False
        if (
            not isinstance(item["path"], str)
            or not isinstance(item["size"], int)
            or not isinstance(item["sha256"], str)
        ):
            return False
        relative = Path(item["path"])
        if relative.is_absolute() or ".." in relative.parts:
            return False
        path = (destination / relative).resolve()
        if (
            not path.is_relative_to(destination.resolve())
            or not path.is_file()
            or path.is_symlink()
            or path.stat().st_size != item["size"]
            or _sha256(path) != item["sha256"]
        ):
            return False
    return len(manifest["files"]) >= 4


def provision(destination: Path) -> None:
    destination = destination.expanduser().resolve()
    if _verify_existing(destination):
        print(f"reranker_model=ready path={destination}")
        return
    if destination.exists():
        raise RuntimeError(
            f"destination exists but does not match the locked manifest: {destination}"
        )
    info = HfApi().model_info(MODEL_ID, revision=REVISION, files_metadata=True)
    if info.sha != REVISION:
        raise RuntimeError("Hugging Face resolved a different model revision")
    snapshot = Path(
        snapshot_download(
            repo_id=MODEL_ID,
            revision=REVISION,
            allow_patterns=sorted(ALLOWED_NAMES),
        )
    ).resolve()
    parent = destination.parent
    parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=f".{destination.name}.", dir=parent))
    try:
        files: list[dict[str, object]] = []
        for name in sorted(ALLOWED_NAMES):
            source = snapshot / name
            if not source.is_file():
                continue
            target = staging / name
            shutil.copyfile(source, target)
            os.chmod(target, 0o600)
            files.append(
                {"path": name, "size": target.stat().st_size, "sha256": _sha256(target)}
            )
        required = {"config.json", "model.safetensors", "tokenizer_config.json"}
        if not required.issubset({item["path"] for item in files}) or len(files) < 4:
            raise RuntimeError("locked reranker snapshot is missing required model files")
        manifest = {
            "schema_version": 1,
            "model_id": MODEL_ID,
            "revision": REVISION,
            "files": files,
        }
        manifest_path = staging / "openim-model-manifest.json"
        manifest_path.write_text(
            json.dumps(manifest, ensure_ascii=True, indent=2) + "\n", encoding="utf-8"
        )
        os.chmod(manifest_path, 0o600)
        staging.replace(destination)
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise
    if not _verify_existing(destination):
        raise RuntimeError("provisioned reranker failed local manifest verification")
    print(f"reranker_model=ready path={destination}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Provision the exact OpenIM enterprise RAG reranker outside the repository."
    )
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    provision(args.destination)


if __name__ == "__main__":
    main()
