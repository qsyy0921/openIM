from fastapi.testclient import TestClient
import math

from intelligence_worker.app import create_app
from intelligence_worker.config import RERANKER_MODEL, RERANKER_REVISION
from intelligence_worker.models import RerankRequest, RerankResponse, RerankScore


class _NeverCalled:
    async def generate(self, _):
        raise AssertionError("candidate model must not be called")


class _Reranker:
    model = RERANKER_MODEL
    revision = RERANKER_REVISION

    async def rank(self, request: RerankRequest) -> RerankResponse:
        return RerankResponse(
            model=self.model,
            revision=self.revision,
            scores=[
                RerankScore(candidate_id=item.candidate_id, score=float(index))
                for index, item in enumerate(request.candidates)
            ],
        )


def test_rerank_endpoint_preserves_ids_and_locked_contract() -> None:
    with TestClient(create_app(_NeverCalled(), reranker=_Reranker())) as http:
        response = http.post(
            "/v1/rerank",
            json={
                "query": "第三方安全评估制度",
                "candidates": [
                    {"candidate_id": "chunk-b", "content": "无关内容"},
                    {"candidate_id": "chunk-a", "content": "第三方安全评估每年执行一次"},
                ],
            },
        )
    assert response.status_code == 200
    assert response.json() == {
        "model": RERANKER_MODEL,
        "revision": RERANKER_REVISION,
        "scores": [
            {"candidate_id": "chunk-b", "score": 0.0},
            {"candidate_id": "chunk-a", "score": 1.0},
        ],
    }


def test_rerank_rejects_duplicate_ids_before_model() -> None:
    with TestClient(create_app(_NeverCalled(), reranker=_Reranker())) as http:
        response = http.post(
            "/v1/rerank",
            json={
                "query": "policy",
                "candidates": [
                    {"candidate_id": "same", "content": "a"},
                    {"candidate_id": "same", "content": "b"},
                ],
            },
        )
    assert response.status_code == 422


class _InvalidReranker(_Reranker):
    async def rank(self, request: RerankRequest) -> RerankResponse:
        if math.isfinite(float("nan")):
            raise AssertionError("unreachable")
        raise ValueError("non-finite score")


def test_rerank_failure_is_explicit_without_fallback() -> None:
    with TestClient(create_app(_NeverCalled(), reranker=_InvalidReranker())) as http:
        response = http.post(
            "/v1/rerank",
            json={"query": "policy", "candidates": [{"candidate_id": "one", "content": "text"}]},
        )
    assert response.status_code == 502
    assert response.json()["detail"] == "required reranker failed"


def test_rerank_is_unavailable_when_not_configured() -> None:
    with TestClient(create_app(_NeverCalled())) as http:
        response = http.post(
            "/v1/rerank",
            json={"query": "policy", "candidates": [{"candidate_id": "one", "content": "text"}]},
        )
    assert response.status_code == 503
