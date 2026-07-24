# OpenIM 企业知识库 RAG 评测报告

- 状态：`in_progress`
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
| 全量检索样本 | 1,120 | 运行中 |
| Recall@5 | >= 0.85 | 待结果 |
| Recall@10 对比历史 Recall@8 | >= 0.928846 | 待结果 |
| MRR | >= 0.70 | 待结果 |
| ACL leakage | 0 | 待结果 |
| 旧版本泄漏 | 0 | 待结果 |
| 来源与 checksum 完整性 | 1.0 | 待结果 |
| 生成样本 | >= 120 | 待运行 |
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

Node2 长任务由 `ops/run-node2-enterprise-rag-evaluation.sh` 串联后两段。
脚本先独立校验 1,120 条检索报告的全部发布门槛，只有通过才运行
`60 + 60` Terra 生成评测；已有报告必须通过同一契约才会复用，失败报告
不会被静默覆盖。

## 6. 当前证据

- 新 pgvector 索引在隔离数据库完成 `2704/2704` 当前发布 Chunk 投影，
  激活 generation `dc117419-fb2c-43db-b069-6c0c9f303ac6`；
- 迁移 0001-0032 首次和重复执行通过，数据集导入为
  `520 documents / 624 versions / 3224 chunks / 520 grants / 2 members`；
- ACL、撤权、旧版本、checksum、伪引用、固定 reranker/no-fallback 和
  EvaluationRun 幂等集成测试通过；
- 全量检索评测已于 2026-07-24 01:35 +08:00 使用与发布包一致的
  `knowledge-rag-admin` SHA-256 `430a3082...8818` 启动；门槛检查后的
  Terra 生成评测已排队，尚未把运行中状态记为通过；
- 2026-07-24 14:05 +08:00 的只读检查显示已完成 `664/1120` 次重排，
  另有一次 32 候选重排正在执行；Worker 约占用 24 个 CPU 核，已完成
  重排平均耗时约 `66.8s`。检索、生成和最终报告均尚未生成，因此该证据
  只证明评测仍在 CPU 满载推进，不代表任何指标已通过，也不应重启评测；
- Terra 120 条生成评测、Node2 导入和 Web/OpenIM/Telegram E2E 尚未
  完成。

## 7. 最终结论

待全量报告、Node2 三通道和故障注入完成后填写。在此之前，本报告不声称
企业知识库生产验收通过。
