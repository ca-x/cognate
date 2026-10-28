import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { KeyRound } from "lucide-react"
import { errorMessage, redirectToOIDC, useApi } from "./api"
import { Button } from "./components/ui/button"

export function AccountPage() {
  useTranslation()
  const { auth, session } = useApi()
  const options = useQuery({ queryKey: ["login-options"], queryFn: ({ signal }) => auth.getLoginOptions({}, { signal }) })
  const bind = useMutation({ mutationFn: async () => { const result = await auth.startOIDCLogin({ linkCurrentAccount: true }); redirectToOIDC(result.authorizationUrl) } })
  return <>
    <div className="page-heading"><div><h1>{t("账号与登录")}</h1><p>{t("为当前工作空间配置登录方式。")}</p></div></div>
    <div className="source-card"><div className="source-card-heading"><div className="service-icon"><KeyRound size={24} aria-hidden="true" /></div><div><h2 className="font-semibold">OpenID Connect</h2><p className="mt-1 text-sm text-muted-foreground">{t("当前账号：")}{session?.username}</p></div></div><div className="source-card-body space-y-4"><p className="text-sm leading-7 text-muted-foreground">{t("主动绑定后，该 OIDC 身份会登录到当前账号。已属于其他账号的身份不会被转移，也不会按邮箱自动合并。")}</p>{options.isPending ? <p role="status" className="text-sm">{t("正在读取登录设置…")}</p> : options.isError ? <p role="alert" className="error-message">{errorMessage(options.error)}</p> : options.data?.oidcEnabled ? <Button disabled={bind.isPending} onClick={() => bind.mutate()}>{bind.isPending ? t("正在前往验证…") : t("绑定或验证 OIDC 身份")}</Button> : <p className="text-sm text-muted-foreground">{t("此服务尚未启用 OpenID Connect 登录。")}</p>}{bind.isError && <p role="alert" className="error-message">{errorMessage(bind.error)}</p>}</div></div>
  </>
}
