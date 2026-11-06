import "./server-github-proxy-i18n"
import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { useTranslation } from "react-i18next"
import { Network, RefreshCw } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { acceptGitHubProxySettings, validGitHubProxyInput } from "./server-github-proxy-state"

type Notice = "" | "saved" | "restored" | "reloaded" | "invalid" | "conflict" | "unknown" | "denied" | "unavailable"

export function GitHubProxySettingsPanel({ owner }: { owner: string }) {
  const { t } = useTranslation("serverGitHubProxy")
  const { githubProxyManagement: client } = useApi()
  const cache = useQueryClient()
  const id = useId()
  const queryKey = ["server-github-proxy-settings", owner]
  const settings = useQuery({ queryKey, queryFn: async ({ signal }) => acceptGitHubProxySettings((await client.getGitHubProxySettings({}, { signal })).settings), retry: false, staleTime: Infinity, gcTime: 0, refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false })
  const input = useRef<HTMLInputElement>(null)
  const feedback = useRef<HTMLParagraphElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const reloadButton = useRef<HTMLButtonElement>(null)
  const restoreButton = useRef<HTMLButtonElement>(null)
  const keepEditing = useRef<HTMLButtonElement>(null)
  const active = useRef<AbortController | null>(null)
  const mounted = useRef(false)
  const confirmationTrigger = useRef<"reload" | "restore">("reload")
  const [hasInput, setHasInput] = useState(false)
  const [busy, setBusy] = useState(false)
  const [needsReload, setNeedsReload] = useState(false)
  const [notice, setNotice] = useState<Notice>("")
  const [confirmation, setConfirmation] = useState<"reload" | "restore" | null>(null)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; active.current?.abort(); if (input.current) input.current.value = "" }
  }, [client])
  useLayoutEffect(() => { if (!busy && notice) feedback.current?.focus({ preventScroll: true }) }, [busy, notice])
  function clearInput() {
    if (input.current) input.current.value = ""
    setHasInput(false)
  }
  async function save(restore: boolean) {
    if (active.current || settings.isFetching || settings.isError || !settings.data || needsReload || notice === "denied") return
    let proxyUrl = restore ? "" : input.current?.value ?? ""
    if (!restore && (!proxyUrl || !validGitHubProxyInput(proxyUrl))) { setNotice("invalid"); return }
    const expectedRevision = settings.data.revision
    const controller = new AbortController()
    active.current = controller
    setBusy(true)
    setNotice("")
    clearInput()
    try {
      await cache.cancelQueries({ queryKey, exact: true })
      const response = await client.updateGitHubProxySettings({ expectedRevision, proxyUrl }, { signal: controller.signal })
      const accepted = acceptGitHubProxySettings(response.settings, expectedRevision)
      if (accepted.configured === restore) throw new ConnectError("Invalid GitHub proxy settings response", Code.DataLoss)
      if (!mounted.current || controller.signal.aborted) return
      cache.setQueryData(queryKey, accepted)
      setNeedsReload(false)
      setNotice(restore ? "restored" : "saved")
    } catch (error) {
      if (!mounted.current || controller.signal.aborted) return
      const code = ConnectError.from(error).code
      setNeedsReload(code !== Code.InvalidArgument)
      setNotice(code === Code.Aborted ? "conflict" : code === Code.InvalidArgument ? "invalid" : code === Code.PermissionDenied || code === Code.Unauthenticated ? "denied" : "unknown")
    } finally {
      proxyUrl = ""
      if (active.current === controller) active.current = null
      if (mounted.current && !controller.signal.aborted) setBusy(false)
    }
  }
  async function reload() {
    if (active.current || settings.isFetching) return
    const controller = new AbortController()
    active.current = controller
    setBusy(true)
    setNotice("")
    clearInput()
    try {
      await cache.cancelQueries({ queryKey, exact: true })
      const accepted = acceptGitHubProxySettings((await client.getGitHubProxySettings({}, { signal: controller.signal })).settings)
      if (!mounted.current || controller.signal.aborted) return
      cache.setQueryData(queryKey, accepted)
      setNeedsReload(false)
      setNotice("reloaded")
    } catch (error) {
      if (!mounted.current || controller.signal.aborted) return
      setNeedsReload(true)
      const code = ConnectError.from(error).code
      setNotice(code === Code.PermissionDenied || code === Code.Unauthenticated ? "denied" : "unavailable")
    } finally {
      if (active.current === controller) active.current = null
      if (mounted.current && !controller.signal.aborted) setBusy(false)
    }
  }
  function confirm(action: "reload" | "restore") {
    confirmationTrigger.current = action
    setConfirmation(action)
  }
  function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); void save(false) }
  const code = settings.isError ? ConnectError.from(settings.error).code : undefined
  const authorized = !!settings.data && !settings.isError && notice !== "denied"
  const locked = busy || settings.isFetching || needsReload
  const successfulNotice = ["saved", "restored", "reloaded"].includes(notice)
  return <section className="management-card mt-6" aria-labelledby={`${id}-heading`}>
    <h2 ref={heading} id={`${id}-heading`} tabIndex={-1} className="flex items-center gap-2 text-lg font-semibold"><Network size={20} aria-hidden="true" />{t("title")}</h2>
    <p className="mt-3 max-w-3xl text-sm leading-6 text-muted-foreground">{t("scope")}</p>
    <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">{t("protocols")}</p>
    {settings.isPending && <p role="status" className="mt-4 text-sm">{t("loading")}</p>}
    {settings.isError && <p role="alert" className="mt-4 error-message">{t(code === Code.PermissionDenied ? "restricted" : code === Code.Unimplemented ? "unsupported" : "unavailable")}</p>}
    {authorized && <form onSubmit={submit} autoComplete="off" className="mt-5 max-w-2xl space-y-4" aria-busy={busy}>
      <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm leading-6"><p className="font-medium">{t("current")}</p><p className="mt-1 break-all">{settings.data!.configured ? settings.data!.endpoint : t("environment")}</p><p className="mt-2 text-muted-foreground">{t("redacted")}</p></div>
      <div className="space-y-2"><label htmlFor={`${id}-url`} className="block text-sm font-medium">{t("url")}</label><Input ref={input} id={`${id}-url`} type="password" autoComplete="new-password" autoCapitalize="none" spellCheck={false} maxLength={4096} disabled={locked} aria-invalid={notice === "invalid" || undefined} aria-describedby={`${id}-help${notice ? ` ${id}-feedback` : ""}`} onInput={() => { setHasInput(!!input.current?.value); if (notice === "invalid") setNotice("") }} /><p id={`${id}-help`} className="text-sm leading-6 text-muted-foreground">{t("inputHelp")}</p></div>
      <div className="management-actions"><Button type="submit" disabled={locked || !hasInput}>{t(busy ? "working" : "save")}</Button><Button ref={restoreButton} variant="outline" disabled={locked || !settings.data!.configured} onClick={() => confirm("restore")}>{t("restore")}</Button></div>
    </form>}
    {notice && <p id={`${id}-feedback`} ref={feedback} tabIndex={-1} role={successfulNotice ? "status" : "alert"} className={`mt-4 text-sm leading-6 ${successfulNotice ? "" : "error-message"}`}>{t(notice)}</p>}
    {needsReload && <p className="mt-3 text-sm leading-6 text-muted-foreground">{t("noReplay")}</p>}
    <Button ref={reloadButton} className="mt-4" variant="outline" disabled={busy || settings.isFetching} onClick={() => { if (hasInput) confirm("reload"); else void reload() }}><RefreshCw size={16} aria-hidden="true" />{t(busy ? "working" : "reload")}</Button>
    <Dialog open={confirmation !== null} onOpenChange={(open) => { if (!open) setConfirmation(null) }}><DialogContent className="management-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); keepEditing.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); const trigger = confirmationTrigger.current === "reload" ? reloadButton.current : restoreButton.current; if (trigger && !trigger.disabled) trigger.focus(); else heading.current?.focus() }}><DialogTitle>{t(confirmation === "restore" ? "restoreTitle" : "discardTitle")}</DialogTitle><DialogDescription>{t(confirmation === "restore" ? "restoreHelp" : "discardHelp")}</DialogDescription><div className="management-actions mt-5"><Button ref={keepEditing} variant="outline" onClick={() => setConfirmation(null)}>{t("cancel")}</Button><Button onClick={() => { const action = confirmation; setConfirmation(null); if (action === "restore") void save(true); else void reload() }}>{t(confirmation === "restore" ? "restore" : "reload")}</Button></div></DialogContent></Dialog>
  </section>
}
