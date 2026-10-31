import "./server-logging-i18n"
import "./management-ui.css"
import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent } from "react"
import { Code, ConnectError } from "@connectrpc/connect"
import { create } from "@bufbuild/protobuf"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { FileText, RefreshCw, ShieldCheck } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { GetLogSettingsResponseSchema, type GetLogSettingsResponse, type LogSettings } from "./gen/skillhub/management/v1/logging_pb"

type Draft = { level: string; maxFileMib: string; maxBackups: string; retentionDays: string }
type Notice = "saved" | "reloaded" | "conflict" | "uncertain" | "invalid" | "unavailable" | ""
const levels = ["debug", "info", "warn", "error"] as const
const limits = { maxFileMib: 100, maxBackups: 100, retentionDays: 365 } as const

function toDraft(settings: LogSettings): Draft {
  return { level: settings.level, maxFileMib: String(settings.maxFileMib), maxBackups: String(settings.maxBackups), retentionDays: String(settings.retentionDays) }
}

function validInteger(value: string, maximum: number) {
  return /^\d{1,3}$/.test(value) && Number(value) >= 1 && Number(value) <= maximum
}

function LoggingEditor({ initial, owner, unverified }: { initial: GetLogSettingsResponse & { settings: LogSettings }; owner: string; unverified: boolean }) {
  const { t, i18n } = useTranslation("serverLogging")
  const { logManagement } = useApi()
  const queryClient = useQueryClient()
  const [current, setCurrent] = useState(initial)
  const [draft, setDraft] = useState(() => toDraft(initial.settings))
  const [notice, setNotice] = useState<Notice>("")
  const [needsReload, setNeedsReload] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [confirmation, setConfirmation] = useState<"reload" | "save" | null>(null)
  const reloadTrigger = useRef<HTMLButtonElement>(null)
  const policyHeading = useRef<HTMLHeadingElement>(null)
  const cancelConfirmation = useRef<HTMLButtonElement>(null)
  const feedback = useRef<HTMLParagraphElement>(null)
  const controller = useRef(new AbortController())
  const id = useId()
  useEffect(() => {
    const active = new AbortController()
    controller.current = active
    return () => active.abort()
  }, [logManagement])
  const changed = JSON.stringify(draft) !== JSON.stringify(toDraft(current.settings))
  const reducesRetention = Object.keys(limits).some((field) => Number(draft[field as keyof typeof limits]) < current.settings[field as keyof typeof limits])
  const valid = levels.some((level) => level === draft.level) && Object.entries(limits).every(([field, maximum]) => validInteger(draft[field as keyof typeof limits], maximum))
  const mutation = useMutation({
    retry: false,
    mutationFn: async ({ signal, settings }: { signal: AbortSignal; settings: LogSettings }) => logManagement.updateLogSettings({ expectedRevision: settings.revision, settings }, { signal }),
    onSuccess: (result, variables) => {
      if (variables.signal.aborted) return
      if (!result.settings) { setNeedsReload(true); setNotice("uncertain"); return }
      const next = { ...create(GetLogSettingsResponseSchema, { settings: result.settings, health: result.health }), settings: result.settings }
      setCurrent(next)
      setDraft(toDraft(result.settings))
      setNeedsReload(false)
      setNotice("saved")
      queryClient.setQueryData(["server-log-settings", owner], next)
    },
    onError: (failure, variables) => {
      if (variables.signal.aborted) return
      const code = ConnectError.from(failure).code
      setNeedsReload(code !== Code.InvalidArgument)
      setNotice(code === Code.Aborted ? "conflict" : code === Code.InvalidArgument ? "invalid" : "uncertain")
    },
  })
  async function reload() {
    const signal = controller.current.signal
    setReloading(true)
    try {
      const result = await logManagement.getLogSettings({}, { signal })
      if (!result.settings || signal.aborted) { if (!signal.aborted) setNotice("unavailable"); return }
      setCurrent({ ...result, settings: result.settings })
      setDraft(toDraft(result.settings))
      setNeedsReload(false)
      setNotice("reloaded")
      mutation.reset()
      queryClient.setQueryData(["server-log-settings", owner], result)
    } catch {
      if (!signal.aborted) { setNeedsReload(true); setNotice("unavailable") }
    } finally {
      if (!signal.aborted) setReloading(false)
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!valid) { event.currentTarget.querySelector<HTMLElement>("[aria-invalid=true]")?.focus(); return }
    if (!changed || needsReload || unverified || reloading || mutation.isPending) return
    if (reducesRetention) setConfirmation("save")
    else save()
  }
  function save() {
    if (valid && changed && !needsReload && !unverified && !reloading && !mutation.isPending) mutation.mutate({ signal: controller.current.signal, settings: { ...current.settings, level: draft.level, maxFileMib: Number(draft.maxFileMib), maxBackups: Number(draft.maxBackups), retentionDays: Number(draft.retentionDays) } })
  }
  const busy = mutation.isPending || reloading
  useLayoutEffect(() => {
    if (!busy && notice && (document.activeElement === document.body || (document.activeElement instanceof HTMLButtonElement && document.activeElement.disabled))) feedback.current?.focus()
  }, [busy, notice])
  const number = (value: bigint | number) => new Intl.NumberFormat(i18n.resolvedLanguage).format(value)
  const health = current.health
  const healthUnverified = unverified || needsReload || reloading
  return <div className="management-log-grid">
    <section className="management-card min-w-0" aria-labelledby={`${id}-policy`}>
      <div className="mb-6 space-y-2"><div className="flex flex-wrap items-start justify-between gap-2"><h2 ref={policyHeading} tabIndex={-1} id={`${id}-policy`} className="text-lg font-semibold">{t("policy")}</h2><span className="management-status">{t("revision", { revision: number(current.settings.revision) })}</span></div><p className="text-sm leading-6 text-muted-foreground">{t("policyHelp")}</p></div>
      <form onSubmit={submit} className="space-y-6" aria-busy={busy} noValidate>
        <fieldset disabled={busy} className="min-w-0 space-y-5">
          <div className="space-y-2"><label htmlFor={`${id}-level`} className="text-sm font-medium">{t("level")}</label><Select value={draft.level} onValueChange={(level) => setDraft((value) => ({ ...value, level }))}><SelectTrigger id={`${id}-level`} aria-describedby={`${id}-level-help`}><SelectValue /></SelectTrigger><SelectContent>{levels.map((level) => <SelectItem key={level} value={level}>{t(level)}</SelectItem>)}</SelectContent></Select><p id={`${id}-level-help`} className="text-sm leading-6 text-muted-foreground">{t("levelHelp")}</p></div>
          {([{ field: "maxFileMib", label: "maxFile", help: "maxFileHelp" }, { field: "maxBackups", label: "backups", help: "backupsHelp" }, { field: "retentionDays", label: "days", help: "daysHelp" }] as const).map(({ field, label, help }) => {
            const invalid = !validInteger(draft[field], limits[field])
            return <div key={field} className="space-y-2"><label htmlFor={`${id}-${field}`} className="text-sm font-medium">{t(label)}</label><Input id={`${id}-${field}`} type="number" inputMode="numeric" min={1} max={limits[field]} step={1} required value={draft[field]} aria-invalid={invalid} aria-describedby={`${id}-${field}-help${invalid ? ` ${id}-${field}-error` : ""}`} onChange={(event) => setDraft((value) => ({ ...value, [field]: event.target.value }))} /><p id={`${id}-${field}-help`} className="text-sm leading-6 text-muted-foreground">{t(help)}</p>{invalid && <p id={`${id}-${field}-error`} className="error-message">{t("range", { min: 1, max: limits[field] })}</p>}</div>
          })}
        </fieldset>
        <div className="management-feedback space-y-2 text-sm leading-6"><p>{valid && t("budget", { mib: number((Number(draft.maxBackups) + 1) * Number(draft.maxFileMib)) })}</p><p className="text-muted-foreground">{t("pruning")}</p></div>
        <p className="field-help" role="status">{t(changed ? "unsaved" : "currentPolicy")}</p>
        {notice && <p ref={feedback} tabIndex={-1} role={notice === "saved" || notice === "reloaded" ? "status" : "alert"} className={notice === "saved" || notice === "reloaded" ? "management-feedback text-sm" : "management-feedback error-message"}>{t(notice)}</p>}
        <div className="management-actions"><Button type="submit" disabled={busy || !valid || !changed || needsReload || unverified}>{mutation.isPending ? t("saving") : t("save")}</Button><Button ref={reloadTrigger} variant="outline" disabled={busy} onClick={() => { if (changed) setConfirmation("reload"); else void reload() }}><RefreshCw size={16} aria-hidden="true" />{reloading ? t("reloading") : changed ? t("discard") : t("reload")}</Button></div>
      </form>
    </section>
    <aside className="min-w-0 space-y-6">
      <section className="management-card" aria-labelledby={`${id}-health`}><h2 id={`${id}-health`} className="font-semibold">{t("health")}</h2><p className="management-status mt-3" data-status={healthUnverified || !health ? "unknown" : health.degraded ? "degraded" : "active"}>{t(healthUnverified || !health ? "unverified" : health.degraded ? "degradedLabel" : "healthyLabel")}</p><p className="mt-3 text-sm leading-6">{t(healthUnverified || !health ? "healthUnknown" : health.degraded ? "degraded" : "healthy")}</p><p className="mt-3 field-help">{t("snapshot")}</p><dl className="management-health my-5 space-y-3 text-sm">{([["writeFailures", health?.writeFailures], ["cleanupFailures", health?.cleanupFailures], ["droppedRecords", health?.droppedRecords]] as const).map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{t(label)}</dt><dd className="font-mono tabular-nums">{value === undefined ? "—" : number(value)}</dd></div>)}</dl><p className="text-sm leading-6 text-muted-foreground">{t("healthHelp")}</p></section>
      <section className="management-card"><h2 className="flex items-center gap-2 font-semibold"><ShieldCheck size={18} aria-hidden="true" />{t("privacy")}</h2><p className="mt-3 text-sm leading-6 text-muted-foreground">{t("privacyHelp")}</p></section>
    </aside>
    <Dialog open={confirmation !== null} onOpenChange={(open) => { if (!open) setConfirmation(null) }}>
      {confirmation && <DialogContent className="management-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); cancelConfirmation.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); if (reloadTrigger.current && !reloadTrigger.current.disabled) reloadTrigger.current.focus(); else policyHeading.current?.focus() }}>
        <DialogTitle>{t(confirmation === "reload" ? "discardTitle" : "pruneTitle")}</DialogTitle>
        <DialogDescription>{t(confirmation === "reload" ? "discardHelp" : "pruning")}</DialogDescription>
        <div className="management-actions"><DialogClose asChild><Button ref={cancelConfirmation} variant="outline" data-management-cancel>{t("cancel")}</Button></DialogClose><Button onClick={() => { const action = confirmation; setConfirmation(null); if (action === "reload") void reload(); else save() }}>{t(confirmation === "reload" ? "confirmDiscard" : "confirmPrune")}</Button></div>
      </DialogContent>}
    </Dialog>
  </div>
}

export function ServerLoggingPage() {
  const { t } = useTranslation("serverLogging")
  const { session, logManagement } = useApi()
  const heading = useRef<HTMLHeadingElement>(null)
  const query = useQuery({ queryKey: ["server-log-settings", session?.userId], queryFn: ({ signal }) => logManagement.getLogSettings({}, { signal }), enabled: !!session, retry: false, staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false })
  useLayoutEffect(() => {
    if (document.activeElement === document.body && !query.isPending) heading.current?.focus()
  }, [query.status, query.fetchStatus])
  if (!session) return null
  const code = query.isError ? ConnectError.from(query.error).code : undefined
  return <div className="management-page">
    <div className="page-heading"><div><p className="management-eyebrow">{t("administration")}</p><h1 ref={heading} tabIndex={-1} className="flex flex-wrap items-center gap-3"><FileText size={26} aria-hidden="true" />{t("title")}</h1><p>{t("introduction")}</p></div></div>
    {query.isPending && <p role="status">{t("loading")}</p>}
    {query.isError && <div className="management-feedback space-y-4" role="alert"><h2 className="font-semibold">{t(code === Code.PermissionDenied ? "restricted" : code === Code.Unimplemented ? "unsupported" : "unavailable")}</h2>{code === Code.PermissionDenied ? <><p className="text-sm leading-6 text-muted-foreground">{t("restrictedHelp")}</p><p className="text-sm">{t("identity")}</p><code className="block break-all text-sm">{session.userId}</code></> : <Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch() }}>{query.isFetching ? t("reloading") : t("reload")}</Button>}</div>}
    {query.data?.settings ? <LoggingEditor key={session.userId} initial={{ ...query.data, settings: query.data.settings }} owner={session.userId} unverified={query.isError} /> : !query.isPending && !query.isError && <p role="alert" className="error-message">{t("unavailable")}</p>}
  </div>
}
