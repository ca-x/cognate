import { Code, ConnectError } from "@connectrpc/connect"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { AssetKind, CodexAuthMode, CodexModality, CodexReasoningEffort, CredentialReadiness, type CodexProvider, type DraftView, type DraftSchema } from "./gen/skillhub/configuration/v1/configuration_pb"
import type { AssetMetadata } from "./configuration-state"

export const codexSlot = "codex.provider-key"
export const efforts = [CodexReasoningEffort.NONE, CodexReasoningEffort.MINIMAL, CodexReasoningEffort.LOW, CodexReasoningEffort.MEDIUM, CodexReasoningEffort.HIGH, CodexReasoningEffort.XHIGH, CodexReasoningEffort.MAX] as const
export const effortLabels = ["none", "minimal", "low", "medium", "high", "xhigh", "max"] as const
export const modalities = [CodexModality.TEXT, CodexModality.IMAGE] as const
export type CatalogRow = {
  model: string
  displayName?: string
  contextWindow?: string
  supportsParallelToolCalls?: boolean
  inputModalities?: CodexModality[]
  baseInstructions?: string
  reasoningLevels?: CodexReasoningEffort[]
  defaultReasoningLevel?: CodexReasoningEffort
}
export type CodexFields = { name: string; endpoint: string; model: string; reasoningEffort?: CodexReasoningEffort; catalog?: CatalogRow[] }
export type CodexView = { mode: "editable" | "readonly"; asset: AssetMetadata; provider: CodexProvider; present: boolean; readiness: CredentialReadiness }
export type CodexDraft = MessageInitShape<typeof DraftSchema>
const bytes = (value: string) => new TextEncoder().encode(value).length
const control = /\p{Cc}/u
const modelPattern = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/
export const validUnicode = (value: string) => !/[\uD800-\uDFFF]/u.test(value)
const validText = (value: string, maximum: number) => validUnicode(value) && bytes(value) >= 1 && bytes(value) <= maximum
const validEffort = (value: CodexReasoningEffort) => efforts.some((effort) => effort === value)

export function supportsCodex(asset: AssetMetadata) {
  return asset.agentId === "codex" && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1
}

export function parseContextWindow(value: string): bigint | undefined {
  if (!/^[1-9][0-9]*$/.test(value) || value.length > 10) return
  const parsed = BigInt(value)
  return parsed <= 2147483647n ? parsed : undefined
}

export function codexFields(provider?: CodexProvider, name = ""): CodexFields {
  return { name, endpoint: provider?.endpoint ?? "", model: provider?.model ?? "", reasoningEffort: provider?.reasoningEffort, catalog: provider?.catalog?.models.map((row) => ({ model: row.model, displayName: row.displayName, contextWindow: row.contextWindow?.toString(), supportsParallelToolCalls: row.supportsParallelToolCalls, inputModalities: row.inputModalities ? [...row.inputModalities.values] : undefined, baseInstructions: row.baseInstructions, reasoningLevels: row.reasoningLevels ? [...row.reasoningLevels.values] : undefined, defaultReasoningLevel: row.defaultReasoningLevel })) }
}

export function validateCodex(fields: CodexFields): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!validText(fields.name, 128) || control.test(fields.name) || fields.name !== fields.name.trim()) errors.name = "名称须为 1–128 个 UTF-8 字节，且不含控制字符。"
  let endpointValid = false
  try {
    const address = new URL(fields.endpoint)
    const path = decodeURIComponent(address.pathname)
    endpointValid = address.protocol === "https:" && !!address.hostname && !address.username && !address.password && !/[?#\\]/.test(fields.endpoint) && !/^https:\/\/[^/]*@/i.test(fields.endpoint) && !/^https:\/\/[^/]*:(?:0+)?(?:\/|$)/i.test(fields.endpoint) && !control.test(fields.endpoint) && !control.test(path) && !path.includes("\\") && fields.endpoint === fields.endpoint.trim() && validText(fields.endpoint, 2048) && /^https:\/\//i.test(fields.endpoint)
  } catch { endpointValid = false }
  if (!endpointValid) errors.endpoint = "Codex 端点须为绝对 HTTPS 地址，最多 2048 UTF-8 字节，无空白边界、凭据、query、fragment、反斜线或控制字符，端口为 1–65535。"
  if (!modelPattern.test(fields.model)) errors.model = "模型 ID 须为 1–128 个 ASCII 字符，以字母或数字开头，仅含字母、数字及 . _ : / -。"
  if (fields.reasoningEffort !== undefined && !validEffort(fields.reasoningEffort)) errors.reasoningEffort = "请选择有效的推理等级。"
  if (fields.catalog !== undefined) {
    if (fields.catalog.length < 1 || fields.catalog.length > 32) errors.catalog = "目录须包含 1–32 个有序模型，且主模型必须在目录中。"
    const seen = new Set<string>()
    fields.catalog.forEach((row, index) => {
      const prefix = `catalog.${index}.`
      if (!modelPattern.test(row.model) || seen.has(row.model)) errors[prefix + "model"] = "模型 ID 无效或重复；请显式修复引用，不会自动重命名。"
      seen.add(row.model)
      if (row.displayName !== undefined && (!validText(row.displayName, 256) || row.displayName !== row.displayName.trim() || control.test(row.displayName))) errors[prefix + "displayName"] = "展示名须为 1–256 UTF-8 字节，无首尾空白或控制字符。"
      if (row.contextWindow !== undefined && parseContextWindow(row.contextWindow) === undefined) errors[prefix + "contextWindow"] = "上下文窗口须为 1–2147483647 的正十进制整数；不接受指数、小数、空白或前导零。"
      if (row.supportsParallelToolCalls !== undefined && typeof row.supportsParallelToolCalls !== "boolean") errors[prefix + "supportsParallelToolCalls"] = "请选择未设置、false 或 true。"
      if (row.baseInstructions !== undefined && (!validText(row.baseInstructions, 2048) || /[^\P{Cc}\r\n\t]/u.test(row.baseInstructions))) errors[prefix + "baseInstructions"] = "私有基础指令须为 1–2048 UTF-8 字节；仅允许 CR、LF、TAB 控制字符，保留原文。"
      if (row.inputModalities !== undefined && (row.inputModalities.length < 1 || row.inputModalities.length > 2 || new Set(row.inputModalities).size !== row.inputModalities.length || row.inputModalities.some((value) => !modalities.some((allowed) => allowed === value)))) errors[prefix + "inputModalities"] = "输入模态须为非空、有序且不重复的 text/image 集合。"
      if (row.reasoningLevels !== undefined && (row.reasoningLevels.length < 1 || row.reasoningLevels.length > 7 || new Set(row.reasoningLevels).size !== row.reasoningLevels.length || row.reasoningLevels.some((value) => !validEffort(value)))) errors[prefix + "reasoningLevels"] = "推理等级须为非空、有序且不重复的有效集合。"
      if (row.defaultReasoningLevel !== undefined && (!validEffort(row.defaultReasoningLevel) || !row.reasoningLevels?.includes(row.defaultReasoningLevel))) errors[prefix + "defaultReasoningLevel"] = "默认推理等级必须包含在此行显式设置的推理等级中。"
      if (row.model === fields.model && fields.reasoningEffort !== undefined && row.reasoningLevels && !row.reasoningLevels.includes(fields.reasoningEffort)) errors.reasoningEffort = "主模型的推理等级必须包含顶层推理等级；请显式修复引用。"
    })
    if (!seen.has(fields.model)) errors.model = "主模型必须在目录中；删除或改名后请显式修复引用。"
    if (!Object.keys(errors).length) {
      const catalog = { models: fields.catalog.map((row) => ({ ...row, contextWindow: row.contextWindow === undefined ? undefined : parseContextWindow(row.contextWindow)?.toString(), inputModalities: row.inputModalities?.map((value) => value === CodexModality.TEXT ? "text" : "image"), reasoningLevels: row.reasoningLevels?.map((value) => effortLabels[efforts.findIndex((entry) => entry === value)]), defaultReasoningLevel: row.defaultReasoningLevel === undefined ? undefined : effortLabels[efforts.findIndex((entry) => entry === row.defaultReasoningLevel)] })) }
      const encodedBytes = bytes(JSON.stringify(catalog)) - 2 * fields.catalog.filter((row) => row.contextWindow !== undefined).length
      if (encodedBytes > 32768) errors.catalog = "目录超出 32 KiB UTF-8 预算；请明确减少内容，不会截断或丢弃字段。"
    }
  }
  return errors
}

export function codexDraft(fields: CodexFields): CodexDraft {
  if (Object.keys(validateCodex(fields)).length) throw new ConnectError("", Code.InvalidArgument)
  return { name: fields.name, payload: { case: "codexProvider", value: { authMode: CodexAuthMode.PROVIDER_KEY, endpoint: fields.endpoint, model: fields.model, reasoningEffort: fields.reasoningEffort, catalog: fields.catalog === undefined ? undefined : { models: fields.catalog.map((row) => ({ ...row, contextWindow: row.contextWindow === undefined ? undefined : parseContextWindow(row.contextWindow), inputModalities: row.inputModalities === undefined ? undefined : { values: [...row.inputModalities] }, reasoningLevels: row.reasoningLevels === undefined ? undefined : { values: [...row.reasoningLevels] } })) } } } }
}

export function requireCodexView(view?: DraftView): CodexView {
  if (!view?.asset || !supportsCodex(view.asset) || !view.draft || view.draft.payload.case !== "codexProvider") throw new ConnectError("", Code.FailedPrecondition)
  const provider = view.draft.payload.value
  if (provider.authMode !== CodexAuthMode.PROVIDER_KEY) throw new ConnectError("", Code.FailedPrecondition)
  if (view.asset.revision < 1n || !view.asset.id || view.asset.name !== view.draft.name || Object.keys(validateCodex(codexFields(provider, view.asset.name))).length) throw new ConnectError("", Code.DataLoss)
  const status = view.secrets[0]
  if (view.secrets.length !== 1 || status?.slotId !== codexSlot || ![CredentialReadiness.COMPLETE, CredentialReadiness.NEEDS_CREDENTIAL].includes(view.credentialReadiness) || view.credentialReadiness !== (status.present ? CredentialReadiness.COMPLETE : CredentialReadiness.NEEDS_CREDENTIAL)) throw new ConnectError("", Code.DataLoss)
  const { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix } = view.asset
  return { mode: view.hasPreservedExtras ? "readonly" : "editable", asset: { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix }, provider, present: status.present, readiness: view.credentialReadiness }
}
