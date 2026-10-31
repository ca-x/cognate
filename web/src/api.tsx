import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Code, ConnectError, createClient, type Client } from "@connectrpc/connect"
import { createConnectTransport } from "@connectrpc/connect-web"
import { AuthService } from "./gen/skillhub/auth/v1/auth_pb"
import { GitHubService } from "./gen/skillhub/github/v1/github_pb"
import { LibraryService } from "./gen/skillhub/library/v1/library_pb"
import { PackService } from "./gen/skillhub/pack/v1/pack_pb"
import { CategoryService, PublicationService, PublicCatalogService } from "./gen/skillhub/catalog/v1/catalog_pb"
import { MCPManagementService } from "./gen/skillhub/mcp/v1/mcp_pb"
import { ImportService } from "./gen/skillhub/import/v1/import_pb"
import { ConfigurationService } from "./gen/skillhub/configuration/v1/configuration_pb"
import { LogManagementService } from "./gen/skillhub/management/v1/logging_pb"
import { BackupManagementService } from "./gen/skillhub/management/v1/backup_pb"

type Session = { token: string; userId: string; username: string }
type ApiContext = {
  session: Session | null
  setSession: (session: Session | null) => void
  auth: Client<typeof AuthService>
  github: Client<typeof GitHubService>
  library: Client<typeof LibraryService>
  packs: Client<typeof PackService>
  categories: Client<typeof CategoryService>
  publications: Client<typeof PublicationService>
  publicCatalog: Client<typeof PublicCatalogService>
  mcp: Client<typeof MCPManagementService>
  imports: Client<typeof ImportService>
  configuration: Client<typeof ConfigurationService>
  logManagement: Client<typeof LogManagementService>
  backupManagement: Client<typeof BackupManagementService>
  completingLogin: boolean
  loginNotice: string
}

const Api = createContext<ApiContext | null>(null)

export function ApiProvider({ children }: { children: ReactNode }) {
  useTranslation()
  const [queryClient] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } }))
  const [session, updateSession] = useState<Session | null>(null)
  const [completingLogin, setCompletingLogin] = useState(() => new URLSearchParams(window.location.search).get("oidc") === "complete")
  const [loginNotice, setLoginNotice] = useNotice()
  const claimed = useRef(false)
  const current = useRef<Session | null>(null)
  const publicCatalog = useMemo(() => createClient(PublicCatalogService, createConnectTransport({
    baseUrl: window.location.origin,
    defaultTimeoutMs: 30_000,
    fetch: (input, init) => globalThis.fetch(input, { ...init, credentials: "omit" }),
  })), [])
  const setSession = useCallback((next: Session | null) => {
    current.current = next
    queryClient.clear()
    updateSession(next)
  }, [queryClient])
  const clients = useMemo(() => {
    const transport = createConnectTransport({
      baseUrl: window.location.origin,
      defaultTimeoutMs: 30_000,
      interceptors: [(next) => async (request) => {
        if (session) request.header.set("Authorization", `Bearer ${session.token}`)
        try {
          return await next(request)
        } catch (error) {
          if (session && current.current?.token === session.token && ConnectError.from(error).code === Code.Unauthenticated) setSession(null)
          throw error
        }
      }],
    })
    return { auth: createClient(AuthService, transport), github: createClient(GitHubService, transport), library: createClient(LibraryService, transport), packs: createClient(PackService, transport), categories: createClient(CategoryService, transport), publications: createClient(PublicationService, transport), mcp: createClient(MCPManagementService, transport), imports: createClient(ImportService, transport), configuration: createClient(ConfigurationService, transport), logManagement: createClient(LogManagementService, transport), backupManagement: createClient(BackupManagementService, transport) }
  }, [session, setSession])
  const value = { session, setSession, ...clients, publicCatalog, completingLogin, loginNotice }
  useEffect(() => {
    const address = new URL(window.location.href)
    const state = address.searchParams.get("oidc")
    if (!state || claimed.current) return
    claimed.current = true
    address.searchParams.delete("oidc")
    window.history.replaceState(window.history.state, "", address)
    if (state !== "complete") { setLoginNotice("OpenID Connect 登录未完成，请重新开始。"); setCompletingLogin(false); return }
    void value.auth.completeOIDCLogin({}).then((result) => {
      setSession({ token: result.sessionToken, userId: result.userId, username: "OpenID Connect" })
    }).catch(() => setLoginNotice("OpenID Connect 登录已失效，请重新开始。")).finally(() => setCompletingLogin(false))
  }, [value.auth, setSession])
  return <QueryClientProvider client={queryClient}><Api value={value}>{children}</Api></QueryClientProvider>
}

export function redirectToOIDC(value: string) {
  const address = new URL(value)
  const local = address.hostname === "localhost" || address.hostname === "[::1]" || /^127(?:\.\d{1,3}){3}$/.test(address.hostname)
  if (address.protocol !== "https:" && !(address.protocol === "http:" && local)) throw new ConnectError("invalid authorization URL", Code.Internal)
  window.location.assign(address.href)
}

export function useApi() {
  const context = use(Api)
  if (!context) throw new Error("API provider is required")
  return context
}

export function errorMessage(error: unknown) {
  switch (ConnectError.from(error).code) {
    case Code.Unauthenticated: return t("用户名或密码不正确，或会话已过期。")
    case Code.AlreadyExists: return t("此用户名已存在，请直接登录。")
    case Code.InvalidArgument: return t("请检查输入格式后重试。")
    case Code.ResourceExhausted: return t("请求较多，请稍后再试。")
    case Code.Unavailable: return t("连接失败，请检查服务或 GitHub 授权后重试。")
    case Code.Aborted: return t("来源已改变，请刷新后重试。")
    case Code.FailedPrecondition: return t("请重新连接 GitHub 并同步来源。")
    case Code.DeadlineExceeded: return t("等待时间较长，请稍后重试。")
    default: return t("操作未完成，请重试。")
  }
}
