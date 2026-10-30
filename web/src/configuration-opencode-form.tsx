import { useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { t } from "./i18n"
import { Button } from "./components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { ChoiceField, TextField, OpenCodeErrors } from "./configuration-opencode-common-fields"
import { OpenCodeV1Fields } from "./configuration-opencode-v1-fields"
import { OpenCodeV2Fields } from "./configuration-opencode-v2-fields"
import { OpenCodeSecretFields, useOpenCodeSecrets } from "./configuration-opencode-secret-fields"
import { openCodeFields, openCodeDraft, openCodeFieldReferences, openCodeReferenceLocations, validateOpenCode, type OpenCodeFields, type OpenCodeView } from "./configuration-opencode-state"
import type { ConfigurationDraft, SecretChanges } from "./configuration-form"

export function ConfigurationOpenCodeForm({ original, pending, blocked, onSubmit, onDirtyChange }: { original?: OpenCodeView; pending: boolean; blocked: boolean; onSubmit: (draft: ConfigurationDraft, secrets: SecretChanges) => Promise<boolean>; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const [initial] = useState(() => openCodeFields(original?.provider, original?.asset.name))
  const [fields, setFields] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [submitting, setSubmitting] = useState(false)
  const [removal, setRemoval] = useState<{ fields: OpenCodeFields; slots: string[] }>()
  const form = useRef<HTMLFormElement>(null)
  const summary = useRef<HTMLDivElement>(null)
  const lock = useRef(false)
  const mounted = useRef(true)
  const readonly = original?.mode === "readonly"
  const disabled = pending || blocked || readonly || submitting
  const references = openCodeFieldReferences(fields)
  const controller = useOpenCodeSecrets({ original, requiredSlots: references, locations: openCodeReferenceLocations(fields), language: i18n.resolvedLanguage })
  useLayoutEffect(() => { onDirtyChange(!readonly && (JSON.stringify(initial) !== JSON.stringify(fields) || controller.dirty || submitting)) }, [fields, initial, readonly, controller.dirty, submitting, onDirtyChange])
  useLayoutEffect(() => { if (Object.keys(errors).length) summary.current?.focus() }, [errors])
  useLayoutEffect(() => { setErrors({}); setRemoval(undefined) }, [i18n.resolvedLanguage])
  useLayoutEffect(() => { mounted.current = true; return () => { mounted.current = false; controller.clearReplacements() } }, [controller.clearReplacements])
  function update(next: OpenCodeFields) {
    if (disabled) return
    const nextReferences = openCodeFieldReferences(next)
    const removed = original?.slots.filter((slot) => slot.present && references.includes(slot.slotId) && !nextReferences.includes(slot.slotId) && !(controller.getAction(slot.slotId).action === "clear" && controller.getAction(slot.slotId).confirmed)).map((slot) => slot.slotId) ?? []
    if (removed.length) { setRemoval({ fields: next, slots: removed }); return }
    setErrors({}); setFields(next)
  }
  function focus(path: string) { const group = form.current?.querySelector<HTMLElement>(`[data-field-path="${CSS.escape(path)}"]`); let parent = group?.closest("details"); while (parent) { parent.open = true; parent = parent.parentElement?.closest("details") ?? null } group?.querySelector<HTMLElement>("input, button, select")?.focus() }
  async function submit() {
    if (disabled || lock.current) return
    const invalid = validateOpenCode(fields)
    if (Object.keys(invalid).length) { setErrors(invalid); return }
    let mutations: SecretChanges
    try { mutations = controller.buildMutations() } catch { setErrors({ secrets: "请检查秘密长度、CLEAR 确认及引用；不会回显输入。" }); return }
    lock.current = true; setSubmitting(true)
    try { await onSubmit(openCodeDraft(fields), mutations) }
    finally { controller.clearReplacements(); lock.current = false; if (mounted.current) setSubmitting(false) }
  }
  return <OpenCodeErrors.Provider value={errors}><form ref={form} className="mt-5 min-w-0 space-y-6 [&_button]:max-w-full [&_button]:whitespace-normal [&_button]:[overflow-wrap:anywhere] [&_button>span]:min-w-0 [&_button>span]:[overflow-wrap:anywhere] [&_label>span]:min-w-0 [&_label>span]:[overflow-wrap:anywhere] [&_legend]:[overflow-wrap:anywhere]" autoComplete="off" noValidate onSubmit={(event) => { event.preventDefault(); void submit() }} aria-busy={pending || submitting}><p className="text-sm leading-6 text-muted-foreground">{t("OpenCode 服务端资产：V1 与 V2 独立格式；保存不会选择 provider、安装 SDK 或应用到设备。")}</p>{readonly && <p role="alert" className="text-sm">{t("此资产保留未知字段，仅可只读查看已知内容。保存和复制均禁用，后端也会拒绝；仍可由原账号明确确认后按版本删除。")}</p>}{Object.keys(errors).length > 0 && <div ref={summary} tabIndex={-1} role="alert" className="space-y-2 rounded-md border border-destructive p-3"><h3 className="font-medium">{t("请修正字段后再保存")}</h3><ul className="space-y-2">{Object.entries(errors).map(([path, message]) => <li key={path}><button type="button" className="text-left text-sm underline" onClick={() => focus(path)}>{path}: {t(message)}</button></li>)}</ul></div>}<fieldset disabled={disabled} className="min-w-0 space-y-5" data-field-path="provider"><legend className="sr-only">OpenCode provider</legend><TextField path="name" label={t("配置名称")} value={fields.name} disabled={disabled} onChange={(name) => update({ ...fields, name })} /><TextField path="providerId" label="OpenCode providerID" value={fields.providerId} disabled={disabled} onChange={(providerId) => update({ ...fields, providerId })} /><ChoiceField path="format" label={t("OpenCode 格式")} value={fields.format ?? ""} disabled={disabled || fields.format !== undefined} choices={["v1", "v2"]} onChange={(format) => { if (format === "v1" || format === "v2") update({ ...fields, format }) }} />{fields.format === "v1" && <OpenCodeV1Fields path="v1" value={fields.v1} disabled={disabled} onChange={(v1) => update({ ...fields, v1 })} />}{fields.format === "v2" && <OpenCodeV2Fields path="v2" value={fields.v2} disabled={disabled} onChange={(v2) => update({ ...fields, v2 })} />}<OpenCodeSecretFields controller={controller} disabled={disabled} /></fieldset><section className="min-w-0 space-y-2 border-t border-border pt-4" aria-label={t("非秘密变更摘要")}><h3 className="font-medium">{t("非秘密变更摘要")}</h3><p className="break-all font-mono text-sm">{fields.format ?? "—"} · {fields.providerId || "—"}</p><p className="text-sm">{t("引用 {{count}} 个资产内凭据槽；不展示任何值。", { count: references.length })}</p><p className="text-sm text-muted-foreground">{t("字段与集合的未设置、显式空值及顺序分别保留。提供方列表持久排序仍待独立后端片。")}</p></section><Button type="submit" disabled={disabled || fields.format === undefined} className="max-w-full whitespace-normal">{t(pending || submitting ? "正在提交配置操作…" : "保存配置资产")}</Button></form><Dialog open={removal !== undefined} onOpenChange={(opened) => { if (!opened) setRemoval(undefined) }}><DialogContent><DialogTitle>{t("确认移除最后引用并 CLEAR？")}</DialogTitle><DialogDescription>{t("这些已保存槽在新草稿中不再被引用；确认会在同一次 CAS 保存中显式 CLEAR。取消保留原草稿。")}</DialogDescription><ul className="my-3 space-y-1 font-mono text-sm">{removal?.slots.map((slot) => <li key={slot}>{slot}</li>)}</ul><div className="mt-5 flex flex-wrap justify-end gap-3"><Button type="button" variant="outline" autoFocus onClick={() => setRemoval(undefined)}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={() => { if (removal) { controller.confirmClears(removal.slots); setFields(removal.fields); setErrors({}) } setRemoval(undefined) }}>{t("确认移除引用并 CLEAR")}</Button></div></DialogContent></Dialog></OpenCodeErrors.Provider>
}
