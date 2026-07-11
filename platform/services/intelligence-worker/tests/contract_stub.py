"""Deterministic contract server used only by local end-to-end tests."""

from fastapi import FastAPI

from intelligence_worker.models import ActionIntent, CandidateRequest, CandidateResponse


app = FastAPI()


@app.post("/v1/candidates", response_model=CandidateResponse)
async def candidate(request: CandidateRequest) -> CandidateResponse:
    citations = [item.citation_id for item in request.evidence]
    suffix = " ".join(f"[{item}]" for item in citations)
    action = None
    if "创建工单" in request.content:
        title = request.content.split("创建工单", 1)[1].lstrip("：: ") or "待处理工单"
        action = ActionIntent(type="create_ticket", title=title[:200])
    return CandidateResponse(
        text=f"测试契约回复：已收到问题“{request.content}”。 {suffix}".strip(),
        model="contract-stub",
        provider_response_id=f"stub-{request.run_id}",
        citation_ids=citations,
        action_intent=action,
    )
