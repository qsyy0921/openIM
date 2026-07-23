import pytest

from intelligence_worker.config import GENERATION_MODEL, LOCAL_RESPONSES_BASE_URL, Settings


def _environment() -> dict[str, str]:
    return {
        "INTELLIGENCE_MODEL_BASE_URL": LOCAL_RESPONSES_BASE_URL + "/",
        "INTELLIGENCE_MODEL_API_KEY": "unit-test-secret",
        "INTELLIGENCE_MODEL": GENERATION_MODEL,
        "INTELLIGENCE_MODEL_TIMEOUT_SECONDS": "30",
        "INTELLIGENCE_MODEL_MAX_OUTPUT_TOKENS": "1024",
        "INTELLIGENCE_MODEL_MAX_RETRIES": "2",
        "INTELLIGENCE_MODEL_RETRY_BASE_SECONDS": "0.25",
        "INTELLIGENCE_EMBEDDING_BASE_URL": "http://127.0.0.1:18083/v1/",
        "INTELLIGENCE_EMBEDDING_API_KEY": "local-only",
        "INTELLIGENCE_EMBEDDING_MODEL": "BAAI/bge-m3",
        "INTELLIGENCE_EMBEDDING_DIMENSION": "1024",
        "INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS": "10",
        "INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY": "0.2",
    }


def test_settings_require_fixed_responses_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    for key, value in _environment().items():
        monkeypatch.setenv(key, value)
    settings = Settings.from_env()
    assert settings.model_base_url == LOCAL_RESPONSES_BASE_URL
    assert settings.model == GENERATION_MODEL
    assert settings.model_max_output_tokens == 1024
    assert settings.model_max_retries == 2
    assert settings.embedding_base_url == "http://127.0.0.1:18083/v1"
    assert settings.embedding_dimension == 1024
    assert "unit-test-secret" not in repr(settings)
    assert "local-only" not in repr(settings)


@pytest.mark.parametrize(
    ("key", "value", "message"),
    [
        ("INTELLIGENCE_MODEL_BASE_URL", "http://127.0.0.1:8318/v1", "fixed loopback"),
        ("INTELLIGENCE_MODEL", "another-model", "fixed generation model"),
        ("INTELLIGENCE_MODEL_MAX_RETRIES", "4", "between 0 and 3"),
    ],
)
def test_settings_reject_route_or_retry_drift(
    monkeypatch: pytest.MonkeyPatch, key: str, value: str, message: str
) -> None:
    for env_key, env_value in _environment().items():
        monkeypatch.setenv(env_key, env_value)
    monkeypatch.setenv(key, value)
    with pytest.raises(ValueError, match=message):
        Settings.from_env()


def test_settings_do_not_fallback_when_api_key_is_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    for key, value in _environment().items():
        monkeypatch.setenv(key, value)
    monkeypatch.delenv("INTELLIGENCE_MODEL_API_KEY")
    with pytest.raises(ValueError, match="INTELLIGENCE_MODEL_API_KEY"):
        Settings.from_env()
