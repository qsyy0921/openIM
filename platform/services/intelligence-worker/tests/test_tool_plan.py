from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import Settings
from intelligence_worker.deepseek_client import DeepSeekClient


def test_tool_plan_returns_only_arguments() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        body = __import__("json").loads(request.content)
        assert body["messages"][1]["content"].find("mcp.arxiv.search") >= 0
        return httpx.Response(200, json={
            "id": "tool-plan-1",
            "model": "test-model",
            "choices": [{"finish_reason": "stop", "message": {"content": '{"arguments":{"query":"agent memory"}}'}}],
        })

    client = DeepSeekClient(
        Settings("https://api.deepseek.test", "secret", "test-model", 1, 512),
        httpx.MockTransport(handler),
    )
    with TestClient(create_app(client)) as http:
        response = http.post("/v1/tool-plans", json={
            "run_id": "run-1",
            "content": "查找 agent memory 论文",
            "operation": {
                "operation_id": "mcp.arxiv.search",
                "name": "arXiv search",
                "summary": "Search papers",
                "input_schema": {
                    "type": "object",
                    "properties": {"query": {"type": "string"}},
                    "required": ["query"],
                    "additionalProperties": False,
                },
            },
        })
    assert response.status_code == 200
    assert response.json() == {"arguments": {"query": "agent memory"}, "provider_response_id": "tool-plan-1"}
