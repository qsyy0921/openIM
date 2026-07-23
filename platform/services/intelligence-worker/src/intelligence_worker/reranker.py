from __future__ import annotations

import asyncio
from dataclasses import dataclass
import hashlib
import json
import math
from pathlib import Path
from typing import Protocol

from .config import RetrievalSettings, Settings
from .models import RerankRequest, RerankResponse, RerankScore


class Reranker(Protocol):
    model: str
    revision: str

    async def rank(self, request: RerankRequest) -> RerankResponse: ...


@dataclass(frozen=True)
class _ManifestFile:
    path: str
    size: int
    sha256: str


class LocalCrossEncoderReranker:
    def __init__(self, settings: Settings | RetrievalSettings) -> None:
        self.model = settings.reranker_model
        self.revision = settings.reranker_revision
        self._path = Path(settings.reranker_path).expanduser().resolve(strict=True)
        self._max_length = settings.reranker_max_length
        self._batch_size = settings.reranker_batch_size
        self._verify_manifest()

        import torch
        from transformers import AutoModelForSequenceClassification, AutoTokenizer

        if settings.reranker_device != "cpu":
            raise ValueError("reranker device violates the fixed CPU contract")
        self._torch = torch
        self._tokenizer = AutoTokenizer.from_pretrained(
            self._path, local_files_only=True, trust_remote_code=False
        )
        self._encoder = AutoModelForSequenceClassification.from_pretrained(
            self._path, local_files_only=True, trust_remote_code=False
        )
        self._encoder.to("cpu")
        self._encoder.eval()
        self._lock = asyncio.Lock()

    async def rank(self, request: RerankRequest) -> RerankResponse:
        async with self._lock:
            values = await asyncio.to_thread(self._rank_sync, request)
        return RerankResponse(
            model=self.model,
            revision=self.revision,
            scores=[
                RerankScore(candidate_id=candidate.candidate_id, score=score)
                for candidate, score in zip(request.candidates, values, strict=True)
            ],
        )

    def _rank_sync(self, request: RerankRequest) -> list[float]:
        scores: list[float] = []
        pairs = [(request.query, candidate.content) for candidate in request.candidates]
        for start in range(0, len(pairs), self._batch_size):
            encoded = self._tokenizer(
                pairs[start : start + self._batch_size],
                padding=True,
                truncation=True,
                max_length=self._max_length,
                return_tensors="pt",
            )
            with self._torch.inference_mode():
                logits = self._encoder(**encoded, return_dict=True).logits
            batch_scores = logits.view(-1).detach().cpu().float().tolist()
            if len(batch_scores) != len(pairs[start : start + self._batch_size]):
                raise ValueError("reranker output count violates the model contract")
            for value in batch_scores:
                score = float(value)
                if not math.isfinite(score):
                    raise ValueError("reranker returned a non-finite score")
                scores.append(score)
        if len(scores) != len(request.candidates):
            raise ValueError("reranker output count violates the request contract")
        return scores

    def _verify_manifest(self) -> None:
        manifest_path = self._path / "openim-model-manifest.json"
        if not manifest_path.is_file() or manifest_path.is_symlink():
            raise ValueError("reranker manifest is missing")
        try:
            payload = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise ValueError("reranker manifest is invalid") from exc
        if (
            payload.get("schema_version") != 1
            or payload.get("model_id") != self.model
            or payload.get("revision") != self.revision
            or not isinstance(payload.get("files"), list)
            or len(payload["files"]) < 4
        ):
            raise ValueError("reranker manifest violates the locked model contract")
        for raw in payload["files"]:
            entry = self._manifest_file(raw)
            candidate = (self._path / entry.path).resolve(strict=True)
            if candidate.is_symlink() or not candidate.is_file() or not candidate.is_relative_to(self._path):
                raise ValueError("reranker manifest references an unsafe file")
            if candidate.stat().st_size != entry.size:
                raise ValueError("reranker model file size does not match its manifest")
            digest = hashlib.sha256()
            with candidate.open("rb") as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(block)
            if digest.hexdigest() != entry.sha256:
                raise ValueError("reranker model file checksum does not match its manifest")

    @staticmethod
    def _manifest_file(raw: object) -> _ManifestFile:
        if not isinstance(raw, dict):
            raise ValueError("reranker manifest file entry is invalid")
        path, size, checksum = raw.get("path"), raw.get("size"), raw.get("sha256")
        if (
            not isinstance(path, str)
            or not path
            or Path(path).is_absolute()
            or ".." in Path(path).parts
            or not isinstance(size, int)
            or size < 1
            or not isinstance(checksum, str)
            or len(checksum) != 64
            or any(char not in "0123456789abcdef" for char in checksum)
        ):
            raise ValueError("reranker manifest file entry is invalid")
        return _ManifestFile(path=path, size=size, sha256=checksum)
