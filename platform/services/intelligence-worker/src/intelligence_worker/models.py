from pydantic import BaseModel, ConfigDict, Field, model_validator
from typing import Literal


class CandidateRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str = Field(min_length=1, max_length=128)
    tenant_id: str = Field(min_length=1, max_length=128)
    conversation_id: str = Field(min_length=1, max_length=512)
    sender_id: str = Field(min_length=1, max_length=128)
    agent_id: str = Field(min_length=1, max_length=128)
    agent_version_id: str = Field(min_length=1, max_length=128)
    agent_spec_checksum: str = Field(pattern=r"^sha256:[0-9a-f]{64}$")
    instructions: str = Field(min_length=1, max_length=4000)
    model_route: str = Field(min_length=1, max_length=128)
    allowed_action_types: list[str] = Field(max_length=1)
    content: str = Field(min_length=1, max_length=16_000)
    evidence: list["Evidence"] = Field(max_length=8)
    memory: list["MemoryContextFact"] = Field(default_factory=list, max_length=8)
    tool_results: list["ToolResultContext"] = Field(default_factory=list, max_length=1)
    skills: list["SkillContext"] = Field(default_factory=list, max_length=32)


class Evidence(BaseModel):
    model_config = ConfigDict(extra="forbid")

    citation_id: str = Field(pattern=r"^C[1-8]$")
    document_id: str
    version_id: str
    chunk_id: str
    title: str = Field(max_length=500)
    source_uri: str = Field(max_length=2000)
    checksum: str = Field(min_length=1, max_length=256)
    content: str = Field(min_length=1, max_length=16_000)


class MemoryContextFact(BaseModel):
    model_config = ConfigDict(extra="forbid")

    memory_id: str = Field(min_length=1, max_length=128)
    category: Literal["preference", "profile", "procedure", "context"]
    checksum: str = Field(pattern=r"^sha256:[0-9a-f]{64}$")
    content: str = Field(min_length=1, max_length=4000)


class ToolResultContext(BaseModel):
    model_config = ConfigDict(extra="forbid")

    operation_id: str = Field(pattern=r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
    result: dict


class SkillContext(BaseModel):
    model_config = ConfigDict(extra="forbid")

    skill_id: str = Field(pattern=r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
    version: str = Field(pattern=r"^[1-9][0-9]{0,8}$")
    name: str = Field(min_length=1, max_length=120)
    instructions: str = Field(min_length=1, max_length=20_000)
    content_digest: str = Field(pattern=r"^sha256:[0-9a-f]{64}$")


class CandidateResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    text: str = Field(min_length=1, max_length=16_000)
    model: str
    provider_response_id: str
    citation_ids: list[str] = Field(max_length=8)
    grounding_status: Literal["grounded", "insufficient_evidence", "not_applicable"]
    action_intent: "ActionIntent | None" = None


class ActionIntent(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: str = Field(pattern=r"^create_ticket$")
    title: str = Field(min_length=1, max_length=200)


class OperationDiscovery(BaseModel):
    model_config = ConfigDict(extra="forbid")

    operation_id: str = Field(pattern=r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
    name: str = Field(min_length=1, max_length=120)
    summary: str = Field(min_length=1, max_length=512)
    parameter_terms: list[str] = Field(min_length=1, max_length=32)
    examples: list[str] = Field(min_length=1, max_length=8)
    output_kinds: list[Literal["text", "image", "file", "data", "mixed"]] = Field(min_length=1, max_length=5)


class RouteRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str = Field(min_length=1, max_length=128)
    capability_snapshot_id: str = Field(pattern=r"^capability-v1:[0-9a-f]{64}$")
    content: str = Field(min_length=1, max_length=16_000)
    operations: list[OperationDiscovery] = Field(min_length=1, max_length=64)


class IntentView(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: Literal["1"]
    rewritten_intent: str = Field(min_length=1, max_length=1000)
    hypothetical_capability: str = Field(min_length=1, max_length=1000)
    required_inputs: list[str] = Field(max_length=8)
    missing_required_inputs: list[str] = Field(max_length=8)
    unresolved_references: list[str] = Field(max_length=8)
    desired_outputs: list[Literal["text", "image", "file", "data", "mixed"]] = Field(max_length=5)
    tool_requirement: Literal["required", "optional", "none"]


class RouteCandidate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    operation_id: str
    original_lexical_rank: int | None = None
    original_dense_rank: int | None = None
    intent_view_rank: int | None = None
    reason_codes: list[str] = Field(max_length=8)


class RouteResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    status: Literal["selected", "clarify", "no_tool"]
    operation_id: str | None = None
    clarification: str | None = None
    provider_response_id: str
    router_version: Literal["openim-intent-routing-v1"]
    candidates: list[RouteCandidate] = Field(max_length=3)


class MemoryExtractionRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str = Field(min_length=1, max_length=128)
    user_message: str = Field(min_length=1, max_length=16_000)
    assistant_response: str = Field(min_length=1, max_length=16_000)


class ExtractedMemoryFact(BaseModel):
    model_config = ConfigDict(extra="forbid")

    category: Literal["preference", "profile", "procedure", "context"]
    subject: str = Field(min_length=1, max_length=120)
    content: str = Field(min_length=1, max_length=4000)
    confidence: float = Field(ge=0.8, le=1)


class MemoryExtractionResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    provider_response_id: str = Field(min_length=1, max_length=256)
    facts: list[ExtractedMemoryFact] = Field(max_length=8)


class ProactiveRankRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    event_id: str = Field(min_length=1, max_length=128)
    subscription_query: str = Field(min_length=1, max_length=500)
    title: str = Field(min_length=1, max_length=1000)
    summary: str = Field(min_length=1, max_length=20_000)
    memory: list[MemoryContextFact] = Field(default_factory=list, max_length=8)
    minimum_score: float = Field(ge=0, le=1)


class ProactiveRankResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    score: float = Field(ge=0, le=1)
    should_notify: bool
    reason_codes: list[Literal["query_similarity", "memory_interest"]] = Field(min_length=1, max_length=2)
    ranker_version: Literal["openim-proactive-ranker-v1"]
    response_id: str = Field(pattern=r"^rank:[0-9a-f]{64}$")


class ToolPlanTarget(BaseModel):
    model_config = ConfigDict(extra="forbid")

    operation_id: str = Field(pattern=r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
    name: str = Field(min_length=1, max_length=120)
    summary: str = Field(min_length=1, max_length=512)
    input_schema: dict
    skill_instructions: list[str] = Field(default_factory=list, max_length=32)


class ToolPlanRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str = Field(min_length=1, max_length=128)
    content: str = Field(min_length=1, max_length=16_000)
    operation: ToolPlanTarget


class ToolPlanResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    arguments: dict
    provider_response_id: str = Field(min_length=1, max_length=256)


class EmbeddingRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    texts: list[str] = Field(min_length=1, max_length=128)


class EmbeddingResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    model: str = Field(min_length=1, max_length=256)
    dimension: int = Field(ge=8, le=8192)
    vectors: list[list[float]] = Field(min_length=1, max_length=128)


class RerankCandidate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidate_id: str = Field(min_length=1, max_length=128)
    content: str = Field(min_length=1, max_length=8000)


class RerankRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    query: str = Field(min_length=1, max_length=2000)
    candidates: list[RerankCandidate] = Field(min_length=1, max_length=32)

    @model_validator(mode="after")
    def unique_candidate_ids(self) -> "RerankRequest":
        ids = [candidate.candidate_id for candidate in self.candidates]
        if len(ids) != len(set(ids)):
            raise ValueError("candidate_id values must be unique")
        return self


class RerankScore(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidate_id: str = Field(min_length=1, max_length=128)
    score: float


class RerankResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    model: str
    revision: str
    scores: list[RerankScore] = Field(min_length=1, max_length=32)
