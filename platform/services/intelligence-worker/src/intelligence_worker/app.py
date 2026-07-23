from __future__ import annotations

from contextlib import asynccontextmanager
import logging

from fastapi import FastAPI, HTTPException, Request, Response
import httpx

from .config import Settings
from .models import (
    CandidateRequest,
    CandidateResponse,
    EmbeddingRequest,
    EmbeddingResponse,
    MemoryExtractionRequest,
    MemoryExtractionResponse,
    ProactiveRankRequest,
    ProactiveRankResponse,
    RerankRequest,
    RerankResponse,
    RouteRequest,
    RouteResponse,
    ToolPlanRequest,
    ToolPlanResponse,
)
from .provider_errors import ModelProviderError
from .responses_client import ResponsesClient
from .routing import IntentRouter, OpenAIEmbeddingClient
from .proactive import ProactiveRanker
from .metrics import EMBEDDING_BATCH, RERANKER_BATCH, metrics_response, observe_request
from .reranker import LocalCrossEncoderReranker, Reranker


def create_app(
    client: ResponsesClient | None = None,
    router: IntentRouter | None = None,
    proactive_ranker: ProactiveRanker | None = None,
    embedding_provider: OpenAIEmbeddingClient | None = None,
    reranker: Reranker | None = None,
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        owned = client is None
        owned_embedding = None
        if client is None:
            settings = Settings.from_env()
            app.state.candidate_client = ResponsesClient(settings)
            await app.state.candidate_client.verify_model()
            owned_embedding = OpenAIEmbeddingClient(settings)
            app.state.embedding_provider = owned_embedding
            app.state.intent_router = router or IntentRouter(
                app.state.candidate_client, owned_embedding, settings.routing_dense_min_similarity
            )
            app.state.proactive_ranker = proactive_ranker or ProactiveRanker(owned_embedding)
            app.state.reranker = reranker or LocalCrossEncoderReranker(settings)
        else:
            app.state.candidate_client = client
            app.state.intent_router = router
            app.state.proactive_ranker = proactive_ranker
            app.state.embedding_provider = embedding_provider
            app.state.reranker = reranker
        yield
        if owned:
            await app.state.candidate_client.close()
        if owned_embedding is not None:
            await owned_embedding.close()

    app = FastAPI(title="OpenIM Intelligence Worker", version="0.1.0", lifespan=lifespan)
    app.middleware("http")(observe_request)

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/metrics")
    async def metrics() -> Response:
        return metrics_response()

    @app.post("/v1/candidates", response_model=CandidateResponse)
    async def candidates(body: CandidateRequest, request: Request) -> CandidateResponse:
        try:
            return await request.app.state.candidate_client.generate(body)
        except ModelProviderError as exc:
            raise _provider_http_exception("candidate generation", exc) from exc
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("candidate generation failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail={"code": "model_output_rejected", "retryable": False}) from exc

    @app.post("/v1/routes", response_model=RouteResponse)
    async def routes(body: RouteRequest, request: Request) -> RouteResponse:
        if request.app.state.intent_router is None:
            raise HTTPException(status_code=503, detail="intent router is not configured")
        try:
            return await request.app.state.intent_router.route(body)
        except ModelProviderError as exc:
            raise _provider_http_exception("intent routing", exc) from exc
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("intent routing failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required routing dependency failed") from exc

    @app.post("/v1/memory-extractions", response_model=MemoryExtractionResponse)
    async def memory_extractions(body: MemoryExtractionRequest, request: Request) -> MemoryExtractionResponse:
        try:
            return await request.app.state.candidate_client.extract_memory(body)
        except ModelProviderError as exc:
            raise _provider_http_exception("memory extraction", exc) from exc
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("memory extraction failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail={"code": "model_output_rejected", "retryable": False}) from exc

    @app.post("/v1/proactive-ranks", response_model=ProactiveRankResponse)
    async def proactive_ranks(body: ProactiveRankRequest, request: Request) -> ProactiveRankResponse:
        if request.app.state.proactive_ranker is None:
            raise HTTPException(status_code=503, detail="proactive ranker is not configured")
        try:
            return await request.app.state.proactive_ranker.rank(body)
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("proactive ranking failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required proactive ranking dependency failed") from exc

    @app.post("/v1/tool-plans", response_model=ToolPlanResponse)
    async def tool_plans(body: ToolPlanRequest, request: Request) -> ToolPlanResponse:
        try:
            return await request.app.state.candidate_client.plan_tool(body)
        except ModelProviderError as exc:
            raise _provider_http_exception("tool planning", exc) from exc
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("tool planning failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail={"code": "model_output_rejected", "retryable": False}) from exc

    @app.post("/v1/embeddings", response_model=EmbeddingResponse)
    async def embeddings(body: EmbeddingRequest, request: Request) -> EmbeddingResponse:
        provider = request.app.state.embedding_provider
        if provider is None:
            raise HTTPException(status_code=503, detail="embedding provider is not configured")
        try:
            EMBEDDING_BATCH.observe(len(body.texts))
            vectors = await provider.embed(body.texts)
            return EmbeddingResponse(model=provider.model, dimension=provider.dimension, vectors=vectors)
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("embedding generation failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required embedding provider failed") from exc

    @app.post("/v1/rerank", response_model=RerankResponse)
    async def rerank(body: RerankRequest, request: Request) -> RerankResponse:
        provider = request.app.state.reranker
        if provider is None:
            raise HTTPException(status_code=503, detail="reranker is not configured")
        try:
            RERANKER_BATCH.observe(len(body.candidates))
            return await provider.rank(body)
        except ValueError as exc:
            logging.warning("reranking failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required reranker failed") from exc

    return app


def _provider_http_exception(operation: str, exc: ModelProviderError) -> HTTPException:
    logging.warning("%s failed: %s", operation, exc.code)
    return HTTPException(
        status_code=503 if exc.retryable else 502,
        detail={"code": exc.code, "retryable": exc.retryable},
    )


app = create_app()
