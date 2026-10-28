import { formatNumber } from "./i18n"
import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { useId, useState, type FormEvent } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { Globe, LockKeyhole } from "lucide-react"
import { useApi } from "./api"
import { CatalogError } from "./catalog-error"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import type { Publication } from "./gen/skillhub/catalog/v1/catalog_pb"

function PublicationForm({ publication, repositoryName, onSaved }: { publication: Publication; repositoryName: string; onSaved: () => void }) {
  useTranslation()
  const { publications } = useApi()
  const queryClient = useQueryClient()
  const [original] = useState(publication)
  const [confirmed, setConfirmed] = useState(false)
  const confirmationId = useId()
  const publishing = !original.published
  const save = useMutation({
    mutationFn: async () => {
      const result = await publications.setPublication({ repositoryId: original.repositoryId, expectedRevision: original.revision, published: publishing, confirmFullContents: publishing && confirmed })
      if (!result.publication) throw new ConnectError("missing publication", Code.Internal)
      return result
    },
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["catalog"] }); onSaved() },
  })
  function submit(event: FormEvent) { event.preventDefault(); if (!publishing || confirmed) save.mutate() }
  return <form onSubmit={submit} className="mt-5 space-y-5" aria-busy={save.isPending}>
    <p className="break-words font-mono text-sm font-medium">{repositoryName}</p>
    {publishing ? <><p className="text-sm leading-7">{t("此仓库中所有已收录技能的完整目录，包括二进制文件、隐藏文件和其他目录内容，都将可以被任何人匿名下载。GitHub 仓库公开不代表应自动在此发布；请先检查技能目录中没有密钥或个人文件。")}</p><label htmlFor={confirmationId} className="publication-confirmation"><Checkbox id={confirmationId} checked={confirmed} disabled={save.isPending} onCheckedChange={(checked) => setConfirmed(checked === true)} /><span>{t("我确认上述技能完整目录（包括二进制与隐藏文件）可以公开，并允许匿名下载。")}</span></label></> : <p className="text-sm leading-7">{t("取消公开后应阻止新的浏览、详情和下载授权，并终止后续流式下载分块。已发送或下载的文件无法收回；私有资料库和 Pack 不会被删除。")}</p>}
    {save.isError && <CatalogError error={save.error} />}
    <div className="flex flex-wrap gap-3"><Button type="submit" className={publishing ? undefined : "danger-button"} disabled={save.isPending || publishing && !confirmed}>{save.isPending ? t("正在提交…") : publishing ? t("确认公开此仓库") : t("取消公开此仓库")}</Button><DialogClose asChild><Button variant="outline">{t("返回")}</Button></DialogClose></div>
  </form>
}

function PublicationDialog({ publication, repositoryName }: { publication: Publication; repositoryName: string }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="outline">{publication.published ? t("取消公开") : t("发布到公开目录")}</Button></DialogTrigger>
    <DialogContent><DialogTitle>{publication.published ? t("取消仓库公开？") : t("公开完整技能目录？")}</DialogTitle><DialogDescription>{t("发布仅限当前选中的仓库快照，不改变私有 Pack 的鉴权。必须等待服务端确认后才算生效。")}</DialogDescription>{open && <PublicationForm publication={publication} repositoryName={repositoryName} onSaved={() => setOpen(false)} />}</DialogContent>
  </Dialog>
}

export function PublicationControl({ repositoryId, repositoryName }: { repositoryId: string; repositoryName: string }) {
  useTranslation()
  const { publications, session } = useApi()
  const query = useQuery({ queryKey: ["catalog", "owner", session?.userId, "publication", repositoryId], queryFn: async ({ signal }) => {
    const result = await publications.getPublication({ repositoryId }, { signal })
    if (!result.publication || result.publication.revision < 1n) throw new ConnectError("missing publication state", Code.Internal)
    return result.publication
  }, staleTime: 0 })
  return <section className="publication-control" aria-labelledby={`publication-${repositoryId}`}>
    <div><h3 id={`publication-${repositoryId}`} className="text-sm font-semibold">{t("公开目录发布")}</h3><p className="mt-1 text-xs leading-6 text-muted-foreground">{t("收录不等于公开。状态由服务端读取，不会默认假定已发布或已保存。")}</p></div>
    {query.isPending ? <p role="status" className="text-sm text-muted-foreground">{t("正在读取发布状态…")}</p> : query.isError ? <CatalogError error={query.error} retry={() => { void query.refetch() }} /> : <div className="flex flex-wrap items-center gap-3"><p className="flex items-center gap-2 text-sm" role="status">{query.data.published ? <Globe size={16} aria-hidden="true" /> : <LockKeyhole size={16} aria-hidden="true" />}{query.data.published ? t("已公开") : t("未公开")}<span className="text-xs text-muted-foreground">{t("版本 ")}{formatNumber(query.data.revision)}</span></p><PublicationDialog publication={query.data} repositoryName={repositoryName} /><Button variant="ghost" disabled={query.isFetching} onClick={() => { void query.refetch() }}>{t("重新读取")}</Button></div>}
  </section>
}
