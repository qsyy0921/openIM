from __future__ import annotations

from dataclasses import dataclass
import os
from urllib.parse import urlparse


@dataclass(frozen=True)
class Settings:
    deepseek_base_url: str
    deepseek_api_key: str
    deepseek_model: str
    deepseek_timeout_seconds: float
    deepseek_max_tokens: int
    embedding_base_url: str = "https://embedding.invalid"
    embedding_api_key: str = "test-only"
    embedding_model: str = "test-embedding"
    embedding_dimension: int = 8
    embedding_timeout_seconds: float = 1
    routing_dense_min_similarity: float = 0.2

    @classmethod
    def from_env(cls) -> "Settings":
        base_url = _required("INTELLIGENCE_DEEPSEEK_BASE_URL").rstrip("/")
        parsed = urlparse(base_url)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("INTELLIGENCE_DEEPSEEK_BASE_URL must be an HTTP(S) URL")
        timeout = float(_required("INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS"))
        if timeout <= 0:
            raise ValueError("INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS must be positive")
        max_tokens = int(_required("INTELLIGENCE_DEEPSEEK_MAX_TOKENS"))
        if max_tokens < 64 or max_tokens > 8192:
            raise ValueError("INTELLIGENCE_DEEPSEEK_MAX_TOKENS must be between 64 and 8192")
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
            deepseek_base_url=base_url,
            deepseek_api_key=_required("INTELLIGENCE_DEEPSEEK_API_KEY"),
            deepseek_model=_required("INTELLIGENCE_DEEPSEEK_MODEL"),
            deepseek_timeout_seconds=timeout,
            deepseek_max_tokens=max_tokens,
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
