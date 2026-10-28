import { formatNumber } from "./i18n"
import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { useId, useState, type FormEvent } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Tags } from "lucide-react"
import { useApi } from "./api"
import { skillCategoryLimit } from "./catalog"
import { CatalogError } from "./catalog-error"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import type { Category } from "./gen/skillhub/catalog/v1/catalog_pb"

function CategoryChoices({ skillId, choices, current, onSaved }: { skillId: string; choices: Category[]; current: Category[]; onSaved: () => void }) {
  useTranslation()
  const { categories } = useApi()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState(() => current.map((category) => category.id))
  const prefix = useId()
  const available = [...choices, ...current.filter((entry) => !choices.some((category) => category.id === entry.id))]
  const save = useMutation({ mutationFn: () => categories.setSkillCategories({ skillId, categoryIds: selected }), onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["catalog"] }); onSaved() } })
  const unchanged = selected.length === current.length && current.every((entry) => selected.includes(entry.id))
  function submit(event: FormEvent) { event.preventDefault(); if (selected.length <= skillCategoryLimit) save.mutate() }
  return <form onSubmit={submit} className="mt-5 space-y-5" aria-busy={save.isPending}>
    <fieldset disabled={save.isPending}><legend className="mb-3 text-sm font-medium">{t("已选 ")}{formatNumber(selected.length)}/{formatNumber(skillCategoryLimit)} {t(" 个分类")}</legend>{available.length === 0 ? <p className="py-4 text-sm text-muted-foreground">{t("还没有可选分类，请先到分类管理中创建。")}</p> : <div className="category-choices">{available.map((category, index) => {
      const checked = selected.includes(category.id)
      return <label key={category.id} htmlFor={`${prefix}-${index}`} className="category-choice"><Checkbox id={`${prefix}-${index}`} checked={checked} disabled={!checked && selected.length >= skillCategoryLimit} onCheckedChange={(next) => setSelected((value) => next === true ? [...value, category.id] : value.filter((entry) => entry !== category.id))} /><span className="min-w-0 break-words">{category.name}</span></label>
    })}</div>}</fieldset>
    {save.isError && <CatalogError error={save.error} />}
    <div className="flex flex-wrap gap-3"><Button type="submit" disabled={save.isPending || unchanged || selected.length > skillCategoryLimit}>{save.isPending ? t("正在保存…") : t("保存分类")}</Button><DialogClose asChild><Button variant="outline">{t("取消")}</Button></DialogClose></div>
    <Link to="/categories" className="inline-flex min-h-11 items-center text-sm text-primary underline underline-offset-4">{t("前往分类管理")}</Link>
  </form>
}

function SkillCategoriesContent({ skillId, onSaved }: { skillId: string; onSaved: () => void }) {
  useTranslation()
  const { categories, session } = useApi()
  const choices = useQuery({ queryKey: ["catalog", "owner", session?.userId, "categories"], queryFn: ({ signal }) => categories.listCategories({}, { signal }), staleTime: 0, refetchOnWindowFocus: false })
  const assigned = useQuery({ queryKey: ["catalog", "owner", session?.userId, "skill", skillId, "categories"], queryFn: ({ signal }) => categories.getSkillCategories({ skillId }, { signal }), staleTime: 0, refetchOnWindowFocus: false })
  if (choices.isPending || assigned.isPending) return <p role="status" className="mt-5 text-sm text-muted-foreground">{t("正在读取分类与当前选择…")}</p>
  if (choices.isError || assigned.isError) return <CatalogError error={choices.error ?? assigned.error} retry={() => { void choices.refetch(); void assigned.refetch() }} />
  return <CategoryChoices skillId={skillId} choices={choices.data.categories} current={assigned.data.categories} onSaved={onSaved} />
}

export function SkillCategories({ skillId, skillName }: { skillId: string; skillName: string }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="ghost" aria-label={t("编辑 {{value1}} 的分类", { value1: skillName })}><Tags size={15} aria-hidden="true" />{t("编辑分类")}</Button></DialogTrigger>
    <DialogContent><DialogTitle>{t("技能分类")}</DialogTitle><DialogDescription className="break-words">{skillName}{t("：最多选择 ")}{formatNumber(skillCategoryLimit)} {t(" 个自己的分类。只修改管理元数据，不修改技能文件、内容 hash 或发布状态。")}</DialogDescription>{open && <SkillCategoriesContent skillId={skillId} onSaved={() => setOpen(false)} />}</DialogContent>
  </Dialog>
}
