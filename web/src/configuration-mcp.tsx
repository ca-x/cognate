import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react"
import { Link, useBlocker } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { Code, ConnectError, type Client } from "@connectrpc/connect"
import { clone } from "@bufbuild/protobuf"
import { Plus, RefreshCw, Workflow } from "lucide-react"
import { useApi } from "./api"
import { formatNumber } from "./i18n"
import { Preferences } from "./preferences"
import type { Asset } from "./gen/skillhub/configuration/v1/configuration_pb"
import { DraftSchema, MCPConfigurationService, type View } from "./gen/skillhub/configuration/mcp/v1/mcp_pb"
import { MCPRequestGate, requireMCPAsset, requireMCPPreview, requireMCPView, requireMCPWritten, supportsMCP, validateMCP } from "./configuration-mcp-state"
import { MCPConfigurationForm, type MCPFormAttempt } from "./configuration-mcp-form"
import { ConfigurationOrderControl } from "./configuration-order"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import "./configuration-mcp-i18n"
import "./configuration-mcp.css"

type MCPClient = Client<typeof MCPConfigurationService>
type Action = { kind: "create" } | { kind: "edit" | "copy" | "delete" | "metadata"; asset: Asset }
type Gate = MCPRequestGate["state"]

function Declaration({ view }: { view: View }) {
  const { t } = useTranslation("configurationMCP")
  return <ol className="space-y-4">{view.draft?.document?.servers.map(server => <li key={server.id} className="rounded-lg border border-border p-3"><h3 className="font-semibold">{server.name}</h3><p>{server.id} · {server.connection.case} · {t("enabled")}: {String(server.enabled)}</p>{server.connection.case === "stdio" ? <><p className="break-words [overflow-wrap:anywhere]">{t("command")}: {server.connection.value.command}</p><p>{t("args")}: {server.connection.value.args ? `[${server.connection.value.args.values.map(arg => `${arg.id} → ${arg.slotRef}`).join(", ")}]` : "∅"}</p><p>{t("env")}: {server.connection.value.env ? `[${server.connection.value.env.values.map(env => `${env.name} → ${env.slotRef}`).join(", ")}]` : "∅"}</p></> : server.connection.case === "http" || server.connection.case === "sse" ? <><p className="break-words [overflow-wrap:anywhere]">{t("url")}: {server.connection.value.url}</p><p>{t("headers")}: {server.connection.value.headers ? `[${server.connection.value.headers.values.map(header => `${header.name} → ${header.slotRef}`).join(", ")}]` : "∅"}</p></> : null}</li>)}</ol>
}

function Editor({ action, client, run, state, secretEpoch, onDone, onDirtyChange, onSecrets, title }: { action: Action; client: MCPClient; run: MCPRequestGate["run"]; state: Gate; secretEpoch: number; onDone: (result: string) => void; onDirtyChange: (dirty: boolean) => void; onSecrets: (clear: (() => void) | undefined) => void; title: React.RefObject<HTMLHeadingElement | null> }) {
  const { t } = useTranslation("configurationMCP")
  const [view, setView] = useState<View>()
  const [readOnly, setReadOnly] = useState(false)
  const [asset, setAsset] = useState(action.kind === "create" ? undefined : action.asset)
  const [copyName, setCopyName] = useState("")
  const copyNameId = useId()
  const [confirmed, setConfirmed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const alive = useRef(true)
  const confirmation = useRef<HTMLButtonElement>(null)
  const copying = action.kind === "copy", deleting = action.kind === "delete"
  const id = action.kind === "create" ? undefined : action.asset.id
  const invalidCopyName = copying && view?.draft ? validateMCP({ ...view.draft, name: copyName }).name : undefined
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  useEffect(() => {
    if (!id) return
    let current = true
    void run(async signal => {
      const metadata = requireMCPAsset((await client.getAsset({ id }, { signal })).asset)
      if (metadata.id !== id || action.kind === "create" || metadata.agentId !== action.asset.agentId || metadata.kind !== action.asset.kind || metadata.schemaVersion !== action.asset.schemaVersion || action.kind !== "metadata" && metadata.revision !== action.asset.revision) throw new ConnectError("", Code.Aborted)
      if (action.kind === "metadata" || action.kind === "delete" || !supportsMCP(metadata)) return { metadata, parsed: undefined }
      const parsed = requireMCPView((await client.readDraft({ id }, { signal })).view)
      if (parsed.value.asset?.id !== id || parsed.value.asset.revision !== metadata.revision) throw new ConnectError("", Code.Aborted)
      return { metadata, parsed }
    }).then(result => {
      if (current && alive.current && result) { setAsset(result.metadata); setView(result.parsed?.value); setReadOnly(!supportsMCP(result.metadata) || result.parsed?.readOnly === true) }
    })
    return () => { current = false }
  }, [id, client, run, attempt])
  useLayoutEffect(() => { if (copying || deleting) onDirtyChange(copyName !== "" || confirmed) }, [copying, deleting, copyName, confirmed, onDirtyChange])
  const perform: MCPFormAttempt = async (kind, draft, secrets) => {
    if (state.blocked || readOnly || action.kind !== "create" && !view) return
    if (title.current?.closest("[role=dialog]")?.contains(document.activeElement)) title.current?.focus()
    if (kind === "preview") return run(async signal => requireMCPPreview(await client.preview({ id: asset?.id ?? "", expectedRevision: asset?.revision ?? 0n, draft, secrets }, { signal }), draft))
    const result = await run(async signal => {
      const response = asset ? await client.updateAsset({ id: asset.id, expectedRevision: asset.revision, draft, secrets }, { signal }) : await client.createAsset({ draft, secrets }, { signal })
      return requireMCPWritten(response.view, draft, asset, false, view?.secrets ?? [], secrets)
    }, true)
    if (result && alive.current) { onDone("saved"); return true }
  }
  async function metadataOperation() {
    if (state.pending || state.blocked || !confirmed || !asset || readOnly || copying && (!view?.draft || invalidCopyName)) return
    title.current?.focus()
    const result = await run(async signal => {
      if (copying && view?.draft) {
        const draft = clone(DraftSchema, view.draft)
        draft.name = copyName
        return requireMCPWritten((await client.copyAsset({ id: asset.id, expectedSourceRevision: asset.revision, name: copyName }, { signal })).view, draft, asset, true, [], [])
      }
      await client.deleteAsset({ id: asset.id, expectedRevision: asset.revision }, { signal })
      return true
    }, true)
    if (result && alive.current) onDone(copying ? "copied" : "deleted")
  }
  return <>
    {asset && <dl className="my-4 space-y-2 text-sm"><div><dt>{t("identity")}</dt><dd className="break-all font-mono">{asset.id}</dd></div><div><dt>{t("name")}</dt><dd>{asset.name}</dd></div><div><dt>{t("revision")}</dt><dd>{formatNumber(asset.revision)}</dd></div></dl>}
    {readOnly && <p role="status" className="mcp-status">{t("unavailable")}</p>}
    {action.kind === "metadata" ? <p className="mcp-help">{t("read")}</p> : action.kind === "create" || action.kind === "edit" ? !readOnly && (action.kind === "create" || view) ? <MCPConfigurationForm original={view} pending={state.pending} blocked={state.blocked} secretEpoch={secretEpoch} onAttempt={perform} onDirtyChange={onDirtyChange} onSecrets={onSecrets} /> : null : !readOnly && (deleting || view) ? <div className="mt-5 space-y-4"><p className="mcp-help">{t(copying ? "copyHelp" : "deleteHelp")}</p>{copying && view && <><Declaration view={view} /><div className="mcp-field"><label htmlFor={copyNameId}>{t("name")}</label><Input id={copyNameId} value={copyName} aria-invalid={!!invalidCopyName} aria-describedby={invalidCopyName ? `${copyNameId}-error` : undefined} disabled={state.pending || state.blocked} onChange={event => { setCopyName(event.target.value); setConfirmed(false) }} autoComplete="off" />{invalidCopyName && <p id={`${copyNameId}-error`}>{t(invalidCopyName)}</p>}</div></>}<div className="mcp-toggle"><Checkbox ref={confirmation} id="mcp-operation-confirm" checked={confirmed} disabled={state.pending || state.blocked} onCheckedChange={value => setConfirmed(value === true)} /><label htmlFor="mcp-operation-confirm">{t(deleting ? "deleteConfirm" : "confirmAction")}</label></div><Button disabled={!confirmed || state.pending || state.blocked || copying && !!invalidCopyName} onClick={() => void metadataOperation()}>{t(copying ? "copy" : "delete")}</Button></div> : null}
    {id && !view && action.kind !== "delete" && action.kind !== "metadata" && !state.pending && !readOnly && <Button type="button" variant="outline" disabled={state.blocked} onClick={() => setAttempt(value => value + 1)}>{t("refresh")}</Button>}
  </>
}

function MCPWorkspace({ owner, client }: { owner: string; client: MCPClient }) {
  const { t } = useTranslation("configurationMCP")
  const { configuration } = useApi()
  const queryClient = useQueryClient()
  const [action, setAction] = useState<Action | null>(null)
  const [dirty, setDirty] = useState(false)
  const [confirm, setConfirm] = useState<"discard" | "readback" | null>(null)
  const [notice, setNotice] = useState("")
  const [secretEpoch, setSecretEpoch] = useState(0)
  const [request, setRequest] = useState<Gate>({ pending: false, blocked: false, uncertain: false })
  const gate = useRef<MCPRequestGate | undefined>(undefined)
  const clearSecrets = useRef<(() => void) | undefined>(undefined)
  const heading = useRef<HTMLHeadingElement>(null)
  const title = useRef<HTMLHeadingElement>(null)
  const editor = useRef<HTMLDivElement>(null)
  const workspace = useRef<HTMLDivElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const discardTrigger = useRef<HTMLElement | null>(null)
  const lastFocus = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    const current = new MCPRequestGate(() => { if (gate.current === current) setRequest({ ...current.state }) })
    gate.current = current
    setRequest({ ...current.state })
    return () => { gate.current = undefined; clearSecrets.current?.(); current.dispose() }
  }, [client])
  const run = useCallback<MCPRequestGate["run"]>((send, write) => gate.current?.run(send, write) ?? Promise.resolve(undefined), [])
  const secrets = useCallback((clear: (() => void) | undefined) => { clearSecrets.current = clear }, [])
  const blocker = useBlocker({ shouldBlockFn: () => dirty || request.pending, withResolver: true, enableBeforeUnload: dirty || request.pending })
  useEffect(() => () => { blocker.reset?.() }, [blocker.reset])
  useEffect(() => {
    const track = (event: FocusEvent) => { const target = event.target; lastFocus.current = target instanceof HTMLElement && (workspace.current?.contains(target) || editor.current?.contains(target)) ? target : null }
    document.addEventListener("focusin", track)
    return () => document.removeEventListener("focusin", track)
  }, [])
  useLayoutEffect(() => {
    const previous = lastFocus.current, active = document.activeElement
    if (previous && (!previous.isConnected || previous.matches(":disabled")) && (active === document.body || active === previous)) (action ? title.current : heading.current)?.focus()
  })
  const query = useQuery({ queryKey: ["mcp-configuration-assets", owner, client], retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, queryFn: async ({ signal }) => {
    try {
      const values = (await client.listAssets({}, { signal })).assets.map(requireMCPAsset)
      if (values.length > 250 || new Set(values.map(asset => asset.id)).size !== values.length) throw new ConnectError("", Code.DataLoss)
      return values
    } catch (error) { throw new ConnectError("", ConnectError.from(error).code) }
  } })
  const invalidate = useCallback(() => { void queryClient.invalidateQueries({ queryKey: ["mcp-configuration-assets", owner] }) }, [queryClient, owner])
  function close() {
    if (dirty || request.pending) { discardTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setConfirm("discard"); return }
    clearSecrets.current?.(); gate.current?.cancel(); setAction(null)
  }
  function done(result: string) {
    clearSecrets.current?.(); setNotice(result); setDirty(false); setAction(null); blocker.reset?.()
    if (result === "deleted") trigger.current = null
    invalidate()
  }
  function open(next: Action, source: HTMLElement) {
    if (query.isFetching || query.isError || request.pending || request.blocked && next.kind !== "metadata") return
    trigger.current = source; setNotice(""); setDirty(false); setAction(next)
  }
  async function confirmDiscard() {
    const mode = confirm
    clearSecrets.current?.(); gate.current?.cancel(); setDirty(false); setConfirm(null); setAction(null)
    if (mode !== "readback") { blocker.proceed?.(); return }
    blocker.reset?.()
    const result = await gate.current?.readback(async signal => {
      const assets = (await client.listAssets({}, { signal })).assets.map(requireMCPAsset)
      if (assets.length > 250 || new Set(assets.map(asset => asset.id)).size !== assets.length) throw new ConnectError("", Code.DataLoss)
      if (action && action.kind !== "create") {
        try {
          const current = requireMCPAsset((await client.getAsset({ id: action.asset.id }, { signal })).asset)
          if (current.id !== action.asset.id) throw new ConnectError("", Code.DataLoss)
        } catch (error) { if (ConnectError.from(error).code !== Code.NotFound) throw error }
      }
      return assets
    })
    if (result) { queryClient.setQueryData(["mcp-configuration-assets", owner, client], result); setNotice("reconciled") }
  }
  function restoreFocus() {
    const active = document.activeElement
    if (active instanceof HTMLElement && active !== document.body && active.isConnected && !editor.current?.contains(active) && !active.closest(".mcp-editor")) return
    if (trigger.current?.isConnected && !trigger.current.matches(":disabled")) trigger.current.focus()
    else heading.current?.focus()
  }
  const failure = <>{request.failure !== undefined && <p role="alert">{t(request.failure === Code.Aborted ? "conflict" : "error")}</p>}{request.uncertain && <p role="alert">{t("unknown")}</p>}{request.blocked && <><p role="status">{t("blocked")}</p><Button type="button" variant="outline" disabled={request.pending} onClick={() => setConfirm("readback")}>{t("readback")}</Button></>}{request.pending && <><p role="status">{t("pending")}</p><Button type="button" variant="outline" onClick={() => { clearSecrets.current?.(); gate.current?.cancel(); setSecretEpoch(value => value + 1) }}>{t("cancel")}</Button></>}</>
  return <div ref={workspace} className="mcp-workspace">
    <Link to="/configuration" className="text-primary underline underline-offset-4">{t("back")}</Link>
    <div className="page-heading mt-5"><div><h1 ref={heading} tabIndex={-1} data-configuration-heading>{t("title")}</h1><p>{t("scope")}</p></div><Button disabled={query.isFetching || query.isError || request.blocked || request.pending || action !== null} onClick={event => open({ kind: "create" }, event.currentTarget)}><Plus size={17} aria-hidden="true" />{t("create")}</Button></div>
    <p className="mcp-scope mb-5">{t("privacy")}</p>
    <div className="mcp-status mb-5" role="status" aria-live="polite">{notice && t(notice)}</div>
    {!action && <div className="mb-5 space-y-3">{failure}</div>}
    <ConfigurationOrderControl owner={owner} client={configuration} assets={query.data ?? []} disabled={action !== null || request.pending || request.blocked || query.isFetching || query.isError} invalidate={invalidate} />
    <section aria-labelledby="mcp-assets-title" aria-busy={query.isFetching}><div className="mcp-actions my-5"><h2 id="mcp-assets-title" className="mr-auto font-semibold">{t("list")}</h2><Button variant="outline" disabled={query.isFetching || request.pending || action !== null} onClick={() => { if (request.blocked) setConfirm("readback"); else void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t("refresh")}</Button></div>
      {query.isPending ? <p role="status">{t("loading")}</p> : query.isError ? <p role="alert">{t("error")}</p> : !query.data.length ? <p className="mcp-scope">{t("empty")}</p> : <ul className="mcp-list">{query.data.map(asset => <li className="mcp-asset" key={asset.id} data-mcp-asset={asset.id}><h3 className="flex items-start gap-3 font-semibold"><Workflow size={18} className="mt-1 shrink-0 text-primary" aria-hidden="true" />{asset.name}</h3><dl><div><dt>{t("identity")}</dt><dd>{asset.id}</dd></div><div><dt>{t("revision")}</dt><dd>{asset.agentId} · schema {formatNumber(asset.schemaVersion)} · {formatNumber(asset.revision)}</dd></div></dl>{!supportsMCP(asset) && <p className="mcp-help">{t("readonly")}</p>}<div className="mcp-actions mt-4"><Button variant="outline" disabled={request.pending || action !== null} onClick={event => open({ kind: "metadata", asset }, event.currentTarget)}>{t("metadata")}</Button>{supportsMCP(asset) && <>{(["edit", "copy", "delete"] as const).map(kind => <Button key={kind} variant={kind === "delete" ? "ghost" : "outline"} disabled={request.pending || request.blocked || action !== null || asset.revision === 9223372036854775807n} onClick={event => open({ kind, asset }, event.currentTarget)}>{t(kind)}</Button>)}</>}</div></li>)}</ul>}
    </section>
    <Dialog open={action !== null} onOpenChange={next => { if (!next) close() }}><DialogContent ref={editor} className="mcp-editor sm:max-w-3xl" onOpenAutoFocus={event => { event.preventDefault(); title.current?.focus() }} onCloseAutoFocus={event => { event.preventDefault(); restoreFocus() }}><DialogTitle ref={title} tabIndex={-1}>{action && t(action.kind)}</DialogTitle><DialogDescription>{t("editorScope")}</DialogDescription><div className="my-4"><Preferences /></div><div className="space-y-3">{failure}</div>{action && <Editor key={action.kind === "create" ? "create" : `${action.kind}:${action.asset.id}`} action={action} client={client} run={run} state={request} secretEpoch={secretEpoch} onDone={done} onDirtyChange={setDirty} onSecrets={secrets} title={title} />}</DialogContent></Dialog>
    <Dialog open={confirm !== null || blocker.status === "blocked"} onOpenChange={next => { if (!next) { setConfirm(null); blocker.reset?.() } }}><DialogContent className="mcp-editor" onOpenAutoFocus={event => { if (!discardTrigger.current) discardTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; event.preventDefault(); cancel.current?.focus() }} onCloseAutoFocus={event => { event.preventDefault(); if (editor.current?.isConnected) { const target = discardTrigger.current; if (target?.isConnected && editor.current.contains(target) && !target.matches(":disabled")) target.focus(); else title.current?.focus() } else restoreFocus(); discardTrigger.current = null }}><DialogTitle>{t(confirm === "readback" ? "readback" : "discardTitle")}</DialogTitle><DialogDescription>{t(confirm === "readback" ? "readbackHelp" : "discardHelp")}</DialogDescription><div className="mcp-actions mt-5"><Button ref={cancel} variant="outline" onClick={() => { setConfirm(null); blocker.reset?.() }}>{t("continue")}</Button><Button onClick={() => void confirmDiscard()}>{t(confirm === "readback" ? "readback" : "discard")}</Button></div></DialogContent></Dialog>
  </div>
}

export function MCPConfigurationPage() {
  const { session, mcpConfiguration } = useApi()
  const identity = useRef({ client: mcpConfiguration, generation: 0 })
  if (identity.current.client !== mcpConfiguration) identity.current = { client: mcpConfiguration, generation: identity.current.generation + 1 }
  return session ? <MCPWorkspace key={`${window.location.origin}:${session.userId}:${session.token}:${identity.current.generation}`} owner={session.userId} client={mcpConfiguration} /> : null
}
