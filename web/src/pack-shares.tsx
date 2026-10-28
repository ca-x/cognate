import { formatDate } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Copy, KeyRound, ShieldX } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { PackError } from "./pack-error"

export function PackShares({ packId }: { packId: string }) {
  useTranslation()
  const { session, packs } = useApi()
  const queryClient = useQueryClient()
  const [expiryDays, setExpiryDays] = useState("7")
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<unknown>(null)
  const [credential, setCredential] = useState<{ id: string; value: string } | null>(null)
  const [copyNotice, setCopyNotice] = useNotice()
  const [confirming, setConfirming] = useState("")
  const active = useRef<AbortController | null>(null)
  useEffect(() => () => active.current?.abort(), [])
  const key = ["pack-shares", session?.userId, packId]
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => packs.listShares({ packId }, { signal }) })
  const revocation = useMutation({ mutationFn: (shareId: string) => packs.revokeShare({ packId, shareId }), onSuccess: async (_, id) => { setConfirming(""); setCredential((current) => current?.id === id ? null : current); await queryClient.invalidateQueries({ queryKey: key }) } })
  async function create() {
    const controller = new AbortController()
    active.current = controller
    setPending(true)
    setFailure(null)
    setCopyNotice("")
    try {
      const expiresAtUnix = expiryDays === "0" ? 0n : BigInt(Math.floor(Date.now() / 1000) + Number(expiryDays) * 86400)
      const result = await packs.createShare({ packId, expiresAtUnix }, { signal: controller.signal })
      if (!controller.signal.aborted) setCredential({ id: result.share?.id ?? "", value: result.shareToken })
      await queryClient.invalidateQueries({ queryKey: key })
    } catch (error) { if (!controller.signal.aborted) setFailure(error) } finally { if (active.current === controller) { active.current = null; setPending(false) } }
  }
  async function copy() {
    if (!credential) return
    try { await navigator.clipboard.writeText(credential.value); setCopyNotice("已复制。请只交给获授权的订阅者。") } catch { setCopyNotice("浏览器不支持复制，请手动选择并复制令牌。") }
  }
  return <section className="pack-sharing" aria-labelledby="pack-sharing-title">
    <h3 id="pack-sharing-title" className="flex items-center gap-2 font-semibold"><KeyRound size={17} aria-hidden="true" />{t("分享与订阅")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("持有令牌即可读取这个组合的最新内容。令牌仅在创建时显示，不会放入地址或浏览器存储。")}</p>
    <div className="pack-share-controls mt-4"><div className="min-w-0 space-y-2"><label htmlFor="share-expiry">{t("有效期")}</label><select id="share-expiry" className="pack-select" disabled={pending || !!credential} value={expiryDays} onChange={(event) => setExpiryDays(event.target.value)}><option value="1">{t("1 天")}</option><option value="7">{t("7 天")}</option><option value="30">{t("30 天")}</option><option value="0">{t("长期有效，直到撤销")}</option></select></div><Button disabled={pending || !!credential} onClick={() => { void create() }}>{pending ? t("正在创建…") : t("创建分享令牌")}</Button></div>
    {failure != null && <PackError error={failure} />}
    {credential && <div className="share-secret mt-5"><label htmlFor="created-share-token" className="font-medium">{t("请保存令牌：关闭后无法再次查看")}</label><textarea id="created-share-token" className="pack-textarea mt-3 font-mono text-xs" readOnly rows={4} autoComplete="off" spellCheck={false} value={credential.value} aria-describedby="share-secret-help" /><p id="share-secret-help" className="mt-2 text-xs text-muted-foreground">{t("这是访问凭据，请勿上传截图、写进仓库或粘贴到公开渠道。浏览器剪贴板不由 Cognate 管理。")}</p><div className="mt-4 flex flex-wrap gap-3"><Button variant="outline" onClick={() => { void copy() }}><Copy size={15} aria-hidden="true" />{t("复制令牌")}</Button><Button variant="ghost" onClick={() => { setCredential(null); setCopyNotice("") }}>{t("已保存，隐藏令牌")}</Button></div><p role="status" className="mt-2 text-sm text-muted-foreground">{copyNotice}</p></div>}
    <div className="mt-6"><h4 className="text-sm font-medium">{t("已有分享")}</h4>{query.isPending ? <p className="mt-3 text-sm" role="status">{t("正在读取分享状态…")}</p> : query.isError ? <PackError error={query.error} retry={() => { void query.refetch() }} /> : !query.data?.shares.length ? <p className="mt-3 text-sm text-muted-foreground">{t("尚未创建分享。")}</p> : <ul className="mt-3 space-y-3">{query.data.shares.map((entry) => {
      const revoked = entry.revokedAtUnix !== 0n
      const expired = entry.expiresAtUnix !== 0n && Number(entry.expiresAtUnix) * 1000 <= Date.now()
      return <li className="share-record" key={entry.id}><div className="min-w-0"><p className="break-all font-mono text-xs">{entry.id}</p><p className="mt-2 text-xs text-muted-foreground">{revoked ? t("已撤销") : expired ? t("已过期") : t("有效")} · {entry.expiresAtUnix === 0n ? t("长期有效") : t("{{value1}} 到期", { value1: formatDate(Number(entry.expiresAtUnix) * 1000) })}</p></div>{!revoked && !expired && (confirming === entry.id ? <div className="flex flex-wrap gap-2"><Button className="danger-button" disabled={revocation.isPending} onClick={() => revocation.mutate(entry.id)}>{t("确认撤销")}</Button><Button variant="ghost" disabled={revocation.isPending} onClick={() => setConfirming("")}>{t("取消")}</Button></div> : <Button variant="outline" disabled={revocation.isPending} onClick={() => setConfirming(entry.id)}><ShieldX size={15} aria-hidden="true" />{t("撤销")}</Button>)}</li>
    })}</ul>}{revocation.isError && <PackError error={revocation.error} />}</div>
  </section>
}
