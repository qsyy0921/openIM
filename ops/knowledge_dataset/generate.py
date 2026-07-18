#!/usr/bin/env python3
"""Generate the deterministic Xinglan single-enterprise Chinese RAG dataset."""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import statistics
import uuid
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Any


GENERATOR_VERSION = "1.0.0"
DATASET_VERSION = "1.0.0"
RELEASE_DATE = "2026-07-14"
SEED = 20260714
TENANT_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
MEMBER_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
NAMESPACE = uuid.UUID("77ae33c3-7664-4e75-b6d4-bf96643f27a1")
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "datasets" / "enterprise-knowledge" / "v1"


@dataclass(frozen=True)
class Domain:
    code: str
    name: str
    owner: str
    approver: str
    system: str
    escalation: str
    objective: str
    topics: tuple[str, ...]


DOMAINS: tuple[Domain, ...] = (
    Domain("GOV", "公司治理", "总裁办公室", "总经理", "治理决策台", "经营管理委员会", "确保授权、决策和经营风险可追溯", ("授权审批矩阵", "制度文件控制", "经营例会管理", "经营风险登记", "内部审计整改", "业务连续性治理", "印章授权管理", "企业档案保留")),
    Domain("HR", "人力资源", "人力资源部", "人力资源负责人", "人力云", "分管副总经理", "确保员工全生命周期规则统一且可审计", ("招聘录用", "员工入职", "试用期转正", "请假休假", "考勤异常", "差旅派遣", "绩效评估", "员工离职")),
    Domain("FIN", "财务管理", "财务部", "财务负责人", "财务云", "财务管理委员会", "确保资金、票据和核算过程准确合规", ("费用报销", "差旅报销", "年度预算", "对公付款", "发票管理", "固定资产核算", "月度结账", "坏账准备")),
    Domain("PROC", "采购供应", "采购部", "采购负责人", "采购云", "采购评审委员会", "确保供应商和采购活动透明可复核", ("采购申请", "供应商准入", "询价比价", "合同采购", "到货验收", "供应商评价", "紧急采购", "合同续约")),
    Domain("LEGAL", "法务合规", "法务部", "法务负责人", "合同法务台", "合规委员会", "控制合同、知识产权和隐私合规风险", ("合同审查", "保密协议", "知识产权登记", "个人信息保护", "争议案件处理", "外部律师聘用", "法律文件用印", "法务档案管理")),
    Domain("SEC", "信息安全", "信息安全部", "首席信息安全官", "安全运营中心", "安全应急指挥组", "保护身份、数据、终端和审计证据", ("访问权限控制", "数据分级分类", "漏洞管理", "安全事件响应", "终端安全", "密钥凭据管理", "安全日志审计", "第三方安全评估")),
    Domain("IT", "IT运维", "信息技术部", "信息技术负责人", "服务工单中心", "重大故障指挥组", "保障企业系统稳定、可恢复和容量充足", ("账号开通", "服务请求", "生产变更", "故障处理", "备份恢复", "容量管理", "办公网络", "灾难恢复演练")),
    Domain("RD", "研发工程", "研发效能部", "研发负责人", "研发协作平台", "技术委员会", "保障软件交付质量和工程过程可追溯", ("需求评审", "架构评审", "编码规范", "分支管理", "代码评审", "测试准入", "版本发布", "技术债治理")),
    Domain("PROD", "产品管理", "产品部", "产品负责人", "产品规划台", "产品决策委员会", "让产品决策基于证据并形成闭环", ("用户研究", "产品路线图", "需求文档", "交互设计评审", "产品实验", "灰度测试", "发布说明", "产品退市")),
    Domain("SALES", "销售管理", "销售运营部", "销售负责人", "客户关系平台", "商业决策委员会", "规范商机、价格和收入预测过程", ("销售线索", "商机阶段", "产品报价", "折扣审批", "合同交接", "销售预测", "渠道伙伴", "赢单复盘")),
    Domain("CS", "客户成功", "客户成功部", "客户成功负责人", "客户成功平台", "客户体验委员会", "提升客户采用、支持和续约质量", ("客户上线", "客户健康度", "支持服务等级", "重大客诉升级", "客户续约", "客户培训", "季度业务回顾", "流失复盘")),
    Domain("PM", "项目管理", "项目管理办公室", "项目管理负责人", "项目组合平台", "项目治理委员会", "统一项目范围、风险、变更和验收过程", ("项目立项", "项目计划", "项目风险", "范围变更", "项目例会", "项目验收", "项目复盘", "项目组合评审")),
    Domain("ADM", "行政管理", "行政部", "行政负责人", "行政服务台", "综合保障委员会", "保障办公场所、资产和现场服务有序运行", ("办公门禁", "访客接待", "会议室管理", "办公资产领用", "办公用品申领", "商务出行预订", "突发事件疏散", "公务车辆使用")),
)

FORMS = ("policy", "sop", "faq", "runbook", "decision")
FORM_NAMES = {
    "policy": "管理制度",
    "sop": "标准操作规程",
    "faq": "常见问题手册",
    "runbook": "异常处置手册",
    "decision": "专题决策纪要",
}
SCENARIO_PATTERNS = (
    "季度计划场景中，业务团队会在月末集中提交多笔事项。归口部门需要先合并重复申请，再按业务日期排序，确保批量处理不会掩盖单笔事项的批准责任和异常原因。",
    "紧急时限场景中，外部监管或客户交付日期可能早于正常完成时限。经办人必须说明外部截止日期及其来源，批准人只能压缩等待环节，不能省略风险检查和结果留痕。",
    "跨部门协作场景中，申请会同时影响产品、研发和客户团队。主流程只保留一个责任部门，其他团队通过关联任务提供意见，避免多人并行修改同一批准结论。",
    "外部主体参与场景中，供应商、客户或合作伙伴只能提交约定材料，不能访问企业内部审批意见。对外发送的结果必须经过脱敏检查，并记录发送对象、时间和文件版本。",
    "批量操作场景中，一次申请可能覆盖多个对象。经办人必须附对象清单和逐项结果，抽样通过不能代替全量执行，失败对象要单独标记并进入后续处理队列。",
    "系统迁移场景中，新旧平台可能短期并行。迁移期间以新平台生成的主编号为准，旧系统记录作为附件关联；发现状态不一致时暂停关闭动作并提交数据校正单。",
    "审计整改场景中，审计人员会追溯申请、批准、执行和验证四个时间点。责任部门必须针对缺失证据建立整改措施、责任角色和完成日期，不能只补写结论。",
    "连续性事件场景中，办公地点或核心系统可能暂时不可用。团队先维持必要业务和安全控制，恢复后按离线编号顺序补录，并由独立复核人确认没有重复执行或遗漏事项。",
)
FORM_REVIEW_FOCUS = {
    "policy": "制度审查重点是授权边界、强制规则和例外是否相互矛盾。",
    "sop": "操作规程审查重点是输入、交接、完成条件和恢复步骤能否被一线人员重复执行。",
    "faq": "问答手册审查重点是答案是否与现行制度一致，不能把常见做法写成新的批准权限。",
    "runbook": "处置手册审查重点是止损、升级、恢复和复盘之间是否形成闭环。",
    "decision": "决策纪要审查重点是结论、行动项、负责人和复核日期是否明确。",
}
RESPONSE_HOURS = (2, 4, 8, 12, 24, 36, 48, 72)
COMPLETION_DAYS = (1, 2, 3, 5, 7, 10, 15, 20)
RETENTION_YEARS = (2, 3, 5, 7, 10)
REVIEW_MONTHS = (3, 6, 12)
AMOUNT_THRESHOLDS = (5_000, 10_000, 20_000, 50_000, 100_000, 200_000, 500_000, 1_000_000)
MONEY_DOMAINS = {"FIN", "PROC", "LEGAL", "SALES", "ADM"}
TECH_DOMAINS = {"SEC", "IT", "RD", "PROD", "CS"}


def stable_uuid(kind: str, key: str) -> str:
    return str(uuid.uuid5(NAMESPACE, f"{kind}:{key}"))


def document_title(topic_name: str, form: str) -> str:
    if form == "policy" and topic_name.endswith("管理"):
        return f"{topic_name}制度"
    return f"{topic_name}{FORM_NAMES[form]}"


def digest_text(value: str) -> str:
    return "sha256:" + hashlib.sha256(value.encode("utf-8")).hexdigest()


def write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    with path.open("w", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")


def sql_literal(value: str | None) -> str:
    if value is None:
        return "NULL"
    return "'" + value.replace("'", "''") + "'"


def split_for(key: str) -> str:
    bucket = int(hashlib.sha256(key.encode("utf-8")).hexdigest()[:8], 16) % 100
    return "train" if bucket < 70 else "dev" if bucket < 85 else "test"


def escalation_condition(domain: Domain, topic_index: int, completion_days: int) -> str:
    if domain.code in MONEY_DOMAINS:
        return f"单笔金额达到{AMOUNT_THRESHOLDS[topic_index]:,}元或涉及框架外供应商"
    if domain.code in TECH_DOMAINS:
        users = (topic_index + 1) * 100
        minutes = (topic_index + 1) * 15
        return f"影响用户达到{users}人或持续超过{minutes}分钟"
    if domain.code in {"RD", "PROD", "PM"}:
        return f"计划延期达到{completion_days}个工作日或影响两个以上团队"
    people = (topic_index + 1) * 10
    return f"涉及{people}名员工或跨越两个以上部门"


def build_topics() -> list[dict[str, Any]]:
    topics: list[dict[str, Any]] = []
    for domain_index, domain in enumerate(DOMAINS):
        for topic_index, topic_name in enumerate(domain.topics):
            response_hours = RESPONSE_HOURS[(domain_index + topic_index) % len(RESPONSE_HOURS)]
            completion_days = COMPLETION_DAYS[(domain_index * 2 + topic_index) % len(COMPLETION_DAYS)]
            retention_years = RETENTION_YEARS[(domain_index + topic_index) % len(RETENTION_YEARS)]
            review_months = REVIEW_MONTHS[(domain_index + topic_index) % len(REVIEW_MONTHS)]
            process_code = f"{domain.code}-{topic_index + 1:02d}"
            topics.append(
                {
                    "topic_id": stable_uuid("topic", process_code),
                    "topic_key": process_code.lower(),
                    "topic_index": topic_index + 1,
                    "process_code": process_code,
                    "domain_code": domain.code,
                    "domain_name": domain.name,
                    "topic_name": topic_name,
                    "owner": domain.owner,
                    "approver": domain.approver,
                    "system": domain.system,
                    "escalation_role": domain.escalation,
                    "objective": domain.objective,
                    "response_hours": response_hours,
                    "completion_days": completion_days,
                    "retention_years": retention_years,
                    "review_months": review_months,
                    "escalation_condition": escalation_condition(domain, topic_index, completion_days),
                    "evidence_required": f"申请单、审批记录、处理日志和{topic_name}结果单",
                    "effective_date": f"2026-{(domain_index % 6) + 1:02d}-01",
                    "old_response_hours": min(96, response_hours + 8),
                    "old_completion_days": completion_days + 2,
                    "old_system": "邮件和共享表格",
                }
            )
    return topics


def render_sections(topic: dict[str, Any], form: str, historical: bool = False) -> list[tuple[str, str]]:
    name = topic["topic_name"]
    code = topic["process_code"]
    owner = topic["owner"]
    approver = topic["approver"]
    system = topic["old_system"] if historical else topic["system"]
    response = topic["old_response_hours"] if historical else topic["response_hours"]
    days = topic["old_completion_days"] if historical else topic["completion_days"]
    retention = topic["retention_years"]
    review = topic["review_months"]
    condition = topic["escalation_condition"]
    escalation = topic["escalation_role"]
    evidence = topic["evidence_required"]

    if form == "policy":
        sections = [
            ("目的与适用范围", f"{code}用于规范星澜智协科技有限公司的{name}活动。适用于总部、研发中心和区域团队，目标是{topic['objective']}。任何口头约定均不能替代本制度记录。"),
            ("职责与授权", f"{name}流程的归口责任部门是{owner}。最终批准角色是{approver}。经办人负责材料真实性，归口部门负责时限、证据和例外记录，批准角色不得由经办人代签。"),
            ("控制指标", f"所有{name}申请必须进入{system}，归口部门须在{response}小时内确认受理，并在{days}个工作日内完成或给出书面延期说明。流程编号必须使用{code}前缀。"),
            ("记录与复核", f"必须保存{evidence}，记录保留{retention}年。{owner}每{review}个月复核一次样本，复核结论写入治理决策台，不允许只保留聊天截图。"),
            ("升级与例外", f"当{condition}时，必须升级至{escalation}。紧急情况可以先采取止损措施，但须在24小时内补齐申请、批准和影响说明；例外不能改变后续审计要求。"),
        ]
        if not historical:
            sections.append(("版本变更", f"现行版本自{topic['effective_date']}生效，替代旧版的“{topic['old_system']}、{topic['old_response_hours']}小时受理、{topic['old_completion_days']}个工作日完成”规则。当前统一改为{system}、{response}小时受理、{days}个工作日完成。"))
        return sections

    if form == "sop":
        return [
            ("启动条件", f"经办人在{name}事项事实已明确、材料可核验后启动{code}流程。提交前必须确认申请主体、业务目的、影响范围和期望完成日期。"),
            ("办理步骤", f"第一步，在{system}创建{code}申请并上传{evidence}；第二步，{owner}在{response}小时内完成受理检查；第三步，由{approver}作出批准或退回决定；第四步，经办人在{days}个工作日内完成执行和结果登记。"),
            ("质量检查", f"办结前检查编号、批准人、时间戳、附件和结果单是否完整。缺少任一项不得标记完成，补件过程仍计入{name}流程时长。"),
            ("例外处理", f"如出现{condition}，经办人立即在{system}标记高风险并通知{escalation}。系统不可用时使用连续编号的离线表单，恢复后4小时内补录。"),
            ("归档与关闭", f"{owner}确认结果后关闭流程，将全部记录保留{retention}年，并在每{review}个月的抽样复核中检查完成时限和例外依据。"),
        ]

    if form == "faq":
        return [
            ("提交与受理", f"问：{name}应从哪里提交？答：统一从{system}提交，流程编号为{code}。问：多久确认受理？答：{owner}必须在{response}小时内确认。"),
            ("审批与办结", f"问：谁作最终批准？答：{approver}。问：正常多久完成？答：受理后{days}个工作日内完成，不能按期完成时必须给出书面延期说明。"),
            ("材料与保留", f"问：需要保留哪些材料？答：{evidence}。问：保留多久？答：自流程关闭之日起保留{retention}年。"),
            ("升级场景", f"问：什么时候必须升级？答：当{condition}时升级至{escalation}，不得由经办人自行豁免。"),
            ("常见误区", f"聊天确认、口头同意和未编号邮件不能替代{name}正式记录。紧急处理也必须在24小时内补齐记录，系统恢复后还需完成补录。"),
        ]

    if form == "runbook":
        return [
            ("触发条件", f"当{name}流程超时、材料失真、系统不可用，或满足“{condition}”任一条件时，启动{code}异常处置。"),
            ("即时动作", f"值班人员先冻结不可逆操作，记录时间、影响范围和当前负责人，并在30分钟内通知{owner}。若系统不可用，启用连续编号的离线表单。"),
            ("分级升级", f"普通异常由{owner}处理；满足升级条件时由{escalation}统一指挥。涉及批准有效性的事项必须重新提交{approver}确认，任何人不得补造时间戳。"),
            ("恢复验证", f"恢复后在{system}核对申请、批准、执行和结果四类记录，确认流程编号以{code}开头。遗漏数据须在4小时内补录并标记为恢复补录。"),
            ("复盘关闭", f"异常关闭后5个工作日内完成根因和改进项，证据与{name}主记录一并保留{retention}年。重复异常进入每{review}个月的专题复核。"),
        ]

    if form == "decision":
        return [
            ("会议背景", f"星澜智协于{topic['effective_date']}召开{name}专题会，参会角色包括{owner}、{approver}和审计观察员，会议依据流程{code}讨论执行一致性。"),
            ("已作决策", f"会议确认{name}继续由{owner}归口，所有申请统一进入{system}，受理时限固定为{response}小时，正常完成时限固定为{days}个工作日。"),
            ("风险决策", f"会议确认“{condition}”为强制升级条件，升级接收方为{escalation}。未经记录的口头豁免无效，紧急止损后24小时内必须补齐证据。"),
            ("行动项", f"{owner}负责在30日内抽查20个{name}样本，{approver}负责审阅异常清单，平台管理员负责确保{system}保留完整时间戳。"),
            ("跟踪安排", f"专题结论每{review}个月复核一次；{evidence}至少保留{retention}年。下一次复核只接受系统记录和签署结果单作为证据。"),
        ]

    raise ValueError(f"unsupported form: {form}")


def render_document(topic: dict[str, Any], form: str, version_number: int, historical: bool) -> tuple[str, list[tuple[str, str]]]:
    title = document_title(topic["topic_name"], form)
    status_note = "历史已废止版本，仅用于版本检索测试" if historical else "现行发布版本"
    sections = []
    implementation_notes = (
        f"执行边界：本节属于{topic['domain_name']}领域，只处理{topic['topic_name']}事项；跨领域前置条件必须关联原流程编号，不得在本流程中重复批准。",
        f"责任交接：每次转交都要记录交出角色、接收角色、时间和待办事项；{topic['owner']}对流程完整性负责，但不能替代{topic['approver']}作出最终批准。",
        f"时限计量：工作时限从材料齐备并进入{topic['system']}时开始计算；退回补正期间暂停计时，重新提交后保留原{topic['process_code']}关联关系。",
        f"证据质量：附件应可读取、可校验并与{topic['process_code']}一致；只有结论而没有原始申请、审批或结果记录的，不视为有效证据。",
        f"审计要求：抽查人员应独立于原经办人，发现规则绕过、时间戳缺失或结果不一致时，必须建立整改项并跟踪至关闭。",
        f"版本解释：检索和答复必须优先使用现行发布版本；历史版本仅用于解释变化，不能作为当前业务执行依据。",
    )
    for index, (heading, text) in enumerate(render_sections(topic, form, historical)):
        enrichment = text + "\n\n" + implementation_notes[index]
        if index == 0:
            scenario = SCENARIO_PATTERNS[topic["topic_index"] - 1]
            enrichment += (
                f"\n\n典型业务场景：围绕{topic['topic_name']}，{scenario}"
                f"{FORM_REVIEW_FOCUS[form]}该场景的处理结果仍须回到{topic['system']}并关联{topic['process_code']}。"
            )
        sections.append((heading, enrichment))
    header = (
        f"# {title}\n\n"
        f"> 文档编号：{topic['process_code']}-{form.upper()}\n"
        f"> 版本：v{version_number}\n"
        f"> 状态：{status_note}\n"
        f"> 企业：星澜智协科技有限公司（虚构）\n"
    )
    body = header + "\n" + "\n\n".join(f"## {heading}\n\n{text}" for heading, text in sections) + "\n"
    return body, sections


def evidence_item(resource: dict[str, Any], section_name: str, quote: str) -> dict[str, Any]:
    section = resource["sections"][section_name]
    if quote not in section["content"]:
        raise ValueError(f"evidence quote missing from {resource['document_id']}:{section_name}: {quote}")
    return {
        "document_id": resource["document_id"],
        "version_id": resource["version_id"],
        "chunk_id": section["chunk_id"],
        "source_uri": resource["source_uri"],
        "title": resource["title"],
        "quote": quote,
    }


def qa_record(topic: dict[str, Any], index: int, qa_type: str, question: str, answer: str, evidence: list[dict[str, Any]], required_facts: list[str], negative_version_ids: list[str] | None = None) -> dict[str, Any]:
    key = f"{topic['process_code']}:{index}:{qa_type}"
    return {
        "qa_id": stable_uuid("qa", key),
        "split": split_for(key),
        "type": qa_type,
        "domain_code": topic["domain_code"],
        "domain_name": topic["domain_name"],
        "topic_id": topic["topic_id"],
        "topic_name": topic["topic_name"],
        "question": question,
        "answer": answer,
        "answerable": True,
        "evidence": evidence,
        "required_facts": required_facts,
        "negative_version_ids": negative_version_ids or [],
    }


def build_qas(topic: dict[str, Any], resources: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    name = topic["topic_name"]
    owner = topic["owner"]
    approver = topic["approver"]
    system = topic["system"]
    response = topic["response_hours"]
    days = topic["completion_days"]
    retention = topic["retention_years"]
    review = topic["review_months"]
    condition = topic["escalation_condition"]
    escalation = topic["escalation_role"]
    code = topic["process_code"]
    policy, sop, faq, runbook, decision = (resources[key] for key in FORMS)
    records = [
        qa_record(topic, 1, "single_document", f"{name}流程由哪个部门归口，谁负责最终批准？", f"{name}由{owner}归口，最终批准角色是{approver}。", [evidence_item(policy, "职责与授权", f"{name}流程的归口责任部门是{owner}。"), evidence_item(policy, "职责与授权", f"最终批准角色是{approver}。")], [owner, approver]),
        qa_record(topic, 2, "single_document", f"{name}申请应该在哪个系统提交，编号前缀是什么？", f"应在{system}提交，编号使用{code}前缀。", [evidence_item(sop, "办理步骤", f"在{system}创建{code}申请")], [system, code]),
        qa_record(topic, 3, "numeric", f"{name}的受理确认和正常完成时限分别是多少？", f"{owner}应在{response}小时内确认受理，并在{days}个工作日内完成或给出书面延期说明。", [evidence_item(policy, "控制指标", f"归口部门须在{response}小时内确认受理，并在{days}个工作日内完成或给出书面延期说明。")], [f"{response}小时", f"{days}个工作日"]),
        qa_record(topic, 4, "single_document", f"{name}材料需要保留几年，谁负责周期复核？", f"材料保留{retention}年，由{owner}每{review}个月复核一次。", [evidence_item(policy, "记录与复核", f"记录保留{retention}年。"), evidence_item(policy, "记录与复核", f"{owner}每{review}个月复核一次样本")], [f"{retention}年", owner, f"{review}个月"]),
        qa_record(topic, 5, "procedure", f"请概括{name}从提交到关闭的主要步骤。", f"在{system}提交{code}申请，{owner}受理，{approver}批准，经办人在{days}个工作日内执行登记，最后由{owner}归档关闭。", [evidence_item(sop, "办理步骤", f"第一步，在{system}创建{code}申请"), evidence_item(sop, "归档与关闭", f"{owner}确认结果后关闭流程")], [system, code, owner, approver, f"{days}个工作日"]),
        qa_record(topic, 6, "single_document", f"{name}在哪些情况下必须升级，升级给谁？", f"当{condition}时，必须升级至{escalation}。", [evidence_item(runbook, "触发条件", condition), evidence_item(runbook, "分级升级", f"满足升级条件时由{escalation}统一指挥")], [condition, escalation]),
        qa_record(topic, 7, "multi_document", f"{name}的提交系统、受理责任人和异常升级角色分别是什么？", f"提交系统是{system}，受理责任部门是{owner}，异常满足条件时由{escalation}处理。", [evidence_item(faq, "提交与受理", f"统一从{system}提交"), evidence_item(policy, "职责与授权", f"归口责任部门是{owner}"), evidence_item(runbook, "分级升级", f"由{escalation}统一指挥")], [system, owner, escalation]),
        qa_record(topic, 8, "multi_document", f"{name}的复核周期和专题会确定的抽样行动是什么？", f"制度每{review}个月复核一次，专题会要求{owner}在30日内抽查20个样本。", [evidence_item(policy, "记录与复核", f"每{review}个月复核一次样本"), evidence_item(decision, "行动项", f"{owner}负责在30日内抽查20个{name}样本")], [f"{review}个月", "30日", "20个", owner]),
        qa_record(topic, 9, "decision_trace", f"{name}专题会确认了哪些核心执行指标？", f"专题会确认由{owner}归口，统一使用{system}，{response}小时内受理，{days}个工作日内完成。", [evidence_item(decision, "已作决策", f"{name}继续由{owner}归口"), evidence_item(decision, "已作决策", f"所有申请统一进入{system}"), evidence_item(decision, "已作决策", f"受理时限固定为{response}小时，正常完成时限固定为{days}个工作日")], [owner, system, f"{response}小时", f"{days}个工作日"]),
        qa_record(topic, 10, "version_awareness", f"{name}现行版本相对旧版改变了什么？", f"旧版使用{topic['old_system']}，要求{topic['old_response_hours']}小时受理、{topic['old_completion_days']}个工作日完成；现行版改为{system}、{response}小时和{days}个工作日。", [evidence_item(policy, "版本变更", f"替代旧版的“{topic['old_system']}、{topic['old_response_hours']}小时受理、{topic['old_completion_days']}个工作日完成”规则。"), evidence_item(policy, "版本变更", f"当前统一改为{system}、{response}小时受理、{days}个工作日完成。")], [topic["old_system"], f"{topic['old_response_hours']}小时", f"{topic['old_completion_days']}个工作日", system, f"{response}小时", f"{days}个工作日"], [resources["policy_history"]["version_id"]]),
    ]
    return records


def build_unanswerable(topics: list[dict[str, Any]], count: int = 80) -> list[dict[str, Any]]:
    requests = (
        "该流程负责人本人的私人手机号码是多少？",
        "请提供经办员工的个人工资明细。",
        "系统管理员当前使用的登录密码是什么？",
        "下一年度尚未批准的预算精确金额是多少？",
        "请列出真实客户的身份证号码。",
        "本周五食堂每一道菜的采购单价是多少？",
        "尚未发布的组织调整名单有哪些人？",
        "请给出未记录会议中总经理的口头承诺原文。",
    )
    rows: list[dict[str, Any]] = []
    for index in range(count):
        topic = topics[index % len(topics)]
        request = requests[index % len(requests)]
        question = f"关于{topic['topic_name']}，{request}"
        key = f"unanswerable:{topic['process_code']}:{index}"
        rows.append(
            {
                "qa_id": stable_uuid("qa", key),
                "split": split_for(key),
                "type": "unanswerable",
                "domain_code": topic["domain_code"],
                "domain_name": topic["domain_name"],
                "topic_id": topic["topic_id"],
                "topic_name": topic["topic_name"],
                "question": question,
                "answer": "知识库中没有可引用的证据，无法回答该问题。",
                "answerable": False,
                "evidence": [],
                "required_facts": [],
                "negative_version_ids": [],
            }
        )
    return rows


def generate(output: Path) -> dict[str, Any]:
    tmp = output.with_name(output.name + ".tmp")
    if tmp.exists():
        shutil.rmtree(tmp)
    tmp.mkdir(parents=True)
    raw_root = tmp / "raw"

    company_profile = {
        "dataset_notice": "全部企业、产品、事件和组织信息均为合成数据，不对应任何真实公司。",
        "legal_name": "星澜智协科技有限公司",
        "company_code": "XLZX",
        "timezone": "Asia/Shanghai",
        "currency": "CNY",
        "sites": ["北京总部", "上海研发中心", "深圳客户中心"],
        "products": ["云枢协作平台", "星河知识助手", "企业集成网关"],
        "release_date": RELEASE_DATE,
        "tenant_id": TENANT_ID,
        "local_member_id": MEMBER_ID,
    }
    topics = build_topics()
    documents: list[dict[str, Any]] = []
    versions: list[dict[str, Any]] = []
    chunks: list[dict[str, Any]] = []
    qas: list[dict[str, Any]] = []

    for topic in topics:
        resources: dict[str, dict[str, Any]] = {}
        for form in FORMS:
            doc_key = f"{topic['topic_key']}:{form}"
            document_id = stable_uuid("document", doc_key)
            source_uri = f"knowledge://xinglan/{topic['domain_code'].lower()}/{topic['topic_key']}/{form}"
            title = document_title(topic["topic_name"], form)
            classification = "public" if form == "faq" and topic["domain_code"] in {"PROD", "CS"} else "internal"
            current_version_number = 2 if form == "policy" else 1
            current_version_id = stable_uuid("version", f"{doc_key}:v{current_version_number}")
            documents.append(
                {
                    "id": document_id,
                    "tenant_id": TENANT_ID,
                    "domain_code": topic["domain_code"],
                    "domain_name": topic["domain_name"],
                    "topic_id": topic["topic_id"],
                    "topic_name": topic["topic_name"],
                    "document_form": form,
                    "title": title,
                    "source_uri": source_uri,
                    "classification": classification,
                    "status": "active",
                    "current_version_id": current_version_id,
                }
            )
            version_numbers = (1, 2) if form == "policy" else (1,)
            for version_number in version_numbers:
                historical = form == "policy" and version_number == 1
                body, rendered_sections = render_document(topic, form, version_number, historical)
                version_id = stable_uuid("version", f"{doc_key}:v{version_number}")
                raw_path = Path("raw") / topic["domain_code"].lower() / f"{document_id}-v{version_number}.md"
                full_path = tmp / raw_path
                full_path.parent.mkdir(parents=True, exist_ok=True)
                full_path.write_text(body, encoding="utf-8", newline="\n")
                version_status = "superseded" if historical else "published"
                versions.append(
                    {
                        "id": version_id,
                        "tenant_id": TENANT_ID,
                        "document_id": document_id,
                        "version_number": version_number,
                        "checksum": digest_text(body),
                        "status": version_status,
                        "published_at": None if historical else f"{topic['effective_date']}T00:00:00+08:00",
                        "superseded_at": f"{topic['effective_date']}T00:00:00+08:00" if historical else None,
                        "raw_path": raw_path.as_posix(),
                        "character_count": len(body),
                    }
                )
                section_map: dict[str, dict[str, str]] = {}
                for ordinal, (heading, text) in enumerate(rendered_sections):
                    content = f"## {heading}\n\n{text}"
                    chunk_id = stable_uuid("chunk", f"{version_id}:{ordinal}")
                    chunks.append(
                        {
                            "id": chunk_id,
                            "tenant_id": TENANT_ID,
                            "document_id": document_id,
                            "version_id": version_id,
                            "ordinal": ordinal,
                            "heading": heading,
                            "content": content,
                            "checksum": digest_text(content),
                            "character_count": len(content),
                        }
                    )
                    section_map[heading] = {"chunk_id": chunk_id, "content": content}
                resource = {
                    "document_id": document_id,
                    "version_id": version_id,
                    "source_uri": source_uri,
                    "title": title,
                    "sections": section_map,
                }
                if historical:
                    resources["policy_history"] = resource
                else:
                    resources[form] = resource
        qas.extend(build_qas(topic, resources))

    qas.extend(build_unanswerable(topics))
    documents.sort(key=lambda row: (row["domain_code"], row["topic_name"], row["document_form"]))
    versions.sort(key=lambda row: (row["document_id"], row["version_number"]))
    chunks.sort(key=lambda row: (row["document_id"], row["version_id"], row["ordinal"]))
    qas.sort(key=lambda row: row["qa_id"])

    write_json(tmp / "company_profile.json", company_profile)
    write_jsonl(tmp / "canonical_facts.jsonl", topics)
    write_jsonl(tmp / "documents.jsonl", documents)
    write_jsonl(tmp / "document_versions.jsonl", versions)
    write_jsonl(tmp / "chunks.jsonl", chunks)
    write_jsonl(tmp / "qa.jsonl", qas)
    write_postgres_sql(tmp / "postgres_import.sql", documents, versions, chunks)

    current_lengths = [row["character_count"] for row in versions if row["status"] == "published"]
    chunk_lengths = [row["character_count"] for row in chunks]
    statistics_payload = {
        "dataset_version": DATASET_VERSION,
        "documents": len(documents),
        "versions": len(versions),
        "chunks": len(chunks),
        "qa_cases": len(qas),
        "domains": dict(sorted(Counter(row["domain_code"] for row in documents).items())),
        "document_forms": dict(sorted(Counter(row["document_form"] for row in documents).items())),
        "version_status": dict(sorted(Counter(row["status"] for row in versions).items())),
        "qa_types": dict(sorted(Counter(row["type"] for row in qas).items())),
        "qa_splits": dict(sorted(Counter(row["split"] for row in qas).items())),
        "published_document_characters": {
            "minimum": min(current_lengths),
            "median": statistics.median(current_lengths),
            "maximum": max(current_lengths),
            "total": sum(current_lengths),
        },
        "chunk_characters": {
            "minimum": min(chunk_lengths),
            "median": statistics.median(chunk_lengths),
            "maximum": max(chunk_lengths),
            "total": sum(chunk_lengths),
        },
    }
    write_json(tmp / "statistics.json", statistics_payload)
    (tmp / "README.md").write_text(dataset_readme(statistics_payload), encoding="utf-8", newline="\n")

    file_hashes = {}
    for path in sorted(item for item in tmp.rglob("*") if item.is_file()):
        relative = path.relative_to(tmp).as_posix()
        file_hashes[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
    manifest = {
        "dataset_name": "xinglan-enterprise-knowledge-qa",
        "dataset_version": DATASET_VERSION,
        "generator_version": GENERATOR_VERSION,
        "release_date": RELEASE_DATE,
        "seed": SEED,
        "synthetic": True,
        "language": "zh-CN",
        "tenant_count": 1,
        "counts": {"documents": len(documents), "versions": len(versions), "chunks": len(chunks), "qa_cases": len(qas)},
        "files": file_hashes,
        "release_hash": hashlib.sha256(json.dumps(file_hashes, sort_keys=True).encode("utf-8")).hexdigest(),
    }
    write_json(tmp / "manifest.json", manifest)
    if output.exists():
        shutil.rmtree(output)
    tmp.replace(output)
    return manifest


def write_postgres_sql(path: Path, documents: list[dict[str, Any]], versions: list[dict[str, Any]], chunks: list[dict[str, Any]]) -> None:
    lines = [
        "-- Generated synthetic single-enterprise knowledge fixture; not a production migration.",
        f"-- dataset={DATASET_VERSION} generator={GENERATOR_VERSION}",
        "BEGIN;",
    ]
    for row in documents:
        lines.append(
            "INSERT INTO knowledge.documents (id, tenant_id, title, source_uri, classification, status) VALUES "
            f"({sql_literal(row['id'])}::uuid, {sql_literal(row['tenant_id'])}::uuid, {sql_literal(row['title'])}, {sql_literal(row['source_uri'])}, {sql_literal(row['classification'])}, 'active') "
            "ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, source_uri=EXCLUDED.source_uri, classification=EXCLUDED.classification, status='active';"
        )
    for row in versions:
        published = "NULL" if row["published_at"] is None else f"{sql_literal(row['published_at'])}::timestamptz"
        lines.append(
            "INSERT INTO knowledge.document_versions (id, tenant_id, document_id, version_number, checksum, status, published_at) VALUES "
            f"({sql_literal(row['id'])}::uuid, {sql_literal(row['tenant_id'])}::uuid, {sql_literal(row['document_id'])}::uuid, {row['version_number']}, {sql_literal(row['checksum'])}, {sql_literal(row['status'])}, {published}) "
            "ON CONFLICT (id) DO UPDATE SET checksum=EXCLUDED.checksum, status=EXCLUDED.status, published_at=EXCLUDED.published_at;"
        )
    for row in chunks:
        lines.append(
            "INSERT INTO knowledge.chunks (id, tenant_id, document_id, version_id, ordinal, content, checksum) VALUES "
            f"({sql_literal(row['id'])}::uuid, {sql_literal(row['tenant_id'])}::uuid, {sql_literal(row['document_id'])}::uuid, {sql_literal(row['version_id'])}::uuid, {row['ordinal']}, {sql_literal(row['content'])}, {sql_literal(row['checksum'])}) "
            "ON CONFLICT (id) DO UPDATE SET content=EXCLUDED.content, checksum=EXCLUDED.checksum;"
        )
    for row in documents:
        lines.append(f"UPDATE knowledge.documents SET current_version_id={sql_literal(row['current_version_id'])}::uuid WHERE id={sql_literal(row['id'])}::uuid;")
        lines.append(
            "INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission) VALUES "
            f"({sql_literal(TENANT_ID)}::uuid, {sql_literal(row['id'])}::uuid, {sql_literal(MEMBER_ID)}::uuid, 'read') ON CONFLICT DO NOTHING;"
        )
    lines.extend(["COMMIT;", ""])
    path.write_text("\n".join(lines), encoding="utf-8", newline="\n")


def dataset_readme(stats: dict[str, Any]) -> str:
    return f"""# Xinglan Enterprise Knowledge QA v{DATASET_VERSION}

这是面向 OpenIM 企业知识问答 Pipeline 的中文合成数据集。企业“星澜智协科技有限公司”、组织、产品、系统、人员角色和事件全部为虚构内容，不来自真实公司的内部资料。

## 规模

- 13 个业务领域，104 个主题。
- {stats['documents']} 篇逻辑文档，五种文档形态保持均衡。
- {stats['versions']} 个版本，其中 104 个历史制度版本明确标记为 `superseded`。
- {stats['chunks']} 个语义章节 chunk。
- {stats['qa_cases']} 个 QA，包含单文档、多文档、流程、数值、决策追踪、版本意识和无答案问题。

## 文件

- `company_profile.json`：固定企业事实和本地测试身份。
- `canonical_facts.jsonl`：104 个主题的唯一事实源。
- `documents.jsonl`：对应 `knowledge.documents`。
- `document_versions.jsonl`：对应 `knowledge.document_versions`。
- `chunks.jsonl`：对应 `knowledge.chunks`。
- `qa.jsonl`：标准问题、答案、证据和 split。
- `raw/`：每个版本的 Markdown 原文。
- `postgres_import.sql`：映射现有本地 schema 的幂等开发导入脚本。
- `statistics.json`、`manifest.json`、`validation-report.json`：统计、哈希和质量门禁。

## 生成与验证

```powershell
python ops/knowledge_dataset/generate.py
python ops/knowledge_dataset/validate.py
```

生成器只使用 Python 标准库和固定 UUID5/seed。相同版本连续运行应得到相同 `release_hash`。SQL 文件只用于本地开发环境；它不是生产迁移。

## RAG 使用边界

该版本用于先跑通单企业 RAG，不是 Agent Memory 数据集。评测时只索引 `active` 文档的 `current published` 版本；`superseded` chunk 只用于验证旧版本不能污染现行答案。无答案问题应明确拒答，不得让模型脱离证据自由回答。
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    manifest = generate(args.output.resolve())
    counts = manifest["counts"]
    print(f"dataset_release_hash={manifest['release_hash']}")
    print(f"documents={counts['documents']} versions={counts['versions']} chunks={counts['chunks']} qa_cases={counts['qa_cases']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
