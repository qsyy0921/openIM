from fastapi.testclient import TestClient

from intelligence_worker.config import (
    EMBEDDING_DIMENSION,
    EMBEDDING_MODEL,
    LOCAL_OLLAMA_EMBEDDING_BASE_URL,
    RERANKER_MODEL,
    RERANKER_REVISION,
    RetrievalSettings,
)
from intelligence_worker.models import RerankRequest, RerankResponse, RerankScore
from intelligence_worker.retrieval_app import create_retrieval_app


class _Embedding:
    model = EMBEDDING_MODEL
    dimension = EMBEDDING_DIMENSION

    async def embed(self, texts: list[str]) -> list[list[float]]:
        return [[float(index + 1)] + [0.0] * (self.dimension - 1) for index, _ in enumerate(texts)]


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


def test_retrieval_app_exposes_only_bounded_retrieval_surface() -> None:
    with TestClient(
        create_retrieval_app(embedding_provider=_Embedding(), reranker=_Reranker())
    ) as http:
        assert http.get("/healthz").json() == {
            "status": "ok",
            "surface": "retrieval-only",
        }
        assert http.post("/v1/embeddings", json={"texts": ["policy"]}).status_code == 200
        assert http.post(
            "/v1/rerank",
            json={
                "query": "policy",
                "candidates": [{"candidate_id": "c1", "content": "policy evidence"}],
            },
        ).status_code == 200
        assert http.post("/v1/candidates", json={}).status_code == 404
        assert http.post("/v1/routes", json={}).status_code == 404
        assert http.post("/v1/tool-plans", json={}).status_code == 404
        assert http.post("/v1/memory-extractions", json={}).status_code == 404


def test_retrieval_settings_lock_node2_models(monkeypatch) -> None:
    environment = {
        "INTELLIGENCE_EMBEDDING_BASE_URL": LOCAL_OLLAMA_EMBEDDING_BASE_URL + "/",
        "INTELLIGENCE_EMBEDDING_MODEL": EMBEDDING_MODEL,
        "INTELLIGENCE_EMBEDDING_DIMENSION": str(EMBEDDING_DIMENSION),
        "INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS": "180",
        "INTELLIGENCE_RERANKER_MODEL": RERANKER_MODEL,
        "INTELLIGENCE_RERANKER_REVISION": RERANKER_REVISION,
        "INTELLIGENCE_RERANKER_PATH": "/home/qsyy0921/MFL/models/reranker",
        "INTELLIGENCE_RERANKER_DEVICE": "cpu",
        "INTELLIGENCE_RERANKER_MAX_LENGTH": "512",
        "INTELLIGENCE_RERANKER_BATCH_SIZE": "16",
    }
    for key, value in environment.items():
        monkeypatch.setenv(key, value)
    settings = RetrievalSettings.from_env()
    assert settings.embedding_base_url == LOCAL_OLLAMA_EMBEDDING_BASE_URL
    assert settings.embedding_model == EMBEDDING_MODEL
    assert settings.embedding_dimension == EMBEDDING_DIMENSION
    assert settings.reranker_model == RERANKER_MODEL
    assert settings.reranker_revision == RERANKER_REVISION
    assert settings.reranker_batch_size == 16
    assert "ollama-loopback" not in repr(settings)
