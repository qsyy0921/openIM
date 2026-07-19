from __future__ import annotations

import pytest
import asyncio

from intelligence_worker.models import IntentView, OperationDiscovery, RouteRequest
from intelligence_worker.routing import IntentRouter


class IntentProviderStub:
    def __init__(self, view: IntentView):
        self.view = view
        self.calls = 0

    async def analyze_intent(self, request: RouteRequest) -> tuple[IntentView, str]:
        self.calls += 1
        return self.view, "route-response-1"


class EmbeddingProviderStub:
    def __init__(self, vectors: list[list[float]]):
        self.vectors = vectors
        self.calls = 0

    async def embed(self, texts: list[str]) -> list[list[float]]:
        self.calls += 1
        assert len(texts) == len(self.vectors)
        return self.vectors


def route_request(content: str = "查询报销制度") -> RouteRequest:
    return RouteRequest(
        run_id="run-1",
        capability_snapshot_id="capability-v1:" + "a" * 64,
        content=content,
        operations=[
            OperationDiscovery(
                operation_id="enterprise.knowledge.search",
                name="Enterprise knowledge search",
                summary="Search authorized enterprise policies and procedures",
                parameter_terms=["knowledge", "policy", "知识", "制度"],
                examples=["查询报销制度"],
                output_kinds=["text", "data"],
            ),
            OperationDiscovery(
                operation_id="collaboration.ticket.create",
                name="Create ticket",
                summary="Create an approved collaboration ticket",
                parameter_terms=["ticket", "工单"],
                examples=["创建工单"],
                output_kinds=["text", "data"],
            ),
        ],
    )


def intent_view(**overrides: object) -> IntentView:
    values: dict[str, object] = {
        "schema_version": "1",
        "rewritten_intent": "find the enterprise reimbursement policy",
        "hypothetical_capability": "search authorized enterprise policy documents",
        "required_inputs": ["query"],
        "missing_required_inputs": [],
        "unresolved_references": [],
        "desired_outputs": ["text"],
        "tool_requirement": "required",
    }
    values.update(overrides)
    return IntentView.model_validate(values)


def test_router_fuses_original_and_intent_view_to_pinned_operation() -> None:
    intent = IntentProviderStub(intent_view())
    embedding = EmbeddingProviderStub([
        [1.0, 0.0], [1.0, 0.0], [1.0, 0.0], [0.0, 1.0]
    ])
    response = asyncio.run(IntentRouter(intent, embedding, 0.1).route(route_request()))
    assert response.status == "selected"
    assert response.operation_id == "enterprise.knowledge.search"
    assert response.candidates[0].reason_codes == [
        "original_lexical", "original_dense", "llm_hypothetical"
    ]
    assert intent.calls == 1
    assert embedding.calls == 1


def test_router_returns_structured_clarification_before_embedding() -> None:
    intent = IntentProviderStub(intent_view(
        required_inputs=["project"], missing_required_inputs=["project"]
    ))
    embedding = EmbeddingProviderStub([])
    response = asyncio.run(IntentRouter(intent, embedding, 0.1).route(route_request("处理它")))
    assert response.status == "clarify"
    assert "project" in (response.clarification or "")
    assert embedding.calls == 0


def test_router_honors_tool_free_intent_without_catalog_guess() -> None:
    intent = IntentProviderStub(intent_view(tool_requirement="none"))
    embedding = EmbeddingProviderStub([])
    response = asyncio.run(IntentRouter(intent, embedding, 0.1).route(route_request("你好")))
    assert response.status == "no_tool"
    assert response.operation_id is None
    assert embedding.calls == 0


def test_intent_view_rejects_missing_input_outside_required_set() -> None:
    intent = IntentProviderStub(intent_view(
        required_inputs=["query"], missing_required_inputs=["project"]
    ))
    router = IntentRouter(intent, EmbeddingProviderStub([]), 0.1)
    with pytest.raises(ValueError, match="subset"):
        asyncio.run(router.route(route_request()))
