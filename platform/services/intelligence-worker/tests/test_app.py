from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import GENERATION_MODEL, LOCAL_RESPONSES_BASE_URL, Settings
from intelligence_worker.models import CandidateRequest
from intelligence_worker.responses_client import ResponsesClient


CATALOG_FIELDS = {
    "agent_id": "agent-1",
    "agent_version_id": "version-1",
    "agent_spec_checksum": "sha256:" + "a" * 64,
    "instructions": "Answer only from authorized evidence.",
    "model_route": GENERATION_MODEL,
    "allowed_action_types": ["create_ticket"],
}


def _settings(max_retries: int = 0) -> Settings:
    return Settings(
        LOCAL_RESPONSES_BASE_URL, "unit-test-secret", GENERATION_MODEL, 1, 512,
        model_max_retries=max_retries, model_retry_base_seconds=0.001,
    )


def _response(content: str, response_id: str, model: str = GENERATION_MODEL) -> httpx.Response:
    return httpx.Response(200, json={
        "id": response_id,
        "model": model,
        "status": "completed",
        "output": [{"type": "message", "content": [{"type": "output_text", "text": content}]}],
    })


def test_candidate_calls_responses_api_and_extracts_json() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/v1/responses"
        body = __import__("json").loads(request.content)
        assert body["model"] == GENERATION_MODEL
        assert body["stream"] is False
        assert body["store"] is False
        assert body["text"]["format"]["type"] == "json_schema"
        return _response(
            '{"text":"候选回答 [C1]","citation_ids":["C1"],"grounding_status":"grounded","action_intent":null}',
            "resp_1",
        )

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-1",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "问题",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"evidence"}],
            },
        )
    assert response.status_code == 200
    assert response.json() == {
        "text": "候选回答 [C1]",
        "model": GENERATION_MODEL,
        "provider_response_id": "resp_1",
        "citation_ids": ["C1"],
        "grounding_status": "grounded",
        "action_intent": None,
    }


def test_model_failure_is_explicit_not_fallback() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "unavailable"})

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-1",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "问题",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"evidence"}],
            },
        )
    assert response.status_code == 503
    assert response.json()["detail"] == {"code": "model_unavailable", "retryable": True}


def test_explicit_ticket_command_creates_bounded_candidate() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"建议已生成 [C1]","citation_ids":["C1"],"grounding_status":"grounded","action_intent":null}', "resp_action")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-action",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "创建工单：复核迁移计划",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"evidence"}],
            },
        )
    assert response.status_code == 200
    assert response.json()["action_intent"] == {"type": "create_ticket", "title": "复核迁移计划"}


def test_unsolicited_model_action_is_rejected() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"answer [C1]","citation_ids":["C1"],"grounding_status":"grounded","action_intent":{"type":"create_ticket","title":"unauthorized"}}', "resp_unsolicited")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-no-action",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "普通问答",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"evidence"}],
            },
        )
    assert response.status_code == 502


def test_negated_ticket_phrase_does_not_enter_action_protocol() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"不会创建 [C1]","citation_ids":["C1"],"grounding_status":"grounded","action_intent":null}', "resp_negated")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-negated",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "不要创建工单：这只是讨论",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"evidence"}],
            },
        )
    assert response.status_code == 200
    assert response.json()["action_intent"] is None


def test_insufficient_enterprise_evidence_can_abstain_without_fake_citation() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"现有证据不足以回答该问题。","citation_ids":[],"grounding_status":"insufficient_evidence","action_intent":null}', "resp_abstain")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-abstain", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **CATALOG_FIELDS, "content": "未记录会议中的口头承诺是什么？",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"现有会议纪要没有记录口头承诺。"}],
            },
        )
    assert response.status_code == 200
    assert response.json()["grounding_status"] == "insufficient_evidence"
    assert response.json()["citation_ids"] == []


def test_grounded_enterprise_answer_without_citation_fails_closed() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"没有引用的断言","citation_ids":[],"grounding_status":"grounded","action_intent":null}', "resp_invalid_grounding")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-invalid-grounding", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **CATALOG_FIELDS, "content": "问题",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"证据"}],
            },
        )
    assert response.status_code == 502


def test_candidate_with_undeclared_text_citation_fails_closed() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"回答 [C1] 和 [C2]","citation_ids":["C1"],"grounding_status":"grounded","action_intent":null}', "resp_undeclared_citation")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-undeclared", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **CATALOG_FIELDS, "content": "问题",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"证据"}],
            },
        )
    assert response.status_code == 502


def test_candidate_with_unauthorized_citation_id_fails_closed() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return _response('{"text":"回答 [C2]","citation_ids":["C2"],"grounding_status":"grounded","action_intent":null}', "resp_unauthorized_citation")

    client = ResponsesClient(_settings(), httpx.MockTransport(handler))
    with TestClient(create_app(client)) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-unauthorized", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **CATALOG_FIELDS, "content": "问题",
                "evidence": [{"citation_id":"C1","document_id":"d","version_id":"v","chunk_id":"c","title":"t","source_uri":"doc://d","checksum":"sum","content":"证据"}],
            },
        )
    assert response.status_code == 502


def test_request_rejects_unknown_fields() -> None:
    with TestClient(create_app(_NeverCalled())) as http:
        response = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-1",
                "tenant_id": "tenant-1",
                "conversation_id": "si_a_b",
                "sender_id": "user-1",
                **CATALOG_FIELDS,
                "content": "问题",
                "evidence": [],
                "tool": "write",
            },
        )
    assert response.status_code == 422


class _NeverCalled:
    async def generate(self, _: CandidateRequest):
        raise AssertionError("must not be called")


class _EmbeddingFixture:
    model = "qwen3-embedding:4b"
    dimension = 8

    async def embed(self, texts: list[str]) -> list[list[float]]:
        return [[float(index + 1)] + [0.0] * 7 for index, _ in enumerate(texts)]


def test_embedding_endpoint_preserves_order_and_model_contract() -> None:
    with TestClient(create_app(_NeverCalled(), embedding_provider=_EmbeddingFixture())) as http:
        response = http.post("/v1/embeddings", json={"texts": ["第一段", "第二段"]})
    assert response.status_code == 200
    assert response.json() == {
        "model": "qwen3-embedding:4b", "dimension": 8,
        "vectors": [[1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0], [2.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0]],
    }


def test_embedding_endpoint_fails_closed_when_unconfigured() -> None:
    with TestClient(create_app(_NeverCalled())) as http:
        response = http.post("/v1/embeddings", json={"texts": ["必须嵌入"]})
    assert response.status_code == 503


def test_metrics_endpoint_exposes_bounded_service_metrics() -> None:
    with TestClient(create_app(_NeverCalled())) as http:
        health = http.get("/healthz")
        metrics = http.get("/metrics")

    assert health.status_code == 200
    assert metrics.status_code == 200
    assert "openim_intelligence_http_requests_total" in metrics.text
    assert "openim_intelligence_embedding_batch_size" in metrics.text


def test_unconfigured_model_route_and_disallowed_action_fail_closed() -> None:
    client = ResponsesClient(_settings(), httpx.MockTransport(lambda _: httpx.Response(500)))
    with TestClient(create_app(client)) as http:
        wrong_route = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-route", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **{**CATALOG_FIELDS, "model_route": "other-model"}, "content": "问题", "evidence": [],
            },
        )
        disallowed = http.post(
            "/v1/candidates",
            json={
                "run_id": "run-action-policy", "tenant_id": "tenant-1", "conversation_id": "si_a_b", "sender_id": "user-1",
                **{**CATALOG_FIELDS, "allowed_action_types": []}, "content": "创建工单：禁止的动作", "evidence": [],
            },
        )
    assert wrong_route.status_code == 502
    assert disallowed.status_code == 502
