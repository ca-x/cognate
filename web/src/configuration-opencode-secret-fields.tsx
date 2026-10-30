import { useCallback, useId, useLayoutEffect, useRef, useState } from "react"
import { Code, ConnectError } from "@connectrpc/connect"
import { useTranslation } from "react-i18next"
import { t } from "./i18n"
import { ChoiceField, FieldError } from "./configuration-opencode-common-fields"
import { Input } from "./components/ui/input"
import { Button } from "./components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import type { SecretChanges } from "./configuration-form"
import type { OpenCodeView } from "./configuration-opencode-state"

type SlotAction = { action: "keep" | "later" | "replace" | "clear"; confirmed: boolean }
export function useOpenCodeSecrets({ original, requiredSlots, locations, language }: { original?: OpenCodeView; requiredSlots: string[]; locations: Record<string, string[]>; language?: string }) {
  const [actions, setActions] = useState<Record<string, SlotAction>>({})
  const [hasReplacement, setHasReplacement] = useState<Record<string, boolean>>({})
  const replacements = useRef(new Map<string, string>())
  const inputs = useRef(new Map<string, HTMLInputElement>())
  const owned = useRef<SecretChanges>([])
  const clearReplacements = useCallback(() => {
    replacements.current.clear()
    for (const input of inputs.current.values()) input.value = ""
    for (const mutation of owned.current) mutation.action = { case: "clear", value: {} }
    owned.current = []
  }, [])
  useLayoutEffect(() => {
    clearReplacements()
    setHasReplacement({})
    setActions((current) => Object.fromEntries(Object.entries(current).map(([slot, action]) => [slot, { action: action.action === "replace" ? original ? "keep" : "later" : action.action, confirmed: false }])))
    return clearReplacements
  }, [language, original, clearReplacements])
  const requiredIdentity = requiredSlots.join(",")
  useLayoutEffect(() => {
    const removed = [...replacements.current.keys()].filter((slot) => !requiredSlots.includes(slot))
    for (const slot of removed) { replacements.current.delete(slot); const input = inputs.current.get(slot); if (input) input.value = "" }
    setHasReplacement((current) => Object.fromEntries(Object.entries(current).filter(([slot]) => requiredSlots.includes(slot))))
    setActions((current) => Object.fromEntries(Object.entries(current).filter(([slot]) => requiredSlots.includes(slot) || original?.slots.some((status) => status.slotId === slot && status.present))))
  }, [requiredIdentity, original])
  function getAction(slot: string): SlotAction { return actions[slot] ?? { action: original ? "keep" : "later", confirmed: false } }
  function erase(slot: string) { replacements.current.delete(slot); const input = inputs.current.get(slot); if (input) input.value = ""; setHasReplacement((current) => ({ ...current, [slot]: false })) }
  function choose(slot: string, action: SlotAction["action"]) { erase(slot); setActions((current) => ({ ...current, [slot]: { action, confirmed: false } })) }
  function confirmClears(slots: string[]) {
    slots.forEach(erase)
    setActions((current) => ({ ...current, ...Object.fromEntries(slots.map((slot) => [slot, { action: "clear" as const, confirmed: true }])) }))
  }
  function buildMutations(): SecretChanges {
    const changes: SecretChanges = []
    const relevant = [...new Set([...requiredSlots, ...(original?.slots.filter((slot) => slot.present).map((slot) => slot.slotId) ?? [])])].sort()
    let total = 0
    for (const slot of relevant) {
      const action = getAction(slot)
      const referenced = requiredSlots.includes(slot)
      const previouslyPresent = original?.slots.find((item) => item.slotId === slot)?.present
      if (!referenced && previouslyPresent && !(action.action === "clear" && action.confirmed)) throw new ConnectError("", Code.InvalidArgument)
      if (action.action === "clear") {
        if (!action.confirmed) throw new ConnectError("", Code.InvalidArgument)
        changes.push({ slotId: slot, action: { case: "clear", value: {} } })
      } else if (action.action === "replace") {
        const replacement = replacements.current.get(slot) ?? ""
        const length = new TextEncoder().encode(replacement).length
        total += length
        if (!referenced || length < 1 || length > 8192 || /[\0\r\n\uD800-\uDFFF]/u.test(replacement)) throw new ConnectError("", Code.InvalidArgument)
        changes.push({ slotId: slot, action: { case: "replace", value: replacement } })
      }
    }
    if (total > 65536 || changes.length > 64) throw new ConnectError("", Code.InvalidArgument)
    owned.current = changes
    return changes
  }
  return { getAction, choose, confirmClears, buildMutations, clearReplacements, original, requiredSlots, locations,
    dirty: Object.values(hasReplacement).some(Boolean) || Object.values(actions).some((action) => action.action === "replace" || action.action === "clear"),
    hasReplacement,
    inputRef: (slot: string, input: HTMLInputElement | null) => { if (input) inputs.current.set(slot, input); else inputs.current.delete(slot) },
    inputChanged: (slot: string, value: string) => { replacements.current.set(slot, value); setHasReplacement((current) => ({ ...current, [slot]: value.length > 0 })) },
  }
}
export type OpenCodeSecretController = ReturnType<typeof useOpenCodeSecrets>
export function OpenCodeSecretFields({ controller, disabled }: { controller: OpenCodeSecretController; disabled: boolean }) {
  const { i18n } = useTranslation()
  const id = useId()
  const [clearSlot, setClearSlot] = useState<string>()
  useLayoutEffect(() => { setClearSlot(undefined) }, [i18n.resolvedLanguage])
  const shown = [...new Set([...controller.requiredSlots, ...(controller.original?.slots.filter((slot) => slot.present).map((slot) => slot.slotId) ?? [])])].sort()
  return <section className="min-w-0 space-y-4" data-field-path="secrets"><h3 className="font-semibold">{t("引用凭据槽")}</h3><p className="text-sm text-muted-foreground">{t("只显示引用和存在状态；不会读取已保存秘密。新值仅用于本次显式保存。")}</p><p className="text-sm text-muted-foreground">{t("KEEP 保留当前值；REPLACE 仅替换本次输入；CLEAR 须确认；later 表示稍后补充。")}</p>{shown.length === 0 && <p className="text-sm">{t("当前定义没有凭据引用。")}</p>}{shown.map((slot, index) => {
    const present = controller.original?.slots.find((item) => item.slotId === slot)?.present ?? false
    const action = controller.getAction(slot)
    const referenced = controller.requiredSlots.includes(slot)
    return <fieldset key={slot} className="min-w-0 space-y-3 rounded-md border border-border p-3"><legend className="break-all px-1 font-mono text-sm">{slot}</legend><p className="text-sm font-medium">{t("引用位置")}</p><ul className="space-y-1 font-mono text-xs">{(controller.locations[slot] ?? []).map((path) => <li key={path} className="break-all">{path}</li>)}</ul><p className="text-sm">{t(present ? "已保存值（不回显）" : "尚无保存值")}</p>{!referenced && present && <p role="alert" className="text-sm">{t("已移除最后引用，保存前必须确认同一次 CAS 的 CLEAR。")}</p>}<ChoiceField path={`secrets.${slot}`} label={t("凭据操作 {{slot}}", { slot })} disabled={disabled} value={action.action} choices={referenced ? [controller.original ? "keep" : "later", "replace", "clear"] : ["keep", "clear"]} onChange={(next) => { if (next === "clear") setClearSlot(slot); else if (next === "replace" || next === "keep" || next === "later") controller.choose(slot, next) }} />{action.action === "replace" && <div className="space-y-2"><label htmlFor={`${id}-${index}`}>{t("新秘密 {{slot}}", { slot })}</label><Input id={`${id}-${index}`} type="password" autoComplete="off" spellCheck={false} disabled={disabled} ref={(input) => controller.inputRef(slot, input)} onChange={(event) => controller.inputChanged(slot, event.target.value)} /></div>}{action.action === "clear" && <p className="text-sm">{t(action.confirmed ? "已确认本次 CLEAR，尚未写入服务端。" : "CLEAR 需要重新确认。")}{!action.confirmed && <Button type="button" variant="outline" disabled={disabled} onClick={() => setClearSlot(slot)}>{t("确认 CLEAR")}</Button>}</p>}</fieldset>
  })}<FieldError path="secrets" /><Dialog open={clearSlot !== undefined} onOpenChange={(opened) => { if (!opened) setClearSlot(undefined) }}><DialogContent><DialogTitle>{t("确认清除引用槽？")}</DialogTitle><DialogDescription>{t("只确认下一次保存中的 CLEAR；取消不改变草稿，不影响其它共享引用或客户端文件。")}</DialogDescription><p className="mt-3 break-all font-mono text-sm">{clearSlot}</p><div className="mt-5 flex flex-wrap justify-end gap-3"><Button type="button" variant="outline" autoFocus onClick={() => setClearSlot(undefined)}>{t("继续编辑")}</Button><Button type="button" disabled={disabled} onClick={() => { if (clearSlot) controller.confirmClears([clearSlot]); setClearSlot(undefined) }}>{t("确认 CLEAR")}</Button></div></DialogContent></Dialog></section>
}
