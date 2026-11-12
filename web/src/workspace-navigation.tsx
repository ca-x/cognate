import { useEffect, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { Archive, Check, ChevronRight, Compass, FileText, FolderGit2, GitFork, KeyRound, Layers, LogOut, Menu, Search, Settings, Tags } from "lucide-react"
import { t } from "./i18n"
import { Button } from "./components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import { SearchInput } from "./components/ui/search-input"

export const workspaceNavigation = [
  { label: "技能工作区", items: [
    { to: "/", label: "来源", icon: GitFork },
    { to: "/repositories", label: "仓库", icon: FolderGit2 },
    { to: "/packs", label: "组合", icon: Layers },
    { to: "/categories", label: "分类", icon: Tags },
    { to: "/explore", label: "探索", icon: Compass },
  ] },
  { label: "配置与管理", items: [
    { to: "/configuration", label: "AI 配置", icon: Settings },
    { to: "/mcp-settings", label: "MCP", icon: KeyRound },
    { to: "/server-logging", label: "服务端日志", icon: FileText },
    { to: "/server-backup", label: "服务端备份", icon: Archive },
    { to: "/account", label: "账号", icon: Settings },
  ] },
] as const

export function WorkspaceLinks({ onNavigate }: { onNavigate?: () => void }) {
  useTranslation()
  return <nav aria-label={t("主要导航")}>{workspaceNavigation.map((group) => <div className="navigation-group" key={group.label}><p className="sidebar-label">{t(group.label)}</p>{group.items.map((item) => <Link key={item.to} to={item.to} activeOptions={{ exact: item.to === "/" }} onClick={onNavigate}><item.icon size={18} aria-hidden="true" />{t(item.label)}</Link>)}</div>)}</nav>
}

export function MobileNavigation({ username, leaving, onLogout }: { username: string; leaving: boolean; onLogout: () => void }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 801px)")
    const closeOnDesktop = () => { if (desktop.matches) setOpen(false) }
    desktop.addEventListener("change", closeOnDesktop)
    return () => desktop.removeEventListener("change", closeOnDesktop)
  }, [])
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="ghost" className="workspace-menu-button" aria-label={t("打开导航")}><Menu size={20} aria-hidden="true" /></Button></DialogTrigger>
    <DialogContent className="workspace-navigation-dialog left-0 top-0 translate-x-0 translate-y-0" onOpenAutoFocus={(event) => {
      event.preventDefault()
      const destination = document.querySelector<HTMLAnchorElement>('.workspace-navigation-dialog nav a[data-status="active"]') ?? document.querySelector<HTMLAnchorElement>('.workspace-navigation-dialog nav a')
      destination?.focus({ preventScroll: true })
      destination?.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "instant" })
    }} onCloseAutoFocus={(event) => {
      if (window.matchMedia("(min-width: 801px)").matches) {
        event.preventDefault()
        const destination = document.querySelector<HTMLAnchorElement>('.sidebar nav a[data-status="active"]') ?? document.getElementById("main")
        destination?.focus({ preventScroll: true })
      }
    }}>
      <DialogTitle>Cognate</DialogTitle><DialogDescription>{t("选择要前往的工作区页面。")}</DialogDescription>
      <WorkspaceLinks onNavigate={() => setOpen(false)} />
      <div className="workspace-mobile-account"><div><p>{t("当前账号")}</p><span title={username}>{username}</span></div><Button variant="ghost" disabled={leaving} onClick={onLogout}><LogOut size={16} aria-hidden="true" />{leaving ? t("退出中…") : t("退出")}</Button></div>
    </DialogContent>
  </Dialog>
}

export function QuickNavigation({ pathname }: { pathname: string }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  function changeOpen(next: boolean) { setQuery(""); setOpen(next) }
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.repeat || event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "k") return
      if (!open && document.querySelector('[data-slot="dialog-content"], [data-slot="select-content"]')) return
      event.preventDefault()
      setQuery("")
      setOpen(!open)
    }
    document.addEventListener("keydown", shortcut)
    return () => document.removeEventListener("keydown", shortcut)
  }, [open])
  const search = query.normalize("NFKC").trim().toLocaleLowerCase()
  const groups = workspaceNavigation.map((group) => ({ ...group, items: group.items.filter((item) => `${item.label} ${t(item.label)} ${item.to}`.normalize("NFKC").toLocaleLowerCase().includes(search)) }))
  const count = groups.reduce((total, group) => total + group.items.length, 0)
  return <Dialog open={open} onOpenChange={changeOpen}>
    <DialogTrigger asChild><Button variant="outline" className="workspace-quick-navigation" aria-label={t("快速导航")}><Search size={16} aria-hidden="true" /><span>{t("快速导航")}</span><kbd aria-hidden="true">⌘ / Ctrl K</kbd></Button></DialogTrigger>
    <DialogContent className="workspace-quick-dialog" onEscapeKeyDown={(event) => {
      if (event.isComposing) { event.preventDefault(); return }
      if (query) {
        event.preventDefault()
        setQuery("")
        document.getElementById("workspace-navigation-query")?.focus({ preventScroll: true })
      }
    }}>
      <DialogTitle>{t("快速导航")}</DialogTitle><DialogDescription>{t("查找功能并跳转，不搜索或读取私人技能内容。")}</DialogDescription>
      <label htmlFor="workspace-navigation-query" className="sr-only">{t("查找页面")}</label>
      <SearchInput id="workspace-navigation-query" className="workspace-navigation-query" value={query} onValueChange={setQuery} clearLabel={t("清空页面搜索")} placeholder={t("仓库、AI 配置、备份…")} aria-controls="workspace-navigation-results" />
      <div id="workspace-navigation-results" className="workspace-navigation-results">
        {count === 0 ? <p className="workspace-navigation-empty" role="status">{t("没有匹配的页面，试试其他名称。")}</p> : groups.filter((group) => group.items.length > 0).map((group) => <div className="workspace-navigation-result-group" key={group.label}><h3>{t(group.label)}</h3><ul>{group.items.map((item) => <li key={item.to}><Link to={item.to} activeOptions={{ exact: item.to === "/" }} onClick={() => changeOpen(false)}><item.icon size={18} aria-hidden="true" /><span>{t(item.label)}</span>{item.to === pathname ? <Check size={16} aria-label={t("当前页面")} /> : <ChevronRight size={16} aria-hidden="true" />}</Link></li>)}</ul></div>)}
      </div>
      <p className="workspace-navigation-hint">{t("Tab 选择页面，Enter 前往；Esc 清空搜索，再按关闭。")}</p>
    </DialogContent>
  </Dialog>
}
