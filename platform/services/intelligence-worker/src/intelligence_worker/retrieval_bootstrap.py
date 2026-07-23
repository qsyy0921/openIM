from __future__ import annotations

import os

import uvicorn

from .config import (
    EMBEDDING_DIMENSION,
    EMBEDDING_MODEL,
    LOCAL_OLLAMA_EMBEDDING_BASE_URL,
    RERANKER_MODEL,
    RERANKER_REVISION,
    RetrievalSettings,
)


def main() -> None:
    os.environ["INTELLIGENCE_EMBEDDING_BASE_URL"] = LOCAL_OLLAMA_EMBEDDING_BASE_URL
    os.environ["INTELLIGENCE_EMBEDDING_MODEL"] = EMBEDDING_MODEL
    os.environ["INTELLIGENCE_EMBEDDING_DIMENSION"] = str(EMBEDDING_DIMENSION)
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS", "180")
    os.environ["INTELLIGENCE_RERANKER_MODEL"] = RERANKER_MODEL
    os.environ["INTELLIGENCE_RERANKER_REVISION"] = RERANKER_REVISION
    os.environ["INTELLIGENCE_RERANKER_DEVICE"] = "cpu"
    os.environ["INTELLIGENCE_RERANKER_MAX_LENGTH"] = "512"
    os.environ.setdefault("INTELLIGENCE_RERANKER_BATCH_SIZE", "8")
    RetrievalSettings.from_env()
    uvicorn.run(
        "intelligence_worker.retrieval_app:app",
        host="127.0.0.1",
        port=int(os.environ.get("INTELLIGENCE_HTTP_PORT", "18083")),
        log_level="info",
    )


if __name__ == "__main__":
    main()
