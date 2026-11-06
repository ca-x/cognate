import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { ArrowDownToLine, ChevronDown, CircleAlert, ExternalLink, FileText, FolderGit2, GitBranch, Plus, RefreshCw, Trash2 } from "lucide-react"
import { errorMessage, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { SearchInput } from "./components/ui/search-input"
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import { DiscoveredRepositories } from "./sources"
import { PublicationControl } from "./publication"
import { SkillCategories } from "./skill-categories"
import { CatalogError } from "./catalog-error"
import { ImportRepositories } from "./import-repositories"
import { RepositoryRefreshControl } from "./repository-refresh"
import { sourcePathLabel } from "./import-selection-presentation"
import type { RepositorySource } from "./gen/skillhub/library/v1/library_pb"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import "./repositories-ui-i18n"
import "./repositories-ui.css"
import "./import-sources-i18n"

function collectionError(error: unknown) {
  switch (ConnectError.from(error).code) {
    case Code.InvalidArgument: return t("请输入 owner/repo 或 GitHub HTTPS 地址，并检查分支名。")
    case Code.NotFound: return t("无法获取仓库归档，请检查仓库、分支及 GitHub 授权。私有来源需要服务端凭据具有对应读取权限。")
    case Code.FailedPrecondition: return t("未收录：归档中没有可用的 SKILL.md，或文件格式不安全。")
    case Code.DataLoss: return t("已保存的仓库归档缺失或损坏，当前快照不能使用。请检查服务端数据与备份；程序不会静默覆盖既有快照。")
    case Code.ResourceExhausted: return t("收录达到容量或大小限制，或 GitHub 暂时限流。请稍后重试，或选择较小的仓库。")
    case Code.Unavailable:
    case Code.DeadlineExceeded:
    case Code.Internal: return t("服务端未能确认结果。收录可能已完成，请重新读取状态；不会自动重试或覆盖快照。")
    default: return errorMessage(error)
  }
}

function LibraryError({ error, retry }: { error: unknown; retry?: () => void }) {
  useTranslation()
  return <div className="error-notice" role="alert"><CircleAlert size={18} className="shrink-0" aria-hidden="true" /><p className="flex-1">{collectionError(error)}</p>{retry && <Button variant="outline" onClick={retry}>{t("重试")}</Button>}</div>
}

function RepositorySkills({ repositoryId }: { repositoryId: string }) {
  useTranslation()
  const { library, categories, session } = useApi()
  const [filter, setFilter] = useState("")
  const [categoryId, setCategoryId] = useState("")
  const query = useQuery({ queryKey: ["repository-skills", session?.userId, repositoryId], queryFn: ({ signal }) => library.getRepository({ repositoryId }, { signal }) })
  const categoryQuery = useQuery({ queryKey: ["catalog", "owner", session?.userId, "categories"], queryFn: ({ signal }) => categories.listCategories({}, { signal }) })
  const assignments = useQueries({ queries: categoryId ? (query.data?.skills ?? []).map((entry) => ({ queryKey: ["catalog", "owner", session?.userId, "skill", entry.id, "categories"], queryFn: ({ signal }: { signal: AbortSignal }) => categories.getSkillCategories({ skillId: entry.id }, { signal }) })) : [] })
  if (query.isPending) return <section className="library-detail" aria-label={t("仓库技能")} aria-busy="true"><p role="status" className="text-sm text-muted-foreground">{t("正在读取技能…")}</p><div className="skill-placeholder" aria-hidden="true" /><div className="skill-placeholder" aria-hidden="true" /></section>
  if (!query.data) return <section className="library-detail"><LibraryError error={query.error} retry={() => { void query.refetch() }} /></section>
  const repository = query.data.repository
  const assignmentError = assignments.find((entry) => entry.isError)?.error
  const loadingAssignments = assignments.some((entry) => entry.isPending)
  const skills = query.data.skills.filter((entry, index) => `${entry.name} ${entry.description} ${entry.path}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()) && (!categoryId || assignments[index]?.data?.categories.some((category) => category.id === categoryId)))
  return <section className="library-detail" aria-label={t("仓库技能")}>
    {query.isError && <><LibraryError error={query.error} retry={() => { void query.refetch() }} /><p className="snapshot-unknown" role="status">{t("快照状态未知；下面保留上次读取的内容与筛选，不代表当前校验成功。")}</p></>}
    <div className="snapshot-heading"><div className="min-w-0"><p className="snapshot-eyebrow">{query.isError ? t("上次读取的快照") : t("仓库快照 · ")}{!query.isError && <>{formatNumber(query.data.skills.length)} {t(" 个技能")}</>}</p><h2 className="break-words font-mono text-base font-semibold">{repository?.nameWithOwner}</h2><p className="snapshot-branch"><GitBranch size={15} aria-hidden="true" /><span>{repository?.branch || t("默认分支")}</span></p></div><a className="external-link" href={`https://github.com/${repository?.nameWithOwner}`} target="_blank" rel="noreferrer" aria-label={t("在 GitHub 打开当前仓库")}><ExternalLink size={16} aria-hidden="true" /></a></div>
    {repository && <details className="snapshot-integrity"><summary>{t("归档 SHA-256")}</summary><p className="snapshot-hash"><code>{repository.archiveHash}</code></p></details>}
    {repository && <RepositoryRefreshControl key={session?.userId} repository={repository} />}
    <div className="my-5 w-full"><SearchInput aria-label={t("筛选已收录技能")} placeholder={t("按名称、描述或路径筛选…")} value={filter} onValueChange={setFilter} clearLabel={t("清空搜索")} /></div>
    <div className="mb-5 space-y-2"><label htmlFor="owner-skill-category">{t("按我的分类筛选")}</label><Select value={categoryId || "all"} onValueChange={(value) => setCategoryId(value === "all" ? "" : value)} disabled={categoryQuery.isPending || categoryQuery.isError}><SelectTrigger id="owner-skill-category" className="repository-category-select"><SelectValue /></SelectTrigger><SelectContent className="repository-category-options"><SelectItem value="all">{t("全部技能")}</SelectItem>{categoryId && !categoryQuery.data?.categories.some((entry) => entry.id === categoryId) && <SelectItem value={categoryId}>{t("已选分类（已变更）")}</SelectItem>}{categoryQuery.data?.categories.map((entry) => <SelectItem key={entry.id} value={entry.id}>{entry.name}（{formatNumber(entry.skillCount)}）</SelectItem>)}</SelectContent></Select>{categoryQuery.isPending && <p role="status" className="field-help">{t("正在读取分类…")}</p>}{categoryQuery.isError && <CatalogError error={categoryQuery.error} retry={() => { void categoryQuery.refetch() }} />}</div>
    {loadingAssignments ? <p role="status" className="py-6 text-sm text-muted-foreground">{t("正在读取技能分类…")}</p> : assignmentError ? <CatalogError error={assignmentError} retry={() => { assignments.forEach((entry) => { void entry.refetch() }) }} /> : skills.length === 0 ? <p className="py-6 text-sm text-muted-foreground" role="status">{t("没有匹配的技能，试试其他关键词或分类。")}</p> : <div className="skill-list">{skills.map((entry) => <article key={entry.id} className="skill-card"><div className="flex items-start gap-3"><FileText size={17} className="mt-1 shrink-0 text-muted-foreground" aria-hidden="true" /><div className="min-w-0"><h3 className="break-words text-sm font-semibold">{entry.name}</h3><p className="mt-2 whitespace-pre-line break-words text-sm leading-6 text-muted-foreground">{entry.description}</p></div></div><div className="skill-metadata"><code>{entry.path === "." ? "SKILL.md" : `${entry.path}/SKILL.md`}</code><span title={entry.contentHash}>SHA-256 <code>{entry.contentHash.slice(0, 12)}</code></span></div><div className="mt-3"><SkillCategories skillId={entry.id} skillName={entry.name} /></div></article>)}</div>}
    <p className="mt-5 text-xs leading-6 text-muted-foreground">{t("技能内容已保存在服务端。可在「组合」中整理；本页不执行安装，客户端安装和更新需另行验证。")}</p>
    <PublicationControl repositoryId={repositoryId} repositoryName={repository?.nameWithOwner ?? ""} />
  </section>
}

function SavedRepositorySources({ owner, busy, onBusyChange, onCollected }: { owner: string; busy: boolean; onBusyChange: (value: boolean) => void; onCollected: (repositoryId: string) => void }) {
  useTranslation()
  const { library } = useApi()
  const cache = useQueryClient()
  const [selected, setSelected] = useState("")
  const [filter, setFilter] = useState("")
  const [removing, setRemoving] = useState<RepositorySource | null>(null)
  const cancelDelete = useRef<HTMLButtonElement>(null)
  const removeTrigger = useRef<HTMLButtonElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const active = useRef<AbortController | null>(null)
  const mounted = useRef(false)
  const queryKey = ["repository-sources", owner]
  const query = useQuery({ queryKey, queryFn: ({ signal }) => library.listRepositorySources({}, { signal }), retry: false })
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; active.current?.abort() } }, [library])
  const sync = useMutation({
    retry: false,
    mutationFn: async (source: RepositorySource) => {
      const controller = new AbortController()
      active.current = controller
      try {
        const result = await library.collectRepository({ repository: source.nameWithOwner, branch: source.branch }, { signal: controller.signal, timeoutMs: 120000 })
        if (!result.repository) throw new ConnectError("", Code.DataLoss)
        return result.repository
      } finally { if (active.current === controller) active.current = null }
    },
    onSuccess: (repository) => { if (mounted.current) onCollected(repository.id) },
    onSettled: () => {
      if (!mounted.current) return
      for (const key of ["library-repositories", "collected-repositories", "repository-skills", "repository-sources"]) void cache.invalidateQueries({ queryKey: [key, owner] })
    },
  })
  const deletion = useMutation({
    retry: false,
    mutationFn: async (sourceId: string) => {
      const controller = new AbortController()
      active.current = controller
      try { return await library.deleteRepositorySource({ sourceId }, { signal: controller.signal }) }
      finally { if (active.current === controller) active.current = null }
    },
    onSuccess: () => { if (mounted.current) setRemoving(null) },
    onSettled: () => { if (mounted.current) void cache.invalidateQueries({ queryKey }) },
  })
  const working = sync.isPending || deletion.isPending
  useEffect(() => { onBusyChange(working); return () => onBusyChange(false) }, [working, onBusyChange])
  const sources = query.data?.sources ?? []
  const visible = sources.filter((entry) => `${entry.nameWithOwner} ${entry.branch}`.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()))
  const source = sources.find((entry) => entry.id === selected) ?? sources[0]
  const locked = busy || working || query.isError
  return <section className="list-section saved-sources" aria-labelledby="repository-sources-title">
    <div className="section-toolbar"><div><h2 ref={heading} tabIndex={-1} id="repository-sources-title" className="font-semibold">{t("已保存来源")}{query.isSuccess && <span className="repository-count">{formatNumber(sources.length)}</span>}</h2><p className="mt-1 text-sm leading-6 text-muted-foreground">{t("仅当前账号可见。这里保存仓库与技能线索，不代表内容已下载、验证或安装。")}</p>{query.isSuccess && <p className="mt-2 text-sm text-muted-foreground">{t("待同步 {{count}} 个来源", { count: formatNumber(sources.filter((entry) => !entry.collectedRepositoryId).length) })}</p>}</div><div className="w-full space-y-2 sm:max-w-60"><SearchInput value={filter} onValueChange={setFilter} aria-label={t("筛选已保存来源")} placeholder={t("按仓库或分支筛选…")} clearLabel={t("清空搜索")} /><Button variant="ghost" disabled={working || query.isFetching} onClick={() => { void query.refetch() }}>{t("重新读取来源")}</Button></div></div>
    {query.isError && <div className="error-notice" role="alert"><p>{t("无法读取来源；保留上次列表，不代表当前状态。请重新读取后再操作。")}</p></div>}
    {query.isPending ? <p role="status" className="py-6 text-sm text-muted-foreground">{t("正在读取已保存来源…")}</p> : sources.length === 0 ? query.isSuccess && <div className="list-surface p-5 text-sm leading-6 text-muted-foreground">{t("尚无已保存来源。从 Magpie 或 cc-switch 选择仓库或技能导入后，会保存在这里；同步前不会下载。")}</div> : <div className="library-workbench">
      <div className="collected-repositories max-h-[32rem] overflow-y-auto overscroll-contain" role="group" aria-label={t("选择已保存的来源")}>
        {!visible.length && <p className="p-5 text-sm text-muted-foreground" role="status">{t("没有匹配的来源，当前选择仍保留。")}</p>}
        {visible.map((entry) => <button type="button" key={entry.id} className="collected-repository-select" aria-pressed={source?.id === entry.id} onClick={() => { setSelected(entry.id); sync.reset(); deletion.reset() }} disabled={working}><span className="repository-choice-heading"><span className="break-words text-sm font-medium">{entry.nameWithOwner}</span>{source?.id === entry.id && <span className="repository-selected">{t("已选")}</span>}</span><span className="repository-choice-branch">{entry.branch || t("默认分支")}</span><span className="mt-2 block text-xs text-muted-foreground">{t(entry.collectedRepositoryId ? "已有收录记录" : "待同步，尚未下载")}</span></button>)}
      </div>
      {source && <div className="library-detail min-w-0 space-y-4" aria-label={t("来源技能引用")}>
        <div className="space-y-2"><h3 className="break-words font-semibold">{source.nameWithOwner}</h3><p className="break-all text-sm text-muted-foreground">{source.branch || t("默认分支")}</p><p className="text-sm leading-6 text-muted-foreground">{t(source.collectedRepositoryId ? "已有仓库收录记录；下列仍是来源引用，内容状态请到“已收录”查看。" : "尚未下载技能。同步会拉取整个仓库并扫描 SKILL.md，不会安装或自动加入组合。")}</p></div>
        {source.skills.length ? <ul className="max-h-96 divide-y divide-border overflow-y-auto overscroll-contain" tabIndex={0} aria-label={t("已保存的技能线索")}>{source.skills.map((skill, index) => <li key={`${skill.name}-${skill.path}-${index}`} className="py-3"><p className="break-words text-sm font-medium">{skill.name}</p><p className="mt-1 break-all text-xs text-muted-foreground">{sourcePathLabel(skill.path, t)}</p></li>)}</ul> : <p className="text-sm text-muted-foreground">{t("未单独保存技能引用；同步时会扫描整个仓库。")}</p>}
        <div className="flex flex-wrap gap-3"><Button disabled={locked} onClick={() => { deletion.reset(); sync.mutate(source) }}><RefreshCw size={16} aria-hidden="true" className={sync.isPending ? "motion-safe:animate-spin" : undefined} />{t(sync.isPending ? "正在同步技能…" : "同步技能")}</Button><Button ref={removeTrigger} variant="outline" disabled={locked} onClick={() => { deletion.reset(); setRemoving(source) }}><Trash2 size={16} aria-hidden="true" />{t("删除来源")}</Button></div>
        {sync.isPending && <p role="status" className="text-sm text-muted-foreground">{t("服务端正在拉取并验证，来源记录会保留。")}</p>}
        {sync.isError && <div className="space-y-3"><LibraryError error={sync.error} /><p role="status" className="text-sm text-muted-foreground">{t("来源记录仍保留。可先重新读取结果，再点击“同步技能”重试；不会自动重试。")}</p></div>}
        {sync.isSuccess && <p role="status" className="text-sm">{t("同步完成。来源引用仍保留，实际技能请查看已收录仓库。")}</p>}
      </div>}
    </div>}
    <Dialog open={!!removing} onOpenChange={(open) => { if (!open && !deletion.isPending) setRemoving(null) }}><DialogContent className="repositories-ui-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); cancelDelete.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); if (removeTrigger.current && !removeTrigger.current.disabled) removeTrigger.current.focus(); else heading.current?.focus() }}><DialogTitle>{t("删除已保存来源？")}</DialogTitle><DialogDescription>{t("仅删除此仓库的来源与技能线索，不删除已收录仓库或已验证技能。")}</DialogDescription><p className="mt-3 break-all text-sm">{removing?.nameWithOwner} · {removing?.branch || t("默认分支")}</p>{deletion.isError && <p role="alert" className="mt-3 error-message">{t("无法确认删除结果。请关闭对话框并重新读取来源，核对后再操作；不会自动重试。")}</p>}<div className="mt-5 flex flex-wrap gap-3"><Button ref={cancelDelete} variant="outline" disabled={deletion.isPending} onClick={() => setRemoving(null)}>{t("取消")}</Button><Button disabled={busy || deletion.isPending || deletion.isError || !removing} onClick={() => { if (removing) deletion.mutate(removing.id) }}>{t(deletion.isPending ? "正在删除来源…" : "仅删除来源")}</Button></div></DialogContent></Dialog>
  </section>
}

export function RepositoriesPage() {
  useTranslation()
  const { library, session } = useApi()
  const queryClient = useQueryClient()
  const [repository, setRepository] = useState("")
  const [branch, setBranch] = useState("")
  const [filter, setFilter] = useState("")
  const [selected, setSelected] = useState("")
  const [notice, setNotice] = useNotice()
  const [importCollecting, setImportCollecting] = useState(false)
  const [sourcesBusy, setSourcesBusy] = useState(false)
  const [collectOpen, setCollectOpen] = useState(false)
  const active = useRef<AbortController | null>(null)
  const key = ["library-repositories", session?.userId]
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => library.listRepositories({}, { signal }) })
  useEffect(() => () => active.current?.abort(), [])
  const collection = useMutation({
    retry: false,
    mutationFn: async (input: { repository: string; branch: string; fromForm: boolean }) => {
      const controller = new AbortController()
      active.current = controller
      try { return await library.collectRepository({ repository: input.repository, branch: input.branch }, { signal: controller.signal }) }
      finally { if (active.current === controller) active.current = null }
    },
    onMutate: () => setNotice(""),
    onSuccess: (result, input) => {
      if (result.repository) {
        setSelected(result.repository.id)
        setFilter("")
        setNotice({ key: "{{value1}} 已收录，共 {{value2}} 个技能。", values: { value1: result.repository.nameWithOwner, value2: formatNumber(result.repository.skillCount) } })
      }
      if (input.fromForm) { setRepository(""); setBranch(""); setCollectOpen(false) }
    },
    onError: (error) => { if (ConnectError.from(error).code === Code.Canceled) setNotice("已停止等待。如果服务端已经完成收录，刷新后仍会显示结果。") },
    onSettled: () => { for (const name of ["library-repositories", "collected-repositories", "repository-sources"]) void queryClient.invalidateQueries({ queryKey: [name, session?.userId] }) },
  })
  function submit(event: FormEvent) { event.preventDefault(); if (!collection.isPending && !importCollecting && !sourcesBusy) collection.mutate({ repository, branch, fromForm: true }) }
  const repositories = query.data?.repositories ?? []
  const visible = repositories.filter((entry) => `${entry.nameWithOwner} ${entry.branch}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()))
  const selectedId = repositories.some((entry) => entry.id === selected) ? selected : repositories[0]?.id
  return <div className="repositories-ui">
    <div className="page-heading"><div><p className="workspace-eyebrow">{t("我的资料库")}</p><h1>{t("技能仓库")}</h1><p>{t("先收录内容，再把需要的技能带到你的工作流。")}</p></div><div className="repository-toolbar"><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch(); void queryClient.invalidateQueries({ queryKey: ["repository-skills", session?.userId] }) }}><RefreshCw size={15} aria-hidden="true" />{t("刷新")}</Button><Dialog open={collectOpen} onOpenChange={setCollectOpen}>
      <DialogTrigger asChild><Button><Plus size={16} aria-hidden="true" />{collection.isPending ? t("正在拉取并扫描…") : t("添加 GitHub 仓库")}</Button></DialogTrigger>
      <DialogContent className="repositories-ui-dialog repository-collect-dialog">
      <DialogTitle>{t("添加 GitHub 仓库")}</DialogTitle><DialogDescription>{t("整仓拉取并扫描，找到技能后才会收录。")}</DialogDescription>
      <form className="source-card-body" onSubmit={submit} aria-busy={collection.isPending}>
        <div className="collection-fields"><div className="space-y-2"><label htmlFor="repository-input">{t("GitHub 仓库")}</label><Input id="repository-input" name="repository" placeholder={t("owner/repo 或 https://github.com/owner/repo")} autoComplete="off" maxLength={512} required disabled={collection.isPending} value={repository} onChange={(event) => setRepository(event.target.value)} aria-describedby="collection-help repository-access-help" /></div><div className="space-y-2"><label htmlFor="branch-input">{t("分支 ")}<span className="font-normal text-muted-foreground">{t("（可选）")}</span></label><Input id="branch-input" name="branch" placeholder={t("默认分支")} autoComplete="off" maxLength={256} disabled={collection.isPending} value={branch} onChange={(event) => setBranch(event.target.value)} /></div></div>
        <p className="field-help mt-3" id="collection-help">{t("归档上限 32 MiB，解压后 64 MiB、最多 512 个技能；符号链接不跟随。留空分支或填写 HEAD 都使用默认分支。")}</p>
        <div className="mt-5 flex flex-wrap items-center gap-3"><Button type="submit" disabled={collection.isPending || importCollecting || sourcesBusy || !repository.trim()}><FolderGit2 size={16} aria-hidden="true" />{collection.isPending ? t("正在拉取并扫描…") : t("收录并扫描")}</Button>{collection.isPending && <Button variant="ghost" onClick={() => active.current?.abort()}>{t("停止等待")}</Button>}</div>
        <p className="field-help mt-3" id="repository-access-help">{t("私有来源需要服务端 GitHub 授权。收录默认不公开；只有另行确认发布，才允许匿名下载。")}</p>
        {collection.isError && ConnectError.from(collection.error).code !== Code.Canceled && <LibraryError error={collection.error} />}
        <p className="mt-3 min-h-6 text-sm leading-6 text-muted-foreground" role="status" aria-live="polite">{collection.isPending ? t("服务端正在获取仓库和校验技能内容，请稍候。") : notice}</p>
      </form>
      </DialogContent>
    </Dialog></div></div>
    {!collectOpen && (notice || collection.isPending || collection.isError) && <div className="repository-activity"><p role="status" aria-live="polite">{collection.isPending ? t("服务端正在获取仓库和校验技能内容，请稍候。") : notice}</p>{collection.isPending && <Button variant="outline" onClick={() => setCollectOpen(true)}>{t("查看收录进度")}</Button>}{collection.isError && ConnectError.from(collection.error).code !== Code.Canceled && <LibraryError error={collection.error} />}</div>}
    <section className="list-section" aria-labelledby="library-title">
      <div className="section-toolbar"><div><h2 id="library-title" className="font-semibold">{t("已收录 ")}{query.isSuccess && <span className="repository-count">{formatNumber(repositories.length)}</span>}</h2><p className="mt-1 text-sm text-muted-foreground">{t("这里列出收录记录；打开仓库后会校验归档并读取技能。")}</p></div><div className="w-full sm:max-w-60"><SearchInput aria-label={t("筛选已收录仓库")} placeholder={t("按仓库或分支筛选…")} value={filter} onValueChange={setFilter} clearLabel={t("清空搜索")} /></div></div>
      {query.isError && <><LibraryError error={query.error} retry={() => { void query.refetch() }} />{query.data && <p className="snapshot-unknown" role="status">{t("快照状态未知；下面保留上次读取的内容与筛选，不代表当前校验成功。")}</p>}</>}
      {query.isPending ? <p role="status" className="py-8 text-sm text-muted-foreground">{t("正在读取已收录仓库…")}</p> : !query.data ? null : repositories.length === 0 ? !query.isError && <div className="list-surface empty-state"><FolderGit2 size={26} className="empty-icon" aria-hidden="true" /><h3 className="font-medium">{t("资料库还是空的")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("添加第一个技能仓库，或从下方 Lists 候选中明确选择并收录。")}</p></div> : <div className="library-workbench"><div className="collected-repositories" role="group" aria-label={t("选择已收录的仓库")}>{visible.length === 0 ? <p className="p-5 text-sm text-muted-foreground" role="status">{t("没有匹配的仓库。当前详情仍保留所选快照。")}</p> : visible.map((entry) => <button type="button" key={entry.id} className="collected-repository-select" aria-pressed={selectedId === entry.id} onClick={() => setSelected(entry.id)}><span className="repository-choice-heading"><span className="break-words font-mono text-sm font-medium">{entry.nameWithOwner}</span>{selectedId === entry.id && <span className="repository-selected">{t("已选")}</span>}</span><span className="mt-2 block text-xs text-muted-foreground">{formatNumber(entry.skillCount)} {t(" 个技能 · ")}{entry.manual ? t("手动收录") : t("同步收录")}</span><span className="repository-choice-branch">{entry.branch || t("默认分支")}</span></button>)}</div>{selectedId && <RepositorySkills key={selectedId} repositoryId={selectedId} />}</div>}
    </section>
    {session && <SavedRepositorySources key={session.token} owner={session.userId} busy={collection.isPending || importCollecting} onBusyChange={setSourcesBusy} onCollected={(repositoryId) => { setSelected(repositoryId); setFilter("") }} />}
    <details className="repository-import"><summary><ArrowDownToLine size={18} aria-hidden="true" /><span>{t("从其他工具导入")}</span><span className="repository-import-tools">Magpie / cc-switch</span><ChevronDown size={16} aria-hidden="true" /></summary><ImportRepositories key={session?.token ?? "signed-out"} collectionBusy={collection.isPending || sourcesBusy} onCollectingChange={setImportCollecting} /></details>
    <details className="discovered-repositories"><summary>{t("从 GitHub Lists 收录仓库")}</summary><div className="pt-5"><DiscoveredRepositories busy={collection.isPending || importCollecting || sourcesBusy} onCollect={(name) => { if (!collection.isPending && !importCollecting && !sourcesBusy) collection.mutate({ repository: name, branch: "", fromForm: false }) }} /></div></details>
  </div>
}
