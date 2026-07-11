from pydantic import BaseModel, ConfigDict, Field


class CandidateRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str = Field(min_length=1, max_length=128)
    tenant_id: str = Field(min_length=1, max_length=128)
    conversation_id: str = Field(min_length=1, max_length=512)
    sender_id: str = Field(min_length=1, max_length=128)
    content: str = Field(min_length=1, max_length=16_000)
    evidence: list["Evidence"] = Field(max_length=8)


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


class CandidateResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    text: str = Field(min_length=1, max_length=16_000)
    model: str
    provider_response_id: str
    citation_ids: list[str] = Field(max_length=8)
    action_intent: "ActionIntent | None" = None


class ActionIntent(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: str = Field(pattern=r"^create_ticket$")
    title: str = Field(min_length=1, max_length=200)
