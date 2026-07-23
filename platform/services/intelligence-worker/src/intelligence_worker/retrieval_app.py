from __future__ import annotations

from contextlib import asynccontextmanager
import logging

from fastapi import FastAPI, HTTPException, Request, Response
import httpx

from .config import RetrievalSettings
from .metrics import EMBEDDING_BATCH, RERANKER_BATCH, metrics_response, observe_request
from .models import EmbeddingRequest, EmbeddingResponse, RerankRequest, RerankResponse
from .reranker import LocalCrossEncoderReranker, Reranker
from .routing import OpenAIEmbeddingClient


def create_retrieval_app(
    settings: RetrievalSettings | None = None,
    embedding_provider: OpenAIEmbeddingClient | None = None,
    reranker: Reranker | None = None,
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        resolved = settings
        if embedding_provider is None or reranker is None:
            resolved = resolved or RetrievalSettings.from_env()
        owned_embedding = None
        if embedding_provider is None:
            if resolved is None:
                raise RuntimeError("retrieval settings are unavailable")
            owned_embedding = OpenAIEmbeddingClient(resolved)
            app.state.embedding_provider = owned_embedding
        else:
            app.state.embedding_provider = embedding_provider
        if reranker is None:
            if resolved is None:
                raise RuntimeError("retrieval settings are unavailable")
            app.state.reranker = LocalCrossEncoderReranker(resolved)
        else:
            app.state.reranker = reranker
        yield
        if owned_embedding is not None:
            await owned_embedding.close()

    app = FastAPI(
        title="OpenIM Retrieval Evaluation Worker",
        version="0.1.0",
        lifespan=lifespan,
    )
    app.middleware("http")(observe_request)

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        return {"status": "ok", "surface": "retrieval-only"}

    @app.get("/metrics")
    async def metrics() -> Response:
        return metrics_response()

    @app.post("/v1/embeddings", response_model=EmbeddingResponse)
    async def embeddings(body: EmbeddingRequest, request: Request) -> EmbeddingResponse:
        provider = request.app.state.embedding_provider
        try:
            EMBEDDING_BATCH.observe(len(body.texts))
            vectors = await provider.embed(body.texts)
            return EmbeddingResponse(
                model=provider.model,
                dimension=provider.dimension,
                vectors=vectors,
            )
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("embedding generation failed: %s", type(exc).__name__)
            raise HTTPException(
                status_code=502,
                detail="required embedding provider failed",
            ) from exc

    @app.post("/v1/rerank", response_model=RerankResponse)
    async def rerank_request(body: RerankRequest, request: Request) -> RerankResponse:
        provider = request.app.state.reranker
        try:
            RERANKER_BATCH.observe(len(body.candidates))
            return await provider.rank(body)
        except ValueError as exc:
            logging.warning("reranking failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required reranker failed") from exc

    return app


app = create_retrieval_app()
