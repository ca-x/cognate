import "./packs-ui.css"
import "./packs-ui-i18n"
import { formatDate, t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useCallback, useEffect, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Copy, KeyRound, RefreshCw, ShieldX } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
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
  const [now, setNow] = useState(Date.now)
  const active = useRef<AbortController | null>(null)
  const shareHeading = useRef<HTMLHeadingElement>(null)
  const shareList = useRef<HTMLUListElement | null>(null)
  const shareListRef = useCallback((list: HTMLUListElement | null) => {
    if (!list && shareList.current?.contains(document.activeElement)) shareHeading.current?.focus()
    shareList.current = list
  }, [])
  const returnFocus = useRef<HTMLButtonElement | null>(null)
  useEffect(() => () => active.current?.abort(), [])
  const key = ["pack-shares", session?.userId, packId]
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => packs.listShares({ packId }, { signal }) })
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  const revocation = useMutation({
    retry: false,
    mutationFn: (shareId: string) => packs.revokeShare({ packId, shareId }),
    onSuccess: async (_, id) => {
      setConfirming("")
      setCredential((current) => current?.id === id ? null : current)
      await queryClient.invalidateQueries({ queryKey: key })
    },
  })
  async function refresh() {
    const result = await query.refetch()
    if (result.isSuccess) { setFailure(null); revocation.reset() }
  }
  async function create() {
    if (active.current || pending || credential || failure || query.isError || revocation.isPending) return
    const controller = new AbortController()
    active.current = controller
    setPending(true)
    setFailure(null)
    setCopyNotice("")
    try {
      const expiresAtUnix = expiryDays === "0" ? 0n : BigInt(Math.floor(Date.now() / 1000) + Number(expiryDays) * 86400)
      const result = await packs.createShare({ packId, expiresAtUnix }, { signal: controller.signal })
      if (!result.share || !result.shareToken) throw new Error("missing share receipt")
      if (!controller.signal.aborted) setCredential({ id: result.share.id, value: result.shareToken })
      await queryClient.invalidateQueries({ queryKey: key })
    } catch (error) { if (!controller.signal.aborted) setFailure(error) } finally {
      if (active.current === controller) { active.current = null; setPending(false) }
    }
  }
  async function copy() {
    if (!credential) return
    try { await navigator.clipboard.writeText(credential.value); setCopyNotice("已复制。请只交给获授权的订阅者。") } catch { setCopyNotice("浏览器不支持复制，请手动选择并复制令牌。") }
  }
  const locked = pending || !!credential || query.isPending || query.isError || !!failure || revocation.isPending || revocation.isError
  return <section className="pack-sharing" aria-labelledby="pack-sharing-title" aria-busy={pending}>
    <h3 id="pack-sharing-title" className="flex items-center gap-2 font-semibold"><KeyRound size={17} aria-hidden="true" />{t("分享与订阅")}</h3>
    <p className="mt-2 text-sm text-muted-foreground">{t("持有令牌即可读取这个组合的最新内容。令牌仅在创建时显示，不会放入地址或浏览器存储。")}</p>
    <p className="packs-scope">{t("分享范围：仅技能，不含配置内容或密钥。撤销只阻止后续读取，不能收回已分发或安装的内容。")}</p>
    <div className="pack-share-controls mt-4">
      <div className="min-w-0 space-y-2"><label htmlFor="share-expiry">{t("有效期")}</label><Select disabled={locked} value={expiryDays} onValueChange={setExpiryDays}><SelectTrigger id="share-expiry" className="w-full"><SelectValue /></SelectTrigger><SelectContent className="packs-ui-select"><SelectItem value="1">{t("1 天")}</SelectItem><SelectItem value="7">{t("7 天")}</SelectItem><SelectItem value="30">{t("30 天")}</SelectItem><SelectItem value="0">{t("长期有效，直到撤销")}</SelectItem></SelectContent></Select></div>
      <Button disabled={locked} onClick={() => { void create() }}>{pending ? t("正在创建…") : t("创建分享令牌")}</Button>
    </div>
    {failure != null && <><PackError error={failure} /><p role="alert" className="packs-scope">{t("创建结果未确认，不会自动重放。请重新读取分享状态；若已创建但未收到令牌，可撤销该记录。")}</p></>}
    {credential && <div className="share-secret mt-5">
      <label htmlFor="created-share-token" className="font-medium">{t("请保存令牌：关闭后无法再次查看")}</label>
      <textarea id="created-share-token" className="pack-textarea mt-3 font-mono text-xs" readOnly rows={4} autoComplete="off" spellCheck={false} value={credential.value} aria-describedby="share-secret-help" />
      <p id="share-secret-help" className="mt-2 text-xs text-muted-foreground">{t("这是访问凭据，请勿上传截图、写进仓库或粘贴到公开渠道。浏览器剪贴板不由 Cognate 管理。")}</p>
      <div className="packs-actions mt-4"><Button variant="outline" onClick={() => { void copy() }}><Copy size={15} aria-hidden="true" />{t("复制令牌")}</Button><Button variant="ghost" onClick={() => { setCredential(null); setCopyNotice(""); shareHeading.current?.focus() }}>{t("已保存，隐藏令牌")}</Button></div>
      <p role="status" className="mt-2 text-sm text-muted-foreground">{copyNotice}</p>
    </div>}
    <div className="mt-6">
      <div className="packs-section-heading"><h4 ref={shareHeading} tabIndex={-1} className="text-sm font-medium">{t("已有分享")}</h4><Button variant="ghost" disabled={query.isFetching || pending || revocation.isPending} onClick={() => { void refresh() }}><RefreshCw size={15} aria-hidden="true" />{t("重新读取分享状态")}</Button></div>
      {query.isPending ? <p className="mt-3 text-sm" role="status">{t("正在读取分享状态…")}</p> : query.isError ? <><p role="status" className="packs-scope">{t("分享状态未知，不能据此判断已过期或撤销。")}</p><PackError error={query.error} retry={() => { void refresh() }} /></> : !query.data?.shares.length ? <p className="mt-3 text-sm text-muted-foreground">{t("尚未创建分享。")}</p> : <ul ref={shareListRef} className="mt-3 space-y-3">{query.data.shares.map((entry) => {
        const revoked = entry.revokedAtUnix !== 0n
        const expired = entry.expiresAtUnix !== 0n && Number(entry.expiresAtUnix) * 1000 <= now
        return <li className="share-record" key={entry.id}>
          <div className="min-w-0"><p className="break-all font-mono text-xs">{entry.id}</p><p className="mt-2 text-xs text-muted-foreground"><span className="packs-status" data-state={revoked || expired ? "inactive" : "active"}>{revoked ? t("已撤销") : expired ? t("已过期") : t("有效")}</span> · {entry.expiresAtUnix === 0n ? t("长期有效") : t("{{value1}} 到期", { value1: formatDate(Number(entry.expiresAtUnix) * 1000) })}</p></div>
          {!revoked && !expired && <Button variant="outline" disabled={pending || revocation.isPending || revocation.isError} onClick={(event) => { returnFocus.current = event.currentTarget; setConfirming(entry.id) }}><ShieldX size={15} aria-hidden="true" />{t("撤销")}</Button>}
        </li>
      })}</ul>}
      {revocation.isError && <><PackError error={revocation.error} /><p role="alert" className="packs-scope">{t("撤销结果未确认。请重新读取分享状态核对，不会自动重放。")}</p></>}
    </div>
    <Dialog open={!!confirming} onOpenChange={(open) => { if (!open && !revocation.isPending) setConfirming("") }}><DialogContent className="packs-ui-dialog" onCloseAutoFocus={(event) => { event.preventDefault(); const target = returnFocus.current; if (target?.isConnected && !target.disabled) target.focus(); else shareHeading.current?.focus() }} onEscapeKeyDown={(event) => { if (revocation.isPending) event.preventDefault() }} onPointerDownOutside={(event) => { if (revocation.isPending) event.preventDefault() }}>
      <DialogTitle>{t("确认撤销")}</DialogTitle><DialogDescription>{t("撤销只阻止后续读取，已经发送或安装的内容无法收回。")}</DialogDescription><p className="my-4 break-all font-mono text-xs">{confirming}</p>
      <div className="packs-actions"><DialogClose asChild><Button variant="outline" disabled={revocation.isPending}>{t("取消")}</Button></DialogClose><Button className="danger-button" disabled={revocation.isPending || revocation.isError || query.isError} onClick={() => revocation.mutate(confirming)}>{revocation.isPending ? t("正在撤销…") : t("确认撤销")}</Button></div>
      {revocation.isError && <PackError error={revocation.error} />}
    </DialogContent></Dialog>
  </section>
}
