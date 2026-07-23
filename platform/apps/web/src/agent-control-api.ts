import { PlatformAPIError } from "./platform-api";

export type MemoryFact = {
  fact_id: string;
  category: "preference" | "profile" | "procedure" | "context";
  content: string;
  checksum: string;
  updated_at: string;
};

export type MemoryExposure = {
  exposure_id: string;
  run_id: string;
  fact_id: string;
  category: string;
  content: string;
  retrieval_reason: string;
  feedback?: "helpful" | "not_helpful" | "incorrect";
  created_at: string;
};

export type MemorySnapshot = { facts: MemoryFact[]; exposures: MemoryExposure[] };

export type GroupMemoryProposal = {
  proposal_id: string;
  source_run_id: string;
  proposed_by_member_id: string;
  category: MemoryFact["category"];
  content: string;
  checksum: string;
  state: "pending" | "approved" | "rejected";
  reviewed_by_member_id?: string;
  confidence: number;
  created_at: string;
  reviewed_at?: string;
};

export type GroupMemorySnapshot = {
  source_channel: "openim";
  conversation_id: string;
  facts: MemoryFact[];
  proposals: GroupMemoryProposal[];
};

export type ProactivePreference = {
  enabled: boolean;
  timezone: string;
  quiet_start: string;
  quiet_end: string;
  daily_budget: number;
  minimum_score: number;
};

export type ProactiveSubscription = {
  subscription_id: string;
  agent_id: string;
  query: string;
  categories: string[];
  source_channel: "openim" | "telegram";
  enabled: boolean;
  poll_interval_seconds: number;
  baseline_complete: boolean;
  last_success_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
};

export type ProactiveEvent = {
  event_id: string;
  subscription_id: string;
  title: string;
  summary: string;
  url: string;
  state: string;
  score?: number;
  rank_reasons: string[];
  suppression_reason?: string;
  run_id?: string;
  feedback?: "interesting" | "not_interesting" | "dismissed";
  published_at: string;
  created_at: string;
  updated_at: string;
};

export type ProactiveSnapshot = {
  preference: ProactivePreference;
  subscriptions: ProactiveSubscription[];
  events: ProactiveEvent[];
};

export type ToolApproval = {
  approval_id: string;
  run_id: string;
  operation_id: string;
  arguments_digest: string;
  risk: "write" | "external_side_effect" | "privileged";
  policy_reason: string;
  expires_at: string;
};

export type AgentReplayBundle = Record<string, unknown> & {
  schema_version: number;
  checksum: string;
};

export function serializeAgentReplay(bundle: AgentReplayBundle): string {
  return `${JSON.stringify(bundle, null, 2)}\n`;
}

export type AgentDelegation = {
  delegation_id: string;
  parent_run_id: string;
  child_run_id: string;
  target_agent_id: string;
  target_agent_slug: string;
  task: string;
  state: "queued" | "running" | "completed" | "failed" | "unknown";
  last_error?: string;
  created_at: string;
  updated_at: string;
};

export type RuntimeQueueSnapshot = {
  states: Record<string, number>;
  oldest_ready_seconds: number;
};

export type RuntimeControl = {
  component: "agent_execution" | "agent_delivery" | "proactive_dispatch";
  paused: boolean;
  reason: string;
  revision: number;
  updated_by_member_id?: string;
  updated_at?: string;
};

export type AgentAdminSnapshot = {
  roles: Array<"platform_admin" | "agent_admin" | "knowledge_admin">;
  controls: RuntimeControl[];
  operations: {
    generated_at: string;
    agent_runs: RuntimeQueueSnapshot;
    deliveries: RuntimeQueueSnapshot;
    proactive_events: RuntimeQueueSnapshot;
    memory_extractions: RuntimeQueueSnapshot;
    delegations: RuntimeQueueSnapshot;
    tool_approvals: RuntimeQueueSnapshot;
    mcp_health: RuntimeQueueSnapshot;
  };
};

export type AgentAdminRole = "platform_admin" | "agent_admin" | "knowledge_admin";

export type AgentCatalogTool = {
  id: string;
  operation_id: string;
  version: string;
  name: string;
  summary: string;
  source_type?: string;
  source_id: string;
  source_operation: string;
  risk: "read" | "write" | "external_side_effect" | "privileged";
  permissions: string[];
  idempotency: "native" | "keyed" | "none" | "unknown";
  retry_semantics: "safe" | "reconcile_first" | "never";
  timeout_ms: number;
  audience: "passive" | "proactive_source" | "internal" | "admin";
  parameter_terms: string[];
  examples: string[];
  output_kinds: string[];
  input_schema: Record<string, unknown>;
  schema_digest?: string;
};

export type AgentAdminCatalog = {
  roles: AgentAdminRole[];
  agents: Array<{ id: string; slug: string; display_name: string; description: string; status: string; active_version_id: string; capability_snapshot_id: string; version_number: number; deployment_revision: number }>;
  skills: Array<{ id: string; skill_id: string; version: string; name: string; summary: string; tool_operations: string[]; audience: string; content_digest: string }>;
  tools: AgentCatalogTool[];
  mcp_servers: Array<{ id: string; slug: string; enabled: boolean; state: string; last_error_code: string; catalog_digest: string; restart_count: number; checked_at: string }>;
  members: Array<{ id: string; display_name: string; status: string; roles: AgentAdminRole[] }>;
  capability_snapshots: Array<{ id: string; tool_count: number; created_at: string }>;
  remote_a2a_enabled: boolean;
  remote_agents: RemoteA2AAgent[];
};

export type RemoteA2AAgent = {
  id: string;
  slug: string;
  display_name: string;
  card_url: string;
  endpoint_url?: string;
  expected_card_digest: string;
  observed_card_digest?: string;
  auth_env_key?: string;
  enabled: boolean;
  lifecycle_state: "registered" | "verified" | "degraded" | "disabled";
  lifecycle_revision: number;
  last_error_code?: string;
  last_verified_at?: string;
  updated_at: string;
};

export type RegisterRemoteA2AInput = {
  slug: string;
  display_name: string;
  card_url: string;
  expected_card_digest: string;
  auth_env_key: string;
};

export type PublishSkillInput = {
  skill_id: string;
  version: string;
  name: string;
  summary: string;
  instructions: string;
  tool_operations: string[];
  audience: AgentCatalogTool["audience"];
};

export type CreateAgentInput = {
  slug: string;
  display_name: string;
  description: string;
  mention_alias: string;
  capability_snapshot_id: string;
  skill_ids: string[];
  spec: Record<string, unknown>;
};

export type CreateProactiveSubscription = {
  agent_id: string;
  query: string;
  categories: string[];
  source_channel: "openim" | "telegram";
  poll_interval_seconds: number;
};

type ErrorPayload = { code?: unknown; message?: unknown; correlation_id?: unknown };

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

async function payload(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try { return JSON.parse(text) as unknown; } catch { return null; }
}

async function checked(response: Response): Promise<Record<string, unknown>> {
  const value = await payload(response);
  const body = record(value);
  if (!response.ok) {
    const error = (body ?? {}) as ErrorPayload;
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : "Agent control request failed",
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  if (!body) throw new Error("Agent control response is malformed");
  return body;
}

function query(deviceID: string): string {
  return new URLSearchParams({ platform_id: "5", device_id: deviceID }).toString();
}

function mutationHeaders(idToken: string, idempotencyKey?: string): HeadersInit {
  return {
    Authorization: `Bearer ${idToken}`,
    "Content-Type": "application/json",
    ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {})
  };
}

function deviceBody(deviceID: string): { platform_id: number; device_id: string } {
  return { platform_id: 5, device_id: deviceID };
}

function validMemorySnapshot(body: Record<string, unknown>): body is MemorySnapshot & Record<string, unknown> {
  return Array.isArray(body.facts) && Array.isArray(body.exposures);
}

function validProactiveSnapshot(body: Record<string, unknown>): body is ProactiveSnapshot & Record<string, unknown> {
  const preference = record(body.preference);
  return preference !== null && typeof preference.enabled === "boolean" &&
    typeof preference.timezone === "string" && Array.isArray(body.subscriptions) && Array.isArray(body.events);
}

function validToolApproval(value: unknown): value is ToolApproval {
  const item = record(value);
  if (!item) return false;
  return typeof item.approval_id === "string" && typeof item.run_id === "string" &&
    typeof item.operation_id === "string" && typeof item.arguments_digest === "string" &&
    (item.risk === "write" || item.risk === "external_side_effect" || item.risk === "privileged") &&
    typeof item.policy_reason === "string" && typeof item.expires_at === "string";
}

function validDelegation(value: unknown): value is AgentDelegation {
  const item = record(value);
  if (!item) return false;
  return typeof item.delegation_id === "string" && typeof item.parent_run_id === "string" &&
    typeof item.child_run_id === "string" && typeof item.target_agent_id === "string" &&
    typeof item.target_agent_slug === "string" && typeof item.task === "string" &&
    (item.state === "queued" || item.state === "running" || item.state === "completed" || item.state === "failed" || item.state === "unknown") &&
    typeof item.created_at === "string" && typeof item.updated_at === "string" &&
    (item.last_error === undefined || typeof item.last_error === "string");
}

function validRuntimeQueue(value: unknown): value is RuntimeQueueSnapshot {
  const item = record(value);
  const states = record(item?.states);
  return item !== null && states !== null && Object.values(states).every((count) => typeof count === "number" && Number.isInteger(count) && count >= 0) &&
    typeof item.oldest_ready_seconds === "number" && Number.isFinite(item.oldest_ready_seconds) && item.oldest_ready_seconds >= 0;
}

function validRuntimeControl(value: unknown): value is RuntimeControl {
  const item = record(value);
  return item !== null && (item.component === "agent_execution" || item.component === "agent_delivery" || item.component === "proactive_dispatch") &&
    typeof item.paused === "boolean" && typeof item.reason === "string" && typeof item.revision === "number" &&
    Number.isInteger(item.revision) && item.revision >= 0;
}

function validAdminSnapshot(body: Record<string, unknown>): body is AgentAdminSnapshot & Record<string, unknown> {
  const operations = record(body.operations);
  const roles = body.roles;
  return Array.isArray(roles) && roles.every((role) => role === "platform_admin" || role === "agent_admin" || role === "knowledge_admin") &&
    Array.isArray(body.controls) && body.controls.length === 3 && body.controls.every(validRuntimeControl) && operations !== null &&
    typeof operations.generated_at === "string" && validRuntimeQueue(operations.agent_runs) && validRuntimeQueue(operations.deliveries) &&
    validRuntimeQueue(operations.proactive_events) && validRuntimeQueue(operations.memory_extractions) &&
    validRuntimeQueue(operations.delegations) && validRuntimeQueue(operations.tool_approvals) && validRuntimeQueue(operations.mcp_health);
}

function validAdminCatalog(body: Record<string, unknown>): body is AgentAdminCatalog & Record<string, unknown> {
  return Array.isArray(body.roles) && Array.isArray(body.agents) && Array.isArray(body.skills) &&
    Array.isArray(body.tools) && Array.isArray(body.mcp_servers) && Array.isArray(body.members) &&
    Array.isArray(body.capability_snapshots) && typeof body.remote_a2a_enabled === "boolean" && Array.isArray(body.remote_agents);
}

export async function getAgentMemory(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<MemorySnapshot> {
  const body = await checked(await request(`${baseURL}/v1/agent/memory?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!validMemorySnapshot(body)) throw new Error("Agent memory response is malformed");
  return body;
}

export async function getAgentGroupMemory(baseURL: string, idToken: string, deviceID: string, conversationID: string, request: typeof fetch = fetch): Promise<GroupMemorySnapshot> {
  const params = new URLSearchParams({ platform_id: "5", device_id: deviceID, source_channel: "openim", conversation_id: conversationID });
  const body = await checked(await request(`${baseURL}/v1/agent/group-memory?${params.toString()}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (body.source_channel !== "openim" || body.conversation_id !== conversationID || !Array.isArray(body.facts) || !Array.isArray(body.proposals)) {
    throw new Error("Agent group memory response is malformed");
  }
  return body as GroupMemorySnapshot & Record<string, unknown>;
}

export async function reviewAgentGroupMemory(baseURL: string, idToken: string, deviceID: string, conversationID: string, proposalID: string, decision: "approve" | "reject", request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/group-memory/proposals/${encodeURIComponent(proposalID)}/decision`, {
    method: "POST", headers: mutationHeaders(idToken),
    body: JSON.stringify({ ...deviceBody(deviceID), source_channel: "openim", conversation_id: conversationID, decision })
  }));
}

export async function deleteAgentMemoryFact(baseURL: string, idToken: string, deviceID: string, factID: string, idempotencyKey: string, request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/memory/facts/${encodeURIComponent(factID)}`, {
    method: "DELETE", headers: mutationHeaders(idToken, idempotencyKey), body: JSON.stringify(deviceBody(deviceID))
  }));
}

export async function feedbackAgentMemory(baseURL: string, idToken: string, deviceID: string, exposureID: string, signal: "helpful" | "not_helpful" | "incorrect", request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/memory/exposures/${encodeURIComponent(exposureID)}/feedback`, {
    method: "POST", headers: mutationHeaders(idToken), body: JSON.stringify({ ...deviceBody(deviceID), signal })
  }));
}

export async function getAgentProactive(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<ProactiveSnapshot> {
  const body = await checked(await request(`${baseURL}/v1/agent/proactive?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!validProactiveSnapshot(body)) throw new Error("Agent proactive response is malformed");
  return body;
}

export async function createAgentSubscription(baseURL: string, idToken: string, deviceID: string, input: CreateProactiveSubscription, request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/proactive/subscriptions`, {
    method: "POST", headers: mutationHeaders(idToken), body: JSON.stringify({ ...deviceBody(deviceID), ...input })
  }));
}

export async function setAgentSubscriptionEnabled(baseURL: string, idToken: string, deviceID: string, subscriptionID: string, enabled: boolean, request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/proactive/subscriptions/${encodeURIComponent(subscriptionID)}`, {
    method: "PATCH", headers: mutationHeaders(idToken), body: JSON.stringify({ ...deviceBody(deviceID), enabled })
  }));
}

export async function updateAgentProactivePreference(baseURL: string, idToken: string, deviceID: string, preference: ProactivePreference, request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/proactive/preferences`, {
    method: "PUT", headers: mutationHeaders(idToken), body: JSON.stringify({ ...deviceBody(deviceID), ...preference })
  }));
}

export async function acknowledgeAgentEvent(baseURL: string, idToken: string, deviceID: string, eventID: string, signal: "interesting" | "not_interesting" | "dismissed", request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/proactive/events/${encodeURIComponent(eventID)}/acknowledge`, {
    method: "POST", headers: mutationHeaders(idToken), body: JSON.stringify({ ...deviceBody(deviceID), signal })
  }));
}

export async function getAgentToolApprovals(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<ToolApproval[]> {
  const body = await checked(await request(`${baseURL}/v1/agent/tool-approvals?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!Array.isArray(body.approvals) || !body.approvals.every(validToolApproval)) {
    throw new Error("Agent tool approval response is malformed");
  }
  return body.approvals;
}

export async function decideAgentToolApproval(baseURL: string, idToken: string, deviceID: string, approvalID: string, argumentsDigest: string, decision: "approve" | "reject", request: typeof fetch = fetch): Promise<void> {
  await checked(await request(`${baseURL}/v1/agent/tool-approvals/${encodeURIComponent(approvalID)}/decision`, {
    method: "POST",
    headers: mutationHeaders(idToken),
    body: JSON.stringify({ ...deviceBody(deviceID), arguments_digest: argumentsDigest, decision })
  }));
}

export async function getAgentReplay(baseURL: string, idToken: string, deviceID: string, runID: string, request: typeof fetch = fetch): Promise<AgentReplayBundle> {
  const body = await checked(await request(`${baseURL}/v1/agent/runs/${encodeURIComponent(runID)}/replay?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (body.schema_version !== 1 || typeof body.checksum !== "string") throw new Error("Agent replay response is malformed");
  return body as AgentReplayBundle;
}

export async function getAgentDelegations(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<AgentDelegation[]> {
  const body = await checked(await request(`${baseURL}/v1/agent/delegations?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!Array.isArray(body.delegations) || !body.delegations.every(validDelegation)) {
    throw new Error("Agent delegation response is malformed");
  }
  return body.delegations;
}

export async function getAgentAdminSnapshot(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<AgentAdminSnapshot> {
  const body = await checked(await request(`${baseURL}/v1/admin/agent/operations?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!validAdminSnapshot(body)) throw new Error("Agent administrator response is malformed");
  return body;
}

export async function setAgentRuntimeControl(baseURL: string, idToken: string, deviceID: string, control: RuntimeControl, paused: boolean, reason: string, request: typeof fetch = fetch): Promise<RuntimeControl> {
  const body = await checked(await request(`${baseURL}/v1/admin/agent/runtime-controls/${control.component}`, {
    method: "PUT",
    headers: mutationHeaders(idToken),
    body: JSON.stringify({ ...deviceBody(deviceID), paused, reason, expected_revision: control.revision })
  }));
  if (!validRuntimeControl(body)) throw new Error("Agent runtime control response is malformed");
  return body;
}

export async function getAgentAdminCatalog(baseURL: string, idToken: string, deviceID: string, request: typeof fetch = fetch): Promise<AgentAdminCatalog> {
  const body = await checked(await request(`${baseURL}/v1/admin/agent/catalog?${query(deviceID)}`, {
    headers: { Authorization: `Bearer ${idToken}` }
  }));
  if (!validAdminCatalog(body)) throw new Error("Agent administrator catalog response is malformed");
  return body;
}

async function catalogMutation(baseURL: string, idToken: string, deviceID: string, path: string, method: "POST" | "PUT", input: Record<string, unknown>, request: typeof fetch): Promise<void> {
  await checked(await request(`${baseURL}${path}`, {
    method,
    headers: mutationHeaders(idToken),
    body: JSON.stringify({ ...deviceBody(deviceID), ...input })
  }));
}

export async function createCatalogAgent(baseURL: string, idToken: string, deviceID: string, input: CreateAgentInput, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, "/v1/admin/agent/catalog/agents", "POST", input, request);
}

export async function publishCatalogSkill(baseURL: string, idToken: string, deviceID: string, input: PublishSkillInput, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, "/v1/admin/agent/catalog/skills", "POST", input, request);
}

export async function publishCatalogTool(baseURL: string, idToken: string, deviceID: string, input: Omit<AgentCatalogTool, "id" | "schema_digest" | "source_type">, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, "/v1/admin/agent/catalog/tools", "POST", input, request);
}

export async function publishCapabilitySnapshot(baseURL: string, idToken: string, deviceID: string, tools: Array<{ operation_id: string; version: string }>, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, "/v1/admin/agent/catalog/capability-snapshots", "POST", { tools }, request);
}

export async function setCatalogMCPEnabled(baseURL: string, idToken: string, deviceID: string, slug: string, enabled: boolean, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, `/v1/admin/agent/catalog/mcp/${encodeURIComponent(slug)}`, "PUT", { enabled }, request);
}

export async function setCatalogMemberRole(baseURL: string, idToken: string, deviceID: string, memberID: string, role: AgentAdminRole, enabled: boolean, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, `/v1/admin/agent/catalog/members/${encodeURIComponent(memberID)}/roles/${role}`, "PUT", { enabled }, request);
}

export async function registerRemoteA2AAgent(baseURL: string, idToken: string, deviceID: string, input: RegisterRemoteA2AInput, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, "/v1/admin/agent/catalog/remote-agents", "POST", input, request);
}

export async function verifyRemoteA2AAgent(baseURL: string, idToken: string, deviceID: string, slug: string, expectedRevision: number, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, `/v1/admin/agent/catalog/remote-agents/${encodeURIComponent(slug)}/verify`, "POST", { expected_revision: expectedRevision }, request);
}

export async function setRemoteA2AAgentEnabled(baseURL: string, idToken: string, deviceID: string, slug: string, enabled: boolean, expectedRevision: number, request: typeof fetch = fetch): Promise<void> {
  await catalogMutation(baseURL, idToken, deviceID, `/v1/admin/agent/catalog/remote-agents/${encodeURIComponent(slug)}`, "PUT", { enabled, expected_revision: expectedRevision }, request);
}
