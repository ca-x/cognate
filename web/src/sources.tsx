import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { create } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { CircleAlert, ExternalLink, FolderGit2, GitFork, LockKeyhole, RefreshCw, Search, ShieldCheck } from "lucide-react"
import { CatalogSchema } from "./gen/skillhub/github/v1/github_pb"
import { errorMessage, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { Checkbox } from "./components/ui/checkbox"
import { DeviceFlow } from "./device-auth"

function useCatalog() {
  const { github, session } = useApi()
  const key = ["github-catalog", session?.userId]
  const query = useQuery({ queryKey: key, refetchInterval: 30_000, queryFn: async ({ signal }) => (await github.getCatalog({}, { signal })).catalog ?? create(CatalogSchema) })
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
  const lists = catalog.data?.lists.filter((list) => `${list.name} ${list.slug}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase())) ?? []
  return <>
    <div className="page-heading"><div><h1>{t("你的 skill 来源")}</h1><p>{t("连接 GitHub，选择要同步的收藏列表。")}</p></div>{catalog.data?.connected && <Button variant="outline" disabled={busy} onClick={() => { setNotice(""); sync.mutate() }}><RefreshCw size={16} aria-hidden="true" />{sync.isPending ? t("同步中…") : t("立即同步")}</Button>}</div>
    <div className="source-card">
      <div className="source-card-heading"><div className="service-icon"><GitFork size={25} aria-hidden="true" /></div><div className="flex-1"><h2 className="font-semibold">GitHub</h2><p className="mt-1 text-sm text-muted-foreground">{t("Star Lists · 按你已有的分类收录仓库")}</p></div>{catalog.data?.connected && <span className="connection-state"><ShieldCheck size={15} aria-hidden="true" />{t("已连接")}</span>}</div>
      {catalog.isPending ? <p className="px-6 pb-6 text-sm text-muted-foreground" role="status">{t("正在读取连接状态…")}</p> : catalog.isError ? <div className="px-6 pb-6"><ErrorNotice error={catalog.error} retry={refresh} /></div> : catalog.data?.connected ? <div className="source-card-body"><p className="mb-4 text-sm text-muted-foreground">{t("选择列表后，Cognate 将同步其中的仓库。列表内容只对你的账号可见。")}</p><details><summary>{t("重新授权或更换 token")}</summary><div className="pt-4"><SourceForm generation={catalog.data.credentialGeneration} deviceEnabled={catalog.data.deviceFlowEnabled} onConnected={connected} /></div></details></div> : <div className="source-card-body">{catalog.data?.authorizationRequired && <p role="status" className="mb-4 text-sm text-muted-foreground">{t("GitHub 凭据已过期，请重新授权。旧来源内容已隐藏。")}</p>}<SourceForm generation={catalog.data?.credentialGeneration} deviceEnabled={catalog.data?.deviceFlowEnabled} onConnected={connected} /><p className="mt-5 flex items-center gap-2 text-xs text-muted-foreground"><LockKeyhole size={14} aria-hidden="true" />{t("token 不会通过查询接口返回。")}</p></div>}
    </div>
    {catalog.data?.connected && <section className="list-section" aria-labelledby="lists-title" aria-busy={busy}>
      <div className="section-toolbar"><div><h2 id="lists-title" className="font-semibold">{t("收藏列表 ")}<span className="ml-2 font-normal text-muted-foreground">{formatNumber(catalog.data.lists.length)}</span></h2><p className="mt-1 text-sm text-muted-foreground">{t("已选择 ")}{formatNumber(catalog.data.lists.filter((list) => list.subscribed).length)} {t(" 个列表")}</p></div><div className="filter-field"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选收藏列表")} placeholder={t("筛选列表…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div></div>
      {lists.length === 0 ? <EmptyState icon={<GitFork size={24} aria-hidden="true" />} title={filter ? t("没有匹配的列表") : t("还没有收藏列表")}>{filter ? t("试试其他名称，或清空筛选。") : t("在 GitHub 整理 Star Lists 后，点击“立即同步”获取。")}</EmptyState> : <div className="list-surface">{lists.map((list) => <div className="source-list-row" key={list.id}><label className="list-selection" htmlFor={`list-${list.id}`}><Checkbox id={`list-${list.id}`} checked={list.subscribed} disabled={busy} onCheckedChange={(checked) => { setNotice(""); subscription.mutate({ listId: list.id, subscribed: checked === true }) }} /><span><span className="flex flex-wrap items-center gap-2 font-medium">{list.name}{list.private && <LockKeyhole size={13} aria-label={t("私有列表")} />}</span><span className="mt-1 block font-mono text-xs text-muted-foreground">{list.slug}</span></span></label><span className="text-xs text-muted-foreground">{list.subscribed ? t("已订阅") : t("未订阅")}</span></div>)}</div>}
      {sync.isError && <ErrorNotice error={sync.error} />}{subscription.isError && <ErrorNotice error={subscription.error} />}
      <p className="mt-3 min-h-6 text-sm text-muted-foreground" role="status" aria-live="polite">{busy ? t("正在保存并同步，请稍候…") : notice}</p>
    </section>}
  </>
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
