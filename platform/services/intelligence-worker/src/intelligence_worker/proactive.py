from __future__ import annotations

import hashlib
import json

from .models import ProactiveRankRequest, ProactiveRankResponse
from .routing import EmbeddingProvider, _cosine


class ProactiveRanker:
    version = "openim-proactive-ranker-v1"

    def __init__(self, embedding_provider: EmbeddingProvider):
        self._embedding_provider = embedding_provider

    async def rank(self, request: ProactiveRankRequest) -> ProactiveRankResponse:
        event_text = f"{request.title}\n{request.summary}"
        texts = [request.subscription_query, event_text]
        texts.extend(item.content for item in request.memory)
        vectors = await self._embedding_provider.embed(texts)
        query_similarity = _normalized_cosine(vectors[0], vectors[1])
        memory_interest = 0.0
        if request.memory:
            memory_interest = max(_normalized_cosine(vector, vectors[1]) for vector in vectors[2:])
        score = min(1.0, max(0.0, 0.8 * query_similarity + 0.2 * memory_interest))
        reasons = ["query_similarity"]
        if request.memory:
            reasons.append("memory_interest")
        canonical = json.dumps(request.model_dump(), ensure_ascii=False, sort_keys=True, separators=(",", ":"))
        response_id = "rank:" + hashlib.sha256(canonical.encode("utf-8")).hexdigest()
        return ProactiveRankResponse(
            score=round(score, 8),
            should_notify=score >= request.minimum_score,
            reason_codes=reasons,
            ranker_version=self.version,
            response_id=response_id,
        )


def _normalized_cosine(left: list[float], right: list[float]) -> float:
    return (_cosine(left, right) + 1.0) / 2.0
