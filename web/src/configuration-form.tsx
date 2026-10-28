import { useId, useLayoutEffect, useRef, useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { t } from "./i18n"
import { ClaudeAuthMode, DraftSchema, SecretMutationSchema } from "./gen/skillhub/configuration/v1/configuration_pb"
import { secretSlots, type ClaudeView, type SecretSlot } from "./configuration-state"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { Checkbox } from "./components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"

export type ConfigurationDraft = MessageInitShape<typeof DraftSchema>
export type SecretChanges = MessageInitShape<typeof SecretMutationSchema>[]
type SecretEdit = { action: "keep" | "replace" | "clear"; value: string; confirmed: boolean }
type OptionalField = { enabled: boolean; value: string }
const modelFields = ["defaultModel", "haiku", "sonnet", "opus"] as const
const modelLabels = { defaultModel: "默认模型", haiku: "Haiku 模型", sonnet: "Sonnet 模型", opus: "Opus 模型" }
const byteLength = (value: string) => new TextEncoder().encode(value).length
const control = /\p{Cc}/u

export function ConfigurationForm({ original, pending, blocked, onSubmit, onDirtyChange }: { original?: ClaudeView; pending: boolean; blocked: boolean; onSubmit: (draft: ConfigurationDraft, secrets: SecretChanges) => Promise<boolean>; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const id = useId()
  const form = useRef<HTMLFormElement>(null)
  const [name, setName] = useState(original?.asset.name ?? "")
  const [authMode, setAuthMode] = useState(original?.provider.authMode ?? ClaudeAuthMode.NATIVE_LOGIN)
  const [endpoint, setEndpoint] = useState<OptionalField>({ enabled: original?.provider.endpoint !== undefined, value: original?.provider.endpoint ?? "" })
  const [models, setModels] = useState(() => Object.fromEntries(modelFields.map((field) => [field, { enabled: original?.provider.models?.[field] !== undefined, value: original?.provider.models?.[field] ?? "" }])) as Record<typeof modelFields[number], OptionalField>)
  const [secrets, setSecrets] = useState<Record<SecretSlot, SecretEdit>>(() => Object.fromEntries(secretSlots.map((slot) => [slot, { action: original ? "keep" : "clear", value: "", confirmed: false }])) as Record<SecretSlot, SecretEdit>)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const disabled = pending || blocked
  const present = (slot: SecretSlot) => original?.secrets.some((secret) => secret.slotId === slot && secret.present) ?? false

  useLayoutEffect(() => {
    onDirtyChange(name !== (original?.asset.name ?? "") || authMode !== (original?.provider.authMode ?? ClaudeAuthMode.NATIVE_LOGIN)
      || endpoint.enabled !== (original?.provider.endpoint !== undefined) || endpoint.value !== (original?.provider.endpoint ?? "")
      || modelFields.some((field) => models[field].enabled !== (original?.provider.models?.[field] !== undefined) || models[field].value !== (original?.provider.models?.[field] ?? ""))
      || secretSlots.some((slot) => secrets[slot].action !== (original ? "keep" : "clear") || secrets[slot].value !== "" || secrets[slot].confirmed))
  }, [name, authMode, endpoint, models, secrets, original, onDirtyChange])

  useLayoutEffect(() => {
    setSecrets((current) => Object.fromEntries(secretSlots.map((slot) => [slot, { ...current[slot], value: "" }])) as Record<SecretSlot, SecretEdit>)
    setErrors({})
  }, [i18n.resolvedLanguage])

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (disabled) return
    const issues: Record<string, string> = {}
    const trimmed = name.trim()
    if (!trimmed || byteLength(trimmed) > 128 || control.test(trimmed)) issues.name = "名称须为 1–128 个 UTF-8 字节，且不含控制字符。"
    if (endpoint.enabled) {
      let valid = false
      try {
        const address = new URL(endpoint.value)
        valid = ["http:", "https:"].includes(address.protocol) && Boolean(address.hostname) && !address.username && !address.password && !/[?#]/.test(endpoint.value) && !/^[^:]+:\/\/[^/]*@/.test(endpoint.value) && !control.test(endpoint.value) && endpoint.value === endpoint.value.trim() && byteLength(endpoint.value) <= 2048
      } catch { valid = false }
      if (!valid) issues.endpoint = "端点须为绝对 HTTP(S) 地址，最多 2048 个 UTF-8 字节，不含凭据、query、fragment 或控制字符。"
      if (authMode === ClaudeAuthMode.NATIVE_LOGIN) issues.endpoint = "原生登录不能使用自定义端点，请显式取消端点设置。"
    }
    for (const field of modelFields) {
      const model = models[field]
      if (model.enabled && (!model.value || byteLength(model.value) > 256 || control.test(model.value))) issues[field] = "模型值须为 1–256 个 UTF-8 字节，不含控制字符；不会自动修剪或改变大小写。"
    }
    for (const slot of secretSlots) {
      const edit = secrets[slot]
      if (edit.action === "replace" && (!edit.value || byteLength(edit.value) > 8192 || /[\x00\r\n]/.test(edit.value))) issues[slot] = "新凭据须为 1–8192 个 UTF-8 字节，不含 NUL 或换行；不会自动修剪。"
      if (edit.action === "clear" && present(slot) && !edit.confirmed) issues[slot] = "请确认清除已保存的凭据。"
      const retained = edit.action === "replace" || edit.action === "keep" && present(slot)
      if (retained && (authMode === ClaudeAuthMode.NATIVE_LOGIN || authMode === ClaudeAuthMode.API_KEY && slot === "claude.auth-token" || authMode === ClaudeAuthMode.AUTH_TOKEN && slot === "claude.api-key")) issues[slot] = "此认证模式不允许保留该凭据。请显式选择清除；系统不会自动删除旧凭据。"
    }
    setErrors(issues)
    if (Object.keys(issues).length) {
      requestAnimationFrame(() => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
      return
    }
    const draft: ConfigurationDraft = { name: trimmed, payload: { case: "claudeProvider", value: { endpoint: endpoint.enabled ? endpoint.value : undefined, authMode, models: Object.fromEntries(modelFields.filter((field) => models[field].enabled).map((field) => [field, models[field].value])) } } }
    const mutations: SecretChanges = secretSlots.map((slot) => ({ slotId: slot, action: secrets[slot].action === "replace" ? { case: "replace", value: secrets[slot].value } : secrets[slot].action === "keep" ? { case: "keep", value: {} } : { case: "clear", value: {} } }))
    try { await onSubmit(draft, mutations) } finally {
      setSecrets((current) => Object.fromEntries(secretSlots.map((slot) => [slot, { ...current[slot], value: "" }])) as Record<SecretSlot, SecretEdit>)
    }
  }

  function fieldError(field: string) {
    return errors[field] && <p id={`${id}-${field}-error`} role="alert" className="text-sm text-destructive">{t(errors[field])}</p>
  }

  return <form ref={form} className="mt-5 min-w-0 space-y-5" onSubmit={(event) => { void submit(event) }} noValidate autoComplete="off" aria-busy={pending}>
    <div className="space-y-2"><label htmlFor={`${id}-name`}>{t("配置资产名称")}</label><Input id={`${id}-name`} value={name} disabled={disabled} required autoComplete="off" onChange={(event) => setName(event.target.value)} aria-invalid={Boolean(errors.name)} aria-describedby={errors.name ? `${id}-name-error` : undefined} />{fieldError("name")}</div>
    <div className="space-y-2"><label htmlFor={`${id}-auth`}>{t("Claude 认证模式")}</label><Select value={String(authMode)} disabled={disabled} onValueChange={(value) => { const mode = Number(value); if ([ClaudeAuthMode.NATIVE_LOGIN, ClaudeAuthMode.API_KEY, ClaudeAuthMode.AUTH_TOKEN].includes(mode)) setAuthMode(mode) }}><SelectTrigger id={`${id}-auth`} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value={String(ClaudeAuthMode.NATIVE_LOGIN)}>{t("交回原生登录")}</SelectItem><SelectItem value={String(ClaudeAuthMode.API_KEY)}>{t("API key")}</SelectItem><SelectItem value={String(ClaudeAuthMode.AUTH_TOKEN)}>{t("Auth token")}</SelectItem></SelectContent></Select><p className="text-sm text-muted-foreground">{t("原生登录不导入 OAuth 会话。切换认证模式不自动清除旧凭据，也不切换任何设备的 provider。")}</p></div>
    <fieldset disabled={disabled} className="min-w-0 space-y-2"><legend className="font-medium">{t("可选端点")}</legend><label className="flex min-h-11 items-center gap-3" htmlFor={`${id}-endpoint-enabled`}><Checkbox id={`${id}-endpoint-enabled`} checked={endpoint.enabled} onCheckedChange={(checked) => setEndpoint((current) => ({ ...current, enabled: checked === true }))} />{t("设置自定义端点")}</label>{endpoint.enabled && <><label className="block" htmlFor={`${id}-endpoint`}>{t("端点地址")}</label><Input id={`${id}-endpoint`} value={endpoint.value} type="text" inputMode="url" spellCheck={false} autoComplete="off" onChange={(event) => setEndpoint((current) => ({ ...current, value: event.target.value }))} aria-invalid={Boolean(errors.endpoint)} aria-describedby={`${id}-endpoint-help${errors.endpoint ? ` ${id}-endpoint-error` : ""}`} />{fieldError("endpoint")}</>}<p id={`${id}-endpoint-help`} className="text-sm text-muted-foreground">{t("未设置与空字符串不同。允许保存本地地址，但保存不会探测端点或发送凭据。")}</p></fieldset>
    <fieldset disabled={disabled} className="min-w-0 space-y-3"><legend className="font-medium">{t("可选模型映射")}</legend><p className="text-sm text-muted-foreground">{t("仅提交明确设置的模型。取消设置表示 absent，不提交空字符串；保留原始大小写与空白。")}</p>{modelFields.map((field) => <div key={field} className="space-y-2"><label className="flex min-h-11 items-center gap-3" htmlFor={`${id}-${field}-enabled`}><Checkbox id={`${id}-${field}-enabled`} checked={models[field].enabled} onCheckedChange={(checked) => setModels((current) => ({ ...current, [field]: { ...current[field], enabled: checked === true } }))} />{t(modelLabels[field])}</label>{models[field].enabled && <Input id={`${id}-${field}`} aria-label={t(modelLabels[field])} value={models[field].value} spellCheck={false} autoComplete="off" onChange={(event) => setModels((current) => ({ ...current, [field]: { ...current[field], value: event.target.value } }))} aria-invalid={Boolean(errors[field])} aria-describedby={errors[field] ? `${id}-${field}-error` : undefined} />}{fieldError(field)}</div>)}</fieldset>
    <fieldset disabled={disabled} className="min-w-0 space-y-4"><legend className="font-medium">{t("凭据变更")}</legend><p className="text-sm text-muted-foreground">{t("只显示凭据是否存在，不读取原文。新值仅暂存在此表单内存，提交、切换语言或关闭后清除；错误不会回显凭据。")}</p>{secretSlots.map((slot) => <div key={slot} className="min-w-0 space-y-2 rounded-lg border border-border p-3"><label className="block" htmlFor={`${id}-${slot}`}>{slot === "claude.api-key" ? t("API key") : t("Auth token")}</label><p className="text-sm text-muted-foreground">{present(slot) ? t("已保存凭据：存在") : t("已保存凭据：不存在")}</p><Select value={secrets[slot].action} onValueChange={(action) => { if (action !== "keep" && action !== "replace" && action !== "clear") return; setSecrets((current) => ({ ...current, [slot]: { action, value: "", confirmed: false } })) }} disabled={disabled}><SelectTrigger id={`${id}-${slot}`} className="w-full" aria-invalid={Boolean(errors[slot])} aria-describedby={errors[slot] ? `${id}-${slot}-error` : undefined}><SelectValue /></SelectTrigger><SelectContent>{original && <SelectItem value="keep">{t("保留原凭据（keep）")}</SelectItem>}<SelectItem value="replace">{t("输入新凭据（replace）")}</SelectItem><SelectItem value="clear">{original ? t("清除凭据（clear）") : t("不提供凭据")}</SelectItem></SelectContent></Select>{secrets[slot].action === "replace" && <><label className="block text-sm" htmlFor={`${id}-${slot}-replacement`}>{t("新凭据")}</label><Input id={`${id}-${slot}-replacement`} type="password" value={secrets[slot].value} autoComplete="new-password" autoCapitalize="off" spellCheck={false} onChange={(event) => setSecrets((current) => ({ ...current, [slot]: { ...current[slot], value: event.target.value } }))} /></>}{secrets[slot].action === "clear" && present(slot) && <label className="flex min-h-11 items-start gap-3 py-2" htmlFor={`${id}-${slot}-confirm`}><Checkbox id={`${id}-${slot}-confirm`} className="mt-1" checked={secrets[slot].confirmed} onCheckedChange={(checked) => setSecrets((current) => ({ ...current, [slot]: { ...current[slot], confirmed: checked === true } }))} /><span className="text-sm">{t("确认清除已保存凭据；保存后不能在此找回原文。")}</span></label>}{fieldError(slot)}</div>)}</fieldset>
    <p className="text-sm leading-6 text-muted-foreground">{t("缺少相应凭据时仍可保存为待补凭据草稿。凭据完整也不表示已验证连通性、可应用或已应用。")}</p>
    <Button type="submit" className="max-w-full whitespace-normal" disabled={disabled}>{pending ? t("正在保存配置资产…") : t("仅保存配置资产")}</Button>
  </form>
}
