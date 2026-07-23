from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import GENERATION_MODEL, LOCAL_RESPONSES_BASE_URL, Settings
from intelligence_worker.responses_client import ResponsesClient


def test_tool_plan_returns_only_arguments() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        body = __import__("json").loads(request.content)
        assert body["input"].find("mcp.arxiv.search") >= 0
        return httpx.Response(200, json={
            "id": "tool-plan-1",
            "model": GENERATION_MODEL,
            "status": "completed",
            "output": [{"type": "message", "content": [{"type": "output_text", "text": '{"arguments":{"query":"agent memory"}}'}]}],
        })

    client = ResponsesClient(
        Settings(LOCAL_RESPONSES_BASE_URL, "unit-test-secret", GENERATION_MODEL, 1, 512, model_max_retries=0),
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
