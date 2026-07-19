from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parent


GROUPS = [
    ("enterprise.knowledge", [
        ("search", "检索企业知识", "制度检索", "查询差旅报销制度"),
        ("summarize", "总结知识文档", "文档归纳", "总结安全响应规范"),
        ("compare_versions", "比较文档版本", "版本差异", "比较采购制度两个版本"),
        ("find_owner", "查找知识负责人", "制度负责人", "谁负责数据分级制度"),
        ("list_sources", "列出知识来源", "来源清单", "列出答案对应的制度来源"),
    ]),
    ("calendar", [
        ("event.create", "创建日程", "新建会议日程", "创建明天下午的项目例会"),
        ("event.list", "查询日程", "日程列表", "查看本周日程"),
        ("event.reschedule", "调整日程", "改期", "把项目例会改到周五"),
        ("availability.find", "查询忙闲", "空闲时间", "查找三个人共同空闲时间"),
        ("room.reserve", "预订会议室", "会议室预订", "预订八楼会议室"),
    ]),
    ("collaboration.task", [
        ("create", "创建任务", "新增待办", "创建一项发布检查任务"),
        ("list", "查询任务", "待办列表", "列出我未完成的任务"),
        ("update", "更新任务", "修改待办", "更新发布任务截止日期"),
        ("assign", "分配任务", "任务负责人", "把测试任务分配给质量负责人"),
        ("complete", "完成任务", "标记完成", "将发布检查标记完成"),
    ]),
    ("collaboration.ticket", [
        ("create", "创建协作工单", "新建工单", "创建工单：复核迁移计划"),
        ("get", "查询工单", "工单详情", "查看工单当前状态"),
        ("comment", "评论工单", "工单备注", "给迁移工单添加复核意见"),
        ("close", "关闭工单", "工单关闭", "关闭已经处理完的工单"),
    ]),
    ("docs.document", [
        ("create", "创建云文档", "新建文档", "创建项目复盘文档"),
        ("search", "搜索云文档", "文档搜索", "搜索季度规划文档"),
        ("summarize", "总结云文档", "文档摘要", "总结项目复盘文档"),
        ("share", "共享云文档", "文档权限", "把复盘文档共享给项目组"),
        ("export", "导出云文档", "文档导出", "把复盘文档导出为 PDF"),
    ]),
    ("meeting", [
        ("schedule", "安排视频会议", "会议安排", "安排一次线上评审会议"),
        ("join", "加入视频会议", "进入会议", "加入正在进行的评审会议"),
        ("transcript.get", "获取会议转写", "会议逐字稿", "读取今天评审会的逐字稿"),
        ("summary.generate", "生成会议纪要", "会议总结", "生成评审会议纪要"),
        ("action_items.extract", "提取会议待办", "会议行动项", "从会议纪要提取行动项"),
    ]),
    ("directory", [
        ("people.search", "搜索企业成员", "员工搜索", "查找平台研发负责人"),
        ("department.list", "查询部门", "部门目录", "列出研发中心的部门"),
        ("profile.get", "查看成员资料", "员工资料", "查看质量负责人的企业资料"),
    ]),
    ("agent", [
        ("delegate", "委派后台 Agent", "后台委派", "委派知识助手汇总安全制度"),
        ("catalog.list", "查询 Agent 目录", "助手列表", "列出可用的企业 Agent"),
        ("run.status", "查询 Agent 运行", "运行状态", "查看后台 Agent 任务状态"),
        ("memory.forget", "删除 Agent 记忆", "遗忘记忆", "删除关于通知偏好的记忆"),
    ]),
]


def catalog() -> list[dict[str, object]]:
    result: list[dict[str, object]] = []
    for prefix, entries in GROUPS:
        for suffix, name, unique_term, example in entries:
            operation_id = f"{prefix}.{suffix}"
            result.append({
                "operation_id": operation_id,
                "name": name,
                "summary": f"在企业协作平台中{name}，并遵守当前身份、权限、审计和幂等约束",
                "parameter_terms": [unique_term, name, operation_id.replace(".", " ")],
                "examples": [example],
                "output_kinds": ["text", "data"],
            })
    if len(result) != 36:
        raise RuntimeError(f"expected 36 operations, got {len(result)}")
    return result


def intent(operation: dict[str, object], *, required: bool = True) -> dict[str, object]:
    return {
        "schema_version": "1",
        "rewritten_intent": f"使用 {operation['operation_id']} 完成 {operation['name']}",
        "hypothetical_capability": f"企业协作能力 {operation['parameter_terms'][0]} 负责 {operation['summary']}",
        "required_inputs": ["request"] if required else [],
        "missing_required_inputs": [],
        "unresolved_references": [],
        "desired_outputs": ["data"],
        "tool_requirement": "required",
    }


def cases(operations: list[dict[str, object]]) -> list[dict[str, object]]:
    result: list[dict[str, object]] = []
    for operation in operations:
        operation_id = str(operation["operation_id"])
        contents = [
            f"请执行操作 {operation_id}",
            f"我需要{operation['name']}",
            f"请处理{operation['parameter_terms'][0]}",
            str(operation["examples"][0]),
            "请根据当前业务上下文完成这项工作",
        ]
        for index, content in enumerate(contents, start=1):
            result.append({
                "case_id": f"{operation_id.replace('.', '-')}-{index}",
                "content": content,
                "intent_view": intent(operation),
                "expected_status": "selected",
                "expected_operation_id": operation_id,
            })
    for index, reference in enumerate(["它", "那个事项", "之前的内容", "这件事", "上面的任务"], start=1):
        result.append({
            "case_id": f"clarify-reference-{index}",
            "content": f"把{reference}处理一下",
            "intent_view": {
                "schema_version": "1", "rewritten_intent": "处理未解析的业务目标",
                "hypothetical_capability": "需要解析目标后才能选择企业协作能力",
                "required_inputs": ["target"], "missing_required_inputs": ["target"],
                "unresolved_references": [reference], "desired_outputs": ["data"],
                "tool_requirement": "required",
            },
            "expected_status": "clarify", "expected_operation_id": None,
        })
    for index, content in enumerate(["你好", "谢谢", "讲个笑话", "你是谁", "今天心情不错"], start=1):
        result.append({
            "case_id": f"no-tool-{index}", "content": content,
            "intent_view": {
                "schema_version": "1", "rewritten_intent": "普通对话无需外部能力",
                "hypothetical_capability": "直接生成自然语言回复",
                "required_inputs": [], "missing_required_inputs": [], "unresolved_references": [],
                "desired_outputs": ["text"], "tool_requirement": "none",
            },
            "expected_status": "no_tool", "expected_operation_id": None,
        })
    if len(result) != 190:
        raise RuntimeError(f"expected 190 cases, got {len(result)}")
    return result


def main() -> None:
    operations = catalog()
    (ROOT / "routing_catalog.json").write_text(
        json.dumps(operations, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    (ROOT / "routing_cases.jsonl").write_text(
        "".join(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n" for case in cases(operations)),
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
