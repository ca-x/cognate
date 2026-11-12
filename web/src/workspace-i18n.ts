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
  "打开导航": "Open navigation",
  "选择要前往的工作区页面。": "Choose a workspace page to open.",
  "快速导航": "Quick navigation",
  "查找功能并跳转，不搜索或读取私人技能内容。": "Find a page to open. This does not search or read private skill content.",
  "查找页面": "Find a page",
  "清空页面搜索": "Clear page search",
  "仓库、AI 配置、备份…": "Repositories, AI configuration, backups…",
  "没有匹配的页面，试试其他名称。": "No matching pages. Try another name.",
  "当前页面": "Current page",
  "当前账号": "Signed in as",
  "Tab 选择页面，Enter 前往；Esc 清空搜索，再按关闭。": "Tab to a page, Enter to open it. Esc clears search, then closes.",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
