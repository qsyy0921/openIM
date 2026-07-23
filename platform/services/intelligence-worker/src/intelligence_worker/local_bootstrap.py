from __future__ import annotations

import asyncio
import os
from pathlib import Path

import uvicorn
import yaml

from .config import (
    GENERATION_MODEL,
    LOCAL_RESPONSES_BASE_URL,
    RERANKER_MODEL,
    RERANKER_REVISION,
    Settings,
)
from .responses_client import ResponsesClient

LOCAL_EMBEDDING_FORWARD_BASE_URL = "http://127.0.0.1:11435/v1"


def _load_cli_proxy_key() -> str:
    default_path = Path.home() / ".cli-proxy-api" / "config.yaml"
    path = Path(os.environ.get("OPENIM_CLIPROXY_CONFIG", str(default_path))).expanduser().resolve()
    if not path.is_file():
        raise RuntimeError("CLIProxyAPI configuration file is missing")
    with path.open("r", encoding="utf-8") as stream:
        payload = yaml.safe_load(stream)
    keys = payload.get("api-keys") if isinstance(payload, dict) else None
    if not isinstance(keys, list):
        raise RuntimeError("CLIProxyAPI configuration has no api-keys list")
    usable = [item.strip() for item in keys if isinstance(item, str) and item.strip()]
    if len(usable) != 1:
        raise RuntimeError("CLIProxyAPI configuration must contain exactly one usable local API key")
    return usable[0]


async def _verify(settings: Settings) -> None:
    client = ResponsesClient(settings)
    try:
        await client.verify_model()
    finally:
        await client.close()


def main() -> None:
    os.environ["INTELLIGENCE_MODEL_BASE_URL"] = LOCAL_RESPONSES_BASE_URL
    os.environ["INTELLIGENCE_MODEL"] = GENERATION_MODEL
    os.environ["INTELLIGENCE_MODEL_API_KEY"] = _load_cli_proxy_key()
    os.environ.setdefault("INTELLIGENCE_MODEL_TIMEOUT_SECONDS", "120")
    os.environ.setdefault("INTELLIGENCE_MODEL_MAX_OUTPUT_TOKENS", "1536")
    os.environ.setdefault("INTELLIGENCE_MODEL_MAX_RETRIES", "2")
    os.environ.setdefault("INTELLIGENCE_MODEL_RETRY_BASE_SECONDS", "0.25")
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_BASE_URL", LOCAL_EMBEDDING_FORWARD_BASE_URL)
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_API_KEY", "local-embedding")
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_MODEL", "qwen3-embedding:4b")
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_DIMENSION", "2560")
    os.environ.setdefault("INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS", "180")
    os.environ.setdefault("INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY", "0.2")
    model_root = Path(
        os.environ.get("OPENIM_MODEL_ROOT", str(Path.home() / ".cache" / "openim" / "models"))
    ).expanduser()
    os.environ.setdefault("INTELLIGENCE_RERANKER_MODEL", RERANKER_MODEL)
    os.environ.setdefault("INTELLIGENCE_RERANKER_REVISION", RERANKER_REVISION)
    os.environ.setdefault(
        "INTELLIGENCE_RERANKER_PATH",
        str(model_root / f"bge-reranker-v2-m3-{RERANKER_REVISION[:12]}"),
    )
    os.environ.setdefault("INTELLIGENCE_RERANKER_DEVICE", "cpu")
    os.environ.setdefault("INTELLIGENCE_RERANKER_MAX_LENGTH", "512")
    os.environ.setdefault("INTELLIGENCE_RERANKER_BATCH_SIZE", "8")
    settings = Settings.from_env()
    asyncio.run(_verify(settings))
    uvicorn.run(
        "intelligence_worker.app:app",
        host="127.0.0.1",
        port=int(os.environ.get("INTELLIGENCE_HTTP_PORT", "18082")),
        log_level="info",
    )


if __name__ == "__main__":
    main()
