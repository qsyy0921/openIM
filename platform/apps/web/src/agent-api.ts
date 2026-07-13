import { PlatformAPIError } from "./platform-api";

export type AgentCitation = {
  citation_id: string;
  title: string;
  source_uri: string;
  checksum: string;
};

export type AgentIntent = {
  intent_id: string;
  action_type: "create_ticket";
  title: string;
  payload_digest: string;
  state: "pending_approval" | "approved" | "executing" | "succeeded" | "failed" | "expired";
  expires_at: string;
  execution_id?: string;
  execution_state?: "queued" | "running" | "unknown" | "succeeded" | "failed";
  ticket_id?: string;
};

export type AgentRun = {
  run_id: string;
  agent_id: string;
  agent_display_name: string;
  agent_version_number: number;
  agent_spec_checksum: string;
  conversation_id: string;
  prompt: string;
  state: "queued" | "running" | "reply_pending" | "waiting_approval" | "succeeded" | "failed";
  answer?: string;
  model?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
  citations: AgentCitation[];
  intent?: AgentIntent;
};

export type AgentSummary = {
  agent_id: string;
  slug: string;
  display_name: string;
  description: string;
  trigger_alias: string;
  production_version_number: number;
  production_version_id: string;
  spec_checksum: string;
  bot_user_id: string;
};

export type AgentWorkspaceSnapshot = {
  agent_user_id: string;
  runs: AgentRun[];
};

export type ApprovalResult = {
  intent_id: string;
  execution_id: string;
  state: "queued" | "running" | "unknown" | "succeeded" | "failed";
};

type ErrorPayload = { code?: unknown; message?: unknown; correlation_id?: unknown };

async function responsePayload(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    if (response.ok) throw new Error("Agent workspace response is not valid JSON");
    return null;
  }
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

async function checkedPayload(response: Response): Promise<Record<string, unknown>> {
  const payload = await responsePayload(response);
  const body = record(payload);
  if (!response.ok) {
    const error = (body ?? {}) as ErrorPayload;
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : "Agent workspace request failed",
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  if (!body) throw new Error("Agent workspace response is malformed");
  return body;
}

function validWorkspace(body: Record<string, unknown>): body is AgentWorkspaceSnapshot & Record<string, unknown> {
  return typeof body.agent_user_id === "string" && body.agent_user_id.length > 0 && Array.isArray(body.runs);
}

function validAgent(value: unknown): value is AgentSummary {
  const body = record(value);
  return body !== null && typeof body.agent_id === "string" && typeof body.slug === "string" &&
    typeof body.display_name === "string" && typeof body.description === "string" &&
    typeof body.trigger_alias === "string" && /^@[a-z][a-z0-9_-]{0,62}$/.test(body.trigger_alias) &&
    typeof body.production_version_number === "number" && body.production_version_number >= 1 &&
    typeof body.production_version_id === "string" && typeof body.spec_checksum === "string" &&
    /^sha256:[0-9a-f]{64}$/.test(body.spec_checksum) && typeof body.bot_user_id === "string";
}

export async function getAgentCatalog(
  baseURL: string,
  idToken: string,
  deviceID: string,
  request: typeof fetch = fetch
): Promise<AgentSummary[]> {
  const query = new URLSearchParams({ platform_id: "5", device_id: deviceID });
  const response = await request(`${baseURL}/v1/agents?${query}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  });
  const payload = await responsePayload(response);
  if (!response.ok) {
    const body = record(payload);
    const error = (body ?? {}) as ErrorPayload;
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : "Agent catalog request failed",
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  if (!Array.isArray(payload) || !payload.every(validAgent)) throw new Error("Agent catalog response is malformed");
  return payload;
}

export async function getAgentWorkspace(
  baseURL: string,
  idToken: string,
  deviceID: string,
  request: typeof fetch = fetch
): Promise<AgentWorkspaceSnapshot> {
  const query = new URLSearchParams({ platform_id: "5", device_id: deviceID });
  const response = await request(`${baseURL}/v1/agent/workspace?${query}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  });
  const body = await checkedPayload(response);
  if (!validWorkspace(body)) throw new Error("Agent workspace response is malformed");
  return body;
}

export async function approveAgentIntent(
  baseURL: string,
  idToken: string,
  deviceID: string,
  intentID: string,
  payloadDigest: string,
  request: typeof fetch = fetch
): Promise<ApprovalResult> {
  const response = await request(`${baseURL}/v1/agent/intents/${encodeURIComponent(intentID)}/approve`, {
    method: "POST",
    headers: { Authorization: `Bearer ${idToken}`, "Content-Type": "application/json" },
    body: JSON.stringify({ platform_id: 5, device_id: deviceID, payload_digest: payloadDigest })
  });
  const body = await checkedPayload(response);
  if (typeof body.intent_id !== "string" || typeof body.execution_id !== "string" || typeof body.state !== "string") {
    throw new Error("Agent approval response is malformed");
  }
  return body as ApprovalResult & Record<string, unknown>;
}
