import { t } from "./i18n"
import "./server-logging-i18n"
import { useTranslation } from "react-i18next"
import { lazy, StrictMode, Suspense, useEffect, useState } from "react"
import { createRoot } from "react-dom/client"
import { createRootRoute, createRoute, createRouter, Link, Outlet, RouterProvider, useRouterState } from "@tanstack/react-router"
import { Compass, FileText, FolderGit2, GitFork, KeyRound, Layers, LogOut, Server, Settings, Tags } from "lucide-react"
import { ApiProvider, useApi } from "./api"
import { Button } from "./components/ui/button"
import { useMotionRef } from "./lib/motion"
import { validateCatalogSearch } from "./catalog"
import { Preferences } from "./preferences"
import logo from "../../assets/branding/server-web.png"
import "./styles.css"

const SourcesPage = lazy(() => import("./sources").then((module) => ({ default: module.SourcesPage })))
const RepositoriesPage = lazy(() => import("./repositories").then((module) => ({ default: module.RepositoriesPage })))
const AccountPage = lazy(() => import("./account").then((module) => ({ default: module.AccountPage })))
const PacksPage = lazy(() => import("./packs").then((module) => ({ default: module.PacksPage })))
const AuthScreen = lazy(() => import("./auth").then((module) => ({ default: module.AuthScreen })))
const ExplorePage = lazy(() => import("./explore").then((module) => ({ default: module.ExplorePage })))
const PublicSkillPage = lazy(() => import("./explore").then((module) => ({ default: module.PublicSkillPage })))
const CategoriesPage = lazy(() => import("./categories").then((module) => ({ default: module.CategoriesPage })))
const MCPSettingsPage = lazy(() => import("./mcp-settings").then((module) => ({ default: module.MCPSettingsPage })))
const ConfigurationPage = lazy(() => import("./configuration").then((module) => ({ default: module.ConfigurationPage })))
const ServerLoggingPage = lazy(() => import("./server-logging").then((module) => ({ default: module.ServerLoggingPage })))

function MCPSettingsRoute() {
  const { session, mcp } = useApi()
  return session ? <MCPSettingsPage key={session.userId} client={mcp} owner={session.userId} /> : null
}

function PublicWorkspace() {
  useTranslation()
  return <div className="public-workspace">
    <a className="skip-link" href="#main">{t("跳到主要内容")}</a>
    <header className="public-topbar"><Link to="/explore" className="brand"><img src={logo} width="38" height="38" alt="" /><span>Cognate</span></Link><nav aria-label={t("公开导航")}><Link to="/explore" activeOptions={{ exact: true }}>{t("探索")}</Link><Link to="/" className="public-login">{t("登录管理")}</Link></nav><Preferences /></header>
    <main id="main" className="page-content" tabIndex={-1}><Suspense fallback={<p role="status">{t("正在读取公开目录…")}</p>}><Outlet /></Suspense></main>
    <footer className="workspace-footer">Cognate <span aria-hidden="true">·</span> {t(" 公开技能目录 ")}<span aria-hidden="true">·</span> {t(" 开发预览")}</footer>
  </div>
}

function Workspace() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const pageRef = useMotionRef<HTMLDivElement>()
  useEffect(() => {
    const keyboard = () => {
      document.documentElement.dataset.input = "keyboard"
      document.querySelectorAll<HTMLElement>('[data-motion="on"]').forEach((node) => { node.dataset.motion = "off" })
    }
    const pointer = () => { document.documentElement.dataset.input = "pointer" }
    document.addEventListener("keydown", keyboard, true)
    document.addEventListener("pointerdown", pointer, true)
    return () => {
      document.removeEventListener("keydown", keyboard, true)
      document.removeEventListener("pointerdown", pointer, true)
    }
  }, [])
  useTranslation()
  const { session, setSession, auth, completingLogin } = useApi()
  const [leaving, setLeaving] = useState(false)
  const publicRoute = useRouterState({ select: (state) => /^\/explore(?:\/[^/]+)?\/?$/.test(state.location.pathname) && state.matches.some((match) => match.routeId === "/explore" || match.routeId === "/explore/$skillId") })
  if (completingLogin) return <main className="empty-state" role="status">{t("正在完成登录…")}</main>
  if (!session && publicRoute) return <PublicWorkspace />
  if (!session) return <Suspense fallback={<main className="empty-state" role="status">{t("正在读取登录界面…")}</main>}><AuthScreen /></Suspense>
  async function logout() {
    setLeaving(true)
    try { await auth.logout({}) } finally { setSession(null); setLeaving(false) }
  }
  return <div className="workspace">
    <a className="skip-link" href="#main">{t("跳到主要内容")}</a>
    <aside className="sidebar">
      <Link to="/" className="brand"><img src={logo} width="42" height="42" alt="" /><span>Cognate</span></Link>
      <p className="sidebar-label">{t("工作空间")}</p>
      <nav aria-label={t("主要导航")}><Link to="/" activeOptions={{ exact: true }}><GitFork size={18} aria-hidden="true" />{t("来源")}</Link><Link to="/repositories"><FolderGit2 size={18} aria-hidden="true" />{t("仓库")}</Link><Link to="/packs"><Layers size={18} aria-hidden="true" />{t("组合")}</Link><Link to="/categories"><Tags size={18} aria-hidden="true" />{t("分类")}</Link><Link to="/explore"><Compass size={18} aria-hidden="true" />{t("探索")}</Link><Link to="/mcp-settings"><KeyRound size={18} aria-hidden="true" />{t("MCP")}</Link><Link to="/configuration"><Settings size={18} aria-hidden="true" />{t("AI 配置")}</Link><Link to="/server-logging"><FileText size={18} aria-hidden="true" />{t("服务端日志")}</Link><Link to="/account"><Settings size={18} aria-hidden="true" />{t("账号")}</Link></nav>
      <div className="sidebar-bottom"><Server size={15} aria-hidden="true" /><span>{t("自托管工作空间")}</span></div>
    </aside>
    <div className="workspace-body">
      <header className="topbar"><span className="workspace-context"><Layers size={16} aria-hidden="true" />{t("我的资料库")}</span><Preferences /><div className="flex min-w-0 items-center gap-3"><span className="max-w-36 truncate text-sm">{session.username}</span><Button variant="ghost" disabled={leaving} onClick={() => { void logout().catch(() => {}) }}><LogOut size={16} aria-hidden="true" />{leaving ? t("退出中…") : t("退出")}</Button></div></header>
      <main id="main" className="page-content" tabIndex={-1}><div key={pathname} ref={pageRef} className="page-view"><Suspense fallback={<p role="status">{t("正在读取工作空间…")}</p>}><Outlet /></Suspense></div></main>
      <footer className="workspace-footer">Cognate <span aria-hidden="true">·</span> {t(" 开发预览")}</footer>
    </div>
  </div>
}

function NotFoundPage() {
  useTranslation()
  return <div className="empty-state"><h1 className="text-xl font-semibold">{t("找不到这个页面")}</h1><Link to="/" className="mt-4 text-primary underline">{t("返回来源")}</Link></div>
}

function RouteError() {
  useTranslation()
  return <p role="alert">{t("页面加载失败，请刷新重试。")}</p>
}

const rootRoute = createRootRoute({ component: Workspace, notFoundComponent: NotFoundPage, errorComponent: RouteError })
const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: SourcesPage })
const repositoriesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/repositories", component: RepositoriesPage })
const accountRoute = createRoute({ getParentRoute: () => rootRoute, path: "/account", component: AccountPage })
const packsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/packs", component: PacksPage })
const categoriesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/categories", component: CategoriesPage })
const mcpSettingsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/mcp-settings", component: MCPSettingsRoute })
const configurationRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configuration", component: ConfigurationPage })
const serverLoggingRoute = createRoute({ getParentRoute: () => rootRoute, path: "/server-logging", component: ServerLoggingPage })
const exploreRoute = createRoute({ getParentRoute: () => rootRoute, path: "/explore", validateSearch: validateCatalogSearch, component: ExplorePage })
const publicSkillRoute = createRoute({ getParentRoute: () => rootRoute, path: "/explore/$skillId", component: PublicSkillPage })
const router = createRouter({ routeTree: rootRoute.addChildren([indexRoute, repositoriesRoute, packsRoute, accountRoute, categoriesRoute, mcpSettingsRoute, configurationRoute, serverLoggingRoute, exploreRoute, publicSkillRoute]) })

declare module "@tanstack/react-router" { interface Register { router: typeof router } }

createRoot(document.getElementById("root")!).render(<StrictMode><ApiProvider><RouterProvider router={router} /></ApiProvider></StrictMode>)
