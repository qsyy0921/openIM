import asyncio
import json

import httpx
import pytest

from intelligence_worker.config import GENERATION_MODEL, LOCAL_RESPONSES_BASE_URL, Settings
from intelligence_worker.provider_errors import (
    ModelAuthenticationFailed,
    ModelProtocolError,
    ModelRouteUnavailable,
    ModelUnavailable,
)
from intelligence_worker.models import OperationDiscovery, RouteRequest
from intelligence_worker.responses_client import ResponsesClient


def settings(max_retries: int = 2) -> Settings:
    return Settings(
        model_base_url=LOCAL_RESPONSES_BASE_URL,
        model_api_key="unit-test-secret",
        model=GENERATION_MODEL,
        model_timeout_seconds=1,
        model_max_output_tokens=512,
        model_max_retries=max_retries,
        model_retry_base_seconds=0.001,
    )


def response_payload(text: str, response_id: str = "resp_1", model: str = GENERATION_MODEL) -> dict:
    return {
        "id": response_id,
        "model": model,
        "status": "completed",
        "output": [{"type": "message", "content": [{"type": "output_text", "text": text}]}],
    }


def test_model_discovery_requires_exact_model() -> None:
    async def run() -> None:
        client = ResponsesClient(
            settings(),
            httpx.MockTransport(lambda request: httpx.Response(200, json={"data": [{"id": "other-model"}]})),
        )
        try:
            with pytest.raises(ModelRouteUnavailable):
                await client.verify_model()
        finally:
            await client.close()
    asyncio.run(run())


def test_retry_is_bounded_and_never_changes_route_or_model() -> None:
    calls: list[tuple[str, str]] = []
    sleeps: list[float] = []

    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        calls.append((request.url.path, body["model"]))
        return httpx.Response(503)

    async def sleeper(delay: float) -> None:
        sleeps.append(delay)

    async def run() -> None:
        client = ResponsesClient(settings(2), httpx.MockTransport(handler), sleeper)
        try:
            with pytest.raises(ModelUnavailable):
                await client._responses_json("instructions", {"message": "hello"}, "test", {
                    "type": "object", "properties": {"value": {"type": "string"}},
                    "required": ["value"], "additionalProperties": False,
                }, 64)
        finally:
            await client.close()
    asyncio.run(run())
    assert calls == [("/v1/responses", GENERATION_MODEL)] * 3
    assert sleeps == [0.001, 0.002]


def test_authentication_failure_is_not_retried() -> None:
    calls = 0

    def handler(_: httpx.Request) -> httpx.Response:
        nonlocal calls
        calls += 1
        return httpx.Response(401)

    async def run() -> None:
        client = ResponsesClient(settings(), httpx.MockTransport(handler))
        try:
            with pytest.raises(ModelAuthenticationFailed):
                await client.verify_model()
        finally:
            await client.close()
    asyncio.run(run())
    assert calls == 1


def test_response_requires_completed_exact_model_and_one_output_text() -> None:
    async def run() -> None:
        client = ResponsesClient(
            settings(),
            httpx.MockTransport(lambda _: httpx.Response(200, json=response_payload("{}", model="other-model"))),
        )
        try:
            with pytest.raises(ModelProtocolError):
                await client._responses_json("instructions", {}, "test", {
                    "type": "object", "properties": {}, "required": [], "additionalProperties": False,
                }, 64)
        finally:
            await client.close()
    asyncio.run(run())


def test_intent_analysis_marks_authority_context_as_server_resolved() -> None:
    observed: dict[str, object] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        observed["instructions"] = body["instructions"]
        observed["input"] = json.loads(body["input"])
        return httpx.Response(200, json=response_payload(json.dumps({
            "schema_version": "1",
            "rewritten_intent": "search enterprise knowledge",
            "hypothetical_capability": "retrieve an authorized policy",
            "required_inputs": ["query"],
            "missing_required_inputs": [],
            "unresolved_references": [],
            "desired_outputs": ["text"],
            "tool_requirement": "required",
        })))

    request = RouteRequest(
        run_id="run-intent-context",
        capability_snapshot_id="capability-v1:" + "a" * 64,
        content="查询企业制度",
        operations=[OperationDiscovery(
            operation_id="enterprise.knowledge.search",
            name="Enterprise knowledge search",
            summary="Search authorized enterprise knowledge",
            parameter_terms=["query"],
            examples=["search policy"],
            output_kinds=["text"],
        )],
    )

    async def run() -> None:
        client = ResponsesClient(settings(), httpx.MockTransport(handler))
        try:
            await client.analyze_intent(request)
        finally:
            await client.close()

    asyncio.run(run())
    assert observed["input"] == {
        "message": "查询企业制度",
        "server_resolved_context": {
            "tenant_and_member_identity": True,
            "knowledge_acl_scope": True,
            "tool_permissions": True,
            "approval_policy": True,
        },
    }
    assert "知识库访问范围" in str(observed["instructions"])
