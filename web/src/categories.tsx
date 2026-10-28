import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useState, type FormEvent } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { Pencil, Plus, RefreshCw, Tags, Trash2 } from "lucide-react"
import { useApi } from "./api"
import { categoryNameMaxBytes, ownerCategoryLimit, queryByteLength } from "./catalog"
import { CatalogError } from "./catalog-error"
import { Button } from "./components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import { Input } from "./components/ui/input"
import type { Category } from "./gen/skillhub/catalog/v1/catalog_pb"

function CategoryForm({ category, onSaved }: { category?: Category; onSaved: () => void }) {
  useTranslation()
  const { categories } = useApi()
  const queryClient = useQueryClient()
  const [original] = useState(category)
  const [name, setName] = useState(category?.name ?? "")
  const [validation, setValidation] = useNotice()
  const nameBytes = queryByteLength(name.trim())
  const save = useMutation({
    mutationFn: async () => {
      const result = original ? await categories.updateCategory({ categoryId: original.id, expectedRevision: original.revision, name: name.trim() }) : await categories.createCategory({ name: name.trim() })
      if (!result.category) throw new ConnectError("missing category", Code.Internal)
      return result.category
    },
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["catalog"] }); onSaved() },
  })
  function submit(event: FormEvent) {
    event.preventDefault()
    if (!name.trim() || nameBytes > categoryNameMaxBytes) { setValidation({ key: "名称不能为空，最多 {{value1}} 个 UTF-8 字节。", values: { value1: formatNumber(categoryNameMaxBytes) } }); return }
    setValidation("")
    save.mutate()
  }
  return <form onSubmit={submit} className="mt-5 space-y-5" aria-busy={save.isPending}>
    <div className="space-y-2"><label htmlFor="category-name">{t("分类名称")}</label><Input id="category-name" name="name" required maxLength={80} value={name} disabled={save.isPending} onChange={(event) => { setName(event.target.value); setValidation("") }} aria-describedby="category-name-help" aria-invalid={Boolean(validation) || nameBytes > categoryNameMaxBytes} /><p id="category-name-help" className="field-help">{formatNumber(nameBytes)}/{formatNumber(categoryNameMaxBytes)} {t(" 个 UTF-8 字节。中文等字符占用多个字节；分类不会改动 SKILL.md 或内容 hash。")}</p>{(validation || nameBytes > categoryNameMaxBytes) && <p role="alert" className="error-message">{validation || t("名称超过 {{value1}} 个 UTF-8 字节，请缩短。", { value1: formatNumber(categoryNameMaxBytes) })}</p>}</div>
    {save.isError && <CatalogError error={save.error} />}
    <div className="flex flex-wrap gap-3"><Button type="submit" disabled={save.isPending || !name.trim() || nameBytes > categoryNameMaxBytes || original?.name === name.trim()}>{save.isPending ? t("正在保存…") : original ? t("保存名称") : t("创建分类")}</Button><DialogClose asChild><Button variant="outline">{t("取消")}</Button></DialogClose></div>
  </form>
}

function CategoryEditor({ category, disabled, onSaved }: { category?: Category; disabled?: boolean; onSaved: (message: string) => void }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant={category ? "ghost" : "default"} disabled={disabled} aria-label={category ? t("重命名分类 {{value1}}", { value1: category.name }) : undefined}>{category ? <Pencil size={16} aria-hidden="true" /> : <Plus size={16} aria-hidden="true" />}{category ? t("重命名") : t("创建分类")}</Button></DialogTrigger>
    <DialogContent><DialogTitle>{category ? t("重命名分类") : t("创建分类")}</DialogTitle><DialogDescription>{t("分类仅属于当前账号。每个账号最多 ")}{formatNumber(ownerCategoryLimit)} {t(" 个分类，每个技能最多选择 8 个分类。")}</DialogDescription>{open && <CategoryForm category={category} onSaved={() => { setOpen(false); onSaved(category ? "分类名称已保存。" : "分类已创建。") }} />}</DialogContent>
  </Dialog>
}

function DeleteCategoryForm({ category, onDeleted }: { category: Category; onDeleted: () => void }) {
  useTranslation()
  const { categories } = useApi()
  const queryClient = useQueryClient()
  const [original] = useState(category)
  const remove = useMutation({ mutationFn: () => categories.deleteCategory({ categoryId: original.id, expectedRevision: original.revision }), onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["catalog"] }); onDeleted() } })
  return <div className="mt-5 space-y-5">
    <p className="break-words text-sm leading-7">{t("删除「")}{original.name}{t("」会移除该分类及其技能关联，不会删除技能、仓库或本地安装。当前关联 ")}{formatNumber(original.skillCount)} {t(" 个技能。")}</p>
    {remove.isError && <CatalogError error={remove.error} />}
    <div className="flex flex-wrap gap-3"><Button className="danger-button" disabled={remove.isPending} onClick={() => remove.mutate()}>{remove.isPending ? t("正在删除…") : t("删除分类与关联")}</Button><DialogClose asChild><Button variant="outline">{t("取消")}</Button></DialogClose></div>
  </div>
}

function DeleteCategory({ category, onDeleted }: { category: Category; onDeleted: () => void }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="ghost" className="text-destructive" aria-label={t("删除分类 {{value1}}", { value1: category.name })}><Trash2 size={16} aria-hidden="true" />{t("删除")}</Button></DialogTrigger>
    <DialogContent><DialogTitle>{t("删除分类？")}</DialogTitle><DialogDescription>{t("这项操作只影响分类元数据。关闭对话框可取消。")}</DialogDescription>{open && <DeleteCategoryForm category={category} onDeleted={() => { setOpen(false); onDeleted() }} />}</DialogContent>
  </Dialog>
}

export function CategoriesPage() {
  useTranslation()
  const { categories, session } = useApi()
  const [notice, setNotice] = useNotice()
  const query = useQuery({ queryKey: ["catalog", "owner", session?.userId, "categories"], queryFn: ({ signal }) => categories.listCategories({}, { signal }) })
  const atLimit = (query.data?.categories.length ?? 0) >= ownerCategoryLimit
  return <>
    <div className="page-heading"><div><h1>{t("技能分类")}</h1><p>{t("整理自己的技能，一个技能可属于多个分类。到仓库中分配分类，不修改技能内容。")}</p></div><Button variant="outline" disabled={query.isFetching} onClick={() => { setNotice(""); void query.refetch() }}><RefreshCw size={16} aria-hidden="true" />{t("刷新")}</Button></div>
    <div className="section-toolbar"><div><h2 className="font-semibold">{t("我的分类 ")}{query.isSuccess && <span className="ml-2 font-normal text-muted-foreground">{formatNumber(query.data.categories.length)}/{formatNumber(ownerCategoryLimit)}</span>}</h2><p className="mt-1 text-sm text-muted-foreground">{t("这里的技能数包含自己的私有技能，不作为公开目录计数。")}</p></div><CategoryEditor disabled={atLimit} onSaved={setNotice} /></div>
    {notice && <p role="status" className="mb-4 text-sm text-muted-foreground">{notice}</p>}
    {atLimit && <p role="status" className="mb-4 text-sm text-muted-foreground">{t("已达到 ")}{formatNumber(ownerCategoryLimit)} {t(" 个分类的上限，请先整理现有分类。")}</p>}
    {query.isPending ? <p role="status" className="py-10 text-sm text-muted-foreground">{t("正在读取分类…")}</p> : query.isError ? <CatalogError error={query.error} retry={() => { void query.refetch() }} /> : query.data.categories.length === 0 ? <div className="list-surface empty-state" role="status"><Tags size={28} className="empty-icon" aria-hidden="true" /><h3 className="font-medium">{t("还没有分类")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("创建分类后，可以在仓库中为每个技能选择分类。未分类技能仍可使用。")}</p></div> : <ul className="list-surface">{query.data.categories.map((category) => <li key={category.id} className="category-record"><div className="min-w-0"><h3 className="break-words font-medium">{category.name}</h3><p className="mt-1 text-sm text-muted-foreground">{formatNumber(category.skillCount)} {t(" 个技能 · 版本 ")}{formatNumber(category.revision)}</p></div><div className="flex flex-wrap gap-1"><CategoryEditor category={category} onSaved={setNotice} /><DeleteCategory category={category} onDeleted={() => setNotice("分类及其关联已删除，技能未删除。")} /></div></li>)}</ul>}
  </>
}
