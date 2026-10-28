import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { ArrowUpRight, KeyRound } from "lucide-react"
import { DeviceAuthorizationState } from "./gen/skillhub/github/v1/github_pb"
import { errorMessage, useApi } from "./api"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"

function authorizationError(error: unknown) {
  if (ConnectError.from(error).code === Code.AlreadyExists) return t("已有进行中的授权，已尝试恢复进度。")
  return errorMessage(error)
}

function statusMessage(state?: DeviceAuthorizationState) {
  switch (state) {
    case DeviceAuthorizationState.COMPLETE: return t("授权成功，正在更新来源。")
    case DeviceAuthorizationState.DENIED: return t("GitHub 授权被拒绝，可以重新开始。")
    case DeviceAuthorizationState.EXPIRED: return t("验证码已过期，请重新开始授权。")
    case DeviceAuthorizationState.CANCELED: return t("已取消本次授权。")
    case DeviceAuthorizationState.FAILED: return t("授权未完成，请稍后重试。")
    case DeviceAuthorizationState.SUPERSEDED: return t("GitHub 连接已被更新，请刷新来源后重试。")
    default: return ""
  }
}

export function DeviceFlow({ generation, onConnected }: { generation: string; onConnected: () => void }) {
  useTranslation()
  const { github, session } = useApi()
  const queryClient = useQueryClient()
  const key = ["github-device", session?.userId]
  const [privateLists, setPrivateLists] = useState(false)
  const handled = useRef("")
  const status = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      try { return (await github.getDeviceAuthorization({}, { signal })).authorization ?? null }
      catch (error) { if (ConnectError.from(error).code === Code.NotFound) return null; throw error }
    },
    refetchInterval: (query) => query.state.data?.state === DeviceAuthorizationState.PENDING ? 2000 : false,
  })
  const start = useMutation({
    mutationFn: () => github.startDeviceAuthorization({ expectedGeneration: generation, requestUserScope: privateLists }),
    onSuccess: (result) => queryClient.setQueryData(key, result.authorization ?? null),
    onError: () => { void status.refetch() },
  })
  const cancel = useMutation({
    mutationFn: () => github.cancelDeviceAuthorization({ authorizationId: status.data?.id ?? "" }),
    onSuccess: (result) => queryClient.setQueryData(key, result.authorization ?? null),
  })
  const authorization = status.data
  const pending = authorization?.state === DeviceAuthorizationState.PENDING
  useEffect(() => {
    if (authorization?.state === DeviceAuthorizationState.COMPLETE && authorization.credentialGeneration !== generation && handled.current !== authorization.id) {
      handled.current = authorization.id
      onConnected()
    }
  }, [authorization, generation, onConnected])
  return <section aria-label={t("GitHub 设备授权")} className="device-flow" aria-busy={start.isPending || cancel.isPending}>
    {pending ? <>
      <p className="text-sm">{t("在 GitHub 输入以下验证码，完成授权。")}</p>
      <output className="device-code" aria-label={t("GitHub 设备验证码")}>{authorization.userCode || t("正在申请验证码…")}</output>
      <div className="flex flex-wrap items-center gap-3"><a className="device-link" href="https://github.com/login/device" target="_blank" rel="noreferrer">{t("前往 GitHub")}<ArrowUpRight size={16} aria-hidden="true" /></a><Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{cancel.isPending ? t("取消中…") : t("取消本次授权")}</Button></div>
      <p className="mt-3 text-xs leading-6 text-muted-foreground">{t("只输入你在本页主动发起的验证码。授权完成后会自动连接；关闭页面不会停止服务端等待。")}</p>
      <p role="status" className="mt-2 text-sm text-muted-foreground">{t("正在等待 GitHub 确认…")}</p>
    </> : <>
      <Button disabled={start.isPending || status.isPending} onClick={() => { cancel.reset(); start.mutate() }}><KeyRound size={16} aria-hidden="true" />{start.isPending ? t("正在申请验证码…") : t("通过 GitHub 授权")}</Button>
      <label className="device-scope" htmlFor="device-private-lists"><Checkbox id="device-private-lists" checked={privateLists} disabled={start.isPending} onCheckedChange={(value) => setPrivateLists(value === true)} /><span>{t("申请私有 Lists 所需的 user 权限")}<span className="mt-1 block text-xs font-normal text-muted-foreground">{t("此权限也包含个人资料写入，请按需选择。")}</span></span></label>
      {authorization && <p role="status" className="text-sm text-muted-foreground">{statusMessage(authorization.state)}</p>}
    </>}
    {status.isError && <p role="alert" className="error-message">{authorizationError(status.error)}</p>}
    {start.isError && !pending && <p role="alert" className="error-message">{authorizationError(start.error)}</p>}
    {cancel.isError && <p role="alert" className="error-message">{authorizationError(cancel.error)}</p>}
  </section>
}
