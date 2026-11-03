import { t } from "./i18n"
import "./auth-ui-i18n"
import "./auth-ui.css"
import { useTranslation } from "react-i18next"
import { useCallback, useRef } from "react"
import { useMutation, useQuery } from "@tanstack/react-query"
import { Fingerprint, KeyRound, RefreshCw, ShieldCheck } from "lucide-react"
import { errorMessage, redirectToOIDC, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Input } from "./components/ui/input"

export function AccountPage() {
  useTranslation()
  const { auth, session } = useApi()
  const refreshRef = useRef<HTMLButtonElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)
  const preserveOIDCFocus = useCallback((button: HTMLButtonElement | null) => {
    if (!button) return
    return () => { if (document.activeElement === button) refreshRef.current?.focus() }
  }, [])
  const options = useQuery({ queryKey: ["login-options"], queryFn: ({ signal }) => auth.getLoginOptions({}, { signal }) })
  const bind = useMutation({ mutationFn: async () => { const result = await auth.startOIDCLogin({ linkCurrentAccount: true }); redirectToOIDC(result.authorizationUrl) } })
  const bindings = useQuery({ queryKey: ["oidc-bindings", session?.userId], queryFn: ({ signal }) => auth.getOIDCBindings({}, { signal }), enabled: !!session })
  const unlink = useMutation({ mutationFn: async (identityId: bigint) => {
    const password = passwordRef.current?.value ?? ""
    if (passwordRef.current) passwordRef.current.value = ""
    await auth.unlinkOIDC({ identityId, password })
  }, onSuccess: async () => { await bindings.refetch() } })
  const state = options.isError ? t("状态未知") : options.isPending ? t("正在检查") : options.data?.oidcEnabled ? t("可用") : t("未启用")
  return <div className="account-ui">
    <div className="page-heading"><div><h1>{t("账号与登录")}</h1><p>{t("为当前工作空间配置登录方式。")}</p></div></div>
    <section className="account-identity" aria-labelledby="account-identity-title">
      <div className="account-section-title"><Fingerprint size={23} aria-hidden="true" /><h2 id="account-identity-title">{t("当前会话身份")}</h2></div>
      <dl><div><dt>{t("账号显示名称")}</dt><dd>{session?.username}</dd></div><div><dt>{t("用户标识")}</dt><dd className="account-user-id">{session?.userId}</dd></div></dl>
      <p>{t("仅显示当前会话信息，不代表已绑定的身份清单。")}</p>
    </section>
    <section className="account-login-card" aria-labelledby="account-oidc-title">
      <div className="account-login-heading"><div className="service-icon"><KeyRound size={24} aria-hidden="true" /></div><div><span className="auth-eyebrow">{t("身份绑定")}</span><h2 id="account-oidc-title">OpenID Connect</h2></div><span className="account-method-state" role="status">{state}</span></div>
      <div className="account-login-body">
        <div className="space-y-3">
          <h3 className="font-medium">{t("已关联的 OIDC 身份")}</h3>
          {bindings.isPending ? <p role="status">{t("正在读取登录设置…")}</p> : bindings.isError ? <p role="alert">{t("无法读取关联身份，请重试。")}</p> : bindings.data?.bindings.length ? <>
            <ul className="space-y-2">{bindings.data.bindings.map((identity) => <li key={String(identity.identityId)} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border p-3"><span className="min-w-0 break-all">{identity.issuer}</span><Button variant="outline" disabled={!bindings.data.canUnlink || unlink.isPending || bind.isPending} onClick={() => unlink.mutate(identity.identityId)}>{t("取消 OIDC 关联")}</Button></li>)}</ul>
            {bindings.data.canUnlink ? <div className="space-y-2"><label htmlFor="oidc-unlink-password">{t("输入当前账号密码以取消关联")}</label><Input ref={passwordRef} id="oidc-unlink-password" type="password" autoComplete="current-password" maxLength={1024} disabled={unlink.isPending} /><p className="text-sm text-muted-foreground">{t("取消关联后保留当前会话，退出其他会话。之后仍可使用原用户名和密码登录。")}</p></div> : <p>{t("此账号没有本地密码，不能移除唯一登录方式。")}</p>}
          </> : <p>{t("尚未关联 OIDC 身份。")}</p>}
          {bindings.isError && <Button variant="outline" onClick={() => void bindings.refetch()}>{t("重试读取登录方式")}</Button>}
          {unlink.isError && <p role="alert" className="error-message">{t("取消关联失败，请检查当前账号密码并重试。")}</p>}
          {unlink.isSuccess && <p role="status">{t("OIDC 关联已取消。")}</p>}
        </div>
        <p>{t("主动绑定后，该 OIDC 身份会登录到当前账号。已属于其他账号的身份不会被转移，也不会按邮箱自动合并。")}</p>
        <div className="account-binding-note"><ShieldCheck size={18} aria-hidden="true" /><p>{t("绑定需要你前往身份提供方明确授权；此页不会显示密码或身份令牌。")}</p></div>
        <div className="account-method-action" aria-busy={options.isFetching || bind.isPending}>
          {options.isError ? <p role="alert" className="error-message">{t("无法读取登录方式，请重试。本地账号仍可登录。")}</p> : options.isPending ? <p role="status">{t("正在读取登录设置…")}</p> : options.data?.oidcEnabled ? <Button ref={preserveOIDCFocus} aria-disabled={bind.isPending || unlink.isPending} onClick={() => { if (!bind.isPending && !unlink.isPending) bind.mutate() }}>{bind.isPending ? t("正在前往验证…") : t("绑定或验证 OIDC 身份")}</Button> : <p>{t("此服务尚未启用 OpenID Connect 登录。")}</p>}
          <Button ref={refreshRef} className="auth-options-refresh" variant={options.isError ? "outline" : "ghost"} aria-disabled={options.isFetching || bind.isPending} onClick={() => { if (!options.isFetching && !bind.isPending) void options.refetch() }}><RefreshCw size={16} aria-hidden="true" />{options.isError ? t("重试读取登录方式") : t("刷新登录方式")}</Button>
          {bind.isError && <p role="alert" className="error-message">{errorMessage(bind.error)}</p>}
        </div>
      </div>
    </section>
  </div>
}
