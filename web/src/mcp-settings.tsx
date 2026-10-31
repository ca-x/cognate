import "./mcp-settings-i18n"
import "./management-ui.css"
import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent, type MouseEvent } from "react"
import { Code, ConnectError, type Client } from "@connectrpc/connect"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Trans, useTranslation } from "react-i18next"
import { CircleAlert, Copy, History, KeyRound, Pencil, Plus, RefreshCw, ShieldCheck, ShieldX } from "lucide-react"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { MCPManagementService, type Credential, type CredentialSpec } from "./gen/skillhub/mcp/v1/mcp_pb"

export type MCPSettingsPageProps = { client: Client<typeof MCPManagementService>; owner: string }

type Metadata = Pick<Credential, "id" | "name" | "scopes" | "createdAtUnix" | "expiresAtUnix" | "revokedAtUnix" | "revision">
type Spec = Pick<CredentialSpec, "name" | "scopes" | "expiresAtUnix">
type WriteKind = "create" | "edit" | "rotate" | "revoke"
type Action = { kind: WriteKind | "audit"; id?: string }
type Notice = "created" | "updated" | "rotated" | "revokedNotice" | "secretHidden"

const requestTimeoutMs = 20_000
const maxValidityMs = 365 * 86_400_000
const credentialLimit = 100
const auditPageSize = 20
const scopeChoices = [
  { value: "catalog:read", label: "catalogRead", help: "catalogReadHelp" },
  { value: "catalog:write", label: "catalogWrite", help: "catalogWriteHelp" },
  { value: "packs:read", label: "packsRead", help: "packsReadHelp" },
  { value: "packs:write", label: "packsWrite", help: "packsWriteHelp" },
  { value: "categories:read", label: "读取技能分类", help: "读取自己的分类及技能分类关联；不能维护分类或公开内容。" },
  { value: "categories:write", label: "维护技能分类", help: "维护自己的分类及技能分类关联；不包含分类读取权限，不修改技能内容，也不能公开内容。" },
  { value: "publication:write", label: "发布或取消公开仓库技能", help: "以仓库为单位公开或取消公开自己的技能。发布前必须明确确认：仓库内所有技能的完整目录（含隐藏文件和二进制文件）均可被匿名下载；取消公开无法收回已下载内容。此权限独立于仓库收录、分类维护和读取权限。" },
] as const
const actionLabels: Record<string, string> = {
  "token.create": "actionCreate", "token.update": "actionUpdate", "token.rotate": "actionRotate", "token.revoke": "actionRevoke",
  catalog_list: "actionCatalogList", catalog_get: "actionCatalogGet", catalog_collect: "actionCatalogCollect",
  packs_list: "actionPacksList", packs_get: "actionPacksGet", packs_create: "actionPacksCreate", packs_update: "actionPacksUpdate", packs_delete: "actionPacksDelete",
}
const outcomeLabels: Record<string, string> = { success: "outcomeSuccess", denied: "outcomeDenied", failure: "outcomeFailure" }

function metadata(credential: Credential): Metadata {
  return {
    id: credential.id, name: credential.name, scopes: [...credential.scopes], createdAtUnix: credential.createdAtUnix,
    expiresAtUnix: credential.expiresAtUnix, revokedAtUnix: credential.revokedAtUnix, revision: credential.revision,
  }
}

function failureCode(error: unknown): Code {
  if (error instanceof ConnectError) return error.code
  if (error instanceof DOMException && error.name === "AbortError") return Code.Canceled
  return Code.Unknown
}

async function readMetadata<Result>(read: () => Promise<Result>): Promise<Result> {
  try { return await read() } catch (error) { throw new ConnectError("", failureCode(error)) }
}

function errorKey(code: Code) {
  switch (code) {
    case Code.Unauthenticated: return "errorSession"
    case Code.PermissionDenied: return "errorPermission"
    case Code.NotFound: return "errorNotFound"
    case Code.InvalidArgument: return "errorInvalid"
    case Code.Aborted: return "errorConflict"
    case Code.ResourceExhausted: return "errorLimit"
    case Code.DeadlineExceeded: return "errorTimeout"
    case Code.Unavailable: return "errorUnavailable"
    case Code.Canceled: return "errorCanceled"
    case Code.FailedPrecondition: return "errorPrecondition"
    case Code.Unimplemented: return "errorUnimplemented"
    default: return "errorUnknown"
  }
}

function ErrorNotice({ code, retry }: { code: Code; retry?: () => void }) {
  const { t } = useTranslation("mcp")
  return <div role="alert" className="error-notice flex-wrap">
    <CircleAlert size={18} className="shrink-0" aria-hidden="true" />
    <p className="min-w-0 flex-1 basis-40 break-words">{t(errorKey(code))}</p>
    {retry && <Button variant="outline" onClick={retry}>{t("refresh")}</Button>}
  </div>
}

function localDateTime(milliseconds: number) {
  const date = new Date(milliseconds)
  const pad = (value: number) => String(value).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function validateDraft(name: string, scopes: string[], expiry: string, now: number) {
  const trimmed = name.trim()
  const bytes = new TextEncoder().encode(trimmed).length
  const expires = new Date(expiry).getTime()
  return {
    name: !trimmed ? "nameRequired" : bytes > 128 ? "nameTooLong" : /[\u0000-\u001f\u007f-\u009f]/u.test(trimmed) ? "nameInvalid" : "",
    scopes: !scopes.length || new Set(scopes).size !== scopes.length || scopes.some((scope) => !scopeChoices.some((choice) => choice.value === scope)) ? "scopesRequired" : "",
    expiry: !expiry || !Number.isFinite(expires) || localDateTime(expires) !== (expiry.length === 16 ? `${expiry}:00` : expiry) ? "expiryInvalid" : Math.floor(expires / 1000) * 1000 <= now ? "expiryPast" : expires > now + maxValidityMs ? "expiryTooFar" : "",
  }
}

function CredentialFacts({ credential, now, unverified = false }: { credential: Metadata; now: number; unverified?: boolean }) {
  const { t, i18n } = useTranslation("mcp")
  const date = (value: bigint) => {
    const instant = new Date(Number(value) * 1000)
    return Number.isFinite(instant.getTime()) ? new Intl.DateTimeFormat(i18n.resolvedLanguage, { dateStyle: "medium", timeStyle: "short" }).format(instant) : t("dateUnknown")
  }
  const status = unverified ? "unverifiedStatus" : credential.revokedAtUnix !== 0n ? "revoked" : Number(credential.expiresAtUnix) * 1000 <= now ? "expired" : "active"
  return <div className="management-facts min-w-0 space-y-4">
    <div className="flex flex-wrap items-start gap-2"><h3 className="min-w-0 flex-1 break-words font-semibold [overflow-wrap:anywhere]">{credential.name}</h3><span className="management-status" data-status={status}>{t(status)}</span></div>
    <dl className="management-metadata grid min-w-0 gap-3 text-sm sm:grid-cols-2">
      <div className="min-w-0"><dt className="text-muted-foreground">{t("credentialId")}</dt><dd className="mt-1 break-all font-mono text-xs">{credential.id}</dd></div>
      <div className="min-w-0"><dt className="text-muted-foreground">{t("revision")}</dt><dd className="mt-1 break-all tabular-nums">{new Intl.NumberFormat(i18n.resolvedLanguage).format(credential.revision)}</dd></div>
      <div><dt className="text-muted-foreground">{t("createdAt")}</dt><dd className="mt-1">{date(credential.createdAtUnix)}</dd></div>
      <div><dt className="text-muted-foreground">{t("expiresAt")}</dt><dd className="mt-1">{date(credential.expiresAtUnix)}</dd></div>
      {credential.revokedAtUnix !== 0n && <div><dt className="text-muted-foreground">{t("revokedAt")}</dt><dd className="mt-1">{date(credential.revokedAtUnix)}</dd></div>}
    </dl>
    <div><p className="text-sm text-muted-foreground">{t("scopes")}</p><ul className="mt-2 flex flex-wrap gap-2">{credential.scopes.map((scope) => <li key={scope} className="min-w-0 rounded-md border border-border px-2 py-1"><code className="break-all text-xs">{scope}</code></li>)}</ul></div>
  </div>
}

function CredentialForm({ original, pending, blocked, onSubmit }: { original?: Metadata; pending: boolean; blocked: boolean; onSubmit: (spec: Spec) => void }) {
  const { t, i18n } = useTranslation("mcp")
  const id = useId()
  const [name, setName] = useState(original?.name ?? "")
  const [scopes, setScopes] = useState<string[]>(() => original ? [...original.scopes] : [])
  const [expiry, setExpiry] = useState(() => localDateTime(original ? Number(original.expiresAtUnix) * 1000 : Math.floor((Date.now() + 30 * 86_400_000) / 60_000) * 60_000))
  const [validity, setValidity] = useState(original ? "custom" : "30")
  const [touched, setTouched] = useState({ name: false, scopes: false, expiry: false })
  const nameRef = useRef<HTMLInputElement>(null)
  const scopesRef = useRef<HTMLButtonElement>(null)
  const expiryRef = useRef<HTMLInputElement>(null)
  const errors = validateDraft(name, scopes, expiry, Date.now())
  const nameBytes = new TextEncoder().encode(name.trim()).length
  const invalidName = Boolean(errors.name && (touched.name || nameBytes > 128))
  const invalidScopes = Boolean(errors.scopes && touched.scopes)
  const invalidExpiry = Boolean(errors.expiry && touched.expiry)
  const disabled = pending || blocked
  const number = new Intl.NumberFormat(i18n.resolvedLanguage)

  function submit(event: FormEvent) {
    event.preventDefault()
    if (disabled) return
    setTouched({ name: true, scopes: true, expiry: true })
    const current = validateDraft(name, scopes, expiry, Date.now())
    if (current.name || current.scopes || current.expiry) {
      if (current.name) nameRef.current?.focus()
      else if (current.scopes) scopesRef.current?.focus()
      else expiryRef.current?.focus()
      return
    }
    onSubmit({ name: name.trim(), scopes: [...scopes], expiresAtUnix: BigInt(Math.floor(new Date(expiry).getTime() / 1000)) })
  }

  return <form className="management-token-form mt-5 min-w-0 space-y-5" onSubmit={submit} noValidate autoComplete="off" aria-busy={pending}>
    <div className="min-w-0 space-y-2">
      <label htmlFor={`${id}-name`}>{t("name")}</label>
      <Input ref={nameRef} id={`${id}-name`} name="mcp-credential-name" value={name} maxLength={128} autoComplete="off" autoCapitalize="off" spellCheck={false} required disabled={disabled} placeholder={t("namePlaceholder")} onChange={(event) => setName(event.target.value)} onBlur={() => setTouched((current) => ({ ...current, name: true }))} aria-invalid={invalidName} aria-describedby={`${id}-name-help${invalidName ? ` ${id}-name-error` : ""}`} />
      <div id={`${id}-name-help`} className="field-help"><p>{t("nameHelp")}</p><p>{t("nameBytes", { used: number.format(nameBytes), max: number.format(128) })}</p></div>
      {invalidName && <p id={`${id}-name-error`} role="alert" className="error-message">{t(errors.name)}</p>}
    </div>
    <fieldset disabled={disabled} className="min-w-0 space-y-2" aria-describedby={`${id}-scopes-help${invalidScopes ? ` ${id}-scopes-error` : ""}`}>
      <legend className="text-sm font-medium">{t("scopes")}</legend>
      <p id={`${id}-scopes-help`} className="field-help">{t("scopesHelp")}</p>
      <div className="management-scopes rounded-lg border border-border">{scopeChoices.map((choice, index) => <label key={choice.value} htmlFor={`${id}-${choice.value}`} className="flex min-h-11 cursor-pointer items-start gap-3 border-b border-border p-3 last:border-b-0">
        <Checkbox ref={index === 0 ? scopesRef : undefined} id={`${id}-${choice.value}`} className="mt-1" checked={scopes.includes(choice.value)} disabled={disabled} aria-invalid={invalidScopes} aria-describedby={`${id}-${choice.value}-help`} onCheckedChange={(checked) => { setScopes((current) => checked === true ? current.includes(choice.value) ? current : [...current, choice.value] : current.filter((scope) => scope !== choice.value)); setTouched((current) => ({ ...current, scopes: true })) }} />
        <span className="min-w-0"><span className="block text-sm">{t(choice.label, { ns: ["mcp", "translation"] })}</span><code className="mt-1 block break-all text-xs text-muted-foreground">{choice.value}</code><span id={`${id}-${choice.value}-help`} className="field-help mt-1 block">{t(choice.help, { ns: ["mcp", "translation"] })}</span></span>
      </label>)}</div>
      {invalidScopes && <p id={`${id}-scopes-error`} role="alert" className="error-message">{t(errors.scopes)}</p>}
    </fieldset>
    <div className="min-w-0 space-y-2">
      <label htmlFor={`${id}-validity`}>{t("validity")}</label>
      <Select value={validity} disabled={disabled} onValueChange={(value) => { setValidity(value); if (value !== "custom") setExpiry(localDateTime(Math.floor((Date.now() + Number(value) * 86_400_000) / 60_000) * 60_000)); setTouched((current) => ({ ...current, expiry: true })) }}>
        <SelectTrigger id={`${id}-validity`} className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent>{[7, 30, 90, 365].map((days) => <SelectItem key={days} value={String(days)}>{t("days", { count: days })}</SelectItem>)}<SelectItem value="custom">{t("customExpiry")}</SelectItem></SelectContent>
      </Select>
      <label htmlFor={`${id}-expiry`} className="block">{t("expiryInput")}</label>
      <Input ref={expiryRef} id={`${id}-expiry`} name="mcp-expiration" type="datetime-local" className="min-w-0 max-w-full" step={1} required autoComplete="off" value={expiry} min={localDateTime(Math.floor(Date.now() / 1000) * 1000 + 1000)} max={localDateTime(Date.now() + maxValidityMs)} disabled={disabled} onChange={(event) => { setExpiry(event.target.value); setValidity("custom") }} onBlur={() => setTouched((current) => ({ ...current, expiry: true }))} aria-invalid={invalidExpiry} aria-describedby={`${id}-expiry-help${invalidExpiry ? ` ${id}-expiry-error` : ""}`} />
      <p id={`${id}-expiry-help`} className="field-help">{t("expiryHelp", { zone: Intl.DateTimeFormat().resolvedOptions().timeZone })}</p>
      {Number.isFinite(new Date(expiry).getTime()) && <p className="field-help">{t("expiryPreview", { value: new Intl.DateTimeFormat(i18n.resolvedLanguage, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(expiry)) })}</p>}
      {invalidExpiry && <p id={`${id}-expiry-error`} role="alert" className="error-message">{t(errors.expiry)}</p>}
    </div>
    <Button type="submit" disabled={disabled} className="max-w-full whitespace-normal">{t(pending ? original ? "saving" : "creating" : original ? "save" : "create")}</Button>
  </form>
}

function CredentialOperation({ client, owner, action, credential, onChanged, onClose, now, unverified = false }: MCPSettingsPageProps & { action: Action; credential?: Metadata; onChanged: (notice: Notice) => void; onClose: () => void; now: number; unverified?: boolean }) {
  const { t } = useTranslation("mcp")
  const id = useId()
  const [original] = useState(credential)
  const [secret, setSecret] = useState("")
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Code | null>(null)
  const [copyNotice, setCopyNotice] = useState<"copied" | "copyFailed" | "">("")
  const [copying, setCopying] = useState(false)
  const active = useRef<AbortController | null>(null)
  const alive = useRef(false)
  const writeAttempted = useRef(false)
  const copyPending = useRef(false)
  const queryClient = useQueryClient()
  useLayoutEffect(() => {
    alive.current = true
    return () => { alive.current = false; active.current?.abort(); active.current = null }
  }, [])

  async function write(spec?: Spec) {
    if (!alive.current || writeAttempted.current || active.current || secret || failure !== null || unverified || action.kind === "audit") return
    if ((action.kind === "create" || action.kind === "edit") && !spec) return
    if (action.kind !== "create" && (!original || original.revokedAtUnix !== 0n || (action.kind === "rotate" && Number(original.expiresAtUnix) * 1000 <= Date.now()))) { setFailure(Code.FailedPrecondition); return }
    writeAttempted.current = true
    const controller = new AbortController()
    active.current = controller
    setPending(true)
    const options = { signal: controller.signal, timeoutMs: requestTimeoutMs }
    try {
      if (action.kind === "create" || action.kind === "rotate") {
        const response = action.kind === "create" ? await client.createCredential({ spec }, options) : await client.rotateCredential({ id: original!.id, expectedRevision: original!.revision }, options)
        try {
          if (controller.signal.aborted || !alive.current) return
          if (!response.token || !response.credential) throw new ConnectError("", Code.DataLoss)
          setSecret(response.token)
          onChanged(action.kind === "create" ? "created" : "rotated")
        } finally { response.token = "" }
      } else {
        const request = { id: original!.id, expectedRevision: original!.revision }
        const response = action.kind === "edit" ? await client.updateCredential({ ...request, spec }, options) : await client.revokeCredential(request, options)
        if (controller.signal.aborted || !alive.current) return
        if (!response.credential) throw new ConnectError("", Code.DataLoss)
        onChanged(action.kind === "edit" ? "updated" : "revokedNotice")
        onClose()
      }
    } catch (error) {
      if (!controller.signal.aborted && alive.current) {
        setFailure(failureCode(error))
        void queryClient.invalidateQueries({ queryKey: ["mcp", owner] })
      }
    } finally {
      if (active.current === controller) { active.current = null; if (alive.current) setPending(false) }
    }
  }

  async function copy() {
    if (!secret || !alive.current || copyPending.current) return
    copyPending.current = true
    setCopying(true)
    setCopyNotice("")
    try { await navigator.clipboard.writeText(secret); if (alive.current) setCopyNotice("copied") }
    catch { if (alive.current) setCopyNotice("copyFailed") }
    finally { copyPending.current = false; if (alive.current) setCopying(false) }
  }

  if (secret) return <section className="management-secret mt-5 min-w-0 space-y-4" data-management-secret aria-labelledby={`${id}-secret-title`}>
    <h3 id={`${id}-secret-title`} className="flex items-start gap-2 font-semibold"><ShieldCheck size={18} aria-hidden="true" />{t("oneTimeTitle")}</h3>
    <p role="status" className="text-sm leading-6">{t(action.kind === "create" ? "created" : "rotated")}</p>
    <p className="text-sm leading-6 text-muted-foreground">{t("oneTimeHelp")}</p>
    <label htmlFor={`${id}-token`} className="block">{t("rawToken")}</label>
    <textarea id={`${id}-token`} readOnly rows={3} value={secret} autoFocus autoComplete="off" autoCapitalize="off" spellCheck={false} aria-describedby={`${id}-secret-help`} className="w-full min-w-0 resize-y rounded-lg border border-border bg-background p-3 font-mono text-sm [overflow-wrap:anywhere]" onFocus={(event) => event.currentTarget.select()} />
    <p id={`${id}-secret-help`} className="field-help">{t("secretHelp")}</p>
    <div className="flex flex-wrap gap-3"><Button variant="outline" disabled={copying} onClick={() => { void copy() }}><Copy size={16} aria-hidden="true" />{t(copying ? "copying" : "copy")}</Button><Button onClick={() => { setSecret(""); setCopyNotice(""); onClose() }}>{t("dismissSecret")}</Button></div>
    <p role="status" className="text-sm text-muted-foreground">{copyNotice && t(copyNotice)}</p>
  </section>

  return <div className="mt-5 min-w-0 space-y-5" aria-busy={pending}>
    {original && <CredentialFacts credential={original} now={now} unverified={unverified} />}
    {failure !== null && <div className="space-y-2"><ErrorNotice code={failure} /><p className="text-sm leading-6 text-muted-foreground">{t("writeUncertain")}</p><Button variant="outline" onClick={onClose}>{t("closeAndReload")}</Button></div>}
    {action.kind === "create" || action.kind === "edit" ? <CredentialForm original={original} pending={pending} blocked={unverified || failure !== null || original?.revokedAtUnix !== undefined && original.revokedAtUnix !== 0n} onSubmit={(spec) => { void write(spec) }} /> : <Button variant="outline" className={action.kind === "revoke" ? "max-w-full whitespace-normal border-destructive text-destructive" : "max-w-full whitespace-normal"} disabled={unverified || pending || failure !== null || !original || original.revokedAtUnix !== 0n || action.kind === "rotate" && Number(original.expiresAtUnix) * 1000 <= now} onClick={() => { void write() }}>{t(pending ? action.kind === "rotate" ? "rotating" : "revoking" : action.kind === "rotate" ? "confirmRotate" : "confirmRevoke")}</Button>}
    <p className="field-help">{t("cancellationHelp")}</p>
  </div>
}

function AuditView({ client, owner, credentialId }: MCPSettingsPageProps & { credentialId: string }) {
  const { t, i18n } = useTranslation("mcp")
  const [page, setPage] = useState(0)
  const query = useQuery({
    queryKey: ["mcp", owner, "audit", credentialId], retry: false, gcTime: 0,
    queryFn: ({ signal }) => readMetadata(async () => {
      const response = await client.listAudit({ credentialId }, { signal, timeoutMs: requestTimeoutMs })
      return response.entries.map((entry) => ({ credentialId: entry.credentialId, action: entry.action, resource: entry.resource, outcome: entry.outcome, createdAtUnix: entry.createdAtUnix }))
    }),
  })
  useLayoutEffect(() => {
    if (document.activeElement === document.body) document.querySelector<HTMLElement>(".management-dialog h2")?.focus()
  }, [query.status, query.fetchStatus])
  if (query.isPending) return <p role="status" className="mt-5 text-sm">{t("loadingAudit")}</p>
  if (query.isError) return <ErrorNotice code={failureCode(query.error)} retry={() => { void query.refetch() }} />
  const entries = query.data
  const total = Math.max(1, Math.ceil(entries.length / auditPageSize))
  const currentPage = Math.min(page, total - 1)
  const number = new Intl.NumberFormat(i18n.resolvedLanguage)
  return <div className="mt-5 min-w-0 space-y-4">
    <Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t(query.isFetching ? "refreshing" : "refresh")}</Button>
    {!entries.length ? <p className="text-sm text-muted-foreground">{t("auditEmpty")}</p> : <>
      <ol className="space-y-3" start={currentPage * auditPageSize + 1}>{entries.slice(currentPage * auditPageSize, (currentPage + 1) * auditPageSize).map((entry, index) => <li key={`${currentPage}-${index}`} className="min-w-0 rounded-lg border border-border p-3">
        <dl className="grid min-w-0 gap-2 text-sm">
          <div><dt className="text-xs text-muted-foreground">{t("auditAction")}</dt><dd>{t(actionLabels[entry.action] ?? "actionUnknown")}</dd></div>
          <div><dt className="text-xs text-muted-foreground">{t("auditOutcome")}</dt><dd>{t(outcomeLabels[entry.outcome] ?? "outcomeUnknown")}</dd></div>
          <div className="min-w-0"><dt className="text-xs text-muted-foreground">{t("credentialId")}</dt><dd className="break-all font-mono text-xs">{entry.credentialId}</dd></div>
          <div className="min-w-0"><dt className="text-xs text-muted-foreground">{t("auditResource")}</dt><dd className="break-all font-mono text-xs">{entry.resource || t("noResource")}</dd></div>
          <div><dt className="text-xs text-muted-foreground">{t("auditTime")}</dt><dd>{new Intl.DateTimeFormat(i18n.resolvedLanguage, { dateStyle: "medium", timeStyle: "short" }).format(new Date(Number(entry.createdAtUnix) * 1000))}</dd></div>
        </dl>
      </li>)}</ol>
      <nav className="flex flex-wrap items-center gap-3" aria-label={t("auditPagination")}><Button variant="outline" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>{t("previous")}</Button><Button variant="outline" disabled={currentPage + 1 >= total} onClick={() => setPage(currentPage + 1)}>{t("next")}</Button><p className="text-sm text-muted-foreground" role="status">{t("auditPage", { page: number.format(currentPage + 1), total: number.format(total) })}</p></nav>
    </>}
  </div>
}

function CredentialDialogBody({ action, client, owner, now, onChanged, onClose }: MCPSettingsPageProps & { action: Action; now: number; onChanged: (notice: Notice) => void; onClose: () => void }) {
  const { t } = useTranslation("mcp")
  const query = useQuery({
    queryKey: ["mcp", owner, "credential", action.id], enabled: action.kind !== "create" && Boolean(action.id), retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false,
    queryFn: ({ signal }) => readMetadata(async () => {
      const response = await client.getCredential({ id: action.id! }, { signal, timeoutMs: requestTimeoutMs })
      if (!response.credential) throw new ConnectError("", Code.DataLoss)
      return metadata(response.credential)
    }),
  })
  useLayoutEffect(() => {
    if (document.activeElement === document.body) document.querySelector<HTMLElement>(".management-dialog h2")?.focus()
  }, [query.status, query.fetchStatus])
  if (action.kind === "create") return <CredentialOperation client={client} owner={owner} action={action} onChanged={onChanged} onClose={onClose} now={now} />
  return <>
    {query.isPending && <p role="status" className="mt-5 text-sm">{t("loadingCredential")}</p>}
    {query.isError && <ErrorNotice code={failureCode(query.error)} retry={() => { void query.refetch() }} />}
    {query.data && (action.kind === "audit" ? <><div className="mt-5"><CredentialFacts credential={query.data} now={now} /></div><AuditView client={client} owner={owner} credentialId={query.data.id} /></> : <CredentialOperation client={client} owner={owner} action={action} credential={query.data} onChanged={onChanged} onClose={onClose} now={now} unverified={query.isError || query.isFetching} />)}
  </>
}

function MCPSettingsSession({ client, owner }: MCPSettingsPageProps) {
  const { t, i18n } = useTranslation("mcp")
  const [action, setAction] = useState<Action | null>(null)
  const [notice, setNotice] = useState<Notice | "">("")
  const [now, setNow] = useState(Date.now)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const content = useRef<HTMLDivElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const queryClient = useQueryClient()
  const id = useId()
  const key = ["mcp", owner]
  const query = useQuery({
    queryKey: [...key, "credentials"], retry: false, gcTime: 0, staleTime: 0,
    queryFn: ({ signal }) => readMetadata(async () => (await client.listCredentials({}, { signal, timeoutMs: requestTimeoutMs })).credentials.map(metadata)),
  })
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 30_000); return () => window.clearInterval(timer) }, [])
  useLayoutEffect(() => {
    if (!action && (query.isError && document.activeElement === document.body || document.activeElement instanceof HTMLButtonElement && document.activeElement.disabled)) heading.current?.focus()
  }, [query.isError, query.data, action])

  function open(next: Action, event: MouseEvent<HTMLButtonElement>) {
    trigger.current = event.currentTarget
    setNotice("")
    setAction(next)
  }
  function refresh() { void queryClient.invalidateQueries({ queryKey: key }) }
  function close() { setAction(null); setNotice((current) => current === "created" || current === "rotated" ? "secretHidden" : current); refresh() }
  const title = action?.kind === "create" ? "create" : action?.kind === "edit" ? "edit" : action?.kind === "rotate" ? "rotate" : action?.kind === "revoke" ? "revoke" : "audit"
  const description = action?.kind === "create" ? "createDescription" : action?.kind === "edit" ? "editDescription" : action?.kind === "rotate" ? "rotateDescription" : action?.kind === "revoke" ? "revokeDescription" : "auditDescription"

  return <div className="management-page">
    <div className="page-heading flex-wrap"><div className="min-w-0"><p className="management-eyebrow">{t("workspaceAccess")}</p><h1 ref={heading} tabIndex={-1}>{t("title")}</h1><p>{t("introduction")}</p></div><Button disabled={query.isPending || query.isError || (query.data?.length ?? 0) >= credentialLimit} onClick={(event) => open({ kind: "create" }, event)}><Plus size={17} aria-hidden="true" />{t("create")}</Button></div>
    <section className="management-card management-endpoint min-w-0 space-y-3" aria-labelledby={`${id}-endpoint`}>
      <h2 id={`${id}-endpoint`} className="flex items-center gap-2 font-semibold"><KeyRound size={18} aria-hidden="true" />{t("endpoint")}</h2>
      <code className="inline-block max-w-full rounded-md bg-muted px-3 py-2 text-sm">/mcp</code>
      <p className="text-sm leading-6 text-muted-foreground">{t("connectionHelp")}</p>
      <p className="text-sm leading-6">{t("assetBoundary")}</p>
      <details className="management-disclosure"><summary>{t("permissionBoundary")}</summary><p className="mt-3 text-sm leading-6 text-muted-foreground"><Trans ns="mcp" i18nKey="capabilityHelp" components={{ code: <code /> }} /></p><p className="mt-3 text-sm leading-6 text-muted-foreground">{t("callChecks")}</p></details>
    </section>
    <p role="status" className="mt-4 text-sm text-success">{notice && t(notice)}</p>
    <section className="mt-7 min-w-0" aria-labelledby={`${id}-credentials`} aria-busy={query.isFetching}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3"><h2 id={`${id}-credentials`} className="font-semibold">{t("credentials")}</h2><Button variant="outline" disabled={query.isFetching} onClick={refresh}><RefreshCw size={16} aria-hidden="true" />{t(query.isFetching ? "refreshing" : "refresh")}</Button></div>
      <p className="field-help">{t("capacityHelp")}</p>
      {query.data && !query.isError && <p className="mt-2 text-sm text-muted-foreground">{t("credentialCount", { count: new Intl.NumberFormat(i18n.resolvedLanguage).format(query.data.length), limit: new Intl.NumberFormat(i18n.resolvedLanguage).format(credentialLimit) })}</p>}
      {query.isPending && <p role="status" className="mt-5 text-sm">{t("loading")}</p>}
      {query.isError && <ErrorNotice code={failureCode(query.error)} retry={refresh} />}
      {query.isError && query.data && <p className="management-feedback" role="status">{t("staleMetadata")}</p>}
      {query.data && (!query.data.length ? !query.isError && <div className="empty-state mt-4 rounded-xl border border-border bg-card"><KeyRound className="empty-icon" size={30} aria-hidden="true" /><h3 className="font-semibold">{t("emptyTitle")}</h3><p className="mt-2 max-w-md text-sm leading-6 text-muted-foreground">{t("emptyHelp")}</p></div> : <ul className="mt-4 space-y-4">{query.data.map((credential) => <li key={credential.id} className="management-card management-credential min-w-0">
        <CredentialFacts credential={credential} now={now} unverified={query.isError} />
        <div className="management-actions">
          <Button variant="outline" disabled={query.isError || credential.revokedAtUnix !== 0n} aria-label={t("editLabel", { name: credential.name })} onClick={(event) => open({ kind: "edit", id: credential.id }, event)}><Pencil size={15} aria-hidden="true" />{t("edit")}</Button>
          <Button variant="outline" disabled={query.isError || credential.revokedAtUnix !== 0n || Number(credential.expiresAtUnix) * 1000 <= now} aria-label={t("rotateLabel", { name: credential.name })} onClick={(event) => open({ kind: "rotate", id: credential.id }, event)}><RefreshCw size={15} aria-hidden="true" />{t("rotate")}</Button>
          <Button variant="ghost" disabled={query.isError} aria-label={t("auditLabel", { name: credential.name })} onClick={(event) => open({ kind: "audit", id: credential.id }, event)}><History size={15} aria-hidden="true" />{t("audit")}</Button>
          <Button variant="outline" disabled={query.isError || credential.revokedAtUnix !== 0n} className="management-danger text-destructive" aria-label={t("revokeLabel", { name: credential.name })} onClick={(event) => open({ kind: "revoke", id: credential.id }, event)}><ShieldX size={15} aria-hidden="true" />{t("revoke")}</Button>
        </div>
      </li>)}</ul>)}
    </section>
    <Dialog open={Boolean(action)} onOpenChange={(open) => { if (!open) close() }}>
      {action && <DialogContent ref={content} className="management-dialog" onOpenAutoFocus={(event) => { if (action.kind === "rotate" || action.kind === "revoke") { event.preventDefault(); content.current?.querySelector<HTMLButtonElement>("[data-mcp-cancel]")?.focus() } }} onCloseAutoFocus={(event) => { event.preventDefault(); if (trigger.current?.isConnected && !trigger.current.disabled) trigger.current.focus(); else heading.current?.focus() }}>
        <DialogTitle tabIndex={-1}>{t(title)}</DialogTitle><DialogDescription>{t(description)}</DialogDescription>
        <CredentialDialogBody action={action} client={client} owner={owner} now={now} onChanged={(message) => { if (message === "revokedNotice") trigger.current = null; setNotice(message); refresh() }} onClose={close} />
        <div className="mt-6 border-t border-border pt-4"><DialogClose asChild><Button variant="outline" data-mcp-cancel>{t("close")}</Button></DialogClose></div>
      </DialogContent>}
    </Dialog>
  </div>
}

export function MCPSettingsPage({ client, owner }: MCPSettingsPageProps) {
  const [boundary, setBoundary] = useState({ client, owner, generation: 0 })
  if (boundary.client !== client || boundary.owner !== owner) setBoundary({ client, owner, generation: boundary.generation + 1 })
  if (!owner) return <ErrorNotice code={Code.Unauthenticated} />
  return <MCPSettingsSession key={boundary.generation} client={client} owner={owner} />
}
