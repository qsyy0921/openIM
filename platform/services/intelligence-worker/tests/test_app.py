from fastapi.testclient import TestClient
import httpx

from intelligence_worker.app import create_app
from intelligence_worker.config import Settings
from intelligence_worker.models import CandidateRequest
from intelligence_worker.deepseek_client import DeepSeekClient


CATALOG_FIELDS = {
    "agent_id": "agent-1",
    "agent_version_id": "version-1",
    "agent_spec_checksum": "sha256:" + "a" * 64,
    "instructions": "Answer only from authorized evidence.",
    "model_route": "test-model",
    "allowed_action_types": ["create_ticket"],
}


def test_candidate_calls_deepseek_and_extracts_json() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/chat/completions"
        body = __import__("json").loads(request.content)
        assert body["model"] == "test-model"
        assert body["response_format"] == {"type": "json_object"}
        assert body["thinking"] == {"type": "disabled"}
        return httpx.Response(
            200,
            json={
                "id": "resp_1",
                "model": "test-model-2026",
                "choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": '{"text":"候选回答 [C1]","citation_ids":["C1"],"action_intent":null}'}}],
            },
        )

    client = DeepSeekClient(
        Settings("https://api.deepseek.test", "secret", "test-model", 1, 512),
        httpx.MockTransport(handler),
    )
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
        "model": "test-model-2026",
        "provider_response_id": "resp_1",
        "citation_ids": ["C1"],
        "action_intent": None,
    }


def test_model_failure_is_explicit_not_fallback() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "unavailable"})

    client = DeepSeekClient(
        Settings("https://api.deepseek.test", "secret", "test-model", 1, 512),
        httpx.MockTransport(handler),
    )
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
    assert response.status_code == 502
    assert response.json()["detail"] == "required model provider failed"


def test_explicit_ticket_command_creates_bounded_candidate() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "id": "resp_action",
                "model": "test-model",
                "choices": [{"finish_reason": "stop", "message": {"content": '{"text":"建议已生成 [C1]","citation_ids":["C1"],"action_intent":null}'}}],
            },
        )

    client = DeepSeekClient(Settings("https://api.deepseek.test", "secret", "test-model", 1, 512), httpx.MockTransport(handler))
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
        return httpx.Response(
            200,
            json={
                "id": "resp_unsolicited",
                "model": "test-model",
                "choices": [{"finish_reason": "stop", "message": {"content": '{"text":"answer [C1]","citation_ids":["C1"],"action_intent":{"type":"create_ticket","title":"unauthorized"}}'}}],
            },
        )

    client = DeepSeekClient(Settings("https://api.deepseek.test", "secret", "test-model", 1, 512), httpx.MockTransport(handler))
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
        return httpx.Response(
            200,
            json={
                "id": "resp_negated",
                "model": "test-model",
                "choices": [{"finish_reason": "stop", "message": {"content": '{"text":"不会创建 [C1]","citation_ids":["C1"],"action_intent":null}'}}],
            },
        )

    client = DeepSeekClient(Settings("https://api.deepseek.test", "secret", "test-model", 1, 512), httpx.MockTransport(handler))
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


def test_unconfigured_model_route_and_disallowed_action_fail_closed() -> None:
    client = DeepSeekClient(Settings("https://api.deepseek.test", "secret", "test-model", 1, 512), httpx.MockTransport(lambda _: httpx.Response(500)))
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
