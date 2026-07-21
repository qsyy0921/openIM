from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import GENERATION_MODEL, LOCAL_RESPONSES_BASE_URL, Settings
from intelligence_worker.responses_client import ResponsesClient


def _client(content: str) -> ResponsesClient:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/v1/responses"
        body = __import__("json").loads(request.content)
        assert "API Key" in body["instructions"]
        return httpx.Response(200, json={
            "id": "memory-response-1",
            "model": GENERATION_MODEL,
            "status": "completed",
            "output": [{"type": "message", "content": [{"type": "output_text", "text": content}]}],
        })

    return ResponsesClient(
        Settings(LOCAL_RESPONSES_BASE_URL, "unit-test-secret", GENERATION_MODEL, 1, 512, model_max_retries=0),
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
    assert response.json()["detail"] == {"code": "model_output_rejected", "retryable": False}
