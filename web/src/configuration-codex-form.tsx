import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { t } from "./i18n"
import { CodexModality } from "./gen/skillhub/configuration/v1/configuration_pb"
import { codexDraft, codexFields, codexSlot, effortLabels, efforts, modalities, validateCodex, validUnicode, type CatalogRow, type CodexFields, type CodexView } from "./configuration-codex-state"
import type { ConfigurationDraft, SecretChanges } from "./configuration-form"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"

export function ConfigurationCodexForm({ original, pending, blocked, onSubmit, onDirtyChange }: { original?: CodexView; pending: boolean; blocked: boolean; onSubmit: (draft: ConfigurationDraft, secrets: SecretChanges) => Promise<boolean>; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const id = useId()
  const [initial] = useState(() => codexFields(original?.provider, original?.asset.name))
  const [fields, setFields] = useState<CodexFields>(initial)
  const [action, setAction] = useState<"keep" | "replace" | "clear">(original ? "keep" : "clear")
  const [confirmed, setConfirmed] = useState(false)
  const [hasReplacement, setHasReplacement] = useState(false)
  const password = useRef<HTMLInputElement>(null)
  const replacement = useRef("")
  const mutations = useRef<SecretChanges>([])
  const rowIds = useRef(initial.catalog?.map((_, index) => index) ?? [])
  const nextRow = useRef(rowIds.current.length)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [clearCatalog, setClearCatalog] = useState(false)
  const [submitting, setSubmitting] = useState(false)
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

  const update = <Key extends keyof CodexFields,>(key: Key, value: CodexFields[Key]) => setFields((current) => ({ ...current, [key]: value }))
  function updateRow(index: number, change: Partial<CatalogRow>) {
    setFields((current) => ({ ...current, catalog: current.catalog?.map((row, position) => position === index ? { ...row, ...change } : row) }))
  }
  function moveRow(index: number, direction: number) {
    const target = index + direction
    const rows = [...(fields.catalog ?? [])]
    if (target < 0 || target >= rows.length) return
    ;[rows[index], rows[target]] = [rows[target], rows[index]]
    ;[rowIds.current[index], rowIds.current[target]] = [rowIds.current[target], rowIds.current[index]]
    update("catalog", rows)
  }
  function error(field: string) {
    return errors[field] && <p id={`${id}-${field}-error`} role="alert" className="text-sm text-destructive">{t(errors[field])}</p>
  }
  function textField(field: string, label: string, value: string, change: (value: string) => void, multiline = false) {
    const props = { id: `${id}-${field}`, value, disabled, autoComplete: "off", spellCheck: false, "aria-invalid": !!errors[field], "aria-describedby": errors[field] ? `${id}-${field}-error` : undefined, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => change(event.target.value) }
    return <div className="min-w-0 space-y-2"><label className="block text-sm" htmlFor={props.id}>{t(label)}</label>{multiline ? <textarea {...props} rows={5} className="min-h-28 w-full min-w-0 resize-y rounded-lg border border-border bg-card p-3 text-sm disabled:opacity-50" /> : <Input {...props} />}{error(field)}</div>
  }
  function choice(field: string, label: string, value: string, options: { value: string; label: string; disabled?: boolean }[], change: (value: string) => void) {
    return <div className="space-y-2"><label className="block text-sm" htmlFor={`${id}-${field}`}>{t(label)}</label><Select value={value} disabled={disabled} onValueChange={change}><SelectTrigger id={`${id}-${field}`} className="w-full" aria-invalid={!!errors[field]} aria-describedby={errors[field] ? `${id}-${field}-error` : undefined}><SelectValue /></SelectTrigger><SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value} disabled={option.disabled}>{t(option.label)}</SelectItem>)}</SelectContent></Select>{error(field)}</div>
  }
  const effortOptions = [{ value: "absent", label: "未设置（absent）" }, ...efforts.map((value, index) => ({ value: String(value), label: effortLabels[index] }))]
  function effort(field: string, label: string, value: number | undefined, change: (value: typeof efforts[number] | undefined) => void, allowed: readonly number[] = efforts) {
    const options: { value: string; label: string; disabled?: boolean }[] = effortOptions.filter((option) => option.value === "absent" || allowed.some((entry) => String(entry) === option.value))
    if (value !== undefined && !allowed.includes(value)) options.push({ value: String(value), label: t("引用无效，请显式修复"), disabled: true })
    return choice(field, label, value === undefined ? "absent" : String(value), options, (selected) => change(efforts.find((entry) => String(entry) === selected)))
  }
  function optional(field: string, label: string, enabled: boolean, toggle: (enabled: boolean) => void, children: ReactNode) {
    return <div className="min-w-0 space-y-2"><label className="flex min-h-11 items-center gap-3" htmlFor={`${id}-${field}-enabled`}><Checkbox id={`${id}-${field}-enabled`} disabled={disabled} checked={enabled} onCheckedChange={(checked) => toggle(checked === true)} />{t(label)}</label>{enabled && children}</div>
  }
  function orderedSet<Value extends number>(field: string, label: string, values: Value[] | undefined, allowed: readonly Value[], labelOf: (value: Value) => string, change: (values: Value[] | undefined) => void) {
    return optional(field, label, values !== undefined, (enabled) => change(enabled ? [] : undefined), <div className="space-y-3 rounded-lg border border-border p-3"><div className="flex flex-wrap gap-3">{allowed.map((value) => <label key={value} className="flex min-h-11 items-center gap-2" htmlFor={`${id}-${field}-${value}`}><Checkbox id={`${id}-${field}-${value}`} disabled={disabled} checked={values?.includes(value) ?? false} onCheckedChange={(checked) => change(checked === true ? [...(values ?? []), value] : values?.filter((entry) => entry !== value))} />{labelOf(value)}</label>)}</div><ol className="space-y-2">{values?.map((value, position) => <li key={value} className="flex flex-wrap items-center gap-2"><span className="mr-auto font-mono text-sm">{position + 1}. {labelOf(value)}</span>{[-1, 1].map((direction) => <Button key={direction} type="button" variant="outline" disabled={disabled || position + direction < 0 || position + direction >= values.length} aria-label={`${t(label)} ${labelOf(value)} ${t(direction < 0 ? "上移" : "下移")}`} onClick={() => { const reordered = [...values]; [reordered[position], reordered[position + direction]] = [reordered[position + direction], reordered[position]]; change(reordered) }}>{t(direction < 0 ? "上移" : "下移")}</Button>)}</li>)}</ol>{error(field)}</div>)
  }

  async function submit() {
    if (disabled) return
    const issues = validateCodex(fields)
    if (action === "replace" && (!validUnicode(replacement.current) || new TextEncoder().encode(replacement.current).length < 1 || new TextEncoder().encode(replacement.current).length > 8192 || /[\0\r\n]/.test(replacement.current))) issues.secret = "新凭据须为 1–8192 UTF-8 字节，不含 NUL、CR 或 LF；不会 trim。"
    if (action === "keep" && !original) issues.secret = "新建资产不能 KEEP；请选择 REPLACE 或 CLEAR。"
    if (action === "clear" && original?.present && !confirmed) issues.secret = "请确认清除已保存凭据。"
    setErrors(issues)
    if (Object.keys(issues).length) { clearSecret(); setHasReplacement(false); return }
    mutations.current = [{ slotId: codexSlot, action: action === "replace" ? { case: "replace", value: replacement.current } : action === "keep" ? { case: "keep", value: {} } : { case: "clear", value: {} } }]
    setSubmitting(true)
    try { await onSubmit(codexDraft(fields), mutations.current) } finally { clearSecret(); setHasReplacement(false); setSubmitting(false) }
  }

  return <form className="mt-5 min-w-0 space-y-5" autoComplete="off" noValidate aria-busy={pending || submitting} onSubmit={(event) => { event.preventDefault(); void submit() }}>
    <p className="text-sm leading-6 text-muted-foreground">{t("Codex 仅支持自定义 HTTPS Responses / provider-key 资产。官方 ChatGPT / OAuth 账号不在此表单支持范围内。保存不选择 provider，不写设备文件，也不代表可应用。")}</p>
    {readonly && <p role="alert" className="rounded-lg border border-border p-3 text-sm">{t("此资产保留未知字段，仅可只读查看已知内容。保存和复制均禁用，后端也会拒绝；仍可由原账号明确确认后按版本删除。")}</p>}
    {textField("name", "配置资产名称", fields.name, (value) => update("name", value))}
    <p className="text-sm">{t("认证模式：provider-key（仅此模式）")}</p>
    {textField("endpoint", "Codex HTTPS 端点", fields.endpoint, (value) => update("endpoint", value))}
    {textField("model", "Codex 主模型 ID", fields.model, (value) => update("model", value))}
    {effort("reasoningEffort", "顶层推理等级", fields.reasoningEffort, (value) => update("reasoningEffort", value))}
    <p className="text-sm text-muted-foreground">{t("所有可选项区分未设置与显式值。关闭设置仅移除服务端资产 override，不授权删除本机字段；不补默认、不排序、不 trim。")}</p>
    <fieldset className="min-w-0 space-y-4" disabled={disabled}><legend className="font-medium">{t("Codex 模型目录")}</legend>
      {optional("catalog", "设置目录 override", fields.catalog !== undefined, (enabled) => { if (enabled) update("catalog", []); else setClearCatalog(true) }, <>
        <p className="text-sm text-muted-foreground">{t("目录最多 32 行。删除或更改模型 ID 后必须显式修复主模型和推理引用；不会静默修复。")}</p>
        {fields.catalog?.map((row, index) => {
          const prefix = `catalog.${index}.`
          return <fieldset key={rowIds.current[index]} className="min-w-0 space-y-4 rounded-lg border border-border p-3 sm:p-4"><legend className="px-1 text-sm font-medium">{t("目录模型 {{index}}", { index: index + 1 })}</legend>
            {textField(prefix + "model", "模型 ID", row.model, (value) => updateRow(index, { model: value }))}
            {optional(prefix + "displayName", "设置展示名", row.displayName !== undefined, (enabled) => updateRow(index, { displayName: enabled ? "" : undefined }), textField(prefix + "displayName", "展示名", row.displayName ?? "", (value) => updateRow(index, { displayName: value })))}
            {optional(prefix + "contextWindow", "设置上下文窗口", row.contextWindow !== undefined, (enabled) => updateRow(index, { contextWindow: enabled ? "" : undefined }), textField(prefix + "contextWindow", "上下文窗口（精确整数）", row.contextWindow ?? "", (value) => updateRow(index, { contextWindow: value })))}
            {choice(prefix + "supportsParallelToolCalls", "并行工具调用", row.supportsParallelToolCalls === undefined ? "absent" : String(row.supportsParallelToolCalls), [{ value: "absent", label: "未设置（absent）" }, { value: "false", label: "false" }, { value: "true", label: "true" }], (value) => updateRow(index, { supportsParallelToolCalls: value === "absent" ? undefined : value === "true" }))}
            {orderedSet(prefix + "inputModalities", "设置有序输入模态", row.inputModalities, modalities, (value) => value === CodexModality.TEXT ? "text" : "image", (value) => updateRow(index, { inputModalities: value }))}
            {optional(prefix + "baseInstructions", "设置私有基础指令", row.baseInstructions !== undefined, (enabled) => updateRow(index, { baseInstructions: enabled ? "" : undefined }), textField(prefix + "baseInstructions", "私有基础指令（保留原文）", row.baseInstructions ?? "", (value) => updateRow(index, { baseInstructions: value }), true))}
            {orderedSet(prefix + "reasoningLevels", "设置有序推理等级", row.reasoningLevels, efforts, (value) => effortLabels[efforts.findIndex((entry) => entry === value)] ?? "", (value) => updateRow(index, { reasoningLevels: value }))}
            {effort(prefix + "defaultReasoningLevel", "默认推理等级", row.defaultReasoningLevel, (value) => updateRow(index, { defaultReasoningLevel: value }), row.reasoningLevels ?? [])}
            <Button type="button" variant="outline" disabled={disabled || !row.model || row.model === fields.model} onClick={() => update("model", row.model)}>{row.model === fields.model ? t("当前主模型") : t("设为主模型")}</Button>
            <div className="flex flex-wrap gap-3">{[-1, 1].map((direction) => <Button key={direction} type="button" variant="outline" disabled={disabled || index + direction < 0 || index + direction >= (fields.catalog?.length ?? 0)} aria-label={`${t("目录模型 {{index}}", { index: index + 1 })} ${t(direction < 0 ? "上移" : "下移")}`} onClick={() => moveRow(index, direction)}>{t(direction < 0 ? "上移" : "下移")}</Button>)}<Button type="button" variant="outline" disabled={disabled} onClick={() => { rowIds.current.splice(index, 1); update("catalog", fields.catalog?.filter((_, position) => position !== index)) }}>{t("删除此目录行")}</Button></div>
          </fieldset>
        })}
        <Button type="button" variant="outline" disabled={disabled || (fields.catalog?.length ?? 0) >= 32} onClick={() => { rowIds.current.push(nextRow.current++); update("catalog", [...(fields.catalog ?? []), { model: "" }]) }}>{t("添加目录模型")}</Button>
      </>)}{error("catalog")}
    </fieldset>
    <fieldset className="min-w-0 space-y-3" disabled={disabled}><legend className="font-medium">{t("凭据变更")}</legend><p className="text-sm text-muted-foreground">{original?.present ? t("已保存凭据：存在") : t("已保存凭据：不存在")}</p>
      {choice("secret", "codex.provider-key 操作", action, [...(original ? [{ value: "keep", label: "保留原凭据（keep）" }] : []), { value: "replace", label: "输入新凭据（replace）" }, { value: "clear", label: original ? "清除凭据（clear）" : "不提供凭据" }], (value) => { if (value !== "keep" && value !== "replace" && value !== "clear") return; clearSecret(); setHasReplacement(false); setAction(value); setConfirmed(false) })}
      {action === "replace" && <div className="space-y-2"><label htmlFor={`${id}-replacement`} className="block text-sm">{t("新凭据")}</label><Input ref={password} id={`${id}-replacement`} type="password" autoComplete="new-password" autoCapitalize="off" spellCheck={false} disabled={disabled} onChange={(event) => { replacement.current = event.target.value; setHasReplacement(event.target.value !== "") }} /></div>}
      {action === "clear" && original?.present && <label className="flex min-h-11 items-start gap-3" htmlFor={`${id}-clear-confirm`}><Checkbox id={`${id}-clear-confirm`} checked={confirmed} disabled={disabled} onCheckedChange={(checked) => setConfirmed(checked === true)} /><span className="text-sm">{t("确认清除已保存凭据；保存后不能在此找回原文。")}</span></label>}
      <p className="text-sm text-muted-foreground">{t("新凭据仅暂存在表单内存。提交成功或失败、语言切换、确认放弃、账号变化及卸载时清除；KEEP 从不恢复或回显原值。")}</p>
    </fieldset>
    <p className="text-sm text-muted-foreground">{t("缺少相应凭据时仍可保存为待补凭据草稿。凭据完整也不表示已验证连通性、可应用或已应用。")}</p>
    <Button type="submit" disabled={disabled} className="max-w-full whitespace-normal">{pending || submitting ? t("正在保存配置资产…") : t("仅保存配置资产")}</Button>
    <Dialog open={clearCatalog} onOpenChange={setClearCatalog}><DialogContent><DialogTitle>{t("移除资产目录声明？")}</DialogTitle><DialogDescription>{t("确认后目录 override 将变为 absent，仅在保存后更改此服务端资产；不是删除本机 sidecar 或模型目录。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button type="button" variant="outline" autoFocus onClick={() => setClearCatalog(false)}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={() => { update("catalog", undefined); rowIds.current = []; setClearCatalog(false) }}>{t("确认移除目录声明")}</Button></div></DialogContent></Dialog>
  </form>
}
