import { useCallback, useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, ClaudeAuthMode, CredentialReadiness, type Asset, type DraftView } from "./gen/skillhub/configuration/v1/configuration_pb"
import { requireCodexView, supportsCodex, type CodexView } from "./configuration-codex-state"
import { requireGeminiView, supportsGemini, type GeminiView } from "./configuration-gemini-state"

export type AssetMetadata = Pick<Asset, "id" | "agentId" | "kind" | "name" | "schemaVersion" | "revision" | "createdAtUnix" | "updatedAtUnix">
export const secretSlots = ["claude.api-key", "claude.auth-token"] as const
export type SecretSlot = typeof secretSlots[number]

export function metadata(asset: Asset): AssetMetadata {
  return { id: asset.id, agentId: asset.agentId, kind: asset.kind, name: asset.name, schemaVersion: asset.schemaVersion, revision: asset.revision, createdAtUnix: asset.createdAtUnix, updatedAtUnix: asset.updatedAtUnix }
}

export function supportsClaude(asset: AssetMetadata) {
  return asset.agentId === "claude" && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1
}

export function requireClaudeView(view?: DraftView) {
  if (!view?.asset || !supportsClaude(view.asset) || !view.draft || view.draft.payload.case !== "claudeProvider" || view.hasPreservedExtras) throw new ConnectError("", Code.FailedPrecondition)
  if (![ClaudeAuthMode.NATIVE_LOGIN, ClaudeAuthMode.API_KEY, ClaudeAuthMode.AUTH_TOKEN].includes(view.draft.payload.value.authMode)) throw new ConnectError("", Code.FailedPrecondition)
  if (![CredentialReadiness.COMPLETE, CredentialReadiness.NEEDS_CREDENTIAL].includes(view.credentialReadiness) || view.secrets.length !== 2 || !secretSlots.every((slot) => view.secrets.filter((secret) => secret.slotId === slot).length === 1)) throw new ConnectError("", Code.DataLoss)
  return { asset: metadata(view.asset), provider: view.draft.payload.value, secrets: view.secrets.map((secret) => ({ slotId: secret.slotId, present: secret.present })), readiness: view.credentialReadiness }
}

export type ClaudeView = ReturnType<typeof requireClaudeView>
export type ConfigurationView = { kind: "claude"; view: ClaudeView } | { kind: "codex"; view: CodexView } | { kind: "gemini"; view: GeminiView }

export function requireConfigurationView(view?: DraftView): ConfigurationView {
  if (view?.asset && supportsClaude(view.asset)) return { kind: "claude", view: requireClaudeView(view) }
  if (view?.asset && supportsCodex(view.asset)) return { kind: "codex", view: requireCodexView(view) }
  if (view?.asset && supportsGemini(view.asset)) return { kind: "gemini", view: requireGeminiView(view) }
  throw new ConnectError("", Code.FailedPrecondition)
}

export function configurationError(code: Code) {
  switch (code) {
    case Code.Unauthenticated: return "配置会话已失效，请重新登录。"
    case Code.PermissionDenied: return "没有权限管理这些配置资产。"
    case Code.NotFound: return "配置资产不存在或不属于当前账号，请关闭并刷新列表。"
    case Code.InvalidArgument: return "配置未通过校验，请检查字段、凭据操作及认证模式。错误不会回显输入。"
    case Code.Aborted: return "配置版本已改变。请关闭并刷新列表，再重新打开；不会覆盖新版本。"
    case Code.FailedPrecondition:
    case Code.Unimplemented: return "此资产或服务尚不支持当前类型的编辑操作。保留原数据，不会转换为通用 JSON。"
    case Code.ResourceExhausted: return "配置容量或请求达到限制，请稍后刷新重试。"
    case Code.Canceled: return "配置请求已取消，请重新读取状态。"
    default: return "配置请求未完成。请检查连接后刷新状态；不会自动重试写入。"
  }
}

export function useConfigurationRequest(invalidate: () => void) {
  const { i18n } = useTranslation()
  const active = useRef<{ controller: AbortController; write: boolean } | null>(null)
  const generation = useRef(0)
  const interruptedWrite = useRef(false)
  const blockedRef = useRef(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Code>()
  const [uncertain, setUncertain] = useState(false)
  const [blocked, setBlocked] = useState(false)

  useLayoutEffect(() => {
    setPending(false)
    setFailure(undefined)
    if (interruptedWrite.current) {
      interruptedWrite.current = false
      blockedRef.current = true
      setBlocked(true)
      setUncertain(true)
    }
    return () => {
      generation.current++
      if (active.current?.write) { interruptedWrite.current = true; invalidate() }
      active.current?.controller.abort()
      active.current = null
    }
  }, [i18n.resolvedLanguage, invalidate])

  const run = useCallback(async <Result,>(send: (signal: AbortSignal) => Promise<Result>, write = false): Promise<Result | undefined> => {
    if (active.current || write && blockedRef.current) return
    const controller = new AbortController()
    active.current = { controller, write }
    const attempt = ++generation.current
    setPending(true)
    setFailure(undefined)
    try {
      const result = await send(controller.signal)
      if (attempt !== generation.current || controller.signal.aborted) return
      return result
    } catch (error) {
      if (attempt !== generation.current || controller.signal.aborted) return
      const code = ConnectError.from(error).code
      setFailure(code)
      const unknown = write && ![Code.InvalidArgument, Code.NotFound, Code.PermissionDenied, Code.Unauthenticated, Code.ResourceExhausted, Code.FailedPrecondition, Code.Unimplemented, Code.Aborted].includes(code)
      if (unknown || write && code === Code.Aborted) { blockedRef.current = true; setBlocked(true) }
      if (unknown) setUncertain(true)
    } finally {
      if (attempt === generation.current) {
        active.current = null
        setPending(false)
        if (write) invalidate()
      }
    }
  }, [invalidate])

  return { run, pending, failure, uncertain, blocked }
}
