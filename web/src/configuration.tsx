import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useBlocker } from "@tanstack/react-router"
import { Code, ConnectError, type Client } from "@connectrpc/connect"
import { create, equals } from "@bufbuild/protobuf"
import { Plus, RefreshCw, Settings2 } from "lucide-react"
import { useApi } from "./api"
import { t, formatDate, formatNumber, savePreference } from "./i18n"
import { AssetKind, ConfigurationService, CredentialReadiness, GeminiAuthMode, GeminiProviderSchema, type DraftView } from "./gen/skillhub/configuration/v1/configuration_pb"
import { ConfigurationForm, type ConfigurationDraft, type SecretChanges } from "./configuration-form"
import { ConfigurationCodexForm } from "./configuration-codex-form"
import { supportsCodex } from "./configuration-codex-state"
import { ConfigurationGeminiForm } from "./configuration-gemini-form"
import { supportsGemini, validGeminiUnicode } from "./configuration-gemini-state"
import { configurationError, metadata, requireConfigurationView, supportsClaude, useConfigurationRequest, type AssetMetadata, type ConfigurationView } from "./configuration-state"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"

type ConfigurationClient = Client<typeof ConfigurationService>
type Action = { kind: "create" } | { kind: "edit"; asset: AssetMetadata } | { kind: "copy"; asset: AssetMetadata } | { kind: "delete"; asset: AssetMetadata }
type Result = { kind: "saved" | "copied" | "deleted"; readiness?: CredentialReadiness }
const titles = { create: "新建配置资产", edit: "编辑配置资产", copy: "复制配置资产", delete: "删除配置资产" }
const kindLabels: Record<number, string> = { [AssetKind.PROVIDER]: "Provider 配置", [AssetKind.MCP]: "MCP connection 配置", [AssetKind.PROMPT]: "提示词配置", [AssetKind.AGENT_SETTINGS]: "Agent 全局配置", [AssetKind.WORKSPACE]: "工作区配置" }

function ConfigurationLanguage() {
  const { i18n } = useTranslation()
  const id = useId()
  return <div className="mt-4 space-y-2"><label htmlFor={id}>{t("界面语言")}</label><Select value={i18n.resolvedLanguage ?? "zh-CN"} onValueChange={(value) => { if (value === "zh-CN" || value === "en") { savePreference("locale", value); void i18n.changeLanguage(value) } }}><SelectTrigger id={id} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="zh-CN"><span lang="zh-CN">简体中文</span></SelectItem><SelectItem value="en"><span lang="en">English</span></SelectItem></SelectContent></Select></div>
}

function RequestStatus({ request }: { request: ReturnType<typeof useConfigurationRequest> }) {
  return <>{request.failure !== undefined && <p role="alert" className="mt-4 text-sm text-destructive">{t(configurationError(request.failure))}</p>}{request.uncertain && <p role="alert" className="mt-4 text-sm leading-6">{t("配置写入结果待确认。请关闭并刷新列表后重新读取；不会自动重试，已提交的保存或删除不会撤销。")}</p>}{request.blocked && <p className="mt-3 text-sm text-muted-foreground">{t("当前操作已停止。关闭并刷新状态后再操作，不会使用旧版本继续提交。")}</p>}</>
}

function writeView(view?: DraftView) {
  try { return requireConfigurationView(view) } catch { throw new ConnectError("", Code.DataLoss) }
}

function ConfigurationEditor({ client, action, invalidate, onDone, onDirtyChange, onReload }: { client: ConfigurationClient; action: Action & { kind: "create" | "edit" }; invalidate: () => void; onDone: (result: Result) => void; onDirtyChange: (dirty: boolean) => void; onReload: () => void }) {
  const { i18n } = useTranslation()
  const request = useConfigurationRequest(invalidate)
  const [view, setView] = useState<ConfigurationView>()
  const [target, setTarget] = useState<"claude" | "codex" | "gemini">()
  const targetId = useId()
  const [reloadOpen, setReloadOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const id = action.kind === "edit" ? action.asset.id : undefined
  const { run } = request
  useEffect(() => {
    if (!id || view) return
    void run(async (signal) => {
      const response = await client.readDraft({ id }, { signal })
      const loaded = requireConfigurationView(response.view)
      if (loaded.view.asset.id !== id || action.kind !== "edit" || loaded.view.asset.agentId !== action.asset.agentId || loaded.view.asset.kind !== action.asset.kind || loaded.view.asset.schemaVersion !== action.asset.schemaVersion) throw new ConnectError("", Code.DataLoss)
      return loaded
    }).then((loaded) => { if (loaded) setView(loaded) })
  }, [client, id, run, attempt, i18n.resolvedLanguage, view])

  async function save(draft: ConfigurationDraft, secrets: SecretChanges) {
    const geminiInput = draft.payload?.case === "geminiProvider" ? create(GeminiProviderSchema, draft.payload.value) : undefined
    const geminiPresent = secrets[0]?.action?.case === "replace" || secrets[0]?.action?.case === "keep" && view?.kind === "gemini" && view.view.present
    const result = await run(async (signal) => {
      if (view && view.kind !== "claude" && view.view.mode === "readonly") throw new ConnectError("", Code.FailedPrecondition)
      const response = view ? await client.updateAsset({ id: view.view.asset.id, expectedRevision: view.view.asset.revision, draft, secrets }, { signal }) : await client.createAsset({ draft, secrets }, { signal })
      const saved = writeView(response.view)
      if (saved.kind !== (view?.kind ?? target) || view && (saved.view.asset.id !== view.view.asset.id || saved.view.asset.revision !== view.view.asset.revision + 1n) || !view && saved.view.asset.revision !== 1n || saved.kind !== "claude" && saved.view.mode !== "editable") throw new ConnectError("", Code.DataLoss)
      if (saved.kind === "gemini" && (!geminiInput || !equals(GeminiProviderSchema, saved.view.provider, geminiInput) || saved.view.asset.name !== draft.name?.trim() || saved.view.present !== geminiPresent)) throw new ConnectError("", Code.DataLoss)
      return saved
    }, true)
    if (!result) return false
    onDone({ kind: "saved", readiness: result.view.readiness })
    return true
  }

  const selected = view?.kind ?? target
  return <><RequestStatus request={request} />{view && view.kind !== "claude" && request.blocked && action.kind === "edit" && <><Button type="button" variant="outline" className="mt-3" onClick={() => setReloadOpen(true)}>{t("放弃草稿并重新读取")}</Button><Dialog open={reloadOpen} onOpenChange={setReloadOpen}><DialogContent><DialogTitle>{t("确认放弃此非秘密草稿并重新读取最新版本？")}</DialogTitle><DialogDescription>{t("仅在明确确认后刷新元数据并重新读取草稿；不合并、不重放旧写入，也不恢复新凭据。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button type="button" variant="outline" autoFocus onClick={() => setReloadOpen(false)}>{t("继续编辑")}</Button><Button type="button" onClick={onReload}>{t("放弃草稿并重新读取")}</Button></div></DialogContent></Dialog></>}{action.kind === "create" && <div className="mt-5 space-y-2"><label htmlFor={targetId}>{t("创建目标")}</label><Select value={target ?? "unselected"} disabled={target !== undefined || request.pending || request.blocked} onValueChange={(value) => { if (value === "claude" || value === "codex" || value === "gemini") setTarget(value) }}><SelectTrigger id={targetId} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unselected" disabled>{t("请选择目标 agent")}</SelectItem><SelectItem value="claude">Claude</SelectItem><SelectItem value="codex">{t("Codex API provider")}</SelectItem><SelectItem value="gemini">{t("Gemini provider 资产")}</SelectItem></SelectContent></Select><p className="text-sm text-muted-foreground">{t("选择后目标固定；更换目标请关闭并重新新建，未保存更改仍需确认。")}</p></div>}{action.kind === "edit" && !view ? <div className="mt-5 space-y-3"><p role="status" className="text-sm text-muted-foreground">{request.pending ? t("正在读取脱敏编辑草稿…") : t("尚未读取可编辑草稿，不会显示原始配置或凭据。")}</p>{!request.pending && <Button variant="outline" onClick={() => setAttempt((current) => current + 1)}>{t("重新读取配置草稿")}</Button>}</div> : <>{view && <p className="mt-4 text-sm text-muted-foreground">{t("编辑基于版本 {{revision}}；保存时仍需通过 CAS 校验。", { revision: formatNumber(view.view.asset.revision) })}</p>}{selected === "claude" ? <ConfigurationForm key={view ? `${view.view.asset.id}:${view.view.asset.revision}` : "new-claude"} original={view?.kind === "claude" ? view.view : undefined} pending={request.pending} blocked={request.blocked} onSubmit={save} onDirtyChange={onDirtyChange} /> : selected === "codex" ? <ConfigurationCodexForm key={view ? `${view.view.asset.id}:${view.view.asset.revision}` : "new-codex"} original={view?.kind === "codex" ? view.view : undefined} pending={request.pending} blocked={request.blocked} onSubmit={save} onDirtyChange={onDirtyChange} /> : selected === "gemini" ? <ConfigurationGeminiForm key={view ? `${view.view.asset.id}:${view.view.asset.revision}` : "new-gemini"} original={view?.kind === "gemini" ? view.view : undefined} pending={request.pending} blocked={request.blocked} onSubmit={save} onDirtyChange={onDirtyChange} /> : null}</>}</>
}

function ConfigurationMetadataOperation({ client, action, invalidate, onDone, onDirtyChange }: { client: ConfigurationClient; action: Action & { kind: "copy" | "delete" }; invalidate: () => void; onDone: (result: Result) => void; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const request = useConfigurationRequest(invalidate)
  const id = useId()
  const [name, setName] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [invalid, setInvalid] = useState(false)
  const [copyView, setCopyView] = useState<ConfigurationView>()
  const [attempt, setAttempt] = useState(0)
  const { run } = request
  useEffect(() => {
    if (action.kind !== "copy" || !(supportsCodex(action.asset) || supportsGemini(action.asset)) || copyView) return
    void run(async (signal) => {
      const loaded = requireConfigurationView((await client.readDraft({ id: action.asset.id }, { signal })).view)
      if (loaded.kind !== (supportsGemini(action.asset) ? "gemini" : "codex") || loaded.view.asset.id !== action.asset.id || loaded.view.asset.revision !== action.asset.revision) throw new ConnectError("", Code.Aborted)
      return loaded
    }).then((loaded) => { if (loaded) setCopyView(loaded) })
  }, [action, client, copyView, run, attempt, i18n.resolvedLanguage])
  useLayoutEffect(() => { onDirtyChange(request.pending || action.kind === "copy" && name !== "") }, [action.kind, name, request.pending, onDirtyChange])
  const copyReadonly = copyView && copyView.kind !== "claude" && copyView.view.mode === "readonly"
  const guardedCopy = supportsCodex(action.asset) || supportsGemini(action.asset)
  const blocked = request.pending || request.blocked || action.kind === "copy" && guardedCopy && (!copyView || copyReadonly)
  async function submit() {
    if (blocked) return
    if (action.kind === "copy") {
      const trimmed = name.trim()
      if (!trimmed || new TextEncoder().encode(trimmed).length > 128 || /\p{Cc}/u.test(trimmed) || supportsGemini(action.asset) && !validGeminiUnicode(name)) { setInvalid(true); return }
      const result = await request.run(async (signal) => {
        const response = await client.copyAsset({ sourceId: action.asset.id, expectedSourceRevision: action.asset.revision, name: trimmed }, { signal })
        const copied = writeView(response.view)
        if (copied.view.asset.agentId !== action.asset.agentId || copied.view.asset.id === action.asset.id || copied.view.asset.revision !== 1n || copied.kind === "codex" && (copied.view.mode !== "editable" || copied.view.present || copied.view.readiness !== CredentialReadiness.NEEDS_CREDENTIAL)) throw new ConnectError("", Code.DataLoss)
        if (supportsGemini(action.asset) && (copyView?.kind !== "gemini" || copied.kind !== "gemini" || copied.view.mode !== "editable" || copied.view.present || copied.view.asset.name !== trimmed || !equals(GeminiProviderSchema, copied.view.provider, copyView.view.provider) || copied.view.readiness !== (copyView.view.provider.authMode === GeminiAuthMode.NATIVE_LOGIN ? CredentialReadiness.COMPLETE : CredentialReadiness.NEEDS_CREDENTIAL))) throw new ConnectError("", Code.DataLoss)
        return copied
      }, true)
      if (result) onDone({ kind: "copied", readiness: result.view.readiness })
    } else if (confirmed) {
      const result = await request.run((signal) => client.deleteAsset({ id: action.asset.id, expectedRevision: action.asset.revision }, { signal }), true)
      if (result) onDone({ kind: "deleted" })
    }
  }
  return <form className="mt-5 min-w-0 space-y-4" autoComplete="off" onSubmit={(event) => { event.preventDefault(); void submit() }} noValidate aria-busy={request.pending}>
    {action.kind === "copy" && guardedCopy && !copyView && <div className="space-y-3"><p role="status" className="text-sm">{t("复制前读取源资产，确认类型、版本及未知字段限制。")}</p>{!request.pending && <Button type="button" variant="outline" onClick={() => setAttempt((current) => current + 1)}>{t("重新读取配置草稿")}</Button>}</div>}
    {copyReadonly && <p role="alert" className="text-sm">{t("此资产保留未知字段，仅可只读查看已知内容。保存和复制均禁用，后端也会拒绝；仍可由原账号明确确认后按版本删除。")}</p>}
    <p className="break-words text-sm font-medium [overflow-wrap:anywhere]">{action.asset.name}</p><p className="text-sm text-muted-foreground">{t("操作绑定版本 {{revision}}，冲突时不会自动重试。", { revision: formatNumber(action.asset.revision) })}</p>
    {action.kind === "copy" ? <><p className="text-sm leading-6 text-muted-foreground">{t("仅复制 typed 非秘密配置。不会复制任何凭据、选择或设备应用状态，也不会启用新资产。")}</p><label htmlFor={`${id}-name`} className="block">{t("新副本名称")}</label><Input id={`${id}-name`} value={name} disabled={blocked} autoComplete="off" onChange={(event) => { setName(event.target.value); setInvalid(false) }} aria-invalid={invalid} aria-describedby={invalid ? `${id}-error` : undefined} autoFocus />{invalid && <p id={`${id}-error`} role="alert" className="text-sm text-destructive">{t("名称须为 1–128 个 UTF-8 字节，且不含控制字符。")}</p>}</> : <><p className="text-sm leading-6 text-muted-foreground">{t("仅删除服务端配置资产，不能撤销或恢复设备上的配置。此操作不可恢复；不会删除技能。")}</p><label className="flex min-h-11 items-start gap-3 py-2" htmlFor={`${id}-confirm`}><Checkbox id={`${id}-confirm`} checked={confirmed} disabled={blocked} onCheckedChange={(checked) => setConfirmed(checked === true)} className="mt-1" /><span>{t("确认删除此配置资产及其保存的凭据。")}</span></label></>}
    <RequestStatus request={request} /><Button type="submit" variant={action.kind === "delete" ? "outline" : "default"} className={`max-w-full whitespace-normal${action.kind === "delete" ? " text-destructive" : ""}`} disabled={blocked || action.kind === "delete" && !confirmed}>{request.pending ? t("正在提交配置操作…") : action.kind === "delete" ? t("确认删除配置资产") : t("复制为无凭据资产")}</Button>
  </form>
}

function ConfigurationSession({ owner, client }: { owner: string; client: ConfigurationClient }) {
  const { i18n } = useTranslation()
  const queryClient = useQueryClient()
  const [action, setAction] = useState<Action | null>(null)
  const [result, setResult] = useState<Result>()
  const [dirty, setDirty] = useState(false)
  const [discardOpen, setDiscardOpen] = useState(false)
  const [editorVersion, setEditorVersion] = useState(0)
  const createTrigger = useRef<HTMLButtonElement>(null)
  const actionTrigger = useRef<HTMLButtonElement | null>(null)
  const editorContent = useRef<HTMLDivElement>(null)
  const discardTrigger = useRef<HTMLElement | null>(null)
  const discardCancel = useRef<HTMLButtonElement>(null)
  const restoreTrigger = () => {
    const trigger = actionTrigger.current
    if (trigger?.isConnected && !trigger.disabled) trigger.focus()
    else if (createTrigger.current?.isConnected) createTrigger.current.focus()
  }
  const shouldBlockFn = useCallback(() => dirty, [dirty])
  const blocker = useBlocker({ shouldBlockFn, withResolver: true, enableBeforeUnload: dirty })
  useEffect(() => () => { blocker.reset?.() }, [blocker.reset])
  useEffect(() => {
    if (action === null) blocker.reset?.()
  }, [action, blocker.reset])
  const cancelDiscard = () => { setDiscardOpen(false); blocker.reset?.() }
  const discard = () => { setDirty(false); setDiscardOpen(false); setAction(null); blocker.proceed?.() }
  const close = () => { if (dirty) { discardTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setDiscardOpen(true) } else setAction(null) }
  const query = useQuery({
    queryKey: ["configuration-assets", owner, i18n.resolvedLanguage], retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => {
      try { return (await client.listAssets({}, { signal })).assets.map(metadata) } catch (error) { throw new ConnectError("", ConnectError.from(error).code) }
    },
  })
  const invalidate = useCallback(() => { void queryClient.invalidateQueries({ queryKey: ["configuration-assets", owner] }) }, [queryClient, owner])
  const open = (next: Action, trigger: HTMLButtonElement) => { actionTrigger.current = trigger; setResult(undefined); setDirty(false); setAction(next) }
  const done = (next: Result) => { if (next.kind === "deleted") actionTrigger.current = createTrigger.current; setResult(next); setDirty(false); setDiscardOpen(false); setAction(null); blocker.reset?.() }
  const date = (seconds: bigint) => {
    const milliseconds = Number(seconds) * 1000
    return Number.isFinite(milliseconds) && !Number.isNaN(new Date(milliseconds).getTime()) ? formatDate(milliseconds) : t("时间不可用")
  }
  return <div className="min-w-0">
    <div className="page-heading flex-wrap"><div className="min-w-0"><h1>{t("AI 配置")}</h1><p>{t("管理私有配置资产。保存不选择 provider，不修改设备文件，也不表示已经应用。")}</p></div><Button ref={createTrigger} onClick={(event) => open({ kind: "create" }, event.currentTarget)}><Plus size={17} aria-hidden="true" />{t("新建配置资产")}</Button></div>
    <section className="min-w-0 space-y-3 rounded-xl border border-border bg-card p-4 sm:p-6" aria-label={t("配置支持范围")}><div className="flex items-center gap-2 font-medium"><Settings2 size={18} aria-hidden="true" /><span>{t("当前切片：Claude Code、Codex 与 Gemini provider 资产")}</span></div><p className="text-sm leading-6 text-muted-foreground">{t("完整目标仍覆盖 14 类 agent。本页仅接通 Claude Code、Codex 与 Gemini provider 资产的专属编辑；其它 agent、类型或 schema 可查看元数据并按版本删除，不提供通用 JSON 编辑或应用。")}</p><p className="text-sm leading-6 text-muted-foreground">{t("尚未接通 provider 切换、设备应用、来源配置迁移、MCP connections、prompts 或 OpenClaw 专属面板；它们不是已完成能力。")}</p></section>
    <div role="status" aria-live="polite" className="my-4 min-h-6 text-sm leading-6">{result && <><p>{t(result.kind === "saved" ? "配置资产已保存，未应用到设备。" : result.kind === "copied" ? "已复制非秘密配置，未选择或应用。" : "服务端配置资产已删除，设备状态未更改。")}</p>{result.readiness !== undefined && <p className="text-muted-foreground">{t(result.readiness === CredentialReadiness.NEEDS_CREDENTIAL ? "此资产仍需补充凭据；没有被标记为可应用。" : "凭据结构完整，但未验证连通性或本地应用能力。")}</p>}</>}</div>
    <section className="min-w-0" aria-labelledby="configuration-assets-title" aria-busy={query.isFetching}><div className="mb-4 flex flex-wrap items-center justify-between gap-3"><h2 id="configuration-assets-title" className="font-semibold">{t("配置资产列表")}</h2><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t("刷新配置元数据")}</Button></div><p className="mb-4 text-sm text-muted-foreground">{t("浏览列表只读取元数据；点击编辑后才请求脱敏草稿，已保存凭据从不返回原文。")}</p>
      {query.isPending ? <p role="status" className="py-6">{t("正在读取配置元数据…")}</p> : query.isError ? <p role="alert" className="text-sm text-destructive">{t(configurationError(ConnectError.from(query.error).code))}</p> : query.data.length === 0 ? <div className="empty-state rounded-xl border border-border"><h3>{t("还没有配置资产")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("从 Claude、Codex 或 Gemini 专属表单创建私有资产；不会读取当前设备配置。")}</p></div> : <ul className="space-y-3">{query.data.map((asset) => <li key={asset.id} className="min-w-0 rounded-xl border border-border bg-card p-4 sm:p-5"><h3 className="break-words font-semibold [overflow-wrap:anywhere]">{asset.name}</h3><dl className="mt-3 grid min-w-0 gap-3 text-sm sm:grid-cols-2"><div><dt className="text-muted-foreground">{t("目标 agent")}</dt><dd className="mt-1 break-all font-mono">{asset.agentId}</dd></div><div><dt className="text-muted-foreground">{t("资产类型")}</dt><dd className="mt-1">{kindLabels[asset.kind] ? t(kindLabels[asset.kind]) : t("未知配置类型")}</dd></div><div><dt className="text-muted-foreground">{t("Schema / revision")}</dt><dd className="mt-1 break-all font-mono">{formatNumber(asset.schemaVersion)} / {formatNumber(asset.revision)}</dd></div><div><dt className="text-muted-foreground">{t("配置更新时间")}</dt><dd className="mt-1">{date(asset.updatedAtUnix)}</dd></div></dl>{!(supportsClaude(asset) || supportsCodex(asset) || supportsGemini(asset)) && <p className="mt-3 text-sm text-muted-foreground">{t("此资产仅支持元数据浏览及 CAS 删除；未读取其 payload，不代表支持该 agent 的编辑或应用。")}</p>}<div className="mt-4 flex flex-wrap gap-3">{(supportsClaude(asset) || supportsCodex(asset) || supportsGemini(asset)) && <><Button variant="outline" onClick={(event) => open({ kind: "edit", asset }, event.currentTarget)}>{t("编辑配置")}</Button><Button variant="outline" onClick={(event) => open({ kind: "copy", asset }, event.currentTarget)}>{t("复制（不带凭据）")}</Button></>}<Button variant="ghost" onClick={(event) => open({ kind: "delete", asset }, event.currentTarget)}>{t("删除配置资产")}</Button></div></li>)}</ul>}
    </section>
    <Dialog open={action !== null} onOpenChange={(opened) => { if (!opened) close() }}><DialogContent ref={editorContent} className="min-w-0 sm:max-w-2xl" onCloseAutoFocus={(event) => { event.preventDefault(); restoreTrigger() }}><DialogTitle>{action && t(titles[action.kind])}</DialogTitle><DialogDescription>{t("仅操作此账号的服务端资产。离开未保存草稿前需确认放弃；确认后清除表单并停止等待，不撤销已提交的修改。语言切换保留非秘密草稿，清除新凭据并取消在途请求。")}</DialogDescription><ConfigurationLanguage />{action && (action.kind === "create" || action.kind === "edit" ? <ConfigurationEditor onReload={() => { setDirty(false); setDiscardOpen(false); blocker.reset?.(); invalidate(); setEditorVersion((current) => current + 1) }} key={`${action.kind === "create" ? "create" : action.asset.id}:${editorVersion}`} client={client} action={action} invalidate={invalidate} onDone={done} onDirtyChange={setDirty} /> : <ConfigurationMetadataOperation key={`${action.kind}:${action.asset.id}`} client={client} action={action} invalidate={invalidate} onDone={done} onDirtyChange={setDirty} />)}</DialogContent></Dialog>
    <Dialog open={discardOpen || blocker.status === "blocked"} onOpenChange={(opened) => { if (!opened) cancelDiscard() }}><DialogContent onOpenAutoFocus={(event) => { if (!discardTrigger.current) discardTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; event.preventDefault(); discardCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); const content = editorContent.current; const trigger = discardTrigger.current; discardTrigger.current = null; if (content?.isConnected) { if (trigger?.isConnected && content.contains(trigger) && !trigger.matches(":disabled")) trigger.focus(); else content.focus() } else restoreTrigger() }}><DialogTitle>{t("放弃未保存的配置更改？")}</DialogTitle><DialogDescription>{t("取消将保留当前草稿，包括尚未提交的新凭据。确认放弃将清除草稿和新凭据并离开，但不会撤销已提交的服务端修改。")}</DialogDescription><ConfigurationLanguage /><div className="mt-5 flex flex-wrap justify-end gap-3"><Button ref={discardCancel} variant="outline" onClick={cancelDiscard}>{t("继续编辑")}</Button><Button variant="outline" className="text-destructive" onClick={discard}>{t("放弃更改")}</Button></div></DialogContent></Dialog>
  </div>
}

export function ConfigurationPage() {
  const { session, configuration } = useApi()
  return session ? <ConfigurationSession key={`${session.token}:${session.userId}:${window.location.origin}`} owner={session.userId} client={configuration} /> : null
}
