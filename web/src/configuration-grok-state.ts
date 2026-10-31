import { Code, ConnectError } from "@connectrpc/connect"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { AssetKind, CredentialReadiness, GrokBuildMode, type DraftSchema, type DraftView, type GrokBuildProvider } from "./gen/skillhub/configuration/v1/configuration_pb"
import type { AssetMetadata } from "./configuration-state"

export const grokSlot = "grokbuild.api-key"
export type GrokFields = { name: string; mode: GrokBuildMode; profile: string; upstreamModel: string; endpoint: string; modelName: string; apiBackend: string; contextWindow: string; envEnabled: boolean; envKey: string }
export type GrokView = { mode: "editable" | "readonly"; asset: AssetMetadata; provider: GrokBuildProvider; present: boolean; readiness: CredentialReadiness }
export const grokName = (value: string) => value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "")
export const validGrokUnicode = (value: string) => !/[\uD800-\uDFFF]/u.test(value)
const bytes = (value: string) => new TextEncoder().encode(value).length
const literal = (value: string, limit: number) => validGrokUnicode(value) && value.length > 0 && bytes(value) <= limit && grokName(value) === value && !/\p{Cc}/u.test(value)

export function supportsGrok(asset: AssetMetadata) {
  return asset.agentId === "grokbuild" && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1
}

export function grokFields(provider?: GrokBuildProvider, name = ""): GrokFields {
  const model = provider?.model
  return { name, mode: provider?.mode ?? GrokBuildMode.NATIVE_LOGIN, profile: model?.profile ?? "", upstreamModel: model?.upstreamModel ?? "", endpoint: model?.endpoint ?? "", modelName: model?.name ?? "", apiBackend: model?.apiBackend ?? "", contextWindow: model?.contextWindow.toString() ?? "", envEnabled: model?.envKey !== undefined, envKey: model?.envKey ?? "" }
}

export function validateGrok(fields: GrokFields): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!validGrokUnicode(fields.name) || !literal(grokName(fields.name), 128)) errors.name = "名称须为 1–128 个 UTF-8 字节，且不含控制字符。"
  if (fields.mode !== GrokBuildMode.NATIVE_LOGIN && fields.mode !== GrokBuildMode.CUSTOM_MODEL) errors.mode = "请选择 Grok 原生登录或自定义模型。"
  if (fields.mode !== GrokBuildMode.CUSTOM_MODEL) return errors
  for (const [field, limit] of [["profile", 128], ["upstreamModel", 256], ["modelName", 128]] as const) {
    if (!literal(fields[field], limit)) errors[field] = "Grok 字段必须非空、无控制字符或首尾空白，且符合 UTF-8 字节上限；保留原文。"
  }
  let endpointValid = false
  try {
    const address = new URL(fields.endpoint)
    const authority = fields.endpoint.match(/^https:\/\/([^/]+)/i)?.[1] ?? ""
    const path = decodeURIComponent(address.pathname)
    endpointValid = literal(fields.endpoint, 2048) && /^https:\/\//i.test(fields.endpoint) && address.protocol === "https:" && !!address.hostname && !address.username && !address.password && !/[?#\\]/.test(fields.endpoint) && !authority.includes("@") && !authority.includes("%") && !authority.endsWith(":") && address.port !== "0" && !/[\\\p{Cc}]/u.test(path)
  } catch { endpointValid = false }
  if (!endpointValid) errors.endpoint = "Grok 端点须为绝对 HTTPS 地址，最多 2048 UTF-8 字节，无凭据、query、fragment、反斜线或控制字符，端口为 1–65535。"
  if (fields.apiBackend !== "responses") errors.apiBackend = "请显式选择受支持的 responses backend。"
  if (!/^[1-9][0-9]{0,15}$/.test(fields.contextWindow) || BigInt(fields.contextWindow) > 9007199254740991n) errors.contextWindow = "Context window 须为 1–9007199254740991 的规范十进制整数，不接受小数、指数或前导零。"
  if (fields.envEnabled && !/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(fields.envKey)) errors.envKey = "环境变量名须为 1–128 个 ASCII 字符，以字母或下划线开头，仅含字母、数字和下划线。"
  return errors
}

export function grokDraft(fields: GrokFields): MessageInitShape<typeof DraftSchema> {
  if (Object.keys(validateGrok(fields)).length) throw new ConnectError("", Code.InvalidArgument)
  return { name: grokName(fields.name), payload: { case: "grokbuildProvider", value: { mode: fields.mode, model: fields.mode === GrokBuildMode.CUSTOM_MODEL ? { profile: fields.profile, upstreamModel: fields.upstreamModel, endpoint: fields.endpoint, name: fields.modelName, apiBackend: fields.apiBackend, contextWindow: BigInt(fields.contextWindow), envKey: fields.envEnabled ? fields.envKey : undefined } : undefined } } }
}

export function requireGrokView(view?: DraftView): GrokView {
  if (!view?.asset || !supportsGrok(view.asset) || !view.draft || view.draft.payload.case !== "grokbuildProvider") throw new ConnectError("", Code.FailedPrecondition)
  const provider = view.draft.payload.value
  const status = view.secrets[0]
  if (!/^[A-Z2-7]{26}$/.test(view.asset.id) || view.asset.revision < 1n || view.asset.revision > 9223372036854775807n || view.asset.name !== view.draft.name || view.asset.name !== grokName(view.asset.name) || Object.keys(validateGrok(grokFields(provider, view.asset.name))).length || provider.mode === GrokBuildMode.NATIVE_LOGIN && provider.model !== undefined || provider.mode === GrokBuildMode.CUSTOM_MODEL && !provider.model) throw new ConnectError("", Code.DataLoss)
  if (view.secrets.length !== 1 || status?.slotId !== grokSlot || provider.mode === GrokBuildMode.NATIVE_LOGIN && status.present || view.credentialReadiness !== (provider.mode === GrokBuildMode.NATIVE_LOGIN || status.present || provider.model?.envKey !== undefined ? CredentialReadiness.COMPLETE : CredentialReadiness.NEEDS_CREDENTIAL)) throw new ConnectError("", Code.DataLoss)
  const { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix } = view.asset
  return { mode: view.hasPreservedExtras ? "readonly" : "editable", asset: { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix }, provider, present: status.present, readiness: view.credentialReadiness }
}
