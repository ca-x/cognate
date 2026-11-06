import i18n from "./i18n"

const english = {
  "可保存来源，尚未下载或验证": "Ready to save as a source; not downloaded or verified",
  "同步后识别路径": "Path identified after synchronization",
  "仓库根目录": "Repository root",
  "仅保存私有来源，不下载仓库": "Save a private source without downloading the repository",
  "仅保存技能来源，并自动保存所属仓库；尚未下载或验证。": "Save the skill source and its repository automatically. Nothing has been downloaded or verified.",
  "仓库与未下载技能仅保存为私有来源，不会拉取仓库。只选技能也会自动保存所属仓库；导入后到“已保存来源”点击“同步技能”下载并验证。": "Repositories and unfetched skills are saved as private sources without fetching. Selecting only skills also saves their repositories. After importing, use Sync skills under Saved sources to download and verify them.",
  "已保存私有来源，尚未下载": "Private source saved, not downloaded",
  "私有来源已保存，可同步技能": "Private source already saved; ready to synchronize",
  "已保存的来源可在“已保存来源”面板中同步。来源引用尚未下载或验证，不是已安装技能，也不会自动加入组合。": "Use the Saved sources panel to synchronize saved references. Source references are not downloaded, verified, installed, or added to packs automatically.",
  "已创建私有配置资产": "Private configuration asset created",
  "导入未完成，本次未保存所选项目": "Import did not complete; no selected items were saved by this operation",
  "已保存来源，待同步": "Source saved, awaiting synchronization",
  "配置仅创建私有资产；仓库与未下载技能仅保存私有来源。只选技能也会保存所属仓库，不下载、不安装、不自动应用。": "Configurations create private assets only. Repositories and unfetched skills save private sources. Selecting only skills also saves their repositories. Nothing is downloaded, installed, or applied automatically.",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
