import { useCallback, useEffect, useId, useLayoutEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useBlocker } from "@tanstack/react-router"
import { Code, ConnectError, type Client } from "@connectrpc/connect"
import { Plus, RefreshCw, Settings2 } from "lucide-react"
import { useApi } from "./api"
import { t, formatDate, formatNumber } from "./i18n"
import { AssetKind, ConfigurationService, CredentialReadiness, type DraftView } from "./gen/skillhub/configuration/v1/configuration_pb"
import { ConfigurationForm, type ConfigurationDraft, type SecretChanges } from "./configuration-form"
import { configurationError, metadata, requireClaudeView, supportsClaude, useConfigurationRequest, type AssetMetadata, type ClaudeView } from "./configuration-state"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"

type ConfigurationClient = Client<typeof ConfigurationService>
type Action = { kind: "create" } | { kind: "edit"; asset: AssetMetadata } | { kind: "copy"; asset: AssetMetadata } | { kind: "delete"; asset: AssetMetadata }
type Result = { kind: "saved" | "copied" | "deleted"; readiness?: CredentialReadiness }
const titles = { create: "新建 Claude 配置资产", edit: "编辑 Claude 配置资产", copy: "复制配置资产", delete: "删除配置资产" }
const kindLabels: Record<number, string> = { [AssetKind.PROVIDER]: "Provider 配置", [AssetKind.MCP]: "MCP connection 配置", [AssetKind.PROMPT]: "提示词配置", [AssetKind.AGENT_SETTINGS]: "Agent 全局配置", [AssetKind.WORKSPACE]: "工作区配置" }

function RequestStatus({ request }: { request: ReturnType<typeof useConfigurationRequest> }) {
  return <>{request.failure !== undefined && <p role="alert" className="mt-4 text-sm text-destructive">{t(configurationError(request.failure))}</p>}{request.uncertain && <p role="alert" className="mt-4 text-sm leading-6">{t("配置写入结果待确认。请关闭并刷新列表后重新读取；不会自动重试，已提交的保存或删除不会撤销。")}</p>}{request.blocked && <p className="mt-3 text-sm text-muted-foreground">{t("当前操作已停止。关闭并刷新状态后再操作，不会使用旧版本继续提交。")}</p>}</>
}

function writeView(view?: DraftView) {
  try { return requireClaudeView(view) } catch { throw new ConnectError("", Code.DataLoss) }
}

function ConfigurationEditor({ client, action, invalidate, onDone, onDirtyChange }: { client: ConfigurationClient; action: Action & { kind: "create" | "edit" }; invalidate: () => void; onDone: (result: Result) => void; onDirtyChange: (dirty: boolean) => void }) {
  const { i18n } = useTranslation()
  const request = useConfigurationRequest(invalidate)
  const [view, setView] = useState<ClaudeView>()
  const [attempt, setAttempt] = useState(0)
  const id = action.kind === "edit" ? action.asset.id : undefined
  const { run } = request
  useEffect(() => {
    if (!id || view) return
    void run(async (signal) => {
      const response = await client.readDraft({ id }, { signal })
      const loaded = requireClaudeView(response.view)
      if (loaded.asset.id !== id) throw new ConnectError("", Code.DataLoss)
      return loaded
    }).then((loaded) => { if (loaded) setView(loaded) })
  }, [client, id, run, attempt, i18n.resolvedLanguage, view])

  async function save(draft: ConfigurationDraft, secrets: SecretChanges) {
    const result = await run(async (signal) => {
      const response = view ? await client.updateAsset({ id: view.asset.id, expectedRevision: view.asset.revision, draft, secrets }, { signal }) : await client.createAsset({ draft, secrets }, { signal })
      return writeView(response.view)
    }, true)
    if (!result) return false
    onDone({ kind: "saved", readiness: result.readiness })
    return true
  }

  return <><RequestStatus request={request} />{action.kind === "edit" && !view ? <div className="mt-5 space-y-3"><p role="status" className="text-sm text-muted-foreground">{request.pending ? t("正在读取脱敏编辑草稿…") : t("尚未读取可编辑草稿，不会显示原始配置或凭据。")}</p>{!request.pending && <Button variant="outline" onClick={() => setAttempt((current) => current + 1)}>{t("重新读取配置草稿")}</Button>}</div> : <>{view && <p className="mt-4 text-sm text-muted-foreground">{t("编辑基于版本 {{revision}}；保存时仍需通过 CAS 校验。", { revision: formatNumber(view.asset.revision) })}</p>}<ConfigurationForm key={view ? `${view.asset.id}:${view.asset.revision}` : "new"} original={view} pending={request.pending} blocked={request.blocked} onSubmit={save} onDirtyChange={onDirtyChange} /></>}</>
}

function ConfigurationMetadataOperation({ client, action, invalidate, onDone, onDirtyChange }: { client: ConfigurationClient; action: Action & { kind: "copy" | "delete" }; invalidate: () => void; onDone: (result: Result) => void; onDirtyChange: (dirty: boolean) => void }) {
  useTranslation()
  const request = useConfigurationRequest(invalidate)
  const id = useId()
  const [name, setName] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [invalid, setInvalid] = useState(false)
  useLayoutEffect(() => { onDirtyChange(action.kind === "copy" && name !== "") }, [action.kind, name, onDirtyChange])
  const blocked = request.pending || request.blocked
  async function submit() {
    if (blocked) return
    if (action.kind === "copy") {
      const trimmed = name.trim()
      if (!trimmed || new TextEncoder().encode(trimmed).length > 128 || /\p{Cc}/u.test(trimmed)) { setInvalid(true); return }
      const result = await request.run(async (signal) => {
        const response = await client.copyAsset({ sourceId: action.asset.id, expectedSourceRevision: action.asset.revision, name: trimmed }, { signal })
        return writeView(response.view)
      }, true)
      if (result) onDone({ kind: "copied", readiness: result.readiness })
    } else if (confirmed) {
      const result = await request.run((signal) => client.deleteAsset({ id: action.asset.id, expectedRevision: action.asset.revision }, { signal }), true)
      if (result) onDone({ kind: "deleted" })
    }
  }
  return <form className="mt-5 min-w-0 space-y-4" autoComplete="off" onSubmit={(event) => { event.preventDefault(); void submit() }} noValidate aria-busy={request.pending}>
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
  const shouldBlockFn = useCallback(() => dirty, [dirty])
  const blocker = useBlocker({ shouldBlockFn, withResolver: true, enableBeforeUnload: dirty })
  useEffect(() => () => { blocker.reset?.() }, [blocker.reset])
  useEffect(() => {
    if (action === null) blocker.reset?.()
  }, [action, blocker.reset])
  const cancelDiscard = () => { setDiscardOpen(false); blocker.reset?.() }
  const discard = () => { setDirty(false); setDiscardOpen(false); setAction(null); blocker.proceed?.() }
  const close = () => { if (dirty) setDiscardOpen(true); else setAction(null) }
  const query = useQuery({
    queryKey: ["configuration-assets", owner, i18n.resolvedLanguage], retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => {
      try { return (await client.listAssets({}, { signal })).assets.map(metadata) } catch (error) { throw new ConnectError("", ConnectError.from(error).code) }
    },
  })
  const invalidate = useCallback(() => { void queryClient.invalidateQueries({ queryKey: ["configuration-assets", owner] }) }, [queryClient, owner])
  const open = (next: Action) => { setResult(undefined); setDirty(false); setAction(next) }
  const done = (next: Result) => { setResult(next); setDirty(false); setDiscardOpen(false); setAction(null) }
  const date = (seconds: bigint) => {
    const milliseconds = Number(seconds) * 1000
    return Number.isFinite(milliseconds) && !Number.isNaN(new Date(milliseconds).getTime()) ? formatDate(milliseconds) : t("时间不可用")
  }
  return <div className="min-w-0">
    <div className="page-heading flex-wrap"><div className="min-w-0"><h1>{t("AI 配置")}</h1><p>{t("管理私有配置资产。保存不选择 provider，不修改设备文件，也不表示已经应用。")}</p></div><Button onClick={() => open({ kind: "create" })}><Plus size={17} aria-hidden="true" />{t("新建 Claude 配置资产")}</Button></div>
    <section className="min-w-0 space-y-3 rounded-xl border border-border bg-card p-4 sm:p-6" aria-label={t("配置支持范围")}><div className="flex items-center gap-2 font-medium"><Settings2 size={18} aria-hidden="true" /><span>{t("当前切片：Claude Code provider 资产")}</span></div><p className="text-sm leading-6 text-muted-foreground">{t("完整目标仍覆盖 14 类 agent。本页仅接通 Claude 专属编辑；其它 agent、类型或 schema 可查看元数据并按版本删除，不提供通用 JSON 编辑或应用。")}</p><p className="text-sm leading-6 text-muted-foreground">{t("尚未接通 provider 切换、设备应用、来源配置迁移、MCP connections、prompts 或 OpenClaw 专属面板；它们不是已完成能力。")}</p></section>
    <div role="status" aria-live="polite" className="my-4 min-h-6 text-sm leading-6">{result && <><p>{t(result.kind === "saved" ? "配置资产已保存，未应用到设备。" : result.kind === "copied" ? "已复制非秘密配置，未选择或应用。" : "服务端配置资产已删除，设备状态未更改。")}</p>{result.readiness !== undefined && <p className="text-muted-foreground">{t(result.readiness === CredentialReadiness.NEEDS_CREDENTIAL ? "此资产仍需补充凭据；没有被标记为可应用。" : "凭据结构完整，但未验证连通性或本地应用能力。")}</p>}</>}</div>
    <section className="min-w-0" aria-labelledby="configuration-assets-title" aria-busy={query.isFetching}><div className="mb-4 flex flex-wrap items-center justify-between gap-3"><h2 id="configuration-assets-title" className="font-semibold">{t("配置资产列表")}</h2><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t("刷新配置元数据")}</Button></div><p className="mb-4 text-sm text-muted-foreground">{t("浏览列表只读取元数据；点击编辑后才请求脱敏草稿，已保存凭据从不返回原文。")}</p>
      {query.isPending ? <p role="status" className="py-6">{t("正在读取配置元数据…")}</p> : query.isError ? <p role="alert" className="text-sm text-destructive">{t(configurationError(ConnectError.from(query.error).code))}</p> : query.data.length === 0 ? <div className="empty-state rounded-xl border border-border"><h3>{t("还没有配置资产")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("从 Claude 专属表单创建私有资产；不会读取当前设备配置。")}</p></div> : <ul className="space-y-3">{query.data.map((asset) => <li key={asset.id} className="min-w-0 rounded-xl border border-border bg-card p-4 sm:p-5"><h3 className="break-words font-semibold [overflow-wrap:anywhere]">{asset.name}</h3><dl className="mt-3 grid min-w-0 gap-3 text-sm sm:grid-cols-2"><div><dt className="text-muted-foreground">{t("目标 agent")}</dt><dd className="mt-1 break-all font-mono">{asset.agentId}</dd></div><div><dt className="text-muted-foreground">{t("资产类型")}</dt><dd className="mt-1">{kindLabels[asset.kind] ? t(kindLabels[asset.kind]) : t("未知配置类型")}</dd></div><div><dt className="text-muted-foreground">{t("Schema / revision")}</dt><dd className="mt-1 break-all font-mono">{formatNumber(asset.schemaVersion)} / {formatNumber(asset.revision)}</dd></div><div><dt className="text-muted-foreground">{t("配置更新时间")}</dt><dd className="mt-1">{date(asset.updatedAtUnix)}</dd></div></dl>{!supportsClaude(asset) && <p className="mt-3 text-sm text-muted-foreground">{t("此资产仅支持元数据浏览及 CAS 删除；未读取其 payload，不代表支持该 agent 的编辑或应用。")}</p>}<div className="mt-4 flex flex-wrap gap-3">{supportsClaude(asset) && <><Button variant="outline" onClick={() => open({ kind: "edit", asset })}>{t("编辑配置")}</Button><Button variant="outline" onClick={() => open({ kind: "copy", asset })}>{t("复制（不带凭据）")}</Button></>}<Button variant="ghost" onClick={() => open({ kind: "delete", asset })}>{t("删除配置资产")}</Button></div></li>)}</ul>}
    </section>
    <Dialog open={action !== null} onOpenChange={(opened) => { if (!opened) close() }}><DialogContent className="min-w-0 sm:max-w-2xl"><DialogTitle>{action && t(titles[action.kind])}</DialogTitle><DialogDescription>{t("仅操作此账号的服务端资产。离开未保存草稿前需确认放弃；确认后清除表单并停止等待，不撤销已提交的修改。语言切换保留非秘密草稿，清除新凭据并取消在途请求。")}</DialogDescription>{action && (action.kind === "create" || action.kind === "edit" ? <ConfigurationEditor key={action.kind === "create" ? "create" : action.asset.id} client={client} action={action} invalidate={invalidate} onDone={done} onDirtyChange={setDirty} /> : <ConfigurationMetadataOperation key={`${action.kind}:${action.asset.id}`} client={client} action={action} invalidate={invalidate} onDone={done} onDirtyChange={setDirty} />)}</DialogContent></Dialog>
    <Dialog open={discardOpen || blocker.status === "blocked"} onOpenChange={(opened) => { if (!opened) cancelDiscard() }}><DialogContent><DialogTitle>{t("放弃未保存的配置更改？")}</DialogTitle><DialogDescription>{t("取消将保留当前草稿，包括尚未提交的新凭据。确认放弃将清除草稿和新凭据并离开，但不会撤销已提交的服务端修改。")}</DialogDescription><div className="mt-5 flex flex-wrap justify-end gap-3"><Button variant="outline" autoFocus onClick={cancelDiscard}>{t("继续编辑")}</Button><Button variant="outline" className="text-destructive" onClick={discard}>{t("放弃更改")}</Button></div></DialogContent></Dialog>
  </div>
}

export function ConfigurationPage() {
  const { session, configuration } = useApi()
  return session ? <ConfigurationSession key={session.token} owner={session.userId} client={configuration} /> : null
}
