import { describe, expect, it, vi } from "vitest";

import {
  initialKnowledgeState,
  KnowledgeController,
  type KnowledgePort,
  type KnowledgeState
} from "./knowledge";
import type {
  KnowledgeAdminSnapshot,
  KnowledgeDocument,
  KnowledgeMember,
  KnowledgeVersion
} from "./knowledge-api";
import { PlatformAPIError } from "./platform-api";

const documents: KnowledgeDocument[] = [
  {
    id: "doc-1", title: "Policy", classification: "internal", status: "active",
    grant_count: 1, created_at: "2030-01-01T00:00:00Z"
  },
  {
    id: "doc-2", title: "Guide", classification: "public", status: "active",
    grant_count: 0, created_at: "2030-01-02T00:00:00Z"
  }
];
const version = (id: string): KnowledgeVersion => ({
  id: `version-${id}`, version_number: 1, checksum: `sha256:${"a".repeat(64)}`,
  status: "draft", ingestion_state: "indexed", created_at: "2030-01-01T00:00:00Z"
});
const member = (id: string): KnowledgeMember => ({
  id: `member-${id}`, display_name: id, status: "active", granted: false
});

function snapshot(documentID?: string): KnowledgeAdminSnapshot {
  return {
    roles: ["knowledge_admin"],
    documents,
    members: documentID ? [member(documentID)] : []
  };
}

function port(overrides: Partial<KnowledgePort> = {}): KnowledgePort {
  return {
    snapshot: vi.fn(async (documentID?: string) => snapshot(documentID)),
    versions: vi.fn(async (documentID: string) => [version(documentID)]),
    upload: vi.fn(),
    publish: vi.fn(),
    unpublish: vi.fn(),
    setGrant: vi.fn(),
    ...overrides
  };
}

describe("KnowledgeController", () => {
  it("loads only after the API confirms an administrator role", async () => {
    const controller = new KnowledgeController(port());
    let state: KnowledgeState = initialKnowledgeState;
    controller.subscribe((value) => { state = value; });
    await controller.start();
    expect(state.visibility).toBe("visible");
    expect(state.selectedDocumentID).toBe("doc-1");
    expect(state.members[0].id).toBe("member-doc-1");
    expect(state.versions[0].id).toBe("version-doc-1");
  });

  it("hides the module on a real 403", async () => {
    const controller = new KnowledgeController(port({
      snapshot: vi.fn().mockRejectedValue(
        new PlatformAPIError("forbidden", "KNOWLEDGE_ADMIN_FORBIDDEN", "corr", 403)
      )
    }));
    let state: KnowledgeState = initialKnowledgeState;
    controller.subscribe((value) => { state = value; });
    await controller.start();
    expect(state.visibility).toBe("hidden");
    expect(state.documents).toEqual([]);
  });

  it("does not let an older document request overwrite the latest selection", async () => {
    let delayedResolve!: (value: KnowledgeAdminSnapshot) => void;
    const delayed = new Promise<KnowledgeAdminSnapshot>((resolve) => { delayedResolve = resolve; });
    let delayDocOne = false;
    const controller = new KnowledgeController(port({
      snapshot: vi.fn(async (documentID?: string) => {
        if (documentID === "doc-1" && delayDocOne) return delayed;
        return snapshot(documentID);
      })
    }));
    let state: KnowledgeState = initialKnowledgeState;
    controller.subscribe((value) => { state = value; });
    await controller.start();
    delayDocOne = true;
    const older = controller.selectDocument("doc-1");
    const latest = controller.selectDocument("doc-2");
    await latest;
    delayedResolve(snapshot("doc-1"));
    await older;
    expect(state.selectedDocumentID).toBe("doc-2");
    expect(state.members[0].id).toBe("member-doc-2");
  });

  it("rejects duplicate concurrent mutations", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const controller = new KnowledgeController(port({
      setGrant: vi.fn(() => pending)
    }));
    await controller.start();
    const first = controller.setGrant("member-a", true);
    await expect(controller.setGrant("member-a", false)).rejects.toThrow(
      "already in progress"
    );
    release();
    await first;
  });

  it("does not publish pending results after stop", async () => {
    let resolve!: (value: KnowledgeAdminSnapshot) => void;
    const pending = new Promise<KnowledgeAdminSnapshot>((done) => { resolve = done; });
    const controller = new KnowledgeController(port({ snapshot: vi.fn(() => pending) }));
    const states: KnowledgeState[] = [];
    controller.subscribe((value) => states.push(value));
    const start = controller.start();
    controller.stop();
    resolve(snapshot());
    await start;
    expect(states.at(-1)?.visibility).toBe("loading");
  });
});
