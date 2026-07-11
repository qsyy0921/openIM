import pytest

from intelligence_worker.config import Settings


def test_settings_require_complete_deepseek_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    values = {
        "INTELLIGENCE_DEEPSEEK_BASE_URL": "https://api.deepseek.test/",
        "INTELLIGENCE_DEEPSEEK_API_KEY": "secret",
        "INTELLIGENCE_DEEPSEEK_MODEL": "deepseek-v4-pro",
        "INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS": "30",
        "INTELLIGENCE_DEEPSEEK_MAX_TOKENS": "1024",
    }
    for key, value in values.items():
        monkeypatch.setenv(key, value)
    settings = Settings.from_env()
    assert settings.deepseek_base_url == "https://api.deepseek.test"
    assert settings.deepseek_model == "deepseek-v4-pro"
    assert settings.deepseek_max_tokens == 1024


def test_settings_do_not_fallback_when_api_key_is_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    for key in (
        "INTELLIGENCE_DEEPSEEK_BASE_URL",
        "INTELLIGENCE_DEEPSEEK_API_KEY",
        "INTELLIGENCE_DEEPSEEK_MODEL",
        "INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS",
        "INTELLIGENCE_DEEPSEEK_MAX_TOKENS",
    ):
        monkeypatch.delenv(key, raising=False)
    with pytest.raises(ValueError, match="INTELLIGENCE_DEEPSEEK_BASE_URL"):
        Settings.from_env()
