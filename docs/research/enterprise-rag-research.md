# OpenIM 企业知识库可信 RAG 调研

- 状态：已完成编码前研究门禁
- 基线提交：`21b06259e4e5d88d40e6ba455ee2fdc0e2ec7e9c`
- 访问日期：2026-07-23
- 适用单元：`enterprise-knowledge-rag`

## 1. 调研问题

当前实现已经具备 PostgreSQL 文档、版本、Chunk、直接成员 ACL、
`qwen3-embedding:4b` 嵌入和确定性评测，但生产检索仍会把一个成员有权
访问的全部 `real[]` 向量读入 Go 进程后计算。它没有真实文件导入、对象
存储、解析任务、数据库 ANN 索引、固定重排器和知识库管理界面。

本次调研只回答完成首个生产垂直切片所需的问题：

1. 如何在生成前得到可更新、可追溯且严格授权的证据；
2. 如何组合中文词法检索、稠密检索和重排；
3. 如何在现有 PostgreSQL、MinIO、OpenIM 和 Agent Runtime 边界内落地；
4. 如何衡量检索、回答、引用和拒答，而不把 LLM Judge 当成唯一事实；
5. 哪些成熟项目的设计值得采用，哪些完整运行时不应引入。

## 2. 源码事实

以下是调研开始时由当前仓库验证的事实：

- `knowledge.documents`、`document_versions`、`chunks` 和
  `authz.document_grants` 由迁移 `0005` 创建。
- `knowledge.chunk_embeddings` 由迁移 `0025` 创建，向量类型是
  `real[]`。
- `internal/retrieval/retrieval.go` 在 SQL 中先连接租户、成员授权和当前
  发布版本，再把有权 Chunk 的内容和向量加载到应用内执行词法、点积和
  RRF。
- `internal/agent/worker.go` 只把授权检索结果送入 Intelligence Worker，
  并检查模型声明的 Citation ID 是否来自本次 Evidence。
- `datasets/enterprise-knowledge/v1` 冻结了 520 份文档、624 个版本、
  3,224 个 Chunk 和 1,120 条 QA。
- OpenIM 仍拥有消息、会话、用户、群组、群成员和多端同步事实；
  PostgreSQL 不复制这些事实。

## 3. 核心论文

### 3.1 RAG

**资料**

- Lewis et al., *Retrieval-Augmented Generation for Knowledge-Intensive NLP
  Tasks*, NeurIPS 2020，arXiv `2005.11401v4`。
- URL：<https://arxiv.org/abs/2005.11401>
- 许可证：arXiv non-exclusive distribution license。

**解决的问题**

论文把参数化生成模型与可更新的非参数知识索引结合，使回答可以依赖外部
证据，并改善知识更新和来源追踪。

**采用**

- 将文档、版本、Chunk、Embedding 和 Citation 作为可独立审计的事实；
- 生成只消费一次检索形成的、带版本与 checksum 的 Evidence；
- 文档更新通过新版本与索引投影完成，不修改模型参数；
- 无授权证据时不调用 Terra，直接产生明确拒答。

**改造或拒绝**

- 不采用论文中的端到端联合训练；
- 不使用 Wikipedia 全局索引；
- 不允许生成模型自行扩大检索范围或绕过企业 ACL。

**对应实现与实验**

- `internal/retrieval`
- `internal/agent/worker.go`
- `internal/knowledge`
- 全量 1,120 条检索评测、分层 120 条以上生成评测。

### 3.2 DPR

**资料**

- Karpukhin et al., *Dense Passage Retrieval for Open-Domain Question
  Answering*, EMNLP 2020，arXiv `2004.04906v3`。
- URL：<https://arxiv.org/abs/2004.04906>
- 许可证：arXiv non-exclusive distribution license。

**解决的问题**

DPR 证明双编码器稠密表示可以有效召回与问题语义相关、但词面不完全重合的
段落，并把 Top-K Passage Recall 作为关键检索指标。

**采用**

- 问题与 Chunk 使用同一锁定嵌入契约；
- ANN 只负责候选召回，不能代替 ACL、版本和发布状态；
- 使用 Recall@5、Recall@10、MRR 与 nDCG@10 衡量检索。

**改造或拒绝**

- 不训练新的双编码器；
- 不采用 dense-only；企业制度编号、角色名和专有词仍需要词法分支。

### 3.3 Self-RAG

**资料**

- Asai et al., *Self-RAG: Learning to Retrieve, Generate, and Critique through
  Self-Reflection*, ICLR 2024，arXiv `2310.11511v1`。
- URL：<https://arxiv.org/abs/2310.11511>
- 许可证：CC BY 4.0。

**解决的问题**

Self-RAG 通过检索和反思 Token 让模型按需检索并评价证据、回答质量和引用。

**采用**

- 区分 `grounded`、`insufficient_evidence` 和 `not_applicable`；
- 把“是否有证据”“引用是否有效”“回答是否受证据支持”拆为独立门禁；
- 保存失败 case，而不是只保存平均分。

**拒绝**

- 不让 Terra 决定是否绕过检索；
- 不引入无边界的自反思循环；
- 不让同一个模型同时生成并成为唯一裁判。

当前项目的权限、发布状态、checksum 和引用合法性必须由确定性代码判断。

### 3.4 BGE-M3

**资料**

- Chen et al., *M3-Embedding: Multi-Linguality, Multi-Functionality,
  Multi-Granularity Text Embeddings Through Self-Knowledge Distillation*，
  arXiv `2402.03216v5`。
- URL：<https://arxiv.org/abs/2402.03216>
- 许可证：CC BY 4.0。

**解决的问题**

BGE-M3 统一讨论多语言、稠密、稀疏和多向量检索，并覆盖长文本输入。其结果
支持“多路召回后融合”而不是依赖单一相似度。

**采用**

- 中文和英文均保留词法与稠密两个独立召回分支；
- 使用确定性 RRF 融合；
- 使用同系列多语言 Cross Encoder 重排融合候选。

**改造或拒绝**

- 当前嵌入仍锁定已真实部署的 `qwen3-embedding:4b`，不借调研名义换模型；
- 不在本 Goal 引入 sparse-vector 或 ColBERT 投影；
- 不索引 8,192 Token 长块，Chunk 仍按可引用的结构边界切分。

### 3.5 ARES

**资料**

- Saad-Falcon et al., *ARES: An Automated Evaluation Framework for
  Retrieval-Augmented Generation Systems*, NAACL 2024。
- URL：<https://aclanthology.org/2024.naacl-long.20/>
- 许可证：CC BY 4.0。

**解决的问题**

ARES 将 RAG 质量拆为 Context Relevance、Answer Faithfulness 和 Answer
Relevance，并用少量人工标注校准自动 Judge。

**采用**

- 分开报告检索相关性、事实覆盖、Faithfulness 和回答正确性；
- 生成抽样必须覆盖部门边界、撤权、旧版本和不可回答问题；
- Judge 输出只能作为补充信号，不能覆盖确定性失败。

**拒绝**

- 不为本次发布训练新的 Judge；
- 不用合成 Judge 分数替代 ACL leakage、checksum、引用集合和金标事实检查。

### 3.6 RAGAS

**资料**

- Es et al., *RAGAs: Automated Evaluation of Retrieval Augmented Generation*，
  EACL 2024 System Demonstrations。
- URL：<https://aclanthology.org/2024.eacl-demo.16/>
- 许可证：CC BY 4.0。

**解决的问题**

RAGAS 提供对检索上下文、Faithfulness 和回答质量分别计量的方法，适合快速
定位流水线退化发生在哪一层。

**采用**

- 检索与生成报告分层；
- 引用 Precision、Recall、checksum integrity 和拒答准确率分别计算；
- 每个失败项保存 QA ID 与确定性原因。

**拒绝**

- 不直接引入 RAGAS Python 运行时；
- 不使用 reference-free LLM 评分作为发布的唯一证据。

## 4. 高星开源项目

星标只用于说明社区规模，不代表安全审查结论。数值是访问日快照。

### 4.1 RAGFlow

- 仓库：<https://github.com/infiniflow/ragflow>
- 快照：`main@3e4c6dfc0a3b927c15989a70b2e7b6d2309310ca`
- 约 85.8k stars；Apache-2.0。

**采用**

- 导入、解析、Chunk、索引、发布是可观察的阶段；
- 管理端展示 Chunk/引用和失败状态；
- 上传源文件与派生索引分离。

**拒绝**

- 不引入完整 RAGFlow 服务、数据库、任务系统和前端；
- 不复制其多解析器自动回退行为；
- 不让其权限模型替代本项目 OIDC、成员和 DocumentGrant。

### 4.2 Haystack

- 仓库：<https://github.com/deepset-ai/haystack>
- 快照：`main@736930666e7d3bfe9defa195a48deacf7d96fca9`
- 约 26.0k stars；Apache-2.0。

**采用**

- Parser、Chunker、Embedder、Retriever、Reranker 和 Generator 使用明确
  输入输出契约；
- 每个阶段失败可观测并可独立测试。

**拒绝**

- 不引入通用 Pipeline Runtime；
- 不开放 provider/model 动态替换，因为生产契约要求严格 no fallback。

### 4.3 LlamaIndex

- 仓库：<https://github.com/run-llama/llama_index>
- 快照：`main@7359b1acc74563f715d4463ace39fb4dc73d79af`
- 约 51.0k stars；MIT。

**采用**

- 文件 Connector 与索引核心之间使用受限 Document 结构；
- 检索后重排和 Citation Envelope 是单独阶段。

**拒绝**

- 不引入完整框架和任意 Connector；
- 本 Goal 只允许 Markdown、TXT、文本层 PDF 和 DOCX；
- 不让框架缓存或 Metadata 成为授权事实。

### 4.4 MinIO 生命周期与隔离决定

- 仓库：<https://github.com/minio/minio>
- 当前项目上游镜像：
  `RELEASE.2024-01-11T07-46-16Z@sha256:796f75ea...`
- 官方最后安全发布：`RELEASE.2025-10-15T17-29-55Z@9e49d5e`。
- 访问日期：2026-07-23；AGPL-3.0。

官方安全发布明确修复了 Service Account/STS 会话策略绕过，并要求立即
升级；仓库随后在 2026 年归档，最后修复版又要求从源码构建容器。因此，
不能把当前 LAN 暴露且由 OpenIM 共享的 2024 MinIO 当成知识库的“受限
凭据边界”。

本 Goal 采用一个独立 MinIO 进程：仅绑定 Node2
`127.0.0.1:12015`，使用独立生成的实例凭据和
`/home/qsyy0921/MFL/data/openim-platform-knowledge-minio` 数据目录。它
不创建 Service Account、不复用 OpenIM MinIO 密钥，也不开放控制台。
这是当前实验室单企业部署的隔离决定，不代表长期对象存储选型已经冻结；
对公网或多租户发布前必须另做对象存储维护性和升级 ADR。

### 4.4 pgvector

- 仓库：<https://github.com/pgvector/pgvector>
- 版本：`v0.8.5@159b79aaad5983fb7459c1e3df2897fbb2d11788`
- 调研快照：`master@a6420355c5d1c08f4c5fbd5112fc17e4cf3b5eb5`
- 约 22.3k stars；PostgreSQL License。

**关键约束**

- `vector` 的 HNSW/IVFFlat 索引最多支持 2,000 维；
- `halfvec` 的 ANN 索引支持到 4,000 维；
- 当前 `qwen3-embedding:4b` 是 2,560 维；
- 官方建议 PostgreSQL FTS 与向量检索组合，并可用 RRF 或 Cross Encoder
  融合；
- ANN 过滤发生在扫描阶段，选择性 ACL 条件必须配合有界候选与迭代扫描
  验证 Recall。

**采用**

- PostgreSQL `halfvec(2560)`、Cosine HNSW、`tsvector` GIN、RRF；
- `hnsw.iterative_scan = strict_order` 和固定 `ef_search`；
- SQL 中同时连接租户、成员授权、当前发布版本和活动索引；
- 索引代际和模型 revision 由 PostgreSQL 管理。

**拒绝**

- 不把 2,560 维强行写成 `vector(2560)` 后伪称存在 ANN 索引；
- 不降维、不换嵌入模型、不引入第二个向量数据库；
- 不保留 `real[]` 内存扫描作为生产备用路径。

## 5. 辅助实现选型

### 5.1 固定多语言重排器

- 模型：`BAAI/bge-reranker-v2-m3`
- Hugging Face revision：
  `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`
- 规模：约 0.6B；多语言 Cross Encoder；Apache-2.0。
- 官方实现：`FlagOpen/FlagEmbedding`
  `7ed43d67ec03fbe5c31c0992dbfa941fb1860549`，约 12k stars，
  Apache-2.0。

采用原因是它直接对 `(query, passage)` 输出相关性，支持中英文，规模能在
现有本机 CPU/内存边界内运行。模型在部署阶段下载到固定目录并校验
revision；运行时使用本地文件且禁止联网取另一个 revision。重排失败会使
检索失败，不返回未重排候选。

待验证实验：

- Top-32 融合候选的 p50/p95 重排延迟；
- 1,120 QA 的 Recall@5、MRR 与无重排基线对比；
- Node2 经 loopback Intelligence tunnel 的真实调用。

### 5.2 PDF

固定流水线是：

1. `pdfcpu/pdfcpu v0.13.0`
   (`198b38fecb5b92bad1fa100fec6071def1aa3ef7`) 严格校验；
2. `ledongthuc/pdf`
   (`v0.0.0-20250511090121-5959a4027728`) 提取文本层。

`pdfcpu` 约 8.7k stars、Apache-2.0；`ledongthuc/pdf` 约 614 stars、
BSD-3-Clause。前者不承担正文提取，后者不绕过前者校验，因此不是两套
Parser fallback。无文本层、加密、超限或无法提取的 PDF 明确失败；OCR 是
非目标。

### 5.3 DOCX、Markdown 与 TXT

- DOCX 使用 Go 标准库 `archive/zip` 和 `encoding/xml`，只读取
  `word/document.xml`；拒绝宏、外部关系、`altChunk`、加密包和压缩炸弹。
- Markdown/TXT 必须是严格 UTF-8、无 NUL，并执行大小与行长限制。
- 不引入通用 Office 套件或 Shell 子进程。

### 5.4 MinIO

- 客户端：`minio/minio-go/v7 v7.2.1`，Apache-2.0。
- 原始文件进入专用 Bucket；对象 Key 只由租户、文档和版本 UUID 组成。
- 原始文件名仅作为经过长度和控制字符校验的展示 Metadata。

## 6. 最终架构决定

```text
OIDC knowledge_admin
  -> bounded upload validation
  -> immutable MinIO object
  -> PostgreSQL DocumentVersion + IngestionJob
  -> leased parser worker
  -> structural chunks + SHA-256
  -> qwen3-embedding:4b
  -> pgvector halfvec(2560) + tsvector projection
  -> explicit publish + DocumentGrant

authenticated member query
  -> tenant/member/device validation
  -> ACL + current published version predicates
  -> lexical Top-N + dense Top-N
  -> deterministic RRF
  -> fixed local bge-reranker-v2-m3
  -> bounded authorized Evidence
  -> Terra Candidate
  -> citation ID/checksum/support validation
  -> durable Candidate
  -> OpenIM or Telegram idempotent delivery
```

## 7. 研究门禁结论

本项目不需要引入完整 RAG 框架。现有 DDD 边界已经能承载所需能力，新增的
必要依赖仅限对象存储客户端、固定 PDF 校验/提取库、pgvector 扩展和固定
本地重排模型。所有采用项都必须通过本地与 Node2 实验；论文结论或 GitHub
星标本身不是验收证据。
