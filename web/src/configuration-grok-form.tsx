import { useId, useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { t } from "./i18n"
import { GrokBuildMode } from "./gen/skillhub/configuration/v1/configuration_pb"
import { grokDraft, grokFields, grokSlot, validateGrok, validGrokUnicode, type GrokFields, type GrokView } from "./configuration-grok-state"
import type { ConfigurationDraft, SecretChanges } from "./configuration-form"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"

export function ConfigurationGrokForm({ original, pending, blocked, onSubmit, onDirtyChange }: { original?: GrokView; pending: boolean; blocked: boolean; onSubmit: (draft: ConfigurationDraft, secrets: SecretChanges) => Promise<boolean>; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const id = useId()
  const form = useRef<HTMLFormElement>(null)
  const [initial] = useState(() => grokFields(original?.provider, original?.asset.name))
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
  const [pendingMode, setPendingMode] = useState<GrokBuildMode>()
  const [clearOpen, setClearOpen] = useState(false)
  const modeTrigger = useRef<HTMLButtonElement>(null)
  const secretTrigger = useRef<HTMLButtonElement>(null)
  const modeCancel = useRef<HTMLButtonElement>(null)
  const clearCancel = useRef<HTMLButtonElement>(null)
  const confirmationOpening = useRef(false)
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

  const update = <Key extends keyof GrokFields,>(key: Key, value: GrokFields[Key]) => setFields((current) => ({ ...current, [key]: value }))
  function error(field: string) {
    return errors[field] && <p id={`${id}-${field}-error`} role="alert" className="text-sm text-destructive">{t(errors[field])}</p>
  }
  function input(field: "profile" | "upstreamModel" | "endpoint" | "modelName" | "contextWindow" | "envKey", label: string) {
    return <div className="min-w-0 space-y-2"><label htmlFor={`${id}-${field}`}>{t(label)}</label><Input id={`${id}-${field}`} value={fields[field]} disabled={disabled} autoComplete="off" autoCapitalize="off" spellCheck={false} inputMode={field === "contextWindow" ? "numeric" : field === "endpoint" ? "url" : "text"} onChange={(event) => update(field, event.target.value)} aria-invalid={!!errors[field]} aria-describedby={errors[field] ? `${id}-${field}-error` : undefined} />{error(field)}</div>
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
    if (pendingMode === GrokBuildMode.NATIVE_LOGIN) {
      setFields({ ...grokFields(undefined, fields.name), mode: pendingMode })
      setAction("clear")
      setConfirmed(true)
    } else setFields((current) => ({ ...current, mode: pendingMode }))
    setErrors({})
    setPendingMode(undefined)
  }
  async function submit() {
    if (disabled || submitLock.current || pendingMode !== undefined || clearOpen) return
    submitLock.current = true
    try {
      const issues = validateGrok(fields)
      const value = replacement.current
      if (action === "replace" && (!validGrokUnicode(value) || new TextEncoder().encode(value).length < 1 || new TextEncoder().encode(value).length > 8192 || /[\0\r\n]/.test(value))) issues.secret = "新凭据须为 1–8192 UTF-8 字节，不含 NUL、CR 或 LF；不会 trim。"
      if (action === "keep" && !original) issues.secret = "新建资产不能 KEEP；请选择 REPLACE 或 CLEAR。"
      if (action === "clear" && original?.present && !confirmed) issues.secret = "请确认清除已保存凭据。"
      if (fields.mode === GrokBuildMode.NATIVE_LOGIN && (action === "replace" || action === "keep" && original?.present)) issues.secret = "Grok 原生登录不能保留服务端 key；请明确确认清除。"
      setErrors(issues)
      if (Object.keys(issues).length) return
      mutations.current = [{ slotId: grokSlot, action: action === "replace" ? { case: "replace", value } : action === "keep" ? { case: "keep", value: {} } : { case: "clear", value: {} } }]
      setSubmitting(true)
      await onSubmit(grokDraft(fields), mutations.current)
    } finally {
      clearSecret()
      setHasReplacement(false)
      submitLock.current = false
      setSubmitting(false)
    }
  }

  const present = action === "replace" ? hasReplacement : action === "keep" && !!original?.present
  return <form ref={form} className="mt-5 min-w-0 space-y-5" autoComplete="off" noValidate aria-busy={pending || submitting} onSubmit={(event) => { event.preventDefault(); void submit() }}>
    <p className="text-sm leading-6 text-muted-foreground">{t("Grok Build 服务端资产：原生登录声明与自定义模型独立。保存不读取登录凭据、不探测端点、不选择 profile、不写本机文件，也不表示已应用。")}</p>
    {readonly && <p role="alert" className="rounded-lg border border-border p-3 text-sm">{t("此资产保留未知字段，仅可只读查看已知内容。保存和复制均禁用，后端也会拒绝；仍可由原账号明确确认后按版本删除。")}</p>}
    <div className="space-y-2"><label htmlFor={`${id}-name`}>{t("配置资产名称")}</label><Input id={`${id}-name`} value={fields.name} disabled={disabled} autoComplete="off" onChange={(event) => update("name", event.target.value)} aria-invalid={!!errors.name} aria-describedby={errors.name ? `${id}-name-error` : undefined} />{error("name")}</div>
    <div className="space-y-2"><label htmlFor={`${id}-mode`}>{t("Grok 认证模式")}</label><Select value={String(fields.mode)} disabled={disabled} onValueChange={(value) => { const mode = Number(value); if (mode !== fields.mode && (mode === GrokBuildMode.NATIVE_LOGIN || mode === GrokBuildMode.CUSTOM_MODEL)) { confirmationOpening.current = true; setPendingMode(mode) } }}><SelectTrigger ref={modeTrigger} id={`${id}-mode`} className="w-full" aria-invalid={!!errors.mode} aria-describedby={errors.mode ? `${id}-mode-error` : undefined}><SelectValue /></SelectTrigger><SelectContent onCloseAutoFocus={(event) => { if (confirmationOpening.current) event.preventDefault() }}><SelectItem value={String(GrokBuildMode.NATIVE_LOGIN)}>{t("Grok 原生登录（native-login）")}</SelectItem><SelectItem value={String(GrokBuildMode.CUSTOM_MODEL)}>{t("Grok 自定义模型（custom-model）")}</SelectItem></SelectContent></Select>{error("mode")}</div>
    {fields.mode === GrokBuildMode.CUSTOM_MODEL && <fieldset disabled={disabled} className="min-w-0 space-y-4"><legend className="font-medium">{t("Grok 自定义模型")}</legend><div className="grid min-w-0 gap-4 sm:grid-cols-2">
      {input("profile", "本地 profile（最多 128 UTF-8 字节）")}{input("upstreamModel", "上游 model（最多 256 UTF-8 字节）")}
      <div className="sm:col-span-2">{input("endpoint", "Grok HTTPS 端点")}</div>{input("modelName", "模型显示名称（最多 128 UTF-8 字节）")}
      <div className="space-y-2"><label htmlFor={`${id}-apiBackend`}>{t("Grok API backend")}</label><Select value={fields.apiBackend || "unselected"} disabled={disabled} onValueChange={(value) => { if (value === "responses") update("apiBackend", value) }}><SelectTrigger id={`${id}-apiBackend`} className="w-full" aria-invalid={!!errors.apiBackend} aria-describedby={errors.apiBackend ? `${id}-apiBackend-error` : undefined}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unselected" disabled>{t("请显式选择 backend")}</SelectItem><SelectItem value="responses">responses</SelectItem></SelectContent></Select>{error("apiBackend")}</div>
      {input("contextWindow", "Context window（精确整数）")}
    </div><p className="text-sm text-muted-foreground">{t("Profile 是本地名称，不等于上游 model。字段保留原文；context window 不经浮点数转换。")}</p>
    <label className="flex min-h-11 items-center gap-3" htmlFor={`${id}-envEnabled`}><Checkbox id={`${id}-envEnabled`} disabled={disabled} checked={fields.envEnabled} onCheckedChange={(checked) => update("envEnabled", checked === true)} />{t("声明环境变量名（不读取变量值）")}</label>{fields.envEnabled && input("envKey", "Grok API key 环境变量名")}<p className="text-sm text-muted-foreground">{t("环境变量名与服务端 key 可共存；声明引用不证明客户端存在该变量，也不证明端点可用。")}</p></fieldset>}
    <fieldset disabled={disabled} className="min-w-0 space-y-3"><legend className="font-medium">{t("凭据变更")}</legend><p className="text-sm text-muted-foreground">{original?.present ? t("已保存凭据：存在") : t("已保存凭据：不存在")}</p>{fields.mode === GrokBuildMode.CUSTOM_MODEL && <>
      <label className="block text-sm" htmlFor={`${id}-secret`}>{t("grokbuild.api-key 操作")}</label><Select value={action} disabled={disabled} onValueChange={(value) => { if (value !== "keep" && value !== "replace" && value !== "clear") return; clearSecret(); setHasReplacement(false); if (value === "clear" && original?.present) { confirmationOpening.current = true; setClearOpen(true); return }; setAction(value); setConfirmed(false) }}><SelectTrigger ref={secretTrigger} id={`${id}-secret`} className="w-full" aria-invalid={!!errors.secret} aria-describedby={errors.secret ? `${id}-secret-error` : undefined}><SelectValue /></SelectTrigger><SelectContent onCloseAutoFocus={(event) => { if (confirmationOpening.current) event.preventDefault() }}>{original && <SelectItem value="keep">{t("保留原凭据（keep）")}</SelectItem>}<SelectItem value="replace">{t("输入新凭据（replace）")}</SelectItem><SelectItem value="clear">{original ? t("清除凭据（clear）") : t("不提供凭据")}</SelectItem></SelectContent></Select>
      {action === "replace" && <div className="space-y-2"><label htmlFor={`${id}-replacement`}>{t("新凭据")}</label><Input ref={password} id={`${id}-replacement`} type="password" disabled={disabled} autoComplete="new-password" autoCapitalize="off" spellCheck={false} onChange={(event) => { replacement.current = event.target.value; setHasReplacement(event.target.value !== "") }} aria-invalid={!!errors.secret} aria-describedby={errors.secret ? `${id}-secret-error` : undefined} /></div>}
    </>}{error("secret")}</fieldset>
    <section aria-label={t("Grok 脱敏审阅")} className="min-w-0 rounded-lg border border-border p-4"><h4 className="font-medium">{t("Grok 脱敏审阅")}</h4><dl className="mt-3 grid min-w-0 gap-3 text-sm sm:grid-cols-2">{[["mode", fields.mode === GrokBuildMode.NATIVE_LOGIN ? "native-login" : "custom-model"], ...(fields.mode === GrokBuildMode.CUSTOM_MODEL ? [["profile", fields.profile], ["upstreamModel", fields.upstreamModel], ["endpoint", fields.endpoint], ["name", fields.modelName], ["apiBackend", fields.apiBackend], ["contextWindow", fields.contextWindow], ["envKey", fields.envEnabled ? fields.envKey : t("未设置（absent）")]] : []), [grokSlot, present ? t("存在") : t("不存在")]].map(([label, value]) => <div key={label} className="min-w-0"><dt className="break-all font-mono text-muted-foreground">{label}</dt><dd className="mt-1 whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{value}</dd></div>)}</dl></section>
    <p className="text-sm leading-6 text-muted-foreground">{t("缺少相应凭据时仍可保存为待补凭据草稿。凭据完整也不表示已验证连通性、可应用或已应用。")}</p><Button type="submit" disabled={disabled} className="max-w-full whitespace-normal">{pending || submitting ? t("正在保存配置资产…") : t("仅保存配置资产")}</Button>
    <Dialog open={pendingMode !== undefined} onOpenChange={(opened) => { if (!opened) cancelMode() }}><DialogContent onOpenAutoFocus={(event) => { event.preventDefault(); modeCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); confirmationOpening.current = false; modeTrigger.current?.focus() }}><DialogTitle>{t("确认更改 Grok 认证模式？")}</DialogTitle><DialogDescription>{pendingMode === GrokBuildMode.NATIVE_LOGIN ? t("切换到原生登录将移除本次模型声明并显式 CLEAR grokbuild.api-key；仅保存后生效，不修改本机登录。取消保留非秘密草稿，但清除尚未提交的新凭据。") : t("切换到自定义模型后需填写完整模型字段并显式选择 backend；不会推断默认值或读取本机凭据。取消保留非秘密草稿，但清除尚未提交的新凭据。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button ref={modeCancel} type="button" variant="outline" onClick={cancelMode}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={confirmMode}>{t("确认更改认证模式")}</Button></div></DialogContent></Dialog>
    <Dialog open={clearOpen} onOpenChange={setClearOpen}><DialogContent onOpenAutoFocus={(event) => { event.preventDefault(); clearCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); confirmationOpening.current = false; secretTrigger.current?.focus() }}><DialogTitle>{t("确认清除 Grok 已保存的 key？")}</DialogTitle><DialogDescription>{t("确认仅设置本次 CLEAR 操作；保存后不能在此找回原文，不删除本机文件或登录凭据。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button ref={clearCancel} type="button" variant="outline" onClick={() => setClearOpen(false)}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={() => { clearSecret(); setHasReplacement(false); setAction("clear"); setConfirmed(true); setClearOpen(false) }}>{t("确认清除 grokbuild.api-key")}</Button></div></DialogContent></Dialog>
  </form>
}
