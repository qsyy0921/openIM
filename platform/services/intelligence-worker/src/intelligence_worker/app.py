from __future__ import annotations

from contextlib import asynccontextmanager
import logging

from fastapi import FastAPI, HTTPException, Request
import httpx

from .config import Settings
from .models import CandidateRequest, CandidateResponse
from .deepseek_client import DeepSeekClient


def create_app(client: DeepSeekClient | None = None) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        owned = client is None
        app.state.candidate_client = client or DeepSeekClient(Settings.from_env())
        yield
        if owned:
            await app.state.candidate_client.close()

    app = FastAPI(title="OpenIM Intelligence Worker", version="0.1.0", lifespan=lifespan)

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        return {"status": "ok"}

    @app.post("/v1/candidates", response_model=CandidateResponse)
    async def candidates(body: CandidateRequest, request: Request) -> CandidateResponse:
        try:
            return await request.app.state.candidate_client.generate(body)
        except (httpx.HTTPError, ValueError) as exc:
            logging.warning("candidate generation failed: %s", type(exc).__name__)
            raise HTTPException(status_code=502, detail="required model provider failed") from exc

    return app


app = create_app()
