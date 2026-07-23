from __future__ import annotations

import math
import re
from typing import Protocol

import httpx

from .config import RetrievalSettings, Settings
from .models import IntentView, OperationDiscovery, RouteCandidate, RouteRequest, RouteResponse


class IntentViewProvider(Protocol):
    async def analyze_intent(self, request: RouteRequest) -> tuple[IntentView, str]: ...


class EmbeddingProvider(Protocol):
    async def embed(self, texts: list[str]) -> list[list[float]]: ...


class OpenAIEmbeddingClient:
    def __init__(
        self,
        settings: Settings | RetrievalSettings,
        transport: httpx.AsyncBaseTransport | None = None,
    ):
        self._model = settings.embedding_model
        self._dimension = settings.embedding_dimension
        self._client = httpx.AsyncClient(
            base_url=settings.embedding_base_url,
            timeout=settings.embedding_timeout_seconds,
            transport=transport,
            headers={"Authorization": f"Bearer {settings.embedding_api_key}"},
            trust_env=False,
        )

    async def close(self) -> None:
        await self._client.aclose()

    @property
    def model(self) -> str:
        return self._model

    @property
    def dimension(self) -> int:
        return self._dimension

    async def embed(self, texts: list[str]) -> list[list[float]]:
        if not texts or len(texts) > 128:
            raise ValueError("embedding batch is empty or exceeds the service bound")
        response = await self._client.post("/embeddings", json={"model": self._model, "input": texts})
        response.raise_for_status()
        payload = response.json()
        data = payload.get("data") if isinstance(payload, dict) else None
        if not isinstance(data, list) or len(data) != len(texts):
            raise ValueError("embedding provider returned an invalid batch")
        ordered: list[list[float] | None] = [None] * len(texts)
        for item in data:
            if not isinstance(item, dict) or not isinstance(item.get("index"), int):
                raise ValueError("embedding provider returned an invalid index")
            index = item["index"]
            vector = item.get("embedding")
            if index < 0 or index >= len(texts) or ordered[index] is not None:
                raise ValueError("embedding provider returned a duplicate or out-of-range index")
            if not isinstance(vector, list) or len(vector) != self._dimension:
                raise ValueError("embedding provider returned an invalid dimension")
            parsed = [float(value) for value in vector]
            if any(not math.isfinite(value) for value in parsed):
                raise ValueError("embedding provider returned a non-finite value")
            ordered[index] = parsed
        if any(item is None for item in ordered):
            raise ValueError("embedding provider omitted a vector")
        return [item for item in ordered if item is not None]


class IntentRouter:
    version = "openim-intent-routing-v1"

    def __init__(self, intent_provider: IntentViewProvider, embedding_provider: EmbeddingProvider, dense_min_similarity: float):
        if dense_min_similarity < -1 or dense_min_similarity > 1:
            raise ValueError("dense_min_similarity must be in [-1, 1]")
        self._intent_provider = intent_provider
        self._embedding_provider = embedding_provider
        self._dense_min_similarity = dense_min_similarity

    async def route(self, request: RouteRequest) -> RouteResponse:
        view, response_id = await self._intent_provider.analyze_intent(request)
        _validate_intent_view(view)
        if view.tool_requirement == "none":
            return RouteResponse(status="no_tool", provider_response_id=response_id, router_version=self.version, candidates=[])
        if view.unresolved_references or view.missing_required_inputs:
            fields = view.unresolved_references + view.missing_required_inputs
            return RouteResponse(
                status="clarify",
                clarification="请补充以下信息：" + "、".join(fields[:8]),
                provider_response_id=response_id,
                router_version=self.version,
                candidates=[],
            )

        operation_texts = [_operation_text(item) for item in request.operations]
        query_texts = [request.content, f"{view.rewritten_intent}\n{view.hypothetical_capability}"]
        vectors = await self._embedding_provider.embed(query_texts + operation_texts)
        original_dense = _rank_dense(vectors[0], vectors[2:], self._dense_min_similarity)
        view_dense = _rank_dense(vectors[1], vectors[2:], self._dense_min_similarity)
        original_lexical = _rank_lexical(request.content, operation_texts)
        view_lexical = _rank_lexical(query_texts[1], operation_texts)

        ranked: list[tuple[tuple[int, int, float, str], RouteCandidate]] = []
        for index, operation in enumerate(request.operations):
            exact = operation.operation_id in request.content.lower() or operation.name.lower() in request.content.lower()
            original_lexical_rank = original_lexical.get(index)
            original_dense_rank = original_dense.get(index)
            view_ranks = [rank for rank in (view_lexical.get(index), view_dense.get(index)) if rank is not None]
            intent_rank = min(view_ranks) if view_ranks else None
            if not exact and original_lexical_rank is None and original_dense_rank is None and intent_rank is None:
                continue
            score = _rrf(original_lexical_rank, 1.0) + _rrf(original_dense_rank, 1.0) + _rrf(intent_rank, 2.0)
            reasons: list[str] = []
            if exact:
                reasons.append("exact_operation")
            if original_lexical_rank is not None:
                reasons.append("original_lexical")
            if original_dense_rank is not None:
                reasons.append("original_dense")
            if intent_rank is not None:
                reasons.append("llm_hypothetical")
            candidate = RouteCandidate(
                operation_id=operation.operation_id,
                original_lexical_rank=original_lexical_rank,
                original_dense_rank=original_dense_rank,
                intent_view_rank=intent_rank,
                reason_codes=reasons,
            )
            ranked.append(((0 if exact else 1, intent_rank or 1_000_000, -score, operation.operation_id), candidate))
        ranked.sort(key=lambda item: item[0])
        candidates = [item[1] for item in ranked[:3]]
        if not candidates:
            return RouteResponse(
                status="clarify", clarification="当前能力快照中没有与请求匹配的可用操作。",
                provider_response_id=response_id, router_version=self.version, candidates=[]
            )
        if len(ranked) > 1 and ranked[0][0][:3] == ranked[1][0][:3]:
            return RouteResponse(
                status="clarify", clarification="请求可能对应多个操作，请明确希望执行的目标。",
                provider_response_id=response_id, router_version=self.version, candidates=candidates
            )
        return RouteResponse(
            status="selected", operation_id=candidates[0].operation_id,
            provider_response_id=response_id, router_version=self.version, candidates=candidates
        )


def _validate_intent_view(view: IntentView) -> None:
    required = set(view.required_inputs)
    if len(required) != len(view.required_inputs) or not set(view.missing_required_inputs).issubset(required):
        raise ValueError("intent view missing inputs are not a subset of required inputs")
    for values in (view.required_inputs, view.missing_required_inputs, view.unresolved_references, view.desired_outputs):
        if len(values) != len(set(values)) or any(not value.strip() for value in values):
            raise ValueError("intent view contains duplicate or empty bounded values")


def _operation_text(operation: OperationDiscovery) -> str:
    return "\n".join([
        operation.operation_id, operation.name, operation.summary,
        " ".join(operation.parameter_terms), " ".join(operation.examples),
        " ".join(operation.output_kinds),
    ])


def _tokens(text: str) -> set[str]:
    normalized = text.lower()
    words = set(re.findall(r"[a-z0-9_]+", normalized))
    chinese = "".join(re.findall(r"[\u4e00-\u9fff]", normalized))
    words.update(chinese[index:index + 2] for index in range(max(0, len(chinese) - 1)))
    words.update(chinese)
    return {item for item in words if item}


def _rank_lexical(query: str, documents: list[str]) -> dict[int, int]:
    query_tokens = _tokens(query)
    scored = []
    for index, document in enumerate(documents):
        overlap = len(query_tokens & _tokens(document))
        if overlap > 0:
            scored.append((-overlap, index))
    scored.sort()
    return {index: rank for rank, (_, index) in enumerate(scored, start=1)}


def _rank_dense(query: list[float], documents: list[list[float]], minimum: float) -> dict[int, int]:
    scored = []
    for index, document in enumerate(documents):
        similarity = _cosine(query, document)
        if similarity >= minimum:
            scored.append((-similarity, index))
    scored.sort()
    return {index: rank for rank, (_, index) in enumerate(scored, start=1)}


def _cosine(left: list[float], right: list[float]) -> float:
    if len(left) != len(right) or not left:
        raise ValueError("routing vectors have inconsistent dimensions")
    numerator = sum(a * b for a, b in zip(left, right, strict=True))
    left_norm = math.sqrt(sum(value * value for value in left))
    right_norm = math.sqrt(sum(value * value for value in right))
    if left_norm == 0 or right_norm == 0:
        raise ValueError("routing vector has zero norm")
    return numerator / (left_norm * right_norm)


def _rrf(rank: int | None, weight: float) -> float:
    return 0.0 if rank is None else weight / (60 + rank)
