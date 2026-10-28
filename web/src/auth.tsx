import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useState, type FormEvent } from "react"
import { useMutation, useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { ArrowRight, FolderGit2, HardDrive, KeyRound, Layers, ShieldCheck } from "lucide-react"
import { useApi, errorMessage, redirectToOIDC } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"
import logo from "../../assets/branding/server-web.png"
import { Preferences } from "./preferences"

export function AuthScreen() {
  useTranslation()
  const { auth, setSession, loginNotice } = useApi()
  const options = useQuery({ queryKey: ["login-options"], queryFn: ({ signal }) => auth.getLoginOptions({}, { signal }) })
  const oidc = useMutation({ mutationFn: async () => { const result = await auth.startOIDCLogin({}); redirectToOIDC(result.authorizationUrl) } })
  const [mode, setMode] = useState<"login" | "register">("login")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [notice, setNotice] = useNotice()
  const login = useMutation({
    mutationFn: async () => {
      if (mode === "register") {
        await auth.register({ username, password })
        setMode("login")
        setNotice("账号已创建，现在可以登录。")
      }
      const result = await auth.login({ username, password })
      const identity = await auth.getSession({}, { headers: { Authorization: `Bearer ${result.sessionToken}` } })
      return { token: result.sessionToken, userId: identity.userId, username }
    },
    onSuccess: (session) => { setPassword(""); setSession(session) },
  })
  function submit(event: FormEvent) { event.preventDefault(); login.mutate() }
  return <main className="auth-layout">
    <aside className="auth-story">
      <div className="brand"><img src={logo} width="48" height="48" alt="" /><span>Cognate</span></div>
      <div className="auth-intro">
        <span className="text-sm text-slate-400">{t("你的 skill 工作空间")}</span>
        <h1>{t("好用的技能，")}<br />{t("随处可用。")}</h1>
        <p>{t("收录仓库、整理组合，让每个 coding agent")}<br className="hidden lg:block" /> {t(" 都用上你选好的 skills。")}</p>
        <div className="source-flow" role="img" aria-label={t("GitHub 仓库经过服务端聚合后同步到本地 agents")}>
          <div><FolderGit2 aria-hidden="true" /><span>{t("仓库")}</span></div><ArrowRight aria-hidden="true" size={16} />
          <div><Layers aria-hidden="true" /><span>{t("组合")}</span></div><ArrowRight aria-hidden="true" size={16} />
          <div><HardDrive aria-hidden="true" /><span>Agents</span></div>
        </div>
      </div>
      <p className="text-xs text-slate-400">{t("自行托管，按你的方式整理。")}</p>
    </aside>
    <section className="auth-form-area" aria-labelledby="auth-title">
      <div className="auth-form-content">
        <div className="mb-7"><Preferences /></div>
        <div className="mb-9"><h2 id="auth-title" className="text-2xl font-semibold tracking-tight">{mode === "login" ? t("登录你的工作空间") : t("创建你的账号")}</h2><p className="mt-2 text-sm text-muted-foreground">{t("使用此 Cognate 服务上的本地账号。")}</p></div>
        <form onSubmit={submit} className="space-y-5" aria-busy={login.isPending}>
          {loginNotice && <p role="alert" className="error-message">{loginNotice}</p>}
          <div className="space-y-2"><label htmlFor="username">{t("用户名")}</label><Input id="username" name="username" autoComplete="username" minLength={3} maxLength={64} pattern="[A-Za-z0-9_.-]{3,64}" required value={username} onChange={(event) => setUsername(event.target.value)} aria-describedby="username-help" /><p id="username-help" className="field-help">{t("3–64 位字母、数字、点、下划线或连字符。")}</p></div>
          <div className="space-y-2"><label htmlFor="password">{t("密码")}</label><Input id="password" name="password" type="password" autoComplete={mode === "login" ? "current-password" : "new-password"} minLength={8} maxLength={1024} required value={password} onChange={(event) => setPassword(event.target.value)} aria-describedby="password-help" /><p id="password-help" className="field-help">{t("至少 8 位。")}</p></div>
          {notice && <p role="status" className="text-sm text-muted-foreground">{notice}</p>}
          {login.isError && <p role="alert" className="error-message">{errorMessage(login.error)}</p>}
          <Button type="submit" className="w-full" disabled={login.isPending}>{login.isPending ? t("正在连接…") : mode === "login" ? t("登录") : t("创建账号并登录")}<ArrowRight size={16} aria-hidden="true" /></Button>
        </form>
        <div className="mt-5 flex items-center justify-center gap-1 text-sm text-muted-foreground">{mode === "login" ? t("还没有账号？") : t("已有账号？")}<Button variant="ghost" className="px-2 text-primary" disabled={login.isPending} onClick={() => { setMode(mode === "login" ? "register" : "login"); login.reset(); setNotice("") }}>{mode === "login" ? t("创建账号") : t("返回登录")}</Button></div>
        {options.data?.oidcEnabled && <div className="mt-5 border-t border-border pt-5"><Button variant="outline" className="w-full" disabled={oidc.isPending || login.isPending} onClick={() => oidc.mutate()}><KeyRound size={16} aria-hidden="true" />{oidc.isPending ? t("正在前往登录…") : t("使用 OpenID Connect 登录")}</Button>{oidc.isError && <p className="error-message mt-3" role="alert">{errorMessage(oidc.error)}</p>}</div>}
        <Link to="/explore" className="mt-6 inline-flex min-h-11 items-center gap-2 text-sm text-primary underline underline-offset-4">{t("免登录探索公开技能")}<ArrowRight size={16} aria-hidden="true" /></Link>
        <p className="mt-10 flex items-center justify-center gap-2 text-xs text-muted-foreground"><ShieldCheck size={15} aria-hidden="true" />{t("会话仅保留在当前页面，刷新后需重新登录。")}</p>
      </div>
    </section>
  </main>
}
