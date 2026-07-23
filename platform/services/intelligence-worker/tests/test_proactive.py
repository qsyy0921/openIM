import math
import asyncio

from intelligence_worker.models import ProactiveRankRequest
from intelligence_worker.proactive import ProactiveRanker


class _Embeddings:
    async def embed(self, texts: list[str]) -> list[list[float]]:
        assert len(texts) == 3
        return [
            [1.0, 0.0],
            [math.sqrt(0.5), math.sqrt(0.5)],
            [0.0, 1.0],
        ]


def test_proactive_ranker_combines_query_and_memory_interest() -> None:
    result = asyncio.run(ProactiveRanker(_Embeddings()).rank(ProactiveRankRequest(
        event_id="event-1",
        subscription_query="agent memory",
        title="A memory paper",
        summary="Research abstract",
        memory=[{
            "memory_id": "memory-1",
            "category": "preference",
            "checksum": "sha256:" + "a" * 64,
            "content": "interested in memory systems",
        }],
        minimum_score=0.5,
    )))
    assert result.should_notify is True
    assert result.reason_codes == ["query_similarity", "memory_interest"]
    assert result.response_id.startswith("rank:")
