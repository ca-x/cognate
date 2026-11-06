import "./server-backup-settings-i18n"
import { clone } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useId, useRef, useState, type FormEvent, type RefObject } from "react"
import { useTranslation } from "react-i18next"
import { CheckCircle2, CircleDot, LoaderCircle, Settings2 } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { BackupSettingsSchema, type BackupSettings } from "./gen/skillhub/management/v1/backup_pb"
import { backupIntervalSeconds, backupSettingsChanged } from "./server-backup-settings-state"

type Client = ReturnType<typeof useApi>["backupManagement"]

function acceptedSettings(value?: BackupSettings) {
  if (!value || value.revision <= 0n || value.automaticIntervalSeconds < 0n || value.automaticIntervalSeconds > 86400n || value.automaticIntervalSeconds > 0n && value.automaticIntervalSeconds < 60n) {
    throw new ConnectError("Invalid backup settings response", Code.DataLoss)
  }
  return value
}

function SettingsForm({ initial, client, applied, reloadTrigger, onEditing, onBeforeSave, onSaved, onReload }: { initial: BackupSettings; client: Client; applied: boolean; reloadTrigger: RefObject<HTMLButtonElement | null>; onEditing: () => void; onBeforeSave: () => Promise<void>; onSaved: (settings: BackupSettings) => void; onReload: () => Promise<BackupSettings> }) {
  const { t } = useTranslation("backupSettings")
  const id = useId()
  const [draft, setDraft] = useState(() => clone(BackupSettingsSchema, initial))
  const [credentialsAction, setCredentialsAction] = useState(initial.credentialsConfigured ? "keep" : "replace")
  const [interval, setInterval] = useState(String(initial.automaticIntervalSeconds || 3600n))
  const [uncertain, setUncertain] = useState(false)
  const [hasCredentialInput, setHasCredentialInput] = useState(false)
  const [confirmReload, setConfirmReload] = useState(false)
  const [reloading, setReloading] = useState(false)
  const keepEditing = useRef<HTMLButtonElement>(null)
  const status = useRef<HTMLParagraphElement>(null)
  useEffect(() => { if (applied) status.current?.focus({ preventScroll: true }) }, [applied])
  const access = useRef<HTMLInputElement>(null)
  const secret = useRef<HTMLInputElement>(null)
  const token = useRef<HTMLInputElement>(null)
  const clearCredentials = () => { for (const input of [access.current, secret.current, token.current]) if (input) input.value = ""; setHasCredentialInput(false) }
  const update = <Field extends keyof BackupSettings>(field: Field, value: BackupSettings[Field]) => {
    onEditing()
    setDraft((current) => ({ ...current, [field]: value }))
  }
  const save = useMutation({
    retry: false,
    onMutate: onBeforeSave,
    mutationFn: async (request: { settings: BackupSettings; credentialsAction: string; accessKeyId: string; secretAccessKey: string; sessionToken: string }) => {
      try {
        const settings = acceptedSettings((await client.updateBackupSettings({ expectedRevision: initial.revision, ...request })).settings)
        if (settings.revision <= initial.revision) throw new ConnectError("Backup settings revision did not advance", Code.DataLoss)
        return settings
      } finally {
        request.accessKeyId = ""
        request.secretAccessKey = ""
        request.sessionToken = ""
      }
    },
    onSuccess: (settings) => onSaved(settings),
    onError: (error) => {
      if (![Code.InvalidArgument, Code.FailedPrecondition, Code.ResourceExhausted, Code.Aborted, Code.PermissionDenied, Code.Unauthenticated].includes(ConnectError.from(error).code)) setUncertain(true)
    },
  })
  const busy = save.isPending || reloading
  const dirty = backupSettingsChanged(initial, draft, interval, credentialsAction, hasCredentialInput)
  const code = save.isError ? ConnectError.from(save.error).code : undefined
  async function reload() {
    clearCredentials()
    setReloading(true)
    try {
      const settings = await onReload()
      setDraft(clone(BackupSettingsSchema, settings))
      setInterval(String(settings.automaticIntervalSeconds || 3600n))
      setCredentialsAction(settings.credentialsConfigured ? "keep" : "replace")
      setUncertain(false)
      save.reset()
      onEditing()
    } catch { setUncertain(true) } finally { setReloading(false); setConfirmReload(false) }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || uncertain || !dirty) return
    const seconds = draft.automaticIntervalSeconds > 0n ? backupIntervalSeconds(interval) : 0n
    if (seconds === undefined) return
    const request = {
      settings: clone(BackupSettingsSchema, draft), credentialsAction,
      accessKeyId: credentialsAction === "replace" ? access.current?.value ?? "" : "",
      secretAccessKey: credentialsAction === "replace" ? secret.current?.value ?? "" : "",
      sessionToken: credentialsAction === "replace" ? token.current?.value ?? "" : "",
    }
    request.settings.automaticIntervalSeconds = seconds
    clearCredentials()
    onEditing()
    save.mutate(request)
  }
  const textFields = ["endpoint", "region", "bucket", "prefix", "recoveryKeyId", "recoveryKeyFile"] as const
  return <><form onSubmit={submit} onInvalidCapture={(event) => {
    if (!(event.target instanceof HTMLElement)) return
    for (let details = event.target.closest("details"); details; details = details.parentElement?.closest("details") ?? null) details.open = true
  }} className="mt-5 space-y-5" aria-busy={busy} data-backup-settings-dirty={dirty} data-backup-settings-revision={String(initial.revision)}>
    <fieldset disabled={busy || uncertain} className="min-w-0 space-y-5">
      <label className="backup-acknowledgement" htmlFor={`${id}-enabled`}><Checkbox id={`${id}-enabled`} checked={draft.enabled} onCheckedChange={(checked) => update("enabled", checked === true)} /><span><span className="block font-medium">{t("enable")}</span><span className="mt-1 block text-sm text-muted-foreground">{t("enableHelp")}</span></span></label>
      <div className="rounded-lg border border-border p-4">
        <label className="backup-acknowledgement" htmlFor={`${id}-automatic`}><Checkbox id={`${id}-automatic`} checked={draft.automaticIntervalSeconds > 0n} onCheckedChange={(checked) => update("automaticIntervalSeconds", checked === true ? backupIntervalSeconds(interval) ?? 3600n : 0n)} /><span>{t("automatic")}</span></label>
        {draft.automaticIntervalSeconds > 0n && <div className="mt-3 space-y-2"><label htmlFor={`${id}-interval`} className="text-sm">{t("interval")}</label><Input id={`${id}-interval`} type="number" min={60} max={86400} step={1} required value={interval} onChange={(event) => { onEditing(); setInterval(event.target.value) }} /><p className="backup-help">{t("intervalHelp")}</p></div>}
      </div>
      <details open={!initial.credentialsConfigured} className="rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium">{t("destination")}</summary><div className="mt-4 space-y-5">
      <div className="grid gap-4 sm:grid-cols-2">
        {textFields.map((field) => <div key={field} className={field === "recoveryKeyFile" || field === "endpoint" ? "space-y-2 sm:col-span-2" : "space-y-2"}><label className="block text-sm font-medium" htmlFor={`${id}-${field}`}>{t(field)}</label><Input id={`${id}-${field}`} required={draft.enabled && field !== "prefix"} type={field === "endpoint" ? "url" : "text"} maxLength={field === "recoveryKeyFile" ? 4096 : 1024} autoComplete="off" spellCheck={false} value={draft[field]} placeholder={field === "endpoint" ? "https://s3.example.com" : undefined} onChange={(event) => update(field, event.target.value)} />{field === "recoveryKeyFile" && <p className="backup-help">{t("keyHelp")}</p>}</div>)}
      </div>
      <div className="space-y-3 rounded-lg border border-border p-4">
        <label className="block text-sm font-medium" htmlFor={`${id}-credentials`}>{t("credentials")}</label>
        <Select value={credentialsAction} onValueChange={(value) => { clearCredentials(); onEditing(); setCredentialsAction(value) }}><SelectTrigger id={`${id}-credentials`} className="w-full"><SelectValue /></SelectTrigger><SelectContent>{initial.credentialsConfigured && <SelectItem value="keep">{t("keep")}</SelectItem>}<SelectItem value="replace">{t(initial.credentialsConfigured ? "replace" : "enter")}</SelectItem></SelectContent></Select>
        <p className="backup-help">{t("credentialsHelp")}</p>
        {credentialsAction === "replace" && <div className="grid gap-4 sm:grid-cols-2" onInput={() => { setHasCredentialInput([access.current, secret.current, token.current].some((input) => Boolean(input?.value))); onEditing() }}><div className="space-y-2"><label htmlFor={`${id}-access`} className="block text-sm">Access key ID</label><Input ref={access} id={`${id}-access`} type="password" autoComplete="new-password" maxLength={4096} required={draft.enabled} /></div><div className="space-y-2"><label htmlFor={`${id}-secret`} className="block text-sm">Secret access key</label><Input ref={secret} id={`${id}-secret`} type="password" autoComplete="new-password" maxLength={4096} required={draft.enabled} /></div><div className="space-y-2 sm:col-span-2"><label htmlFor={`${id}-token`} className="block text-sm">{t("sessionToken")}</label><Input ref={token} id={`${id}-token`} type="password" autoComplete="new-password" maxLength={8192} /></div></div>}
      </div>
      </div></details>
    </fieldset>
    <p className="backup-help">{t("applyHelp")}</p>
    {save.isError && <p role="alert" className="error-message">{t(uncertain ? "unknown" : code === Code.Aborted ? "conflict" : code === Code.FailedPrecondition || code === Code.ResourceExhausted ? "busy" : code === Code.PermissionDenied || code === Code.Unauthenticated ? "denied" : "invalid")}</p>}
    <div className="backup-settings-footer" data-dirty={dirty}>
      <p ref={status} role="status" tabIndex={-1} className="backup-settings-status">{busy ? <LoaderCircle size={18} className="motion-safe:animate-spin" aria-hidden="true" /> : dirty || uncertain ? <CircleDot size={18} aria-hidden="true" /> : <CheckCircle2 size={18} aria-hidden="true" />}<span>{t(busy ? reloading ? "loading" : "saving" : uncertain ? "unconfirmed" : dirty ? "unsaved" : applied ? "saved" : "unchanged")}</span></p>
      <div className="backup-actions"><Button type="submit" disabled={busy || uncertain || !dirty}>{save.isPending && <LoaderCircle size={16} className="motion-safe:animate-spin" aria-hidden="true" />}{t(save.isPending ? "saving" : "save")}</Button><Button ref={reloadTrigger} variant="outline" disabled={busy} onClick={() => { if (dirty && !uncertain) setConfirmReload(true); else void reload() }}>{t(reloading ? "loading" : "reload")}</Button></div>
    </div>
  </form>
    <Dialog open={confirmReload} onOpenChange={(open) => { if (!busy) setConfirmReload(open) }}><DialogContent className="backup-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); keepEditing.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); reloadTrigger.current?.focus() }}>
      <DialogTitle>{t("discardTitle")}</DialogTitle><DialogDescription>{t("discardHelp")}</DialogDescription>
      <div className="backup-actions mt-5"><Button ref={keepEditing} variant="outline" disabled={busy} onClick={() => setConfirmReload(false)}>{t("keepEditing")}</Button><Button disabled={busy} onClick={() => { void reload() }}>{t(reloading ? "loading" : "discardReload")}</Button></div>
    </DialogContent></Dialog>
  </>
}

export function BackupSettingsPanel({ owner, client, onSaved }: { owner: string; client: Client; onSaved: () => void }) {
  const { t } = useTranslation("backupSettings")
  const [applied, setApplied] = useState(false)
  const cache = useQueryClient()
  const reloadTrigger = useRef<HTMLButtonElement>(null)
  const queryKey = ["server-backup-settings", owner]
  const settings = useQuery({ queryKey, queryFn: async ({ signal }) => acceptedSettings((await client.getBackupSettings({}, { signal })).settings), retry: false, refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false })
  return <section className="backup-panel mb-6" aria-labelledby="backup-settings-heading">
    <div className="flex items-center gap-2"><Settings2 size={20} aria-hidden="true" /><h2 id="backup-settings-heading">{t("title")}</h2></div><p className="backup-help">{t("intro")}</p>
    {settings.isPending ? <p role="status" className="backup-help">{t("loading")}</p> : !settings.data ? <div><p role="alert" className="error-message">{t(ConnectError.from(settings.error).code === Code.Unimplemented ? "unsupported" : "loadError")}</p><Button variant="outline" disabled={settings.isFetching} onClick={() => { void settings.refetch() }}>{t("reload")}</Button></div> : <SettingsForm key={String(settings.data.revision)} initial={settings.data} client={client} applied={applied} reloadTrigger={reloadTrigger} onEditing={() => setApplied(false)} onBeforeSave={async () => { await cache.cancelQueries({ queryKey, exact: true }) }} onSaved={(value) => { cache.setQueryData(queryKey, value); setApplied(true); onSaved() }} onReload={async () => acceptedSettings((await settings.refetch({ throwOnError: true })).data)} />}
  </section>
}
