import { PlatformAPIError } from "./platform-api";

export type KnowledgeClassification = "public" | "internal" | "confidential" | "restricted";
export type KnowledgeDocument = {
  id: string;
  title: string;
  classification: KnowledgeClassification;
  status: "active" | "deleted";
  current_version_id?: string;
  current_version?: number;
  current_ingestion_state?: string;
  latest_version_id?: string;
  latest_version?: number;
  latest_ingestion_state?: string;
  latest_failure_code?: string;
  latest_failure_detail?: string;
  grant_count: number;
  created_at: string;
};
export type KnowledgeMember = {
  id: string;
  display_name: string;
  status: "active" | "disabled";
  granted: boolean;
};
export type KnowledgeVersion = {
  id: string;
  version_number: number;
  checksum: string;
  status: "draft" | "published" | "superseded";
  ingestion_state: "legacy_indexed" | "uploading" | "queued" | "processing" | "indexed" | "failed";
  failure_code?: string;
  failure_detail?: string;
  source_format?: "markdown" | "text" | "pdf" | "docx";
  original_filename?: string;
  size_bytes?: number;
  job_state?: "queued" | "leased" | "retryable" | "succeeded" | "failed";
  attempts?: number;
  created_at: string;
  published_at?: string;
};
export type KnowledgeAdminSnapshot = {
  roles: string[];
  documents: KnowledgeDocument[];
  members: KnowledgeMember[];
};
export type KnowledgeUploadResult = {
  document_id: string;
  version_id: string;
  version_number: number;
  ingestion_state: string;
  idempotent: boolean;
};
export type KnowledgeUploadInput = {
  documentID?: string;
  title: string;
  classification: KnowledgeClassification;
  file: File;
  idempotencyKey: string;
};

type ErrorPayload = { code?: unknown; message?: unknown; correlation_id?: unknown };

function query(deviceID: string, documentID?: string): string {
  const values = new URLSearchParams({ platform_id: "5", device_id: deviceID });
  if (documentID) values.set("document_id", documentID);
  return values.toString();
}

async function body(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

async function checked(response: Response): Promise<unknown> {
  const value = await body(response);
  if (!response.ok) {
    const error = typeof value === "object" && value !== null ? value as ErrorPayload : {};
    throw new PlatformAPIError(
      typeof error.message === "string" ? error.message : "Knowledge request failed",
      typeof error.code === "string" ? error.code : "UNKNOWN_ERROR",
      typeof error.correlation_id === "string" ? error.correlation_id : "unavailable",
      response.status
    );
  }
  return value;
}

function authorization(idToken: string): HeadersInit {
  return { Authorization: `Bearer ${idToken}` };
}

function isDocument(value: unknown): value is KnowledgeDocument {
  const item = value as Partial<KnowledgeDocument> | null;
  return typeof item === "object" && item !== null && typeof item.id === "string" &&
    typeof item.title === "string" && typeof item.classification === "string" &&
    typeof item.status === "string" && typeof item.grant_count === "number" &&
    typeof item.created_at === "string";
}

function isMember(value: unknown): value is KnowledgeMember {
  const item = value as Partial<KnowledgeMember> | null;
  return typeof item === "object" && item !== null && typeof item.id === "string" &&
    typeof item.display_name === "string" && typeof item.status === "string" &&
    typeof item.granted === "boolean";
}

function isVersion(value: unknown): value is KnowledgeVersion {
  const item = value as Partial<KnowledgeVersion> | null;
  return typeof item === "object" && item !== null && typeof item.id === "string" &&
    typeof item.version_number === "number" && typeof item.checksum === "string" &&
    typeof item.status === "string" && typeof item.ingestion_state === "string" &&
    typeof item.created_at === "string";
}

export async function getKnowledgeSnapshot(
  baseURL: string,
  idToken: string,
  deviceID: string,
  documentID?: string,
  request: typeof fetch = fetch
): Promise<KnowledgeAdminSnapshot> {
  const value = await checked(await request(
    `${baseURL}/v1/knowledge/documents?${query(deviceID, documentID)}`,
    { headers: authorization(idToken) }
  ));
  const snapshot = value as Partial<KnowledgeAdminSnapshot> | null;
  if (typeof snapshot !== "object" || snapshot === null ||
    !Array.isArray(snapshot.roles) || !snapshot.roles.every((role) => typeof role === "string") ||
    !Array.isArray(snapshot.documents) || !snapshot.documents.every(isDocument) ||
    !Array.isArray(snapshot.members) || !snapshot.members.every(isMember)) {
    throw new Error("Knowledge snapshot response is malformed");
  }
  return snapshot as KnowledgeAdminSnapshot;
}

export async function listKnowledgeVersions(
  baseURL: string,
  idToken: string,
  deviceID: string,
  documentID: string,
  request: typeof fetch = fetch
): Promise<KnowledgeVersion[]> {
  const value = await checked(await request(
    `${baseURL}/v1/knowledge/documents/${encodeURIComponent(documentID)}/versions?${query(deviceID)}`,
    { headers: authorization(idToken) }
  ));
  if (!Array.isArray(value) || !value.every(isVersion)) {
    throw new Error("Knowledge versions response is malformed");
  }
  return value;
}

export async function uploadKnowledge(
  baseURL: string,
  idToken: string,
  deviceID: string,
  input: KnowledgeUploadInput,
  request: typeof fetch = fetch
): Promise<KnowledgeUploadResult> {
  const form = new FormData();
  form.set("title", input.title.trim());
  form.set("classification", input.classification);
  form.set("file", input.file, input.file.name);
  const suffix = input.documentID
    ? `/${encodeURIComponent(input.documentID)}/versions`
    : "";
  const value = await checked(await request(
    `${baseURL}/v1/knowledge/documents${suffix}?${query(deviceID)}`,
    {
      method: "POST",
      headers: {
        ...authorization(idToken),
        "Idempotency-Key": input.idempotencyKey
      },
      body: form
    }
  ));
  const result = value as Partial<KnowledgeUploadResult> | null;
  if (typeof result !== "object" || result === null ||
    typeof result.document_id !== "string" || typeof result.version_id !== "string" ||
    typeof result.version_number !== "number" || typeof result.ingestion_state !== "string" ||
    typeof result.idempotent !== "boolean") {
    throw new Error("Knowledge upload response is malformed");
  }
  return result as KnowledgeUploadResult;
}

async function mutate(
  baseURL: string,
  idToken: string,
  deviceID: string,
  path: string,
  method: "POST" | "PUT" | "DELETE",
  request: typeof fetch
): Promise<void> {
  await checked(await request(`${baseURL}${path}?${query(deviceID)}`, {
    method,
    headers: authorization(idToken)
  }));
}

export function publishKnowledgeVersion(baseURL: string, idToken: string, deviceID: string, documentID: string, versionID: string, request: typeof fetch = fetch): Promise<void> {
  return mutate(baseURL, idToken, deviceID,
    `/v1/knowledge/documents/${encodeURIComponent(documentID)}/versions/${encodeURIComponent(versionID)}/publish`,
    "POST", request);
}

export function unpublishKnowledge(baseURL: string, idToken: string, deviceID: string, documentID: string, request: typeof fetch = fetch): Promise<void> {
  return mutate(baseURL, idToken, deviceID,
    `/v1/knowledge/documents/${encodeURIComponent(documentID)}/unpublish`, "POST", request);
}

export function setKnowledgeGrant(baseURL: string, idToken: string, deviceID: string, documentID: string, memberID: string, enabled: boolean, request: typeof fetch = fetch): Promise<void> {
  return mutate(baseURL, idToken, deviceID,
    `/v1/knowledge/documents/${encodeURIComponent(documentID)}/grants/${encodeURIComponent(memberID)}`,
    enabled ? "PUT" : "DELETE", request);
}
