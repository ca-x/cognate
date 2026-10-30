import { Code, ConnectError } from "@connectrpc/connect"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { AssetKind, CredentialReadiness, GeminiAuthMode, GeminiKeyTransport, type DraftSchema, type DraftView, type GeminiProvider } from "./gen/skillhub/configuration/v1/configuration_pb"
import type { AssetMetadata } from "./configuration-state"

export const geminiSlot = "gemini.api-key"
type OptionalField = { enabled: boolean; value: string }
export type GeminiFields = { name: string; authMode: GeminiAuthMode; endpoint: OptionalField; keyTransport?: GeminiKeyTransport; envModel: OptionalField; settingsModel: OptionalField }
export type GeminiView = { mode: "editable" | "readonly"; asset: AssetMetadata; provider: GeminiProvider; present: boolean; readiness: CredentialReadiness }
export const validGeminiUnicode = (value: string) => !/[\uD800-\uDFFF]/u.test(value)
const bytes = (value: string) => new TextEncoder().encode(value).length
const control = /\p{Cc}/u
const modelPattern = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$/

export function supportsGemini(asset: AssetMetadata) {
  return asset.agentId === "gemini" && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1
}

export function geminiFields(provider?: GeminiProvider, name = ""): GeminiFields {
  return { name, authMode: provider?.authMode ?? GeminiAuthMode.NATIVE_LOGIN, endpoint: { enabled: provider?.endpoint !== undefined, value: provider?.endpoint ?? "" }, keyTransport: provider?.keyTransport, envModel: { enabled: provider?.envModel !== undefined, value: provider?.envModel ?? "" }, settingsModel: { enabled: provider?.settingsModel !== undefined, value: provider?.settingsModel ?? "" } }
}

export function validateGemini(fields: GeminiFields): Record<string, string> {
  const errors: Record<string, string> = {}
  const name = fields.name.trim()
  if (!validGeminiUnicode(fields.name) || bytes(name) < 1 || bytes(name) > 128 || control.test(name)) errors.name = "名称须为 1–128 个 UTF-8 字节，且不含控制字符。"
  if (fields.authMode !== GeminiAuthMode.NATIVE_LOGIN && fields.authMode !== GeminiAuthMode.API_KEY) errors.authMode = "请选择 Gemini 原生登录或 API key 认证模式。"
  if (fields.authMode === GeminiAuthMode.NATIVE_LOGIN && (fields.endpoint.enabled || fields.keyTransport !== undefined)) errors.authMode = "Gemini 原生登录要求端点和传输方式均未设置；请明确确认模式切换。"
  if (fields.authMode === GeminiAuthMode.API_KEY && fields.keyTransport !== GeminiKeyTransport.NATIVE_DEFAULT && fields.keyTransport !== GeminiKeyTransport.BEARER) errors.keyTransport = "请显式选择 Gemini key 传输方式；不会推断默认值。"
  if (fields.endpoint.enabled) {
    const value = fields.endpoint.value
    let valid = false
    try {
      const address = new URL(value)
      const path = decodeURIComponent(value.match(/^https:\/\/[^/]*(\/.*)?$/i)?.[1] ?? "")
      valid = validGeminiUnicode(value) && bytes(value) >= 1 && bytes(value) <= 2048 && value === value.trim() && /^https:\/\//i.test(value) && address.protocol === "https:" && !!address.hostname && !address.username && !address.password && !/[?#\\]/.test(value) && !/^https:\/\/[^/]*@/i.test(value) && !/^https:\/\/[^/]*:(?:0+)?(?:\/|$)/i.test(value) && !control.test(value) && !control.test(path) && !path.includes("\\")
    } catch { valid = false }
    if (!valid) errors.endpoint = "Gemini 端点须为绝对 HTTPS 地址，最多 2048 UTF-8 字节，无凭据、query、fragment、反斜线或控制字符，端口为 1–65535；保留原文。"
  }
  for (const field of ["envModel", "settingsModel"] as const) {
    if (fields[field].enabled && !modelPattern.test(fields[field].value)) errors[field] = "Gemini 模型须为 1–256 个 ASCII 字符，以字母或数字开头，仅含字母、数字及 . _ : / -；不会同步两个来源。"
  }
  return errors
}

export function geminiDraft(fields: GeminiFields): MessageInitShape<typeof DraftSchema> {
  if (Object.keys(validateGemini(fields)).length) throw new ConnectError("", Code.InvalidArgument)
  return { name: fields.name.trim(), payload: { case: "geminiProvider", value: { authMode: fields.authMode, endpoint: fields.endpoint.enabled ? fields.endpoint.value : undefined, keyTransport: fields.keyTransport, envModel: fields.envModel.enabled ? fields.envModel.value : undefined, settingsModel: fields.settingsModel.enabled ? fields.settingsModel.value : undefined } } }
}

export function requireGeminiView(view?: DraftView): GeminiView {
  if (!view?.asset || !supportsGemini(view.asset) || !view.draft || view.draft.payload.case !== "geminiProvider") throw new ConnectError("", Code.FailedPrecondition)
  const provider = view.draft.payload.value
  const status = view.secrets[0]
  if (view.asset.revision < 1n || view.asset.revision > 9223372036854775807n || !view.asset.id || view.asset.name !== view.draft.name || view.asset.name !== view.asset.name.trim() || Object.keys(validateGemini(geminiFields(provider, view.asset.name))).length) throw new ConnectError("", Code.DataLoss)
  if (view.secrets.length !== 1 || status?.slotId !== geminiSlot || provider.authMode === GeminiAuthMode.NATIVE_LOGIN && status.present || view.credentialReadiness !== (provider.authMode === GeminiAuthMode.NATIVE_LOGIN || status.present ? CredentialReadiness.COMPLETE : CredentialReadiness.NEEDS_CREDENTIAL)) throw new ConnectError("", Code.DataLoss)
  const { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix } = view.asset
  return { mode: view.hasPreservedExtras ? "readonly" : "editable", asset: { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix }, provider, present: status.present, readiness: view.credentialReadiness }
}

export function geminiPreview(fields: GeminiFields, present: boolean) {
  return {
    env: { endpoint: fields.authMode === GeminiAuthMode.API_KEY && fields.endpoint.enabled ? fields.endpoint.value : undefined, model: fields.envModel.enabled ? fields.envModel.value : undefined, credential: fields.authMode === GeminiAuthMode.API_KEY ? { slotId: geminiSlot, present } : undefined, transport: fields.authMode === GeminiAuthMode.API_KEY && fields.keyTransport === GeminiKeyTransport.BEARER ? "bearer" : undefined },
    settings: { selectedType: fields.authMode === GeminiAuthMode.NATIVE_LOGIN ? "oauth-personal" : fields.authMode === GeminiAuthMode.API_KEY ? "gemini-api-key" : undefined, model: fields.settingsModel.enabled ? fields.settingsModel.value : undefined },
  }
}
