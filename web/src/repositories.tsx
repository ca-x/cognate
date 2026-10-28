import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { CircleAlert, ExternalLink, FileText, FolderGit2, GitBranch, Plus, RefreshCw, Search } from "lucide-react"
import { errorMessage, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import { DiscoveredRepositories } from "./sources"
import { PublicationControl } from "./publication"
import { SkillCategories } from "./skill-categories"
import { CatalogError } from "./catalog-error"
import { ImportRepositories } from "./import-repositories"
import { RepositoryRefreshControl } from "./repository-refresh"

function collectionError(error: unknown) {
  switch (ConnectError.from(error).code) {
    case Code.InvalidArgument: return t("请输入 owner/repo 或 GitHub HTTPS 地址，并检查分支名。")
    case Code.NotFound: return t("无法获取公开归档，请检查仓库和分支是否存在。当前暂不支持私有仓库内容。")
    case Code.FailedPrecondition: return t("未收录：归档中没有可用的 SKILL.md，或文件格式不安全。")
    case Code.DataLoss: return t("已保存的仓库归档缺失或损坏，当前快照不能使用。请检查服务端数据与备份；程序不会静默覆盖既有快照。")
    case Code.ResourceExhausted: return t("收录达到容量或大小限制，或 GitHub 暂时限流。请稍后重试，或选择较小的仓库。")
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
  if (query.isError) return <section className="library-detail"><LibraryError error={query.error} retry={() => { void query.refetch() }} /></section>
  const repository = query.data.repository
  const assignmentError = assignments.find((entry) => entry.isError)?.error
  const loadingAssignments = assignments.some((entry) => entry.isPending)
  const skills = query.data.skills.filter((entry, index) => `${entry.name} ${entry.description} ${entry.path}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()) && (!categoryId || assignments[index]?.data?.categories.some((category) => category.id === categoryId)))
  return <section className="library-detail" aria-label={t("仓库技能")}>
    <div className="flex items-start justify-between gap-3"><div className="min-w-0"><p className="mb-2 text-xs text-muted-foreground">{t("仓库快照 · ")}{formatNumber(query.data.skills.length)} {t(" 个技能")}</p><h2 className="break-words font-mono text-base font-semibold">{repository?.nameWithOwner}</h2><p className="mt-2 flex items-center gap-2 text-xs text-muted-foreground"><GitBranch size={13} aria-hidden="true" /><span className="break-all">{repository?.branch || t("默认分支")}</span></p></div><a className="external-link" href={`https://github.com/${repository?.nameWithOwner}`} target="_blank" rel="noreferrer" aria-label={t("在 GitHub 打开当前仓库")}><ExternalLink size={16} aria-hidden="true" /></a></div>
    {repository && <RepositoryRefreshControl key={session?.userId} repository={repository} />}
    <PublicationControl repositoryId={repositoryId} repositoryName={repository?.nameWithOwner ?? ""} />
    <div className="filter-field my-5 w-full"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选已收录技能")} placeholder={t("按名称、描述或路径筛选…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div>
    <div className="mb-5 space-y-2"><label htmlFor="owner-skill-category">{t("按我的分类筛选")}</label><select id="owner-skill-category" className="pack-select" value={categoryId} onChange={(event) => setCategoryId(event.target.value)} disabled={categoryQuery.isPending || categoryQuery.isError}><option value="">{t("全部技能")}</option>{categoryId && !categoryQuery.data?.categories.some((entry) => entry.id === categoryId) && <option value={categoryId}>{t("已选分类（已变更）")}</option>}{categoryQuery.data?.categories.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}（{formatNumber(entry.skillCount)}）</option>)}</select>{categoryQuery.isPending && <p role="status" className="field-help">{t("正在读取分类…")}</p>}{categoryQuery.isError && <CatalogError error={categoryQuery.error} retry={() => { void categoryQuery.refetch() }} />}</div>
    {loadingAssignments ? <p role="status" className="py-6 text-sm text-muted-foreground">{t("正在读取技能分类…")}</p> : assignmentError ? <CatalogError error={assignmentError} retry={() => { assignments.forEach((entry) => { void entry.refetch() }) }} /> : skills.length === 0 ? <p className="py-6 text-sm text-muted-foreground" role="status">{t("没有匹配的技能，试试其他关键词或分类。")}</p> : <div className="skill-list">{skills.map((entry) => <article key={entry.id} className="skill-card"><div className="flex items-start gap-3"><FileText size={17} className="mt-1 shrink-0 text-muted-foreground" aria-hidden="true" /><div className="min-w-0"><h3 className="break-words text-sm font-semibold">{entry.name}</h3><p className="mt-2 whitespace-pre-line break-words text-sm leading-6 text-muted-foreground">{entry.description}</p></div></div><div className="skill-metadata"><code>{entry.path === "." ? "SKILL.md" : `${entry.path}/SKILL.md`}</code><span title={entry.contentHash}>SHA-256 <code>{entry.contentHash.slice(0, 12)}</code></span></div><div className="mt-3"><SkillCategories skillId={entry.id} skillName={entry.name} /></div></article>)}</div>}
    <p className="mt-5 text-xs leading-6 text-muted-foreground">{t("技能内容已保存在服务端。可在「组合」中整理；本页不执行安装，客户端安装和更新需另行验证。")}</p>
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
  const active = useRef<AbortController | null>(null)
  const key = ["library-repositories", session?.userId]
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => library.listRepositories({}, { signal }) })
  useEffect(() => () => active.current?.abort(), [])
  const collection = useMutation({
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
      if (input.fromForm) { setRepository(""); setBranch("") }
    },
    onError: (error) => { if (ConnectError.from(error).code === Code.Canceled) setNotice("已停止等待。如果服务端已经完成收录，刷新后仍会显示结果。") },
    onSettled: () => { void queryClient.invalidateQueries({ queryKey: key }) },
  })
  function submit(event: FormEvent) { event.preventDefault(); if (!collection.isPending && !importCollecting) collection.mutate({ repository, branch, fromForm: true }) }
  const repositories = query.data?.repositories ?? []
  const visible = repositories.filter((entry) => `${entry.nameWithOwner} ${entry.branch}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()))
  const selectedId = repositories.some((entry) => entry.id === selected) ? selected : repositories[0]?.id
  return <>
    <div className="page-heading"><div><h1>{t("技能仓库")}</h1><p>{t("先收录内容，再把需要的技能带到你的工作流。")}</p></div><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch(); void queryClient.invalidateQueries({ queryKey: ["repository-skills", session?.userId] }) }}><RefreshCw size={15} aria-hidden="true" />{t("刷新")}</Button></div>
    <section className="source-card" aria-labelledby="collect-title">
      <div className="source-card-heading"><span className="service-icon"><Plus size={22} aria-hidden="true" /></span><div><h2 id="collect-title" className="font-semibold">{t("添加 GitHub 仓库")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("整仓拉取并扫描，找到技能后才会收录。")}</p></div></div>
      <form className="source-card-body" onSubmit={submit} aria-busy={collection.isPending}>
        <div className="collection-fields"><div className="space-y-2"><label htmlFor="repository-input">{t("公开仓库")}</label><Input id="repository-input" name="repository" placeholder={t("owner/repo 或 https://github.com/owner/repo")} autoComplete="off" maxLength={512} required disabled={collection.isPending} value={repository} onChange={(event) => setRepository(event.target.value)} aria-describedby="collection-help" /></div><div className="space-y-2"><label htmlFor="branch-input">{t("分支 ")}<span className="font-normal text-muted-foreground">{t("（可选）")}</span></label><Input id="branch-input" name="branch" placeholder={t("默认分支")} autoComplete="off" maxLength={256} disabled={collection.isPending} value={branch} onChange={(event) => setBranch(event.target.value)} /></div></div>
        <p className="field-help mt-3" id="collection-help">{t("归档上限 32 MiB，解压后 64 MiB、最多 512 个技能；符号链接不跟随。留空分支或填写 HEAD 都使用默认分支。")}</p>
        <div className="mt-5 flex flex-wrap items-center gap-3"><Button type="submit" disabled={collection.isPending || importCollecting || !repository.trim()}><FolderGit2 size={16} aria-hidden="true" />{collection.isPending ? t("正在拉取并扫描…") : t("收录并扫描")}</Button>{collection.isPending && <Button variant="ghost" onClick={() => active.current?.abort()}>{t("停止等待")}</Button>}</div>
        {collection.isError && ConnectError.from(collection.error).code !== Code.Canceled && <LibraryError error={collection.error} />}
        <p className="mt-3 min-h-6 text-sm leading-6 text-muted-foreground" role="status" aria-live="polite">{collection.isPending ? t("服务端正在获取仓库和校验技能内容，请稍候。") : notice}</p>
      </form>
    </section>
    <ImportRepositories key={session?.token ?? "signed-out"} collectionBusy={collection.isPending} onCollectingChange={setImportCollecting} />
    <section className="list-section" aria-labelledby="library-title">
      <div className="section-toolbar"><div><h2 id="library-title" className="font-semibold">{t("已收录 ")}<span className="ml-2 font-normal text-muted-foreground">{formatNumber(repositories.length)}</span></h2><p className="mt-1 text-sm text-muted-foreground">{t("这里列出收录记录；打开仓库后会校验归档并读取技能。")}</p></div><div className="filter-field"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选已收录仓库")} placeholder={t("按仓库或分支筛选…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div></div>
      {query.isPending ? <p role="status" className="py-8 text-sm text-muted-foreground">{t("正在读取已收录仓库…")}</p> : query.isError ? <LibraryError error={query.error} retry={() => { void query.refetch() }} /> : repositories.length === 0 ? <div className="list-surface empty-state"><FolderGit2 size={26} className="empty-icon" aria-hidden="true" /><h3 className="font-medium">{t("资料库还是空的")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("添加第一个公开技能仓库，或从下方的 Lists 中选择。")}</p></div> : <div className="library-workbench"><div className="collected-repositories" role="group" aria-label={t("选择已收录的仓库")}>{visible.length === 0 ? <p className="p-5 text-sm text-muted-foreground" role="status">{t("没有匹配的仓库。")}</p> : visible.map((entry) => <button type="button" key={entry.id} className="collected-repository-select" aria-pressed={selectedId === entry.id} onClick={() => setSelected(entry.id)}><span className="break-words font-mono text-sm font-medium">{entry.nameWithOwner}</span><span className="mt-2 block text-xs text-muted-foreground">{formatNumber(entry.skillCount)} {t(" 个技能 · ")}{entry.manual ? t("手动收录") : t("同步收录")}</span><span className="mt-1 block break-all text-xs text-muted-foreground">{entry.branch || t("默认分支")}</span></button>)}</div>{selectedId && <RepositorySkills key={selectedId} repositoryId={selectedId} />}</div>}
    </section>
    <details className="discovered-repositories"><summary>{t("从 GitHub Lists 收录仓库")}</summary><div className="pt-5"><DiscoveredRepositories busy={collection.isPending || importCollecting} onCollect={(name) => { if (!collection.isPending && !importCollecting) collection.mutate({ repository: name, branch: "", fromForm: false }) }} /></div></details>
  </>
}
