import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { CircleAlert, RefreshCw } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useApi } from "./api"
import { formatNumber, t } from "./i18n"
import { Button } from "./components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import type { RefreshRepositoryResponse, Repository } from "./gen/skillhub/library/v1/library_pb"

function refreshError(error: unknown) {
  switch (ConnectError.from(error).code) {
    case Code.Aborted: return t("仓库或组合版本已改变。请关闭并重新读取，核对后再检查源更新；不会自动重试。")
    case Code.FailedPrecondition: return t("未替换快照：已公开内容发生变化、组合仍引用已移除的技能，或别名存在冲突。请先取消公开或调整组合，再重新读取。")
    case Code.NotFound: return t("此仓库已不可用，或你没有访问权限。请重新读取资料库。")
    case Code.Unauthenticated: return t("登录已失效，请重新登录后读取仓库状态。")
    case Code.ResourceExhausted: return t("抓取达到容量或大小限制，或来源暂时限流。旧快照不会因此被自动替换。")
    default: return t("源更新结果待确认。请关闭并重新读取仓库与组合状态；停止等待不会撤销已提交的更改，也不会自动重试。")
  }
}

function RefreshForm({ repository }: { repository: Repository }) {
  useTranslation()
  const { library, session } = useApi()
  const queryClient = useQueryClient()
  const [snapshot] = useState(() => ({ id: repository.id, name: repository.nameWithOwner, hash: repository.archiveHash, revision: repository.publicationRevision }))
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [result, setResult] = useState<RefreshRepositoryResponse | null>(null)
  const active = useRef<AbortController | null>(null)
  const submitted = useRef(false)
  const invalidate = useCallback(() => {
    for (const queryKey of [["library-repositories", session?.userId], ["collected-repositories", session?.userId], ["repository-skills", session?.userId, snapshot.id], ["packs", session?.userId], ["pack", session?.userId], ["catalog"]]) {
      void queryClient.invalidateQueries({ queryKey })
    }
  }, [queryClient, session?.userId, snapshot.id])
  useEffect(() => () => {
    const controller = active.current
    active.current = null
    controller?.abort()
    if (submitted.current) invalidate()
  }, [library, invalidate])

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitted.current) return
    submitted.current = true
    const controller = new AbortController()
    active.current = controller
    setAttempted(true)
    setPending(true)
    try {
      const response = await library.refreshRepository({ repositoryId: snapshot.id, expectedArchiveHash: snapshot.hash, expectedPublicationRevision: snapshot.revision }, { signal: controller.signal })
      if (active.current !== controller || controller.signal.aborted) return
      if (!response.repository || response.repository.id !== snapshot.id || response.repository.publicationRevision < snapshot.revision) throw new ConnectError("invalid refresh receipt", Code.Internal)
      setResult(response)
    } catch (failure) {
      if (active.current === controller && !controller.signal.aborted) setError(failure)
    } finally {
      if (active.current === controller) {
        active.current = null
        setPending(false)
        invalidate()
      }
    }
  }

  return <form onSubmit={(event) => { void submit(event) }} className="mt-5 space-y-5" aria-busy={pending}>
    <p className="break-words font-mono text-sm font-medium">{snapshot.name}</p>
    <p className="text-sm leading-7">{t("从已保存的来源重新拉取并扫描整仓。同路径技能保留身份与分类，受影响的组合会获得新版本。组合引用的技能被删除或改名时，整次更新会被拒绝。")}</p>
    <p className="text-sm leading-7 text-muted-foreground">{t("已公开仓库不会静默发布新内容。请先取消公开，再检查源更新，核对完整技能目录后重新确认公开。")}</p>
    <p className="text-xs leading-6 text-muted-foreground">{t("基于仓库版本 {{revision}} 检查，冲突时不会自动重试。", { revision: formatNumber(snapshot.revision) })}</p>
    {error !== null && <div className="error-notice" role="alert"><CircleAlert size={18} className="shrink-0" aria-hidden="true" /><p>{refreshError(error)}</p></div>}
    <div className="min-h-12 text-sm leading-6" role="status" aria-live="polite">
      {pending ? t("正在拉取源仓库并校验更新…") : result ? result.changed ? t("已更新快照：新增 {{added}}、更新 {{updated}}、移除 {{removed}} 个技能；{{packs}} 个组合获得新版本。", { added: formatNumber(result.added), updated: formatNumber(result.updated), removed: formatNumber(result.removed), packs: formatNumber(result.packs.length) }) : t("已检查来源，没有内容变化，仓库与组合版本保持不变。") : t("停止等待不会撤销可能已提交的更新；关闭后请重新读取最新状态。")}
    </div>
    <div className="flex flex-wrap gap-3"><Button type="submit" disabled={attempted || snapshot.revision < 1n}>{pending ? t("正在检查源更新…") : t("确认检查并更新快照")}</Button><DialogClose asChild><Button variant="outline">{pending ? t("停止等待并关闭") : t("关闭对话框")}</Button></DialogClose></div>
  </form>
}

export function RepositoryRefreshControl({ repository }: { repository: Repository }) {
  useTranslation()
  const { session } = useApi()
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="outline" className="mt-5"><RefreshCw size={16} aria-hidden="true" />{t("检查源更新")}</Button></DialogTrigger>
    <DialogContent><DialogTitle>{t("检查并更新仓库快照？")}</DialogTitle><DialogDescription>{t("此操作会重新抓取来源，不只是刷新当前页面。只有服务端确认提交后才算更新成功。")}</DialogDescription>{open && <RefreshForm key={session?.userId} repository={repository} />}</DialogContent>
  </Dialog>
}
