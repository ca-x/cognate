import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router"
import { Code, ConnectError } from "@connectrpc/connect"
import { ArrowLeft, ArrowRight, ArrowUpRight, Check, ChevronDown, Compass, Copy, ExternalLink, FileText, GitBranch, Globe, Grid2X2, Info, List, Search, SlidersHorizontal, Terminal } from "lucide-react"
import { useApi } from "./api"
import { catalogCategoryPageSize, catalogMaxPages, catalogPageSize, publicInstallCommand, queryByteLength } from "./catalog"
import { CatalogError } from "./catalog-error"
import { Button } from "./components/ui/button"
import { SearchInput } from "./components/ui/search-input"
import { SpotlightCard } from "./components/react-bits/spotlight-card"
import type { PublicSkill } from "./gen/skillhub/catalog/v1/catalog_pb"
import "./explore.css"
import "./catalog-ui-i18n"

function CatalogSearch({ query, category }: { query: string; category: string }) {
  useTranslation()
  const { publicCatalog } = useApi()
  const navigate = useNavigate({ from: "/explore" })
  const [draft, setDraft] = useState(query)
  const [selected, setSelected] = useState(category)
  const [validation, setValidation] = useNotice()
  const [cursors, setCursors] = useState([""])
  const cursor = cursors.at(-1)!
  const categories = useQuery({ queryKey: ["catalog", "public", "categories", cursor], queryFn: ({ signal }) => publicCatalog.listCategories({ limit: catalogCategoryPageSize, cursor }, { signal }), staleTime: 0 })
  const next = categories.data?.nextCursor
  const repeatedCursor = Boolean(next && cursors.includes(next))
  function applyFilters(nextCategory: string) {
    if (queryByteLength(draft.trim()) > 128) { setValidation("搜索词最多 128 个 UTF-8 字节，中文等字符占用多个字节。"); return }
    setValidation("")
    void navigate({ search: { q: draft.trim() || undefined, category: nextCategory || undefined } })
  }
  function submit(event: FormEvent) {
    event.preventDefault()
    applyFilters(selected)
  }
  return <section className="catalog-search" aria-label={t("搜索与分类筛选")}>
    <form onSubmit={submit} className="catalog-search-form">
      <div className="min-w-0 space-y-2"><label htmlFor="catalog-query">{t("搜索技能")}</label><SearchInput id="catalog-query" placeholder={t("名称、描述或来源…")} maxLength={128} value={draft} onValueChange={(value) => { setDraft(value); setValidation("") }} clearLabel={t("清空技能搜索")} aria-describedby="catalog-query-help" aria-invalid={Boolean(validation)} /></div>
      <Button type="submit"><Search size={16} aria-hidden="true" />{t("查找技能")}</Button>
      <details className="catalog-filter-options"><summary><SlidersHorizontal size={16} aria-hidden="true" /><span>{t("分类筛选")}</span><span className="catalog-filter-summary">{category ? t("已按分类筛选") : t("全部分类与未分类技能")}</span><ChevronDown size={16} aria-hidden="true" /></summary>
      <div className="catalog-filter-body"><div className="min-w-0 space-y-2"><label htmlFor="catalog-category">{t("公开分类")}</label><select id="catalog-category" className="pack-select" value={selected} onChange={(event) => setSelected(event.target.value)}>
        <option value="">{t("全部分类与未分类技能")}</option>
        {selected && !categories.data?.categories.some((entry) => entry.id === selected) && <option value={selected}>{t("已选分类（不在本页或暂不可用）")}</option>}
        {!categories.isError && categories.data?.categories.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}（{formatNumber(entry.skillCount)}）</option>)}
      </select></div>
    {!categories.isError && Boolean(categories.data?.categories.length) && <div className="catalog-quick-categories" role="group" aria-label={t("快速分类")}><Button variant="ghost" aria-pressed={!category} onClick={() => applyFilters("")}>{t("全部技能")}</Button>{categories.data?.categories.slice(0, 6).map((entry) => <Button variant="ghost" key={entry.id} aria-pressed={category === entry.id} onClick={() => applyFilters(category === entry.id ? "" : entry.id)}><span>{entry.name}</span><span className="catalog-category-count">{formatNumber(entry.skillCount)}</span></Button>)}</div>}
    <p id="catalog-query-help" className="field-help catalog-query-help mt-2">{t("搜索词最多 128 个 UTF-8 字节。分类计数仅包含已公开的技能。")}</p>
    {categories.isPending ? <p role="status" className="mt-3 text-sm text-muted-foreground">{t("正在读取公开分类…")}</p> : categories.isError ? <CatalogError error={categories.error} retry={() => { void categories.refetch() }} /> : categories.data.categories.length === 0 && <p role="status" className="mt-3 text-sm text-muted-foreground">{t("本页暂无公开分类，仍可搜索未分类技能。")}</p>}
    {(cursors.length > 1 || next) && <nav className="catalog-category-pages" aria-label={t("分类分页")}>
      <span className="text-xs text-muted-foreground">{t("分类第 ")}{formatNumber(cursors.length)} {t(" 页，每页最多 ")}{formatNumber(catalogCategoryPageSize)} {t(" 项")}</span>
      {cursors.length > 1 && <Button variant="ghost" disabled={categories.isFetching} onClick={() => setCursors((current) => current.slice(0, -1))}>{t("上一页分类")}</Button>}
      {next && <Button variant="ghost" disabled={categories.isError || categories.isFetching || repeatedCursor || cursors.length >= catalogMaxPages} onClick={() => setCursors((current) => [...current, next])}>{t("更多分类")}<ArrowRight size={14} aria-hidden="true" /></Button>}
    </nav>}
    {(repeatedCursor || cursors.length >= catalogMaxPages && next) && <p role="status" className="mt-2 text-sm text-muted-foreground">{t("已达到本次分类浏览边界，请返回前页或重新打开目录。不会自动继续请求。")}</p>}
      </div></details>
    </form>
    {validation && <p role="alert" className="error-message mt-2">{validation}</p>}
  </section>
}

function SkillResults({ query, category, view, onViewChange }: { query: string; category: string; view: "grid" | "list"; onViewChange: (view: "grid" | "list") => void }) {
  useTranslation()
  const { publicCatalog } = useApi()
  const navigate = useNavigate({ from: "/explore" })
  const [cursors, setCursors] = useState([""])
  const cursor = cursors.at(-1)!
  const skills = useQuery({ queryKey: ["catalog", "public", "skills", query, category, cursor], queryFn: ({ signal }) => publicCatalog.listSkills({ query, categoryId: category, limit: catalogPageSize, cursor }, { signal }), staleTime: 0 })
  const next = skills.data?.nextCursor
  const repeatedCursor = Boolean(next && cursors.includes(next))
  return <section className="list-section" aria-labelledby="public-skills-title" aria-busy={skills.isFetching}>
    <div className="section-toolbar catalog-results-toolbar"><div><h2 id="public-skills-title">{t("公开技能")}</h2><span className="catalog-page-size">{t("第 ")}{formatNumber(cursors.length)} {t(" 页 · 每页最多 ")}{formatNumber(catalogPageSize)} {t(" 项")}</span></div><div className="catalog-view-switch" role="group" aria-label={t("技能显示方式")}><Button variant="ghost" aria-label={t("卡片视图")} aria-pressed={view === "grid"} onClick={() => onViewChange("grid")}><Grid2X2 size={16} aria-hidden="true" /><span>{t("卡片")}</span></Button><Button variant="ghost" aria-label={t("列表视图")} aria-pressed={view === "list"} onClick={() => onViewChange("list")}><List size={16} aria-hidden="true" /><span>{t("列表")}</span></Button></div></div>
    <div className="catalog-filter-context" role="group" aria-label={t("当前筛选")}><p>{query ? <><Search size={14} aria-hidden="true" /><span>{t("搜索：{{value1}}", { value1: query })}</span></> : <><Compass size={14} aria-hidden="true" /><span>{t("全部公开技能")}</span></>}{category && <span className="catalog-filter-applied">{t("已按分类筛选")}</span>}</p>{(query || category) && <Button variant="ghost" onClick={() => { void navigate({ search: { q: undefined, category: undefined } }) }}>{t("清除筛选")}</Button>}</div>
    {skills.isPending ? <div className="catalog-state" role="status"><FileText size={24} aria-hidden="true" /><h3>{t("正在读取公开技能…")}</h3></div> : skills.isError ? <div className="catalog-state catalog-state-error"><h3>{t("暂时无法读取目录")}</h3><CatalogError error={skills.error} retry={() => { void skills.refetch() }} /></div> : skills.data.skills.length === 0 ? <div className="catalog-state" role="status"><Compass size={28} aria-hidden="true" /><h3>{t("没有找到公开技能")}</h3><p>{t("试试其他关键词或分类。仓库只有经过明确发布才会出现在这里。")}</p></div> : <ul className="catalog-results" data-view={view}>{skills.data.skills.map((skill) => <li key={skill.id}><article className="catalog-skill">
      <div className="catalog-skill-main"><div className="catalog-file-icon"><FileText size={20} aria-hidden="true" /></div><div className="min-w-0"><h3><Link to="/explore/$skillId" params={{ skillId: skill.id }} className="catalog-skill-link">{skill.name || skill.slug}<ArrowUpRight size={17} aria-hidden="true" /></Link></h3><p className="catalog-description">{skill.description || t("发布者尚未提供描述。")}</p><p className="catalog-source"><GitBranch size={14} aria-hidden="true" /><span>{skill.repositoryName}</span></p></div></div>
      <div className="catalog-skill-context"><div className="catalog-tags">{skill.categories.length === 0 ? <span className="catalog-uncategorized">{t("未分类")}</span> : skill.categories.map((entry) => <Link key={entry.id} to="/explore" search={{ category: entry.id }} className="catalog-tag">{entry.name}</Link>)}</div><p className="catalog-path"><FileText size={13} aria-hidden="true" /><code>{skill.path === "." ? "SKILL.md" : `${skill.path}/SKILL.md`}</code></p></div>
    </article></li>)}</ul>}
    <nav aria-label={t("技能分页")} className="catalog-pagination">
      <Button variant="outline" disabled={cursors.length === 1 || skills.isFetching} onClick={() => setCursors((current) => current.slice(0, -1))}><ArrowLeft size={16} aria-hidden="true" />{t("上一页")}</Button>
      <span role="status" className="text-sm text-muted-foreground">{skills.isSuccess ? t("本页 {{value1}} 个技能", { value1: formatNumber(skills.data.skills.length) }) : t("尚未取得本页结果")}</span>
      <Button variant="outline" disabled={!next || skills.isError || skills.isFetching || repeatedCursor || cursors.length >= catalogMaxPages} onClick={() => { if (next) setCursors((current) => [...current, next]) }}>{t("下一页")}<ArrowRight size={16} aria-hidden="true" /></Button>
    </nav>
    {(repeatedCursor || cursors.length >= catalogMaxPages && next) && <p role="status" className="mt-3 text-sm text-muted-foreground">{t("已达到本次分页浏览边界，请缩小搜索范围。不会自动继续请求。")}</p>}
  </section>
}

export function ExplorePage() {
  useTranslation()
  const [view, setView] = useState<"grid" | "list">("grid")
  const search = useSearch({ from: "/explore" })
  const query = search.q ?? ""
  const category = search.category ?? ""
  const key = JSON.stringify([query, category])
  return <div className="public-catalog">
    <SpotlightCard className="page-heading catalog-hero"><div><h1>{t("探索可复用的技能")}</h1><p>{t("浏览这个 Cognate 上明确公开的技能，无需登录，也无需分享 token。")}</p></div><span className="catalog-access"><Globe size={16} aria-hidden="true" />{t("明确公开 · 匿名浏览")}</span></SpotlightCard>
    <CatalogSearch key={`search:${key}`} query={query} category={category} />
    {queryByteLength(query) > 128 ? <p role="alert" className="error-notice">{t("链接中的搜索词超过 128 个 UTF-8 字节。请缩短搜索词后重试，当前未请求技能列表。")}</p> : <SkillResults key={`skills:${key}`} query={query} category={category} view={view} onViewChange={setView} />}
  </div>
}

function SkillDetails({ skill }: { skill: PublicSkill }) {
  useTranslation()
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle")
  const command = publicInstallCommand(window.location.origin, skill.id)
  const githubSource = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(skill.repositoryName) ? `https://github.com/${skill.repositoryName}` : undefined
  async function copy() {
    try { await navigator.clipboard.writeText(command); setCopyState("copied") }
    catch { setCopyState("failed") }
  }
  return <article>
    <div className="page-heading"><div className="min-w-0"><p className="catalog-eyebrow"><Globe size={16} aria-hidden="true" /><span>{t("公开技能 · ")}{skill.slug}</span></p><h1 className="break-words">{skill.name || skill.slug}</h1><p className="whitespace-pre-line break-words">{skill.description || t("发布者尚未提供描述。")}</p></div></div>
    <div className="catalog-tags">{skill.categories.length === 0 ? <span className="text-sm text-muted-foreground">{t("未分类")}</span> : skill.categories.map((entry) => <Link key={entry.id} to="/explore" search={{ category: entry.id }} className="catalog-tag">{entry.name}</Link>)}</div>
    <div className="catalog-detail-grid"><section className="public-skill-section catalog-origin" aria-labelledby="skill-origin-title"><div className="catalog-panel-heading"><FileText size={19} aria-hidden="true" /><h2 id="skill-origin-title">{t("来源与内容标识")}</h2></div><dl className="public-skill-metadata">
      <div><dt>{t("仓库")}</dt><dd>{githubSource ? <a href={githubSource} target="_blank" rel="noreferrer" className="inline-flex min-h-11 items-center gap-2 text-primary underline underline-offset-4">{skill.repositoryName}<ExternalLink size={14} className="shrink-0" aria-hidden="true" /></a> : skill.repositoryName}</dd></div>
      <div><dt>{t("分支")}</dt><dd className="flex items-start gap-2"><GitBranch size={15} className="mt-1 shrink-0" aria-hidden="true" /><span>{skill.branch || t("默认分支")}</span></dd></div>
      <div><dt>{t("技能路径")}</dt><dd><code>{skill.path === "." ? "SKILL.md" : `${skill.path}/SKILL.md`}</code></dd></div>
      <div><dt>SHA-256</dt><dd><code>{skill.contentHash}</code></dd></div>
      <div><dt>{t("发布版本")}</dt><dd>{formatNumber(skill.publicationRevision)}</dd></div>
    </dl></section>
    <section className="public-skill-section catalog-install" aria-labelledby="public-install-title"><div className="catalog-panel-heading"><Terminal size={19} aria-hidden="true" /><h2 id="public-install-title">{t("匿名安装命令")}</h2></div><p className="catalog-install-intro">{t("命令只指向这个 Cognate，不需要登录、分享 token 或 MCP token。安装整个技能目录前，请先确认你信任其来源。")}</p><div className="install-command"><pre tabIndex={0} aria-label={t("匿名安装命令")}><code>{command}</code></pre><Button variant="outline" onClick={() => { void copy() }}>{copyState === "copied" ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}{copyState === "copied" ? t("已复制命令") : t("复制命令")}</Button></div><p className="catalog-copy-status" role="status">{copyState === "failed" ? t("浏览器无法复制，请手动选择命令。") : copyState === "copied" ? t("仅复制了命令，尚未执行安装。") : t("命令要求服务端和客户端均支持 install-public。复制不代表安装成功；本页不执行安装。")}</p><div className="catalog-install-boundary"><Info size={18} aria-hidden="true" /><div><h3>{t("公开不等于安全审查")}</h3><p>{t("发布者可随时取消公开；页面曾经可见不代表之后仍能下载。命令不绕过私有 Pack 的授权。")}</p></div></div></section></div>
  </article>
}

export function PublicSkillPage() {
  useTranslation()
  const { skillId } = useParams({ from: "/explore/$skillId" })
  const { publicCatalog } = useApi()
  const detail = useQuery<PublicSkill>({ queryKey: ["catalog", "public", "skill", skillId], queryFn: async ({ signal }) => { const result = await publicCatalog.getSkill({ skillId }, { signal }); if (!result.skill) throw new ConnectError("missing public skill", Code.DataLoss); return result.skill }, staleTime: 0 })
  return <div className="public-catalog public-catalog-detail">
    <Link to="/explore" className="catalog-back"><ArrowLeft size={16} aria-hidden="true" />{t("返回公开目录")}</Link>
    {detail.isPending ? <div className="catalog-state" role="status"><FileText size={24} aria-hidden="true" /><h1>{t("正在读取公开技能详情…")}</h1></div> : detail.isError ? <div className="catalog-state catalog-state-error"><h1>{t("暂时无法读取技能")}</h1><CatalogError error={detail.error} retry={() => { void detail.refetch() }} /></div> : <SkillDetails key={detail.data.id} skill={detail.data} />}
  </div>
}
