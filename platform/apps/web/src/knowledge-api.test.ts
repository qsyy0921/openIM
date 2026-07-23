import { describe, expect, it, vi } from "vitest";

import {
  getKnowledgeSnapshot,
  setKnowledgeGrant,
  uploadKnowledge
} from "./knowledge-api";
import { PlatformAPIError } from "./platform-api";

describe("knowledge API", () => {
  it("sends a bounded multipart upload without overriding its boundary", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      document_id: "doc-1",
      version_id: "version-1",
      version_number: 1,
      ingestion_state: "queued",
      idempotent: false
    }), { status: 202 }));
    const file = new File(["policy"], "policy.md", { type: "text/markdown" });
    const result = await uploadKnowledge("/platform-api", "id-token", "browser-1", {
      title: "Policy",
      classification: "internal",
      file,
      idempotencyKey: "knowledge-upload-1234567890"
    }, request);
    expect(result.ingestion_state).toBe("queued");
    const [url, init] = request.mock.calls[0];
    expect(url).toBe("/platform-api/v1/knowledge/documents?platform_id=5&device_id=browser-1");
    expect(init?.method).toBe("POST");
    expect(init?.headers).toEqual({
      Authorization: "Bearer id-token",
      "Idempotency-Key": "knowledge-upload-1234567890"
    });
    const form = init?.body as FormData;
    expect(form.get("title")).toBe("Policy");
    expect(form.get("classification")).toBe("internal");
    expect((form.get("file") as File).name).toBe("policy.md");
  });

  it("preserves forbidden state and correlation ID", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      code: "KNOWLEDGE_ADMIN_FORBIDDEN",
      message: "forbidden",
      correlation_id: "corr-knowledge"
    }), { status: 403 }));
    await expect(getKnowledgeSnapshot(
      "/platform-api", "token", "browser", undefined, request
    )).rejects.toEqual(new PlatformAPIError(
      "forbidden", "KNOWLEDGE_ADMIN_FORBIDDEN", "corr-knowledge", 403
    ));
  });

  it("uses the explicit grant mutation method", async () => {
    const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(
      JSON.stringify({ state: "revoked" }), { status: 200 }
    ));
    await setKnowledgeGrant(
      "/platform-api", "token", "browser", "doc/a", "member/b", false, request
    );
    expect(request).toHaveBeenCalledWith(
      "/platform-api/v1/knowledge/documents/doc%2Fa/grants/member%2Fb?platform_id=5&device_id=browser",
      { method: "DELETE", headers: { Authorization: "Bearer token" } }
    );
  });
});
