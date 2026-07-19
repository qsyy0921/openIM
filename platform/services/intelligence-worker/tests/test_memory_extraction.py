from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import Settings
from intelligence_worker.deepseek_client import DeepSeekClient


def _client(content: str) -> DeepSeekClient:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/chat/completions"
        body = __import__("json").loads(request.content)
        assert "API Key" in body["messages"][0]["content"]
        return httpx.Response(200, json={
            "id": "memory-response-1",
            "model": "test-model",
            "choices": [{"finish_reason": "stop", "message": {"content": content}}],
        })

    return DeepSeekClient(
        Settings("https://api.deepseek.test", "secret", "test-model", 1, 512),
        httpx.MockTransport(handler),
    )


def test_memory_extraction_returns_bounded_user_fact() -> None:
    client = _client('{"facts":[{"category":"preference","subject":"answer style","content":"用户偏好简洁回答","confidence":0.95}]}')
    with TestClient(create_app(client)) as http:
        response = http.post("/v1/memory-extractions", json={
            "run_id": "run-1",
            "user_message": "以后请简洁回答",
            "assistant_response": "好的",
        })
    assert response.status_code == 200
    assert response.json()["facts"][0]["category"] == "preference"


def test_memory_extraction_rejects_sensitive_result() -> None:
    client = _client('{"facts":[{"category":"context","subject":"credential","content":"API Key 是 sk-secret","confidence":0.99}]}')
    with TestClient(create_app(client)) as http:
        response = http.post("/v1/memory-extractions", json={
            "run_id": "run-1",
            "user_message": "记住我的密钥",
            "assistant_response": "不能保存",
        })
    assert response.status_code == 502
    assert response.json()["detail"] == "required memory extraction provider failed"
