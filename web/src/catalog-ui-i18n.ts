import i18n from "./i18n"

const english = {
  "快速分类": "Quick categories",
  "分类筛选": "Category filters",
  "全部技能": "All skills",
  "清空技能搜索": "Clear skill search",
  "技能显示方式": "Skill display",
  "卡片视图": "Card view",
  "列表视图": "List view",
  "卡片": "Cards",
  "列表": "List",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
