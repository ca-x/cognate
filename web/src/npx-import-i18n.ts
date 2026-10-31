import { ImportSelectionSource } from "./gen/skillhub/import/v1/import_pb"

export type ImportSource = "magpie" | "cc-switch" | "npx-skills"

export const selectionSources = {
  magpie: { source: ImportSelectionSource.MAGPIE_LIBRARY_JSON, maximumBytes: 1024 * 1024, timeoutMs: 5000, accept: ".json,application/json", label: "Magpie JSON · 1 MiB" },
  "cc-switch": { source: ImportSelectionSource.CCSWITCH_SCHEMA24, maximumBytes: 16 * 1024 * 1024, timeoutMs: 15000, accept: ".db,.sqlite,.sqlite3", label: "cc-switch schema24 SQLite · 16 MiB" },
  "npx-skills": { source: ImportSelectionSource.NPX_SKILLS_LOCK, maximumBytes: 1024 * 1024, timeoutMs: 5000, accept: ".json,application/json", label: "npx skills lock v3 · 1 MiB" },
} satisfies Record<ImportSource, { source: ImportSelectionSource; maximumBytes: number; timeoutMs: number; accept: string; label: string }>

export function isImportSource(value: string): value is ImportSource {
  return Object.hasOwn(selectionSources, value)
}

const messages = {
  boundaries: {
    en: "Choose an npx skills .skill-lock.json, version 3 only, up to 1 MiB. Each preview batch supports up to 128 skill candidates and 16 repositories. Oversized batches are rejected (ResourceExhausted), never truncated. Only GitHub repository references are supported. Other providers and unknown fields are excluded with notices; no client configuration is imported or automatically installed.",
    "zh-CN": "请选择 npx skills 的 .skill-lock.json，仅支持版本 3，最大 1 MiB。单批预览最多 128 个技能候选、16 个仓库；超限整批拒绝（ResourceExhausted），不会截断。只支持 GitHub 仓库引用；其它来源及未知字段会提示排除。不导入客户端配置，不会自动安装。",
  },
  binding: {
    en: "Lock tree hashes are not Cognate content hashes. Collect a missing repository, then preview again and explicitly bind an existing skill from the same repository. Acknowledge that source content was not compared before saving a skill reference; names never imply a binding.",
    "zh-CN": "锁文件的树哈希不是 Cognate 内容哈希。仓库尚未收录时，请先收录，再重新预览并显式绑定同仓现有技能。保存技能引用前须确认来源内容未比对，不按名称推断绑定。",
  },
  invalid: {
    en: "The lock could not be previewed. Choose a nonempty, valid version 3 JSON lock up to 1 MiB. GitHub references must use safe repository URLs and relative skill paths. Input is not displayed or retried automatically.",
    "zh-CN": "锁文件未能预览。请选择非空、有效的版本 3 JSON 锁文件，最大 1 MiB；GitHub 引用须使用安全仓库地址和相对技能路径。不会显示输入或自动重试。",
  },
  exhausted: {
    en: "The request exceeds a limit or is rate limited (ResourceExhausted). Check the 1 MiB file limit and batch limits of 128 skill candidates and 16 repositories. Oversized batches are rejected in full, never truncated. Use a smaller export copy and preview again; requests are not retried automatically.",
    "zh-CN": "请求超过限制或当前被限流（ResourceExhausted）。请检查 1 MiB 文件上限、单批 128 个技能候选和 16 个仓库上限。超限整批拒绝，不会截断。请使用较小的导出副本重新预览；不会自动重试。",
  },
} as const

export function npxImportText(key: keyof typeof messages, language: string | undefined) {
  return messages[key][language === "zh-CN" ? "zh-CN" : "en"]
}
