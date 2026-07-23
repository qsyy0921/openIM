import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { KnowledgeWorkspace } from "./KnowledgeWorkspace";
import { KnowledgeController, type KnowledgeState } from "./knowledge";

describe("KnowledgeWorkspace", () => {
  it("renders failure state and does not expose object storage paths", () => {
    const state: KnowledgeState = {
      visibility: "visible",
      selectedDocumentID: "doc-1",
      loading: false,
      mutating: false,
      error: null,
      members: [],
      documents: [{
        id: "doc-1", title: "Security policy", classification: "internal",
        status: "active", grant_count: 0, created_at: "2030-01-01T00:00:00Z",
        latest_ingestion_state: "failed", latest_failure_code: "PDF_STRUCTURE_INVALID",
        latest_failure_detail: "PDF failed strict structural validation"
      }],
      versions: [{
        id: "version-1", version_number: 1, checksum: `sha256:${"a".repeat(64)}`,
        status: "draft", ingestion_state: "failed", failure_code: "PDF_STRUCTURE_INVALID",
        failure_detail: "PDF failed strict structural validation",
        original_filename: "security.pdf", created_at: "2030-01-01T00:00:00Z"
      }]
    };
    const controller = new KnowledgeController({
      snapshot: vi.fn(), versions: vi.fn(), upload: vi.fn(), publish: vi.fn(),
      unpublish: vi.fn(), setGrant: vi.fn()
    });
    const html = renderToStaticMarkup(createElement(KnowledgeWorkspace, {
      controller, state, onTestQuestion: vi.fn()
    }));
    expect(html).toContain("PDF_STRUCTURE_INVALID");
    expect(html).toContain("security.pdf");
    expect(html).not.toContain("object_key");
    expect(html).not.toContain("minio");
  });
});
