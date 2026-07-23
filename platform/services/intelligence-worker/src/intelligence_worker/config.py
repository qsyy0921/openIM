from __future__ import annotations

from dataclasses import dataclass, field
import os
from urllib.parse import urlparse


LOCAL_RESPONSES_BASE_URL = "http://127.0.0.1:8317/v1"
GENERATION_MODEL = "gpt-5.6-terra"


@dataclass(frozen=True)
class Settings:
    model_base_url: str
    model_api_key: str = field(repr=False)
    model: str
    model_timeout_seconds: float
    model_max_output_tokens: int
    model_max_retries: int = 2
    model_retry_base_seconds: float = 0.25
    embedding_base_url: str = "https://embedding.invalid"
    embedding_api_key: str = field(default="test-only", repr=False)
    embedding_model: str = "test-embedding"
    embedding_dimension: int = 8
    embedding_timeout_seconds: float = 1
    routing_dense_min_similarity: float = 0.2

    @classmethod
    def from_env(cls) -> "Settings":
        base_url = _required("INTELLIGENCE_MODEL_BASE_URL").rstrip("/")
        parsed = urlparse(base_url)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("INTELLIGENCE_MODEL_BASE_URL must be an HTTP(S) URL")
        if base_url != LOCAL_RESPONSES_BASE_URL:
            raise ValueError("INTELLIGENCE_MODEL_BASE_URL must use the fixed loopback Responses gateway")
        model = _required("INTELLIGENCE_MODEL")
        if model != GENERATION_MODEL:
            raise ValueError("INTELLIGENCE_MODEL must use the fixed generation model")
        timeout = float(_required("INTELLIGENCE_MODEL_TIMEOUT_SECONDS"))
        if timeout <= 0:
            raise ValueError("INTELLIGENCE_MODEL_TIMEOUT_SECONDS must be positive")
        max_tokens = int(_required("INTELLIGENCE_MODEL_MAX_OUTPUT_TOKENS"))
        if max_tokens < 64 or max_tokens > 8192:
            raise ValueError("INTELLIGENCE_MODEL_MAX_OUTPUT_TOKENS must be between 64 and 8192")
        max_retries = int(_required("INTELLIGENCE_MODEL_MAX_RETRIES"))
        if max_retries < 0 or max_retries > 3:
            raise ValueError("INTELLIGENCE_MODEL_MAX_RETRIES must be between 0 and 3")
        retry_base = float(_required("INTELLIGENCE_MODEL_RETRY_BASE_SECONDS"))
        if retry_base <= 0 or retry_base > 5:
            raise ValueError("INTELLIGENCE_MODEL_RETRY_BASE_SECONDS must be in (0, 5]")
        embedding_base_url = _required("INTELLIGENCE_EMBEDDING_BASE_URL").rstrip("/")
        embedding_parsed = urlparse(embedding_base_url)
        if embedding_parsed.scheme not in {"http", "https"} or not embedding_parsed.netloc:
            raise ValueError("INTELLIGENCE_EMBEDDING_BASE_URL must be an HTTP(S) URL")
        embedding_timeout = float(_required("INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS"))
        if embedding_timeout <= 0:
            raise ValueError("INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS must be positive")
        embedding_dimension = int(_required("INTELLIGENCE_EMBEDDING_DIMENSION"))
        if embedding_dimension < 8 or embedding_dimension > 8192:
            raise ValueError("INTELLIGENCE_EMBEDDING_DIMENSION must be between 8 and 8192")
        dense_min_similarity = float(_required("INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY"))
        if dense_min_similarity < -1 or dense_min_similarity > 1:
            raise ValueError("INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY must be in [-1, 1]")
        return cls(
            model_base_url=base_url,
            model_api_key=_required("INTELLIGENCE_MODEL_API_KEY"),
            model=model,
            model_timeout_seconds=timeout,
            model_max_output_tokens=max_tokens,
            model_max_retries=max_retries,
            model_retry_base_seconds=retry_base,
            embedding_base_url=embedding_base_url,
            embedding_api_key=_required("INTELLIGENCE_EMBEDDING_API_KEY"),
            embedding_model=_required("INTELLIGENCE_EMBEDDING_MODEL"),
            embedding_dimension=embedding_dimension,
            embedding_timeout_seconds=embedding_timeout,
            routing_dense_min_similarity=dense_min_similarity,
        )


def _required(key: str) -> str:
    value = os.getenv(key, "").strip()
    if not value:
        raise ValueError(f"required environment variable {key} is missing")
    return value
