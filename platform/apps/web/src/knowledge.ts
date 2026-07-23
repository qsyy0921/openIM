import { PlatformAPIError } from "./platform-api";
import type {
  KnowledgeAdminSnapshot,
  KnowledgeDocument,
  KnowledgeMember,
  KnowledgeUploadInput,
  KnowledgeUploadResult,
  KnowledgeVersion
} from "./knowledge-api";

export type KnowledgeVisibility = "loading" | "hidden" | "visible";
export type KnowledgeState = {
  visibility: KnowledgeVisibility;
  documents: KnowledgeDocument[];
  members: KnowledgeMember[];
  versions: KnowledgeVersion[];
  selectedDocumentID: string | null;
  loading: boolean;
  mutating: boolean;
  error: string | null;
};

export const initialKnowledgeState: KnowledgeState = {
  visibility: "loading",
  documents: [],
  members: [],
  versions: [],
  selectedDocumentID: null,
  loading: false,
  mutating: false,
  error: null
};

export type KnowledgePort = {
  snapshot(documentID?: string): Promise<KnowledgeAdminSnapshot>;
  versions(documentID: string): Promise<KnowledgeVersion[]>;
  upload(input: KnowledgeUploadInput): Promise<KnowledgeUploadResult>;
  publish(documentID: string, versionID: string): Promise<void>;
  unpublish(documentID: string): Promise<void>;
  setGrant(documentID: string, memberID: string, enabled: boolean): Promise<void>;
};

type Listener = (state: KnowledgeState) => void;

function message(error: unknown): string {
  return error instanceof Error ? error.message : "Knowledge request failed";
}

export class KnowledgeController {
  private state: KnowledgeState = initialKnowledgeState;
  private readonly listeners = new Set<Listener>();
  private generation = 0;
  private stopped = false;

  constructor(private readonly port: KnowledgePort) {}

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  async start(): Promise<void> {
    this.stopped = false;
    const generation = ++this.generation;
    this.update({ visibility: "loading", loading: true, error: null });
    try {
      const snapshot = await this.port.snapshot();
      if (!this.current(generation)) return;
      const authorized = snapshot.roles.includes("knowledge_admin") || snapshot.roles.includes("platform_admin");
      if (!authorized) {
        this.update({ ...initialKnowledgeState, visibility: "hidden" });
        return;
      }
      const selected = this.state.selectedDocumentID &&
        snapshot.documents.some((item) => item.id === this.state.selectedDocumentID)
        ? this.state.selectedDocumentID
        : snapshot.documents[0]?.id ?? null;
      this.update({
        visibility: "visible",
        documents: snapshot.documents,
        members: [],
        versions: [],
        selectedDocumentID: selected,
        loading: false,
        error: null
      });
      if (selected) await this.loadSelected(selected, generation);
    } catch (error) {
      if (!this.current(generation)) return;
      if (error instanceof PlatformAPIError && error.status === 403) {
        this.update({ ...initialKnowledgeState, visibility: "hidden" });
        return;
      }
      this.update({ ...initialKnowledgeState, visibility: "hidden", error: message(error) });
    }
  }

  stop(): void {
    this.stopped = true;
    this.generation += 1;
    this.listeners.clear();
  }

  async refresh(): Promise<void> {
    this.requireVisible();
    await this.start();
  }

  async selectDocument(documentID: string): Promise<void> {
    this.requireVisible();
    if (!this.state.documents.some((item) => item.id === documentID)) {
      throw new Error("Knowledge document is unavailable");
    }
    const generation = ++this.generation;
    this.update({ selectedDocumentID: documentID, loading: true, error: null, members: [], versions: [] });
    await this.loadSelected(documentID, generation);
  }

  async upload(input: KnowledgeUploadInput): Promise<KnowledgeUploadResult> {
    this.beginMutation();
    try {
      const result = await this.port.upload(input);
      await this.start();
      if (this.state.visibility === "visible" && this.state.selectedDocumentID !== result.document_id) {
        await this.selectDocument(result.document_id);
      }
      return result;
    } catch (error) {
      this.update({ error: message(error) });
      throw error;
    } finally {
      if (!this.stopped) this.update({ mutating: false });
    }
  }

  async publish(versionID: string): Promise<void> {
    const documentID = this.requireSelected();
    await this.mutate(() => this.port.publish(documentID, versionID));
  }

  async unpublish(): Promise<void> {
    const documentID = this.requireSelected();
    await this.mutate(() => this.port.unpublish(documentID));
  }

  async setGrant(memberID: string, enabled: boolean): Promise<void> {
    const documentID = this.requireSelected();
    await this.mutate(() => this.port.setGrant(documentID, memberID, enabled));
  }

  private async mutate(operation: () => Promise<void>): Promise<void> {
    this.beginMutation();
    const documentID = this.state.selectedDocumentID;
    try {
      await operation();
      await this.start();
      if (documentID && this.state.visibility === "visible" && this.state.selectedDocumentID !== documentID) {
        await this.selectDocument(documentID);
      }
    } catch (error) {
      this.update({ error: message(error) });
      throw error;
    } finally {
      if (!this.stopped) this.update({ mutating: false });
    }
  }

  private async loadSelected(documentID: string, generation: number): Promise<void> {
    try {
      const [snapshot, versions] = await Promise.all([
        this.port.snapshot(documentID),
        this.port.versions(documentID)
      ]);
      if (!this.current(generation) || this.state.selectedDocumentID !== documentID) return;
      this.update({ members: snapshot.members, versions, loading: false, error: null });
    } catch (error) {
      if (!this.current(generation)) return;
      this.update({ loading: false, error: message(error) });
    }
  }

  private beginMutation(): void {
    this.requireVisible();
    if (this.state.mutating) throw new Error("Knowledge mutation is already in progress");
    this.update({ mutating: true, error: null });
  }

  private requireVisible(): void {
    if (this.stopped || this.state.visibility !== "visible") {
      throw new Error("Knowledge administration is unavailable");
    }
  }

  private requireSelected(): string {
    this.requireVisible();
    if (!this.state.selectedDocumentID) throw new Error("Select a knowledge document first");
    return this.state.selectedDocumentID;
  }

  private current(generation: number): boolean {
    return !this.stopped && generation === this.generation;
  }

  private update(patch: Partial<KnowledgeState>): void {
    if (this.stopped) return;
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}
