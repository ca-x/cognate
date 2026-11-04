import i18n from "./i18n"

const english = {
  "按 Agent 筛选": "Filter by agent",
  "全部 Agent": "All agents",
  "搜索配置资产": "Search configuration assets",
  "按名称或 Agent 搜索…": "Search by name or agent…",
  "没有匹配的配置资产": "No matching configuration assets",
  "清除筛选": "Clear filters",
  "技能工作区": "Skill workspace",
  "配置与管理": "Manage",
  "查看收录进度": "View collection progress",
  "从其他工具导入": "Import from other tools",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
