import {
  BookOpen,
  Check,
  FilePlus2,
  FileText,
  RefreshCw,
  Search,
  ShieldCheck,
  Upload,
  X
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import type { KnowledgeClassification } from "./knowledge-api";
import { KnowledgeController, type KnowledgeState } from "./knowledge";

type Props = {
  controller: KnowledgeController;
  state: KnowledgeState;
  onTestQuestion(question: string): Promise<void>;
};

const classificationLabels: Record<KnowledgeClassification, string> = {
  public: "公开",
  internal: "内部",
  confidential: "机密",
  restricted: "受限"
};

function ingestionLabel(value?: string): string {
  const labels: Record<string, string> = {
    uploading: "上传中",
    queued: "待解析",
    processing: "处理中",
    indexed: "已索引",
    failed: "失败",
    legacy_indexed: "历史索引"
  };
  return value ? labels[value] ?? value : "未发布";
}

function idempotencyKey(): string {
  return `knowledge-upload-${crypto.randomUUID()}`;
}

export function KnowledgeWorkspace({ controller, state, onTestQuestion }: Props) {
  const selected = useMemo(
    () => state.documents.find((item) => item.id === state.selectedDocumentID) ?? null,
    [state.documents, state.selectedDocumentID]
  );
  const [uploadMode, setUploadMode] = useState<"document" | "version">("document");
  const [title, setTitle] = useState("");
  const [classification, setClassification] = useState<KnowledgeClassification>("internal");
  const [file, setFile] = useState<File | null>(null);
  const [question, setQuestion] = useState("");
  const [questionError, setQuestionError] = useState<string | null>(null);
  const [asking, setAsking] = useState(false);
  const uploadKey = useRef<string | null>(null);
  const publishableClassification = selected?.classification === "public" || selected?.classification === "internal";

  useEffect(() => {
    if (uploadMode === "version" && selected) {
      setTitle(selected.title);
      setClassification(selected.classification);
    }
  }, [selected, uploadMode]);

  const run = (operation: Promise<unknown>) => {
    void operation.catch(() => undefined);
  };

  const chooseMode = (mode: "document" | "version") => {
    setUploadMode(mode);
    if (mode === "version" && selected) {
      setTitle(selected.title);
      setClassification(selected.classification);
    }
  };

  const submitUpload = async (event: FormEvent) => {
    event.preventDefault();
    if (!file || !title.trim() || (uploadMode === "version" && !selected)) return;
    uploadKey.current ??= idempotencyKey();
    try {
      await controller.upload({
        documentID: uploadMode === "version" ? selected?.id : undefined,
        title: title.trim(),
        classification,
        file,
        idempotencyKey: uploadKey.current
      });
      uploadKey.current = null;
      setFile(null);
      if (uploadMode === "document") setTitle("");
    } catch {
      // Controller state preserves the explicit failure and the key stays stable for retry.
    }
  };

  const ask = async () => {
    const value = question.trim();
    if (!value || asking) return;
    setQuestionError(null);
    setAsking(true);
    try {
      await onTestQuestion(value);
      setQuestion("");
    } catch (error) {
      setQuestionError(error instanceof Error ? error.message : "测试提问失败");
    } finally {
      setAsking(false);
    }
  };

  return (
    <section className="knowledge-workspace" aria-label="企业知识库">
      <aside className="knowledge-sidebar">
        <header className="knowledge-sidebar-header">
          <div>
            <p className="eyebrow">ENTERPRISE KNOWLEDGE</p>
            <h1>知识库</h1>
          </div>
          <button className="icon-button" aria-label="刷新知识库" title="刷新" disabled={state.loading || state.mutating} onClick={() => run(controller.refresh())}>
            <RefreshCw size={17} className={state.loading ? "spin" : ""} />
          </button>
        </header>
        <div className="knowledge-document-list">
          {state.documents.map((document) => (
            <button
              key={document.id}
              className={`knowledge-document-row${document.id === state.selectedDocumentID ? " active" : ""}`}
              onClick={() => run(controller.selectDocument(document.id))}
            >
              <FileText size={18} />
              <span>
                <strong>{document.title}</strong>
                <small>{classificationLabels[document.classification]} · {ingestionLabel(document.latest_ingestion_state)}</small>
              </span>
              <span className={`status-dot ${document.latest_ingestion_state ?? "idle"}`} aria-hidden="true" />
            </button>
          ))}
          {!state.loading && state.documents.length === 0 && (
            <div className="workspace-empty compact">
              <BookOpen size={24} />
              <p>还没有企业知识文档</p>
            </div>
          )}
        </div>
      </aside>

      <div className="knowledge-main">
        <header className="knowledge-main-header">
          <div>
            <p className="eyebrow">DOCUMENT CONTROL</p>
            <h2>{selected?.title ?? "导入第一份知识文档"}</h2>
          </div>
          {selected?.current_version_id && (
            <button className="secondary-button danger-button" disabled={state.mutating} onClick={() => run(controller.unpublish())}>
              <X size={16} />撤销发布
            </button>
          )}
        </header>

        {state.error && <p className="error-banner" role="alert">{state.error}</p>}

        <div className="knowledge-grid">
          <section className="knowledge-section upload-section" aria-labelledby="knowledge-upload-title">
            <div className="section-heading">
              <div>
                <p className="status-label">导入</p>
                <h3 id="knowledge-upload-title">不可变文档版本</h3>
              </div>
              <Upload size={19} />
            </div>
            <div className="segmented-control" aria-label="上传类型">
              <button className={uploadMode === "document" ? "active" : ""} type="button" onClick={() => chooseMode("document")}>新文档</button>
              <button className={uploadMode === "version" ? "active" : ""} type="button" disabled={!selected} onClick={() => chooseMode("version")}>新版本</button>
            </div>
            <form className="knowledge-upload-form" onSubmit={(event) => void submitUpload(event)}>
              <label>
                <span>标题</span>
                <input value={title} maxLength={300} disabled={uploadMode === "version"} onChange={(event) => setTitle(event.target.value)} />
              </label>
              <label>
                <span>密级</span>
                <select value={classification} disabled={uploadMode === "version"} onChange={(event) => setClassification(event.target.value as KnowledgeClassification)}>
                  {Object.entries(classificationLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                </select>
              </label>
              <label className="file-field">
                <span>文件</span>
                <input
                  type="file"
                  accept=".md,.markdown,.txt,.pdf,.docx,text/plain,text/markdown,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document"
                  onChange={(event) => {
                    setFile(event.target.files?.[0] ?? null);
                    uploadKey.current = null;
                  }}
                />
                <small>{file ? `${file.name} · ${Math.ceil(file.size / 1024)} KB` : "Markdown、TXT、文本 PDF 或 DOCX，最大 32 MB"}</small>
              </label>
              <button className="primary-button" disabled={!file || !title.trim() || state.mutating}>
                <FilePlus2 size={17} />{state.mutating ? "正在提交" : "提交解析"}
              </button>
            </form>
          </section>

          <section className="knowledge-section" aria-labelledby="knowledge-versions-title">
            <div className="section-heading">
              <div>
                <p className="status-label">版本</p>
                <h3 id="knowledge-versions-title">解析与发布状态</h3>
              </div>
              <FileText size={19} />
            </div>
            <div className="knowledge-version-list">
              {state.versions.map((version) => (
                <article className="knowledge-version-row" key={version.id}>
                  <div>
                    <strong>v{version.version_number}</strong>
                    <span className={`state-badge ${version.ingestion_state}`}>{ingestionLabel(version.ingestion_state)}</span>
                    <small>{version.original_filename ?? "历史版本"}{version.attempts ? ` · 尝试 ${version.attempts}` : ""}</small>
                  </div>
                  {version.failure_detail && <p className="error-text">{version.failure_code}: {version.failure_detail}</p>}
                  {version.ingestion_state === "indexed" && version.status !== "published" && publishableClassification && (
                    <button className="secondary-button" disabled={state.mutating} onClick={() => run(controller.publish(version.id))}>
                      <Check size={15} />发布
                    </button>
                  )}
                  {version.ingestion_state === "indexed" && version.status !== "published" && !publishableClassification && (
                    <span className="state-badge blocked">当前密级不可发布</span>
                  )}
                  {version.status === "published" && <span className="published-mark"><Check size={15} />当前发布</span>}
                </article>
              ))}
              {selected && !state.loading && state.versions.length === 0 && <p className="muted-text">暂无版本记录</p>}
            </div>
          </section>

          <section className="knowledge-section" aria-labelledby="knowledge-access-title">
            <div className="section-heading">
              <div>
                <p className="status-label">权限</p>
                <h3 id="knowledge-access-title">直接读取授权</h3>
              </div>
              <ShieldCheck size={19} />
            </div>
            <div className="knowledge-member-list">
              {state.members.map((member) => (
                <label className="knowledge-member-row" key={member.id}>
                  <input
                    type="checkbox"
                    checked={member.granted}
                    disabled={member.status !== "active" || state.mutating}
                    onChange={(event) => run(controller.setGrant(member.id, event.target.checked))}
                  />
                  <span>
                    <strong>{member.display_name}</strong>
                    <small>{member.status === "active" ? "有效成员" : "已停用"}</small>
                  </span>
                </label>
              ))}
              {selected && !state.loading && state.members.length === 0 && <p className="muted-text">暂无企业成员</p>}
            </div>
          </section>

          <section className="knowledge-section knowledge-test-section" aria-labelledby="knowledge-test-title">
            <div className="section-heading">
              <div>
                <p className="status-label">验证</p>
                <h3 id="knowledge-test-title">使用当前身份提问</h3>
              </div>
              <Search size={19} />
            </div>
            <textarea value={question} maxLength={2000} placeholder="输入一个必须由已授权文档回答的问题" onChange={(event) => setQuestion(event.target.value)} />
            {questionError && <p className="error-text">{questionError}</p>}
            <button className="primary-button" disabled={!question.trim() || asking} onClick={() => void ask()}>
              <Search size={16} />{asking ? "正在发送" : "发送给智能助手"}
            </button>
          </section>
        </div>
      </div>
    </section>
  );
}
