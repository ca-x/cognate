import "./server-backup-i18n"
import "./server-backup.css"
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { Code, ConnectError } from "@connectrpc/connect"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { Archive, ArrowDownToLine, CheckCircle2, CircleAlert, Clock3, Database, FileSearch, LockKeyhole, RefreshCw, ShieldCheck, Square } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import { acceptBackupJob, acceptBackupPage, acceptBackupStatus, backupMaxPages, backupPageSize, clearBackupIntent, isActiveBackup, readBackupIntent, writeBackupIntent, type BackupJobView } from "./server-backup-state"

type BackupClient = ReturnType<typeof useApi>["backupManagement"]
type RequestScope = { client: BackupClient; controller: AbortController }
type Operation = { kind: "start" | "cancel" | "reconcile"; id: string; scope: RequestScope }
type Notice = "" | "forgotten" | "storageBlocked" | "startUnknown" | "startConfirmed" | "intentNotCleared" | "actionUnknown" | "cancelConfirmed" | "reconciled"

function browserStorage() {
  try { return window.sessionStorage } catch { return undefined }
}

function ConfirmAction({ trigger, title, description, confirm, acknowledgement, onConfirm, onConfirmedClose }: { trigger: ReactNode; title: string; description: string; confirm: string; acknowledgement?: string; onConfirm: () => void; onConfirmedClose?: () => void }) {
  const { t } = useTranslation("serverBackup")
  const [open, setOpen] = useState(false)
  const [checked, setChecked] = useState(false)
  const heading = useRef<HTMLHeadingElement>(null)
  const confirmed = useRef(false)
  const id = useId()
  return <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) confirmed.current = false; if (!next) setChecked(false) }}>
    <DialogTrigger asChild>{trigger}</DialogTrigger>
    <DialogContent className="backup-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); heading.current?.focus() }} onCloseAutoFocus={(event) => { if (confirmed.current && onConfirmedClose) { event.preventDefault(); onConfirmedClose() } }}>
      <DialogTitle ref={heading} tabIndex={-1}>{title}</DialogTitle>
      <DialogDescription>{description}</DialogDescription>
      {acknowledgement && <label className="backup-acknowledgement" htmlFor={id}><Checkbox id={id} checked={checked} onCheckedChange={(value) => setChecked(value === true)} /><span>{acknowledgement}</span></label>}
      <div className="backup-actions mt-6"><DialogClose asChild><Button variant="outline">{t("dismiss")}</Button></DialogClose><Button disabled={!!acknowledgement && !checked} onClick={() => { confirmed.current = true; setOpen(false); setChecked(false); onConfirm() }}>{confirm}</Button></div>
    </DialogContent>
  </Dialog>
}

function TaskPhase({ job }: { job: BackupJobView }) {
  const { t } = useTranslation("serverBackup")
  const Icon = job.phase === "remote_committed" ? CheckCircle2 : isActiveBackup(job) ? Clock3 : CircleAlert
  return <div className="backup-phase" data-phase={job.phase}><Icon size={18} aria-hidden="true" /><span>{t(`phase.${job.phase}`)}</span></div>
}

function TaskMetadata({ job }: { job: BackupJobView }) {
  const { t, i18n } = useTranslation("serverBackup")
  const number = new Intl.NumberFormat(i18n.resolvedLanguage)
  const date = new Intl.DateTimeFormat(i18n.resolvedLanguage, { dateStyle: "medium", timeStyle: "short" })
  return <dl className="backup-metadata">
    <div><dt>{t("created")}</dt><dd><time dateTime={new Date(Number(job.createdUnixMillis)).toISOString()}>{date.format(Number(job.createdUnixMillis))}</time></dd></div>
    <div><dt>{t("updated")}</dt><dd><time dateTime={new Date(Number(job.updatedUnixMillis)).toISOString()}>{date.format(Number(job.updatedUnixMillis))}</time></dd></div>
    <div><dt>{t("objects")}</dt><dd>{number.format(job.objects)}</dd></div>
    <div><dt>{t("ciphertext")}</dt><dd>{number.format(job.ciphertextBytes)}</dd></div>
  </dl>
}

function TaskDetails({ job, owner, client, disabled }: { job: BackupJobView; owner: string; client: BackupClient; disabled: boolean }) {
  const { t } = useTranslation("serverBackup")
  const [open, setOpen] = useState(false)
  const heading = useRef<HTMLHeadingElement>(null)
  const query = useQuery({ queryKey: ["server-backup-job", owner, job.id], queryFn: async ({ signal }) => acceptBackupJob((await client.getBackupJob({ jobId: job.id }, { signal })).job, job.id), enabled: open, retry: false, staleTime: 0, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false, refetchInterval: (query) => open && !query.state.error && query.state.data && isActiveBackup(query.state.data) ? 5_000 : false })
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="outline" disabled={disabled}><FileSearch size={16} aria-hidden="true" />{t("details")}</Button></DialogTrigger>
    <DialogContent className="backup-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); heading.current?.focus() }}>
      <DialogTitle ref={heading} tabIndex={-1}>{t("details")}</DialogTitle><DialogDescription>{t("detailsHelp")}</DialogDescription>
      {query.isPending ? <p className="mt-5" role="status">{t("detailsLoading")}</p> : query.isError || !query.data ? <p className="error-message mt-5" role="alert">{t("detailsError")}</p> : <div className="mt-5 space-y-4"><TaskPhase job={query.data} /><p className="backup-identity"><span>{t("jobIdentity")}</span><code>{query.data.id}</code></p><TaskMetadata job={query.data} /><p className="backup-help">{t("accountingHelp")}</p>{query.data.cancelRequested && <p className="backup-flag">{t("cancelFlag")}</p>}{query.data.errorCode && <p className="backup-help">{t(`code.${query.data.errorCode}`)}</p>}{query.data.phase === "remote_committed" && <p className="backup-help">{t("restoreHelp")}</p>}</div>}
      <div className="backup-actions mt-6"><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t(query.isFetching ? "refreshing" : "refresh")}</Button><DialogClose asChild><Button>{t("dismiss")}</Button></DialogClose></div>
    </DialogContent>
  </Dialog>
}

function BackupWorkspace({ owner, client }: { owner: string; client: BackupClient }) {
  const { t, i18n } = useTranslation("serverBackup")
  const queryClient = useQueryClient()
  const [history, setHistory] = useState<string[]>([""])
  const [intent, setIntent] = useState(() => readBackupIntent(browserStorage(), owner))
  const [notice, setNotice] = useState<Notice>("")
  const historyHeading = useRef<HTMLHeadingElement>(null)
  const focusHistory = () => historyHeading.current?.focus()
  const currentClient = useRef(client)
  currentClient.current = client
  const lifetime = useRef<RequestScope>({ client, controller: new AbortController() })
  const active = useRef<Operation | null>(null)
  const cursor = history.at(-1) ?? ""
  const status = useQuery({ queryKey: ["server-backup-status", owner], queryFn: async ({ signal }) => acceptBackupStatus(await client.getBackupStatus({}, { signal })), retry: false, staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false })
  const tasks = useQuery({ queryKey: ["server-backup-jobs", owner, cursor], queryFn: async ({ signal }) => acceptBackupPage(await client.listBackupJobs({ limit: backupPageSize, cursor }, { signal }), cursor, history), enabled: status.data?.enabled === true && !status.isError, retry: false, staleTime: 0, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false, refetchInterval: (query) => !query.state.error && query.state.data?.jobs.some(isActiveBackup) ? 5_000 : false })
  const live = (scope: RequestScope) => currentClient.current === scope.client && lifetime.current === scope && !scope.controller.signal.aborted
  const mutation = useMutation({
    retry: false,
    mutationFn: async (operation: Operation) => {
      const { client, controller } = operation.scope
      const options = { signal: controller.signal }
      if (operation.kind === "start") return acceptBackupJob((await client.startBackup({ requestId: operation.id }, options)).job)
      if (operation.kind === "cancel") return acceptBackupJob((await client.cancelBackupJob({ jobId: operation.id }, options)).job, operation.id)
      return acceptBackupJob((await client.reconcileBackupJob({ jobId: operation.id }, options)).job, operation.id)
    },
    onSuccess: (job, operation) => {
      if (!live(operation.scope)) return
      if (operation.kind === "start") {
        const cleared = clearBackupIntent(browserStorage(), owner, operation.id)
        setIntent(readBackupIntent(browserStorage(), owner))
        setNotice(cleared ? "startConfirmed" : "intentNotCleared")
        setHistory([""])
      } else setNotice(operation.kind === "cancel" ? "cancelConfirmed" : "reconciled")
      queryClient.setQueryData(["server-backup-job", owner, job.id], job)
      void queryClient.invalidateQueries({ queryKey: ["server-backup-jobs", owner] })
    },
    onError: (_, operation) => {
      if (!live(operation.scope)) return
      setIntent(readBackupIntent(browserStorage(), owner))
      setNotice(operation.kind === "start" ? "startUnknown" : "actionUnknown")
      void queryClient.invalidateQueries({ queryKey: ["server-backup-jobs", owner] })
    },
    onSettled: (_, __, operation) => { if (active.current === operation) active.current = null },
  })
  const resetMutation = mutation.reset
  useLayoutEffect(() => {
    const scope = { client, controller: new AbortController() }
    lifetime.current = scope
    active.current = null
    setIntent(readBackupIntent(browserStorage(), owner))
    setNotice("")
    resetMutation()
    return () => scope.controller.abort()
  }, [client, owner, resetMutation])
  const busy = mutation.isPending
  const readable = status.data?.enabled && !status.isError && tasks.isSuccess && !tasks.isError
  const hasActive = tasks.data?.jobs.some(isActiveBackup) ?? false
  const ready = readable && !tasks.isFetching && !busy
  function operate(kind: Operation["kind"], id: string) {
    if (active.current || !readable || !live(lifetime.current)) return
    const operation = { kind, id, scope: lifetime.current }
    active.current = operation
    setNotice("")
    mutation.mutate(operation)
  }
  function start(requestId?: string) {
    if (!ready || active.current) return
    const stored = readBackupIntent(browserStorage(), owner)
    if (stored.kind === "blocked" || (requestId !== undefined ? stored.kind !== "pending" || stored.requestId !== requestId : stored.kind !== "empty" || hasActive)) {
      setIntent(stored); setNotice("storageBlocked"); return
    }
    let identifier: string
    try { identifier = requestId ?? crypto.randomUUID() } catch { setNotice("storageBlocked"); return }
    if (!writeBackupIntent(browserStorage(), owner, identifier)) { setIntent(readBackupIntent(browserStorage(), owner)); setNotice("storageBlocked"); return }
    setIntent(readBackupIntent(browserStorage(), owner))
    operate("start", identifier)
  }
  function forget(requestId: string) {
    if (active.current) return
    const cleared = clearBackupIntent(browserStorage(), owner, requestId)
    setIntent(readBackupIntent(browserStorage(), owner))
    setNotice(cleared ? "forgotten" : "storageBlocked")
  }
  async function refresh() {
    const scope = lifetime.current
    const response = await status.refetch()
    if (live(scope) && !response.isError && response.data?.enabled) await queryClient.invalidateQueries({ queryKey: ["server-backup-jobs", owner] })
  }
  const statusCode = status.isError ? ConnectError.from(status.error).code : undefined
  const errorNotice = ["storageBlocked", "startUnknown", "intentNotCleared", "actionUnknown"].includes(notice)
  const confirmStart = (continuing = false) => <ConfirmAction trigger={<Button disabled={!ready || (!continuing && (hasActive || intent.kind !== "empty"))}><ArrowDownToLine size={16} aria-hidden="true" />{t(continuing ? "continue" : "start")}</Button>} title={t(continuing ? "continueTitle" : "startTitle")} description={t(continuing ? "pendingHelp" : "startHelp")} acknowledgement={t("scopeAcknowledgement")} confirm={t(continuing ? "continueConfirm" : "confirmStart")} onConfirmedClose={focusHistory} onConfirm={() => {
    if (!continuing) start()
    else if (intent.kind === "pending") start(intent.requestId)
    else setNotice("storageBlocked")
  }} />
  return <div className="backup-page">
    <div className="page-heading"><div><h1>{t("title")}</h1><p>{t("introduction")}</p></div><Button variant="outline" disabled={status.isFetching || busy} onClick={() => { void refresh() }}><RefreshCw size={16} aria-hidden="true" />{t(status.isFetching ? "refreshing" : "refresh")}</Button></div>
    {status.isPending ? <p role="status">{t("loading")}</p> : statusCode === Code.PermissionDenied ? <section className="backup-panel"><ShieldCheck className="backup-emblem" size={24} aria-hidden="true" /><h2>{t("restricted")}</h2><p className="backup-help">{t("restrictedHelp")}</p><p className="backup-identity"><span>{t("identity")}</span><code>{owner}</code></p></section> : status.isError || !status.data ? <p className="error-message" role="alert">{t(statusCode === Code.Unimplemented ? "unsupported" : statusCode === Code.DataLoss ? "invalidResponse" : "unavailable")}</p> : <>
      {!status.data.enabled ? <section className="backup-panel"><LockKeyhole className="backup-emblem" size={24} aria-hidden="true" /><h2>{t("disabled")}</h2><p className="backup-help">{t("disabledHelp")}</p></section> : <>
        <section className="backup-panel" aria-labelledby="backup-tasks-heading">
          <div className="backup-section-heading"><div><span className="backup-mode"><Database size={14} aria-hidden="true" />{t("manualMode")}</span><h2 id="backup-tasks-heading" ref={historyHeading} tabIndex={-1}>{t("history")}</h2><p className="backup-help">{t("historyHelp")}</p></div>{confirmStart()}</div>
          {intent.kind === "blocked" && <p className="error-message" role="alert">{t("storageBlocked")}</p>}
          {intent.kind === "pending" && <div className="backup-pending"><h3>{t("pendingTitle")}</h3><p className="backup-help">{t("pendingHelp")}</p><p className="backup-identity"><span>{t("requestIdentity")}</span><code>{intent.requestId}</code></p><div className="backup-actions">{confirmStart(true)}<ConfirmAction trigger={<Button variant="outline" disabled={busy}>{t("forget")}</Button>} title={t("forgetTitle")} description={t("forgetHelp")} confirm={t("forgetConfirm")} onConfirmedClose={focusHistory} onConfirm={() => forget(intent.requestId)} /></div></div>}
          {busy ? <p className="backup-notice" role="status">{t("operationPending")}</p> : notice && <p className={errorNotice ? "backup-notice error-message" : "backup-notice"} role={errorNotice ? "alert" : "status"}>{t(notice)}</p>}
          {tasks.isPending ? <p className="backup-help" role="status">{t("historyLoading")}</p> : tasks.isError ? <div><p className="error-message" role="alert">{t(ConnectError.from(tasks.error).code === Code.DataLoss ? "invalidResponse" : "historyError")}</p><Button variant="outline" disabled={tasks.isFetching} onClick={() => { void tasks.refetch() }}>{t("refresh")}</Button></div> : !tasks.data || tasks.data.jobs.length === 0 ? <div className="backup-empty"><Archive size={28} aria-hidden="true" /><h3>{t(history.length === 1 ? "empty" : "pageEmpty")}</h3><p className="backup-help">{t(history.length === 1 ? "emptyHelp" : "pageEmptyHelp")}</p></div> : <>
            <ol className="backup-task-list">{tasks.data.jobs.map((job) => <li key={job.id} data-backup-job={job.id}>
              <TaskPhase job={job} /><code className="backup-job-id">{job.id}</code><TaskMetadata job={job} />
              {job.cancelRequested && <p className="backup-flag">{t("cancelFlag")}</p>}{job.errorCode && <p className="backup-help">{t(`code.${job.errorCode}`)}</p>}
              <div className="backup-actions"><TaskDetails job={job} owner={owner} client={client} disabled={busy} />{isActiveBackup(job) && !job.cancelRequested && <ConfirmAction trigger={<Button variant="outline" disabled={busy}><Square size={14} aria-hidden="true" />{t("cancel")}</Button>} title={t("cancelTitle")} description={t("cancelHelp")} confirm={t("cancelConfirm")} onConfirmedClose={focusHistory} onConfirm={() => operate("cancel", job.id)} />}{!isActiveBackup(job) && job.objects > 0 && <Button variant="outline" disabled={busy} onClick={() => operate("reconcile", job.id)}><FileSearch size={16} aria-hidden="true" />{t("reconcile")}</Button>}</div>
            </li>)}</ol>
          </>}
          <div className="backup-pagination"><span>{t("page", { page: new Intl.NumberFormat(i18n.resolvedLanguage).format(history.length) })}</span><div className="backup-actions"><Button variant="outline" disabled={busy || history.length === 1} onClick={() => setHistory((current) => current.slice(0, -1))}>{t("previous")}</Button><Button variant="outline" disabled={busy || tasks.isFetching || !tasks.isSuccess || tasks.isError || !tasks.data?.nextCursor || history.length >= backupMaxPages} onClick={() => { const next = tasks.data?.nextCursor; if (next && !history.includes(next)) setHistory((current) => [...current, next]) }}>{t("next")}</Button></div></div>
          {history.length >= backupMaxPages && tasks.isSuccess && tasks.data.nextCursor && <p className="backup-help">{t("pageLimit")}</p>}
        </section>
      </>}
      <div className="backup-context"><section className="backup-panel"><ShieldCheck className="backup-emblem" size={22} aria-hidden="true" /><h2>{t("boundaries")}</h2><p className="backup-help">{t("scopeHelp")}</p><p className="backup-help">{t("restoreHelp")}</p><p className="backup-help">{t("reconcileHelp")}</p></section><section className="backup-panel"><LockKeyhole className="backup-emblem" size={22} aria-hidden="true" /><h2>{t("deployment")}</h2><p className="backup-help">{t("deploymentHelp")}</p><ul className="backup-config-flags"><li><code>--backup-enabled</code></li><li><code>--backup-s3-config-file</code></li><li><code>--backup-recovery-key-file</code></li></ul></section></div>
    </>}
  </div>
}

export function ServerBackupPage() {
  const { session, backupManagement } = useApi()
  return session ? <BackupWorkspace key={session.userId} owner={session.userId} client={backupManagement} /> : null
}
