# Xinglan Enterprise Knowledge QA v1.0.0

这是面向 OpenIM 企业知识问答 Pipeline 的中文合成数据集。企业“星澜智协科技有限公司”、组织、产品、系统、人员角色和事件全部为虚构内容，不来自真实公司的内部资料。

## 规模

- 13 个业务领域，104 个主题。
- 520 篇逻辑文档，五种文档形态保持均衡。
- 624 个版本，其中 104 个历史制度版本明确标记为 `superseded`。
- 3224 个语义章节 chunk。
- 1120 个 QA，包含单文档、多文档、流程、数值、决策追踪、版本意识和无答案问题。

## 文件

- `company_profile.json`：固定企业事实和本地测试身份。
- `canonical_facts.jsonl`：104 个主题的唯一事实源。
- `documents.jsonl`：对应 `knowledge.documents`。
- `document_versions.jsonl`：对应 `knowledge.document_versions`。
- `chunks.jsonl`：对应 `knowledge.chunks`。
- `qa.jsonl`：标准问题、答案、证据和 split。
- `raw/`：每个版本的 Markdown 原文。
- `postgres_import.sql`：映射现有本地 schema 的幂等开发导入脚本，包含固定的合成 tenant/member 身份种子，因此可在完成迁移的空库中直接执行。
- `statistics.json`、`manifest.json`、`validation-report.json`：统计、哈希和质量门禁。

## 生成与验证

```powershell
python ops/knowledge_dataset/generate.py
python ops/knowledge_dataset/validate.py
```

生成器只使用 Python 标准库和固定 UUID5/seed。相同版本连续运行应得到相同 `release_hash`。SQL 文件只用于本地开发环境；它不是生产迁移。

## RAG 使用边界

该版本用于先跑通单企业 RAG，不是 Agent Memory 数据集。评测时只索引 `active` 文档的 `current published` 版本；`superseded` chunk 只用于验证旧版本不能污染现行答案。无答案问题应明确拒答，不得让模型脱离证据自由回答。
