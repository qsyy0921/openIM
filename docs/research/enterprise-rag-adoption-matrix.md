# 企业 RAG 采用矩阵

- 状态：Accepted for implementation
- 访问日期：2026-07-23
- 关联 ADR：`docs/adr/0010-pgvector-hybrid-enterprise-retrieval.md`

| 来源 | 采用 | 改造 | 拒绝 | 本地落点 | 验证 |
| --- | --- | --- | --- | --- | --- |
| RAG, arXiv 2005.11401v4 | 参数知识与可更新外部证据分离、保留来源 | 企业证据必须先通过 ACL 与发布状态 | 联合训练、Wikipedia 全局索引 | `knowledge`, `retrieval`, `agent` | 引用与版本 E2E |
| DPR, arXiv 2004.04906v3 | Dense Top-K 与 Recall 指标 | 使用锁定 `qwen3-embedding:4b` | Dense-only、重新训练 | `internal/retrieval` | Recall@5/10、MRR |
| Self-RAG, arXiv 2310.11511v1 | Grounding/拒答/引用分门禁 | 反思由确定性策略和离线评测承担 | 运行时自反思循环、自授权检索 | `agent/worker.go` | 无证据与伪引用测试 |
| BGE-M3, arXiv 2402.03216v5 | 多路召回、中文/多语言重排 | 保留现有嵌入模型，采用同系列 reranker | 本 Goal 引入 Sparse/ColBERT | Worker `/v1/rerank` | 1,120 QA 对比 |
| ARES, NAACL 2024 | Context relevance、Faithfulness、answer relevance 分层 | 以冻结金标和规则校准 | Judge-only 发布门禁 | `retrieval/evaluation*` | 失败 case + 分层指标 |
| RAGAS, EACL 2024 | 检索、生成、引用指标分开 | 自建确定性指标 | 引入完整运行时、reference-free 唯一评分 | `eval/` | 报告 schema 校验 |
| RAGFlow `3e4c6df` | 可观察导入阶段、Chunk/引用预览 | 复用本项目 DDD、OIDC 和 ACL | 引入整套服务/数据库/前端 | `internal/knowledge`, Web | 状态机 E2E |
| Haystack `73693066` | 明确组件契约 | 固定为唯一生产实现 | 通用 provider/pipeline fallback | Go/Python typed contracts | 故障注入 |
| LlamaIndex `7359b1ac` | 受限 Connector 与 Citation Envelope | 只支持四种本地格式 | 任意连接器、框架缓存授权 | Parser Registry | 格式安全测试 |
| pgvector 0.8.5 | `halfvec(2560)`、HNSW、FTS、RRF | ACL SQL + iterative scan + 稳定 tie-break | `real[]` 生产扫描、外置向量库、降维 | migration 0032+, retrieval SQL | EXPLAIN、Recall、泄漏率 |
| FlagEmbedding `7ed43d67` | `bge-reranker-v2-m3` Cross Encoder | 固定 revision、本地文件、CPU 有界批量 | 动态模型、远端托管、失败跳过 | Intelligence Worker | revision/延迟/故障测试 |
| pdfcpu 0.13.0 | PDF 严格结构校验 | 只作为固定解析流水线第一阶段 | relaxed 自动重试、Shell 调用 | `knowledge/parser` | 畸形/加密 PDF |
| ledongthuc/pdf `5959a402` | 文本层提取 | 只能消费已通过 pdfcpu 的临时文件 | OCR、备用 Parser | `knowledge/parser` | 文本层/无文本层 |
| minio-go 7.2.1 | S3 对象 API | 专用 Bucket、UUID Key、短超时 | 文件系统副本、公开对象 URL | `knowledge/objectstore` | put/get/delete E2E |
| MinIO server lifecycle | 私有 S3 兼容原件库 | Node2 独立 loopback 进程、实例级凭据和 M2 数据目录 | 复用 LAN 暴露的 OpenIM MinIO、Service Account、公开控制台 | `ops/deploy-node2-knowledge-minio.sh` | topology + health + real object E2E |

## 固定契约

| 项目 | 值 |
| --- | --- |
| 生成 | `POST /v1/responses`, `gpt-5.6-terra`, `reasoning.effort=high`, `stream=false` |
| 嵌入 | `qwen3-embedding:4b`, 2,560 dimensions, normalized |
| 向量 | pgvector 0.8.5 `halfvec(2560)`, cosine HNSW |
| 词法 | PostgreSQL `tsvector` GIN，应用生成中文/英文确定性 Lexeme |
| 融合 | RRF，固定常数与稳定 UUID tie-break |
| 重排 | `BAAI/bge-reranker-v2-m3@953dc6f6...` |
| 原始对象 | MinIO 专用 Bucket，私有 UUID Key |
| 授权 | OIDC member/device + tenant + current `DocumentGrant` |
| 失败语义 | fail closed；无 provider/parser/retriever/reranker fallback |

## 发布前实验门槛

1. 1,120 条检索评测：ACL leakage 为 0、Recall@5 不低于 0.85、
   MRR 不低于 0.70。
2. 至少 120 条生成评测：引用 Precision 不低于 0.95、checksum 完整性
   为 1.0、不可回答拒答不低于 0.95。
3. Parser 安全夹具覆盖伪 MIME、扩展名冲突、错误签名、压缩炸弹、宏、
   外部关系、加密 PDF 和无文本层 PDF。
4. Node2 实测导入、授权、三通道问答、撤权、版本替换与四类故障注入。
