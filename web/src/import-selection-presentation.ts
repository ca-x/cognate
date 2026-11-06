import { selectionNotices, type SelectionPreview } from "./import-selection-state"
import { type ImportConfigurationCandidate, type ImportNoticeCode } from "./gen/skillhub/import/v1/import_pb"

export const importAgentLabels: Record<string, string> = { claude: "Claude Code", codex: "Codex", gemini: "Gemini CLI", grokbuild: "Grok Build", opencode: "OpenCode" }

export function configurationSummary(candidate: ImportConfigurationCandidate) {
  const payload = candidate.draft?.payload
  switch (payload?.case) {
    case "claudeProvider": return [payload.value.endpoint, payload.value.models?.defaultModel].filter(Boolean)
    case "codexProvider": return [payload.value.endpoint, payload.value.model].filter(Boolean)
    case "geminiProvider": return [payload.value.endpoint, payload.value.settingsModel || payload.value.envModel].filter(Boolean)
    case "grokbuildProvider": return [payload.value.model?.endpoint, payload.value.model?.upstreamModel].filter(Boolean)
    case "opencodeProvider": return payload.value.format.case === "v1" ? [payload.value.format.value.options?.baseUrl, ...Object.keys(payload.value.format.value.models?.entries ?? {})].filter(Boolean) : []
    default: return []
  }
}

export function groupSelectionNotices(preview: SelectionPreview) {
  const groups = new Map<ImportNoticeCode, { code: ImportNoticeCode; count: number; ids: string[]; contexts: string[] }>()
  for (const notice of selectionNotices(preview)) {
    const group = groups.get(notice.code) ?? { code: notice.code, count: 0, ids: [], contexts: [] }
    group.count += notice.count
    group.ids.push(notice.id)
    const candidate = [...preview.skills, ...preview.configurations].find((entry) => entry.notices.some((entry) => entry.id === notice.id))
    if (candidate?.safeName && !group.contexts.includes(candidate.safeName)) group.contexts.push(candidate.safeName)
    groups.set(notice.code, group)
  }
  return [...groups.values()]
}

export const selectionNoticeHelp: Record<ImportNoticeCode, string> = {
  0: "未识别的提示不会被自动确认。",
  1: "来源中的密码、API key 和 token 不会复制到配置资产。",
  2: "来源工具的展示信息、安装状态等辅助字段不迁移，不影响仓库收录。",
  3: "备份中的其它数据不在本次导入范围内，不会自动应用到设备。",
  4: "来源文件与仓库内容尚未比对，技能引用必须显式绑定。",
  5: "无法识别的来源字段被排除，不会静默解释或导入。",
}

export const selectionNoticeTitles: Record<ImportNoticeCode, string> = {
  0: "存在无法识别的内容，不会自动导入",
  1: "不导入 API 密钥、密码和 token",
  2: "不导入原软件的界面与安装记录",
  3: "只导入本页列出的技能、仓库与配置",
  4: "技能需要与已收录的仓库核对",
  5: "不导入当前版本无法识别的字段",
}

export const repositoryOutcomeLabels: Record<number, string> = {
  0: "结果未知", 1: "已收录并扫描", 2: "仓库已存在", 3: "尚未尝试收录", 4: "收录已取消",
  5: "授权已失效", 6: "仓库来源未通过校验", 7: "收录超过容量限制", 8: "结果未知，请核对状态",
}
