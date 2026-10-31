import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { CircleAlert, ExternalLink, FolderGit2, GitFork, LockKeyhole, RefreshCw, Search, ShieldCheck } from "lucide-react"
import { errorMessage, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { Checkbox } from "./components/ui/checkbox"
import { DeviceFlow } from "./device-auth"
import "./sources.css"

function useCatalog() {
  const { github, session } = useApi()
  const key = ["github-catalog", session?.userId]
  const query = useQuery({ queryKey: key, refetchInterval: 30_000, queryFn: async ({ signal }) => {
    const result = await github.getCatalog({}, { signal })
    if (!result.catalog) throw new ConnectError("catalog unavailable", Code.Unavailable)
    return result.catalog
  } })
  const [, wakeAtExpiry] = useState(0)
  const deadline = query.data?.connected ? Number(query.data.validUntilUnix) * 1000 : 0
  useEffect(() => {
    const remaining = deadline - Date.now()
    if (remaining <= 0) return
    const timeout = window.setTimeout(() => wakeAtExpiry((value) => value + 1), remaining)
    return () => window.clearTimeout(timeout)
  }, [deadline])
  const expired = !!query.data?.connected && deadline <= Date.now()
  return { ...query, key, data: expired ? undefined : query.data, isError: query.isError || expired, error: expired ? new ConnectError("catalog expired", Code.FailedPrecondition) : query.error }
}

function ErrorNotice({ error, retry }: { error: unknown; retry?: () => void }) {
  useTranslation()
  return <div className="error-notice" role="alert"><CircleAlert size={18} aria-hidden="true" /><p className="flex-1">{errorMessage(error)}</p>{retry && <Button variant="outline" onClick={retry}>{t("重试")}</Button>}</div>
}

function SourceForm({ generation = "", deviceEnabled = false, onConnected }: { generation?: string; deviceEnabled?: boolean; onConnected: () => void }) {
  useTranslation()
  return <div className="space-y-5">
    {deviceEnabled && <DeviceFlow generation={generation} onConnected={onConnected} />}
    {deviceEnabled ? <details><summary>{t("使用已有 token")}</summary><div className="pt-4"><TokenForm generation={generation} onConnected={onConnected} /></div></details> : <TokenForm generation={generation} onConnected={onConnected} />}
  </div>
}

function TokenForm({ generation, onConnected }: { generation: string; onConnected: () => void }) {
  useTranslation()
  const { github } = useApi()
  const [token, setToken] = useState("")
  const connect = useMutation({
    mutationFn: async (value: string) => {
      return github.connectToken({ accessToken: value, expectedGeneration: generation })
    },
    onMutate: () => setToken(""),
    onSuccess: () => onConnected(),
  })
  function submit(event: FormEvent) { event.preventDefault(); connect.mutate(token) }
  return <form onSubmit={submit} className="space-y-4" aria-busy={connect.isPending}>
    <div className="space-y-2"><label htmlFor="github-token">{t("已授权的 GitHub token")}</label><div className="token-input-row"><Input id="github-token" name="github-token" type="password" autoComplete="off" maxLength={3000} required value={token} onChange={(event) => setToken(event.target.value)} aria-describedby="token-help" /><Button type="submit" disabled={connect.isPending || !token.trim()}>{connect.isPending ? t("正在验证…") : generation ? t("更新连接") : t("连接 GitHub")}</Button></div><p id="token-help" className="field-help">{t("公开 Lists 无需额外 scope；私有 Lists 需要 user 权限。token 由服务端校验并加密保存。")}</p></div>
    {connect.isError && <ErrorNotice error={connect.error} />}
  </form>
}

function EmptyState({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  useTranslation()
  return <div className="empty-state"><div className="empty-icon">{icon}</div><h2 className="font-medium">{title}</h2><p className="mt-2 max-w-sm text-sm leading-6 text-muted-foreground">{children}</p></div>
}

export function SourcesPage() {
  useTranslation()
  const { github } = useApi()
  const queryClient = useQueryClient()
  const catalog = useCatalog()
  const [notice, setNotice] = useNotice()
  const [filter, setFilter] = useState("")
  const refresh = () => { void queryClient.invalidateQueries({ queryKey: catalog.key }) }
  const sync = useMutation({
    mutationFn: () => github.sync({}),
    onSuccess: (result) => { queryClient.setQueryData(catalog.key, result.catalog); setNotice("来源已同步。") },
    onError: () => refresh(),
  })
  const subscription = useMutation({
    mutationFn: async ({ listId, subscribed }: { listId: string; subscribed: boolean }) => { await github.setListSubscription({ listId, subscribed }); return github.sync({}) },
    onSuccess: (result) => { queryClient.setQueryData(catalog.key, result.catalog); setNotice("订阅已保存并同步。") },
    onSettled: () => refresh(),
  })
  const busy = sync.isPending || subscription.isPending
  const connected = () => { refresh(); sync.mutate() }
  const source = catalog.isError ? undefined : catalog.data
  const lists = source?.lists.filter((list) => `${list.name} ${list.slug}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase())) ?? []
  const listsUnavailable = source?.lists.length === 0 && (busy || sync.isError || subscription.isError)
  return <div className="sources-page">
    <div className="page-heading">
      <div><h1>{t("GitHub 来源")}</h1><p>{t("连接 GitHub，选择要同步的收藏列表。")}</p></div>
      {source?.connected && <Button variant="outline" disabled={busy} onClick={() => { setNotice(""); sync.mutate() }}><RefreshCw size={16} aria-hidden="true" />{sync.isPending ? t("同步中…") : t("立即同步")}</Button>}
    </div>
    <section className="source-card" aria-labelledby="github-source-title" aria-busy={catalog.isPending}>
      <div className="source-card-heading">
        <div className="service-icon"><GitFork size={23} aria-hidden="true" /></div>
        <div className="sources-service"><h2 id="github-source-title">GitHub</h2><p>{t("Star Lists · 按你已有的分类收录仓库")}</p></div>
        {source?.connected ? <span className="connection-state"><ShieldCheck size={15} aria-hidden="true" />{t("已连接")}</span> : <span className="sources-state">{catalog.isPending ? t("正在读取…") : catalog.isError ? t("状态待确认") : source?.authorizationRequired ? t("需要重新授权") : t("未连接")}</span>}
      </div>
      {catalog.isPending ? <div className="source-card-body"><p className="sources-loading" role="status">{t("正在读取连接状态…")}</p></div> : catalog.isError ? <div className="source-card-body"><ErrorNotice error={catalog.error} retry={refresh} /></div> : source?.connected ? <div className="source-card-body">
        <dl className="sources-identity"><dt>{t("GitHub 账号 ID")}</dt><dd>{source.viewerId}</dd></dl>
        <p className="sources-description">{t("选择列表后，Cognate 将同步其中的仓库。列表内容只对你的账号可见。")}</p>
        <details><summary>{t("重新授权或更换 token")}</summary><div className="pt-4"><SourceForm generation={source.credentialGeneration} deviceEnabled={source.deviceFlowEnabled} onConnected={connected} /></div></details>
      </div> : <div className="source-card-body">
        {source?.authorizationRequired && <p role="status" className="sources-expiry">{t("GitHub 凭据已过期，请重新授权。旧来源内容已隐藏。")}</p>}
        <SourceForm generation={source?.credentialGeneration} deviceEnabled={source?.deviceFlowEnabled} onConnected={connected} />
        <p className="sources-security"><LockKeyhole size={14} aria-hidden="true" />{t("token 不会通过查询接口返回。")}</p>
      </div>}
    </section>
    {source?.connected && <section className="list-section" aria-labelledby="lists-title" aria-busy={busy}>
      <div className="section-toolbar">
        <div><h2 id="lists-title">{t("收藏列表 ")}{!listsUnavailable && <span className="sources-count">{formatNumber(source.lists.length)}</span>}</h2><p className="sources-selection-count">{listsUnavailable ? t("列表待确认") : t("已选择 {{selected}} / {{total}} 个列表", { selected: formatNumber(source.lists.filter((list) => list.subscribed).length), total: formatNumber(source.lists.length) })}</p></div>
        <div className="filter-field"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选收藏列表")} placeholder={t("筛选列表…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div>
      </div>
      <p className="sources-list-help" id="lists-help">{t("勾选即保存订阅并同步仓库；取消勾选会停止订阅。")}</p>
      {lists.length === 0 ? listsUnavailable ? <p className="sources-list-help" role="status">{busy ? t("正在读取收藏列表…") : t("同步未完成，暂时无法确认列表。请重试同步。")}</p> : <div className="list-surface"><EmptyState icon={<GitFork size={24} aria-hidden="true" />} title={filter ? t("没有匹配的列表") : t("还没有收藏列表")}>{filter ? t("试试其他名称，或清空筛选。") : t("在 GitHub 整理 Star Lists 后，点击“立即同步”获取。")}</EmptyState></div> : <div className="list-surface" role="group" aria-labelledby="lists-title" aria-describedby="lists-help">{lists.map((list) => <label className="source-list-row" data-subscribed={list.subscribed} key={list.id} htmlFor={`list-${list.id}`}>
        <Checkbox id={`list-${list.id}`} aria-labelledby={`list-name-${list.id}`} checked={list.subscribed} disabled={busy} onCheckedChange={(checked) => { setNotice(""); subscription.mutate({ listId: list.id, subscribed: checked === true }) }} />
        <span className="sources-list-content"><span className="sources-list-name" id={`list-name-${list.id}`}>{list.name}</span><span className="sources-list-meta"><span className="sources-list-slug">{list.slug}</span>{list.private && <span className="sources-private"><LockKeyhole size={13} aria-hidden="true" />{t("私有列表")}</span>}</span></span>
        <span className="sources-subscription">{list.subscribed ? t("已订阅") : t("未订阅")}</span>
      </label>)}</div>}
      <div className="sources-feedback">
        {sync.isError && <ErrorNotice error={sync.error} />}{subscription.isError && <ErrorNotice error={subscription.error} />}
        <p role="status" aria-live="polite" aria-atomic="true">{subscription.isPending ? t("正在保存订阅并同步仓库…") : sync.isPending ? t("正在同步 GitHub 来源…") : notice}</p>
      </div>
    </section>}
  </div>
}

export function DiscoveredRepositories({ onCollect, busy }: { onCollect: (name: string) => void; busy: boolean }) {
  useTranslation()
  const catalog = useCatalog()
  const [filter, setFilter] = useState("")
  const repos = catalog.data?.repositories.filter((repo) => repo.nameWithOwner.toLocaleLowerCase().includes(filter.toLocaleLowerCase())) ?? []
  return <>
    <p className="mb-5 text-sm leading-6 text-muted-foreground">{t("这些是订阅 Lists 中的仓库元数据，还没有拉取技能内容。可收录公开仓库；私有仓库内容获取尚未接入。")}</p>
    <div className="section-toolbar"><h2 className="text-sm text-muted-foreground">{catalog.data?.repositories.length ?? 0} {t(" 个仓库")}</h2><div className="filter-field"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选仓库")} placeholder={t("按仓库名筛选…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div></div>
    {catalog.isPending ? <p className="py-10 text-sm text-muted-foreground" role="status">{t("正在读取仓库…")}</p> : catalog.isError ? <ErrorNotice error={catalog.error} retry={() => { void catalog.refetch() }} /> : repos.length === 0 ? <div className="list-surface"><EmptyState icon={<FolderGit2 size={26} aria-hidden="true" />} title={filter ? t("没有匹配的仓库") : t("还没有同步的仓库")}>{filter ? t("试试其他仓库名。") : t("在“来源”中连接 GitHub 并订阅列表，仓库将在同步后显示。")}</EmptyState></div> : <div className="list-surface">{repos.map((repo) => <div key={repo.id} className="repository-row"><FolderGit2 size={20} className="text-muted-foreground" aria-hidden="true" /><div className="min-w-0 flex-1"><p className="break-words font-mono text-sm font-medium">{repo.nameWithOwner}</p><p className="mt-1 text-xs text-muted-foreground">GitHub Lists{repo.private ? t(" · 私有仓库") : t(" · 待收录")}</p></div><Button variant="outline" disabled={busy || repo.private} onClick={() => onCollect(repo.nameWithOwner)} aria-label={t("收录 {{value1}} 的技能", { value1: repo.nameWithOwner })}>{repo.private ? t("暂不支持") : t("收录技能")}</Button><a className="external-link" href={`https://github.com/${repo.nameWithOwner}`} target="_blank" rel="noreferrer" aria-label={t("在 GitHub 打开 {{value1}}", { value1: repo.nameWithOwner })}><ExternalLink size={16} aria-hidden="true" /></a></div>)}</div>}
  </>
}
