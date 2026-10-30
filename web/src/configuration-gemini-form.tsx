import { useId, useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { t } from "./i18n"
import { GeminiAuthMode, GeminiKeyTransport } from "./gen/skillhub/configuration/v1/configuration_pb"
import { geminiDraft, geminiFields, geminiPreview, geminiSlot, validateGemini, validGeminiUnicode, type GeminiFields, type GeminiView } from "./configuration-gemini-state"
import type { ConfigurationDraft, SecretChanges } from "./configuration-form"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"

export function ConfigurationGeminiForm({ original, pending, blocked, onSubmit, onDirtyChange }: { original?: GeminiView; pending: boolean; blocked: boolean; onSubmit: (draft: ConfigurationDraft, secrets: SecretChanges) => Promise<boolean>; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const id = useId()
  const form = useRef<HTMLFormElement>(null)
  const [initial] = useState(() => geminiFields(original?.provider, original?.asset.name))
  const [fields, setFields] = useState(initial)
  const [action, setAction] = useState<"keep" | "replace" | "clear">(original ? "keep" : "clear")
  const [confirmed, setConfirmed] = useState(false)
  const [hasReplacement, setHasReplacement] = useState(false)
  const password = useRef<HTMLInputElement>(null)
  const replacement = useRef("")
  const mutations = useRef<SecretChanges>([])
  const submitLock = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [pendingMode, setPendingMode] = useState<GeminiAuthMode>()
  const [clearOpen, setClearOpen] = useState(false)
  const modeTrigger = useRef<HTMLButtonElement>(null)
  const secretTrigger = useRef<HTMLButtonElement>(null)
  const confirmationOpening = useRef(false)
  const modeCancel = useRef<HTMLButtonElement>(null)
  const clearCancel = useRef<HTMLButtonElement>(null)
  const readonly = original?.mode === "readonly"
  const disabled = pending || blocked || readonly || submitting

  function clearSecret() {
    replacement.current = ""
    if (password.current) password.current.value = ""
    for (const mutation of mutations.current) mutation.action = { case: "clear", value: {} }
    mutations.current = []
  }
  useLayoutEffect(() => {
    clearSecret()
    setHasReplacement(false)
    setErrors({})
    return clearSecret
  }, [i18n.resolvedLanguage])
  useLayoutEffect(() => {
    onDirtyChange(!readonly && (JSON.stringify(fields) !== JSON.stringify(initial) || action !== (original ? "keep" : "clear") || hasReplacement || confirmed || submitting))
  }, [fields, initial, action, original, hasReplacement, confirmed, submitting, readonly, onDirtyChange])
  useLayoutEffect(() => {
    if (Object.keys(errors).length) form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
  }, [errors])

  const update = <Key extends keyof GeminiFields,>(key: Key, value: GeminiFields[Key]) => setFields((current) => ({ ...current, [key]: value }))
  function error(field: string) {
    return errors[field] && <p id={`${id}-${field}-error`} role="alert" className="text-sm text-destructive">{t(errors[field])}</p>
  }
  function optional(field: "endpoint" | "envModel" | "settingsModel", label: string, inputLabel: string) {
    return <div className="min-w-0 space-y-2"><label className="flex min-h-11 items-center gap-3" htmlFor={`${id}-${field}-enabled`}><Checkbox id={`${id}-${field}-enabled`} disabled={disabled} checked={fields[field].enabled} onCheckedChange={(checked) => update(field, { ...fields[field], enabled: checked === true })} />{t(label)}</label>{fields[field].enabled && <><label className="block text-sm" htmlFor={`${id}-${field}`}>{t(inputLabel)}</label><Input id={`${id}-${field}`} value={fields[field].value} disabled={disabled} autoComplete="off" spellCheck={false} inputMode={field === "endpoint" ? "url" : "text"} onChange={(event) => update(field, { ...fields[field], value: event.target.value })} aria-invalid={!!errors[field]} aria-describedby={errors[field] ? `${id}-${field}-error` : undefined} /></>}{error(field)}</div>
  }
  function cancelMode() {
    clearSecret()
    setHasReplacement(false)
    setPendingMode(undefined)
  }
  function confirmMode() {
    if (disabled || pendingMode === undefined) return
    clearSecret()
    setHasReplacement(false)
    setFields((current) => ({ ...current, authMode: pendingMode, endpoint: pendingMode === GeminiAuthMode.NATIVE_LOGIN ? { enabled: false, value: "" } : current.endpoint, keyTransport: undefined }))
    if (pendingMode === GeminiAuthMode.NATIVE_LOGIN) { setAction("clear"); setConfirmed(true) }
    setErrors({})
    setPendingMode(undefined)
  }
  async function submit() {
    if (disabled || submitLock.current || pendingMode !== undefined || clearOpen) return
    submitLock.current = true
    try {
      const issues = validateGemini(fields)
      if (action === "replace" && (!validGeminiUnicode(replacement.current) || new TextEncoder().encode(replacement.current).length < 1 || new TextEncoder().encode(replacement.current).length > 8192 || /[\0\r\n]/.test(replacement.current))) issues.secret = "新凭据须为 1–8192 UTF-8 字节，不含 NUL、CR 或 LF；不会 trim。"
      if (action === "keep" && !original) issues.secret = "新建资产不能 KEEP；请选择 REPLACE 或 CLEAR。"
      if (action === "clear" && original?.present && !confirmed) issues.secret = "请确认清除已保存凭据。"
      if (fields.authMode === GeminiAuthMode.NATIVE_LOGIN && (action === "replace" || action === "keep" && original?.present)) issues.secret = "Gemini 原生登录不能保留服务端 key；请明确确认清除。"
      setErrors(issues)
      if (Object.keys(issues).length) return
      mutations.current = [{ slotId: geminiSlot, action: action === "replace" ? { case: "replace", value: replacement.current } : action === "keep" ? { case: "keep", value: {} } : { case: "clear", value: {} } }]
      setSubmitting(true)
      await onSubmit(geminiDraft(fields), mutations.current)
    } finally {
      clearSecret()
      setHasReplacement(false)
      submitLock.current = false
      setSubmitting(false)
    }
  }

  const preview = geminiPreview(fields, action === "replace" ? hasReplacement : action === "keep" && !!original?.present)
  const absent = t("未设置（absent）")
  return <form ref={form} className="mt-5 min-w-0 space-y-5" autoComplete="off" noValidate aria-busy={pending || submitting} onSubmit={(event) => { event.preventDefault(); void submit() }}>
    <p className="text-sm leading-6 text-muted-foreground">{t("Gemini 仅管理原生登录声明或 API key provider 资产。保存不读取登录凭据、不探测端点、不选择 provider、不写设备文件，也不表示可应用或已经登录。")}</p>
    {readonly && <p role="alert" className="rounded-lg border border-border p-3 text-sm">{t("此资产保留未知字段，仅可只读查看已知内容。保存和复制均禁用，后端也会拒绝；仍可由原账号明确确认后按版本删除。")}</p>}
    <div className="space-y-2"><label htmlFor={`${id}-name`}>{t("配置资产名称")}</label><Input id={`${id}-name`} value={fields.name} disabled={disabled} autoComplete="off" onChange={(event) => update("name", event.target.value)} aria-invalid={!!errors.name} aria-describedby={errors.name ? `${id}-name-error` : undefined} />{error("name")}</div>
    <div className="space-y-2"><label htmlFor={`${id}-authMode`}>{t("Gemini 认证模式")}</label><Select value={String(fields.authMode)} disabled={disabled} onValueChange={(value) => { const mode = Number(value); if (mode !== fields.authMode && (mode === GeminiAuthMode.NATIVE_LOGIN || mode === GeminiAuthMode.API_KEY)) { confirmationOpening.current = true; setPendingMode(mode) } }}><SelectTrigger ref={modeTrigger} id={`${id}-authMode`} className="w-full" aria-invalid={!!errors.authMode} aria-describedby={errors.authMode ? `${id}-authMode-error` : undefined}><SelectValue /></SelectTrigger><SelectContent onCloseAutoFocus={(event) => { if (confirmationOpening.current) event.preventDefault() }}><SelectItem value={String(GeminiAuthMode.NATIVE_LOGIN)}>{t("原生 Gemini 登录（native-login）")}</SelectItem><SelectItem value={String(GeminiAuthMode.API_KEY)}>{t("API key")}</SelectItem></SelectContent></Select>{error("authMode")}</div>
    {fields.authMode === GeminiAuthMode.NATIVE_LOGIN ? <p className="text-sm leading-6 text-muted-foreground">{t("由原生 Gemini 登录；Cognate 不读取、导入或刷新登录凭据。凭据结构完整不证明此设备已登录。")}</p> : <fieldset disabled={disabled} className="min-w-0 space-y-4"><legend className="font-medium">{t("Gemini API provider")}</legend>
      {optional("endpoint", "设置 Gemini 端点 override", "Gemini HTTPS 端点")}
      <div className="space-y-2"><label htmlFor={`${id}-keyTransport`}>{t("Gemini key 传输方式（必选）")}</label><Select value={fields.keyTransport === undefined ? "unselected" : String(fields.keyTransport)} disabled={disabled} onValueChange={(value) => { const transport = Number(value); if (transport === GeminiKeyTransport.NATIVE_DEFAULT || transport === GeminiKeyTransport.BEARER) update("keyTransport", transport) }}><SelectTrigger id={`${id}-keyTransport`} className="w-full" aria-invalid={!!errors.keyTransport} aria-describedby={`${id}-transport-help${errors.keyTransport ? ` ${id}-keyTransport-error` : ""}`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unselected" disabled>{t("请显式选择传输方式")}</SelectItem><SelectItem value={String(GeminiKeyTransport.NATIVE_DEFAULT)}>{t("native-default（不声明传输 override）")}</SelectItem><SelectItem value={String(GeminiKeyTransport.BEARER)}>bearer</SelectItem></SelectContent></Select>{error("keyTransport")}<p id={`${id}-transport-help`} className="text-sm text-muted-foreground">{t("两种传输方式共用 gemini.api-key；不会推断 header、借用 OAuth 或生成第二个 token。")}</p></div>
    </fieldset>}
    <fieldset disabled={disabled} className="min-w-0 space-y-3"><legend className="font-medium">{t("Gemini 独立模型来源")}</legend>{optional("envModel", "设置 GEMINI_MODEL", ".env 的 GEMINI_MODEL")}{optional("settingsModel", "设置 settings.json model.name", "settings.json 的 model.name")}<p className="text-sm text-muted-foreground">{t("两个模型各自区分 absent 与显式值，保留大小写，不同步、不 trim、不补默认；取消设置仅移除服务端资产 override。")}</p>{fields.envModel.enabled && fields.settingsModel.enabled && fields.envModel.value !== fields.settingsModel.value && <p role="status" className="rounded-lg border border-border p-3 text-sm">{t("两个 native 来源不同；未验证运行时优先级。资产可以保存，但不代表已选定有效的唯一模型。")}</p>}</fieldset>
    <fieldset disabled={disabled} className="min-w-0 space-y-3"><legend className="font-medium">{t("凭据变更")}</legend><p className="text-sm text-muted-foreground">{original?.present ? t("已保存凭据：存在") : t("已保存凭据：不存在")}</p>{fields.authMode === GeminiAuthMode.API_KEY ? <>
      <label className="block text-sm" htmlFor={`${id}-secret`}>{t("gemini.api-key 操作")}</label><Select value={action} disabled={disabled} onValueChange={(value) => { if (value !== "keep" && value !== "replace" && value !== "clear") return; clearSecret(); setHasReplacement(false); if (value === "clear" && original?.present) { confirmationOpening.current = true; setClearOpen(true); return }; setAction(value); setConfirmed(false) }}><SelectTrigger ref={secretTrigger} id={`${id}-secret`} className="w-full" aria-invalid={!!errors.secret} aria-describedby={errors.secret ? `${id}-secret-error` : undefined}><SelectValue /></SelectTrigger><SelectContent onCloseAutoFocus={(event) => { if (confirmationOpening.current) event.preventDefault() }}>{original && <SelectItem value="keep">{t("保留原凭据（keep）")}</SelectItem>}<SelectItem value="replace">{t("输入新凭据（replace）")}</SelectItem><SelectItem value="clear">{original ? t("清除凭据（clear）") : t("不提供凭据")}</SelectItem></SelectContent></Select>
      {action === "replace" && <div className="space-y-2"><label className="block text-sm" htmlFor={`${id}-replacement`}>{t("新凭据")}</label><Input ref={password} id={`${id}-replacement`} type="password" disabled={disabled} autoComplete="new-password" autoCapitalize="off" spellCheck={false} onChange={(event) => { replacement.current = event.target.value; setHasReplacement(event.target.value !== "") }} aria-invalid={!!errors.secret} aria-describedby={errors.secret ? `${id}-secret-error` : undefined} /></div>}
    </> : <p className="text-sm text-muted-foreground">{t("原生登录不使用服务端 key；本次凭据操作只保留不存在的槽或明确 CLEAR。")}</p>}{action === "clear" && original?.present && confirmed && <p className="text-sm">{t("已明确确认：保存时清除 gemini.api-key。尚未提交，不会修改设备登录态。")}</p>}{error("secret")}<p className="text-sm text-muted-foreground">{t("新凭据仅暂存在表单内存。提交成功或失败、语言切换、确认放弃、账号变化及卸载时清除；KEEP 从不恢复或回显原值。")}</p></fieldset>
    <section aria-labelledby={`${id}-preview`} className="min-w-0 space-y-3"><h3 id={`${id}-preview`} className="font-medium">{t("Gemini provider-owned 逻辑预览（脱敏只读）")}</h3><p className="text-sm leading-6 text-muted-foreground">{t("不含全局或未知内容，非可直接安装文件；slot presence 只表示资产中的凭据状态，不验证 key 或本机登录。")}</p><div className="min-w-0 space-y-4 rounded-lg border border-border p-3"><h4 className="font-mono text-sm">.env</h4><dl className="space-y-3 text-sm">{[["GOOGLE_GEMINI_BASE_URL", preview.env.endpoint], ["GEMINI_MODEL", preview.env.model], ["GEMINI_API_KEY", preview.env.credential ? `${preview.env.credential.slotId} · ${preview.env.credential.present ? t("本次草稿槽：存在") : t("本次草稿槽：不存在")}` : undefined], ["GEMINI_API_KEY_AUTH_MECHANISM", preview.env.transport]].map(([label, value]) => <div key={label} className="min-w-0"><dt className="break-all font-mono text-muted-foreground">{label}</dt><dd className="mt-1 whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{value ?? absent}</dd></div>)}</dl></div><div className="min-w-0 space-y-4 rounded-lg border border-border p-3"><h4 className="font-mono text-sm">settings.json</h4><dl className="space-y-3 text-sm">{[["security.auth.selectedType", preview.settings.selectedType], ["model.name", preview.settings.model]].map(([label, value]) => <div key={label} className="min-w-0"><dt className="break-all font-mono text-muted-foreground">{label}</dt><dd className="mt-1 whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{value ?? absent}</dd></div>)}</dl></div></section>
    <p className="text-sm leading-6 text-muted-foreground">{t("缺少相应凭据时仍可保存为待补凭据草稿。凭据完整也不表示已验证连通性、可应用或已应用。")}</p><Button type="submit" disabled={disabled} className="max-w-full whitespace-normal">{pending || submitting ? t("正在保存配置资产…") : t("仅保存配置资产")}</Button>
    <Dialog open={pendingMode !== undefined} onOpenChange={(opened) => { if (!opened) cancelMode() }}><DialogContent onOpenAutoFocus={(event) => { event.preventDefault(); modeCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); confirmationOpening.current = false; modeTrigger.current?.focus() }}><DialogTitle>{t("确认更改 Gemini 认证模式？")}</DialogTitle><DialogDescription>{t("确认前保留当前非秘密草稿；取消也会清除尚未提交的新凭据。仅保存后更改此服务端资产，不触及本机 OAuth。")}</DialogDescription><ul className="mt-4 list-disc space-y-2 pl-5 text-sm">{pendingMode === GeminiAuthMode.NATIVE_LOGIN ? <><li>{fields.endpoint.enabled ? t("移除资产 endpoint override。") : t("资产 endpoint 保持 absent。")}</li><li>{t("移除资产 keyTransport 声明。")}</li><li>{t("本次显式 CLEAR gemini.api-key，并清除未提交的新凭据。")}</li></> : <><li>{t("必须另行显式选择 keyTransport，不会推断 native-default 或 bearer。")}</li><li>{t("不读取本机登录凭据；可以无 key 保存为待补凭据资产。")}</li></>}<li>{t("两处模型的 presence 与原值保持独立不变。")}</li></ul><div className="mt-5 flex flex-wrap justify-end gap-3"><Button ref={modeCancel} type="button" variant="outline" autoFocus onClick={cancelMode}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={confirmMode}>{t("确认更改认证模式")}</Button></div></DialogContent></Dialog>
    <Dialog open={clearOpen} onOpenChange={setClearOpen}><DialogContent onOpenAutoFocus={(event) => { event.preventDefault(); clearCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); confirmationOpening.current = false; secretTrigger.current?.focus() }}><DialogTitle>{t("确认清除 Gemini 已保存的 key？")}</DialogTitle><DialogDescription>{t("确认仅设置本次 CLEAR 操作；保存后不能在此找回原文，不删除本机文件或登录凭据。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button ref={clearCancel} type="button" variant="outline" autoFocus onClick={() => setClearOpen(false)}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={() => { clearSecret(); setHasReplacement(false); setAction("clear"); setConfirmed(true); setClearOpen(false) }}>{t("确认清除 gemini.api-key")}</Button></div></DialogContent></Dialog>
  </form>
}
