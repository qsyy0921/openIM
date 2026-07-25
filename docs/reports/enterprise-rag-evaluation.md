# OpenIM 企业知识库 RAG 评测报告

- 状态：`retrieval_gate_failed`
- 数据集：`enterprise-knowledge/v1`
- 数据集发布摘要：
  `sha256:35fe1582af8f380cabd5d80f5fd2a19f099055ec2e7056df0a5c521658ffa157`
- QA 文件摘要：
  `sha256:b9f163665f40c3f2e8392d00aa38080520144cf6dcff2e4236563703e0674c61`
- 检索样本：1,120 条全量
- 生成样本：60 条可回答 + 60 条不可回答，固定分层抽样

## 1. 评测边界

本报告验证企业 RAG 的检索、权限、版本、来源、生成、引用和拒答链路。
它不验证 Agent Memory，不把 OpenIM 消息历史当知识库，也不使用 LLM
Judge 代替确定性 ACL、checksum、金标事实和引用集合判断。

指标设计参考 RAG、DPR、ARES 与 RAGAS，但采用当前项目可重放的金标和
确定性规则：

- 检索：Recall@5、Recall@10、MRR、nDCG@10、Precision@5/10；
- 安全：ACL leakage、旧版本泄漏、来源和 checksum 完整性；
- 生成：事实覆盖、Faithfulness、引用 Precision/Recall、checksum、
  Grounding 决策和不可回答拒答；
- 失败证据：保留 QA ID 和稳定原因，不保存文档正文、问题正文、Prompt
  或模型密钥。

## 2. 固定运行契约

| 组件 | 固定值 |
| --- | --- |
| PostgreSQL | 17 + pgvector 0.8.5 |
| 向量 | `halfvec(2560)` cosine HNSW |
| 嵌入 | `qwen3-embedding:4b`, 2,560 dimensions |
| 召回 | ACL-first FTS Top-32 + dense Top-32 |
| 融合 | deterministic RRF |
| 重排 | `BAAI/bge-reranker-v2-m3@953dc6f6...` |
| 生成 | `gpt-5.6-terra`, Responses API, `reasoning.effort=high` |
| fallback | 无 |

## 3. 冻结基线

基线文件是 `eval/enterprise-rag-baseline.json`。历史 schema-v3 在
1,120 条数据上得到 Recall@8 `0.928846`、MRR `0.594903`、来源完整性
`1.0`。历史 Precision@8 与当前 Precision@10 分母不同，不做伪精确
比较。历史 40 条 `qwen2.5:3b` 生成结果全部未通过 Candidate 契约，只
作为负面证据，不是生产生成基线。

## 4. 发布门槛

| 门槛 | 要求 | 当前 |
| --- | ---: | --- |
| 全量检索样本 | 1,120 | 1,120，完成 |
| Recall@5 | >= 0.85 | 0.768269，失败 |
| Recall@10 对比历史 Recall@8 | >= 0.928846 | 0.848077，失败 |
| MRR | >= 0.70 | 0.480470，失败 |
| ACL leakage | 0 | 0，通过 |
| 旧版本泄漏 | 0 | 0，通过 |
| 来源与 checksum 完整性 | 1.0 | 1.0，通过 |
| 生成样本 | >= 120 | 前置门槛失败，未运行 |
| Candidate 契约成功率 | 1.0 | 待结果 |
| 不可回答拒答 | >= 0.95 | 待结果 |
| 引用 Precision | >= 0.95 | 待结果 |
| 引用 checksum | 1.0 | 待结果 |
| Faithfulness | >= 0.95 | 待结果 |

## 5. 可重放命令

检索和生成分别输出原始报告。`finalize` 会重新计算 QA 摘要、校验报告
schema、固定模型和阈值，派生确定性 EvaluationRun ID，再以幂等方式写入
`knowledge.evaluation_runs` 并生成 `eval/enterprise-rag-final.json`。

```powershell
knowledge-rag-admin -mode evaluate `
  -tenant-id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa `
  -member-id bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb `
  -denied-member-id cccccccc-cccc-4ccc-8ccc-cccccccccccc `
  -qa datasets/enterprise-knowledge/v1/qa.jsonl `
  -output eval/enterprise-rag-retrieval.json

knowledge-rag-admin -mode evaluate-generation `
  -tenant-id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa `
  -member-id bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb `
  -generation-answerable 60 -generation-unanswerable 60 `
  -qa datasets/enterprise-knowledge/v1/qa.jsonl `
  -output eval/enterprise-rag-generation.json

knowledge-rag-admin -mode finalize `
  -tenant-id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa `
  -retrieval-report eval/enterprise-rag-retrieval.json `
  -generation-report eval/enterprise-rag-generation.json `
  -application-commit <evaluated-commit> `
  -qa datasets/enterprise-knowledge/v1/qa.jsonl `
  -output eval/enterprise-rag-final.json
```

Node2 长任务由 `ops/run-node2-enterprise-rag-evaluation.sh` 串联三段。
新评测根没有检索报告时，脚本先原子生成完整 1,120 条报告；报告一旦
存在就只按同一契约校验，不会被静默覆盖。只有全部检索发布门槛通过，
脚本才运行 `60 + 60` Terra 生成评测并生成最终报告。门禁还会验证
nDCG、Precision、不可回答检索率等必备指标存在且有限，核对冻结阈值，
并要求最终报告内嵌的检索和生成对象逐字段等于两个原始报告；仅有
`passed=true` 或一个最终报告文件不能放行。

## 6. 当前证据

- 新 pgvector 索引在隔离数据库完成 `2704/2704` 当前发布 Chunk 投影，
  激活 generation `dc117419-fb2c-43db-b069-6c0c9f303ac6`；
- 迁移 0001-0032 首次和重复执行通过，数据集导入为
  `520 documents / 624 versions / 3224 chunks / 520 grants / 2 members`；
- ACL、撤权、旧版本、checksum、伪引用、固定 reranker/no-fallback 和
  EvaluationRun 幂等集成测试通过；
- 全量检索评测使用与发布包一致的 `knowledge-rag-admin` SHA-256
  `430a3082...8818` 完成 1,120 条样本。结果为 Recall@5 `0.768269`、
  Recall@10 `0.848077`、MRR `0.480470`、nDCG@10 `0.515241`、
  Precision@5 `0.182115`、Precision@10 `0.105000`；
- 1,040 条可回答问题中有 158 条未召回金标证据。ACL leakage 和旧版本
  leakage 都是 `0`，provenance 与 checksum integrity 都是 `1.0`；
- `openim-rag-retrieval-evaluation` 正常退出。门禁随后以
  `retrieval gate failed: recall_at_5` 阻止
  `openim-rag-generation-evaluation`，因此不存在生成报告或最终报告，
  也没有把接口探测冒充生成评测；
- 失败分布集中在 `single_document`、`version_awareness` 和 `numeric`。
  对 158 条失败用同一 ACL/current-version 条件做只读词法候选诊断：
  39 条金标位于 Top-32，119 条位于 Top-32 之后，0 条完全无词法匹配；
- 源码调查确认当前 embedding、FTS 和 reranker 都只接收 Chunk 正文，
  没有使用 SQL 已授权加载的文档标题。ADR-0011 已将
  `document-title-content-v1` 标为 proposed；
- ADR-0011 的代码和迁移 0033 已完成本地精确测试，并在 Node2 的隔离
  PostgreSQL 17/pgvector 0.8.5 中通过 0032 升级、四类历史行回填、全新
  安装、重复执行和真实 Knowledge/Retrieval 集成测试。评测数据库随后
  成功激活新的 `2704/2704` title-aware generation；
- 第一次 158 条失败集回归没有产生质量报告。评测器将 128 个问题放入
  单个 embedding 请求，固定 Worker 在 Ollama 顺序处理时于 180 秒超时，
  返回 HTTP 502，服务以状态 1 失败。该失败被保留，不能解释为 Recall
  失败或通过，也没有自动重试；
- 评测器已在本地改为每请求 4 个问题、最多 2 个请求并发，并保持问题和
  向量顺序。该修复作为 `71505d1` 通过精确测试，并以 digest-pinned
  clean build 在新的不可变 Node2 回归根启动；当前尚无结果报告。在真实
  158 条报告通过前仍不重跑全量评测；
- Terra 120 条生成评测、生产发布和 Web/OpenIM/Telegram E2E 尚未
  完成。

## 7. 最终结论

当前实现未通过检索质量门槛，不能进入生成评测或生产切换。安全与完整性
指标通过只说明 ACL-first 边界保持正确，不代表检索质量合格。下一步是按
ADR-0011 实现版本化结构感知投影，先做失败集回归，再决定是否重跑全量。
在完整门槛、Node2 三通道和故障注入完成前，本报告不声称企业知识库生产
验收通过。
