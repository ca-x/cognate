import { formatNumber } from "./i18n"
import { t, useNotice } from "./i18n"
import { useTranslation } from "react-i18next"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Check, Layers, Pencil, Plus, RefreshCw, Search, Trash2, X } from "lucide-react"
import { useApi } from "./api"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import type { Pack } from "./gen/skillhub/pack/v1/pack_pb"
import { PackShares } from "./pack-shares"
import { PackError } from "./pack-error"

export function PacksPage() {
  useTranslation()
  const { session, packs } = useApi()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState("")
  const [creating, setCreating] = useState(false)
  const [notice, setNotice] = useNotice()
  const key = ["packs", session?.userId]
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => packs.listPacks({}, { signal }) })
  const entries = query.data?.packs ?? []
  const selectedId = entries.some((entry) => entry.id === selected) ? selected : entries[0]?.id
  async function saved(id: string) {
    await queryClient.invalidateQueries({ queryKey: key })
    setSelected(id)
    setCreating(false)
    setNotice("组合已保存。分享将使用组合的最新内容。")
  }
  return <>
    <div className="page-heading"><div><h1>{t("技能组合")}</h1><p>{t("从不同仓库挑选技能，组合成可以订阅的 Pack。")}</p></div>{!creating && <Button onClick={() => { setCreating(true); setNotice("") }}><Plus size={16} aria-hidden="true" />{t("新建组合")}</Button>}</div>
    {creating ? <PackEditor onCancel={() => setCreating(false)} onSaved={saved} /> : <>
      <div className="section-toolbar"><p className="text-sm text-muted-foreground">{formatNumber(entries.length)} {t(" 个组合 · 相同别名与内容 hash 只分发一份")}</p><Button variant="outline" disabled={query.isFetching} onClick={() => { void query.refetch(); void queryClient.invalidateQueries({ queryKey: ["pack", session?.userId] }) }}><RefreshCw size={15} aria-hidden="true" />{t("刷新")}</Button></div>
      <p role="status" className="mb-3 text-sm text-muted-foreground">{notice}</p>
      {query.isPending ? <p role="status">{t("正在读取组合…")}</p> : query.isError ? <PackError error={query.error} retry={() => { void query.refetch() }} /> : entries.length === 0 ? <div className="list-surface empty-state"><Layers size={28} className="empty-icon" aria-hidden="true" /><h2 className="font-medium">{t("把需要的技能放在一起")}</h2><p className="mt-2 text-sm text-muted-foreground">{t("先在仓库中收录技能，再创建你的第一个组合。")}</p><Button className="mt-5" onClick={() => setCreating(true)}><Plus size={16} aria-hidden="true" />{t("新建组合")}</Button></div> : <div className="library-workbench"><div className="collected-repositories" role="group" aria-label={t("选择技能组合")}>{entries.map((entry) => <button key={entry.id} type="button" className="collected-repository-select" aria-pressed={selectedId === entry.id} onClick={() => { setSelected(entry.id); setNotice("") }}><span className="break-words font-medium">{entry.name}</span><span className="mt-2 block text-xs text-muted-foreground">{formatNumber(entry.itemCount)} {t(" 个选择 · 版本 ")}{formatNumber(entry.revision)}</span></button>)}</div>{selectedId && <PackDetail key={selectedId} id={selectedId} onSaved={saved} onDeleted={() => { setSelected(""); setNotice("组合与全部分享均已删除。"); void queryClient.invalidateQueries({ queryKey: key }) }} />}</div>}
    </>}
  </>
}

function PackDetail({ id, onSaved, onDeleted }: { id: string; onSaved: (id: string) => Promise<void>; onDeleted: () => void }) {
  useTranslation()
  const { session, packs } = useApi()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const query = useQuery({ queryKey: ["pack", session?.userId, id], queryFn: ({ signal }) => packs.getPack({ packId: id }, { signal }) })
  const pack = query.data?.pack
  const deletion = useMutation({ mutationFn: () => packs.deletePack({ packId: id, expectedRevision: pack?.revision ?? 0n }), onSuccess: onDeleted })
  if (query.isPending) return <div className="library-detail" role="status">{t("正在读取组合内容…")}</div>
  if (query.isError) return <div className="library-detail"><PackError error={query.error} retry={() => { void query.refetch() }} /></div>
  if (!pack) return <p role="alert" className="library-detail">{t("组合已不可用，请刷新列表。")}</p>
  if (editing) return <div className="library-detail"><PackEditor initial={pack} onCancel={() => setEditing(false)} onSaved={async (savedId) => { await queryClient.invalidateQueries({ queryKey: ["pack", session?.userId, id] }); await onSaved(savedId); setEditing(false) }} /></div>
  return <div className="library-detail">
    <div className="pack-detail-heading"><div className="min-w-0"><h2 className="break-words text-lg font-semibold">{pack.name}</h2><p className="mt-2 whitespace-pre-wrap break-words text-sm text-muted-foreground">{pack.description || t("尚未添加描述")}</p></div><Button variant="outline" disabled={deletion.isPending} onClick={() => setEditing(true)}><Pencil size={15} aria-hidden="true" />{t("编辑")}</Button></div>
    <ul className="skill-list mt-6" aria-label={t("组合中的技能")}>{pack.items.map((item) => <li key={item.skillId} className="skill-card"><h3 className="break-words font-medium">{item.name}</h3><p className="mt-1 break-words text-sm text-muted-foreground">{item.repositoryName} · {item.path}</p><div className="skill-metadata"><span>{t("安装别名 ")}<code>{item.slug}</code></span><code title={item.contentHash}>hash {item.contentHash.slice(0, 12)}</code></div></li>)}</ul>
    <PackShares key={pack.id} packId={pack.id} />
    <section className="pack-danger" aria-labelledby="delete-pack-title"><h3 id="delete-pack-title" className="font-medium">{t("删除组合")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("同时撤销并删除全部分享。已经发送或安装的内容无法收回。")}</p>{confirming ? <div className="mt-4 flex flex-wrap gap-3"><Button className="danger-button" disabled={deletion.isPending} onClick={() => deletion.mutate()}>{deletion.isPending ? t("正在删除…") : t("确认删除组合与全部分享")}</Button><Button variant="outline" disabled={deletion.isPending} onClick={() => setConfirming(false)}>{t("取消")}</Button></div> : <Button variant="outline" className="mt-4 text-destructive" onClick={() => setConfirming(true)}><Trash2 size={15} aria-hidden="true" />{t("删除组合")}</Button>}{deletion.isError && <PackError error={deletion.error} />}</section>
  </div>
}

type PickedSkill = { skillId: string; name: string; repositoryName: string; slug: string }

function PackEditor({ initial, onCancel, onSaved }: { initial?: Pack; onCancel: () => void; onSaved: (id: string) => Promise<void> }) {
  useTranslation()
  const { session, packs, library } = useApi()
  const [name, setName] = useState(initial?.name ?? "")
  const [description, setDescription] = useState(initial?.description ?? "")
  const [picked, setPicked] = useState<PickedSkill[]>(() => initial?.items.map((item) => ({ skillId: item.skillId, name: item.name, repositoryName: item.repositoryName, slug: item.slug })) ?? [])
  const [repositoryId, setRepositoryId] = useState("")
  const [filter, setFilter] = useState("")
  const [validation, setValidation] = useNotice()
  const active = useRef<AbortController | null>(null)
  useEffect(() => () => active.current?.abort(), [])
  const repositories = useQuery({ queryKey: ["collected-repositories", session?.userId], queryFn: ({ signal }) => library.listRepositories({}, { signal }) })
  const choices = repositories.data?.repositories ?? []
  const chosenRepository = repositoryId || choices[0]?.id || ""
  const detail = useQuery({ queryKey: ["repository-skills", session?.userId, chosenRepository], queryFn: ({ signal }) => library.getRepository({ repositoryId: chosenRepository }, { signal }), enabled: !!chosenRepository })
  const visible = (detail.data?.skills ?? []).filter((entry) => `${entry.name} ${entry.description} ${entry.path}`.toLocaleLowerCase().includes(filter.toLocaleLowerCase()))
  const save = useMutation({
    mutationFn: async () => {
      const controller = new AbortController()
      active.current = controller
      const draft = { name, description, items: picked.map(({ skillId, slug }) => ({ skillId, slug })) }
      try {
        const result = initial ? await packs.updatePack({ packId: initial.id, expectedRevision: initial.revision, draft }, { signal: controller.signal }) : await packs.createPack({ draft }, { signal: controller.signal })
        if (!result.pack) throw new Error(t("组合未返回"))
        return result.pack.id
      } finally { active.current = null }
    },
    onSuccess: onSaved,
  })
  function submit(event: FormEvent) {
    event.preventDefault()
    const encoder = new TextEncoder()
    if (!name.trim() || encoder.encode(name.trim()).length > 128 || encoder.encode(description.trim()).length > 2048) { setValidation("名称不能为空且最多 128 字节，描述最多 2048 字节（中文通常占 3 字节）。"); return }
    if (picked.length === 0 || picked.length > 256) { setValidation("请选择 1–256 个技能。"); return }
    setValidation("")
    save.mutate()
  }
  return <form className="pack-editor" onSubmit={submit} aria-busy={save.isPending}>
    <div className="pack-detail-heading"><h2 className="text-lg font-semibold">{initial ? t("编辑组合") : t("新建组合")}</h2><Button variant="ghost" disabled={save.isPending} onClick={onCancel}>{t("取消")}</Button></div>
    <fieldset disabled={save.isPending} className="mt-5 min-w-0 space-y-5">
      <div className="space-y-2"><label htmlFor="pack-name">{t("组合名称")}</label><Input id="pack-name" name="name" autoFocus required autoComplete="off" maxLength={128} value={name} onChange={(event) => setName(event.target.value)} placeholder={t("例如：日常开发")} /></div>
      <div className="space-y-2"><label htmlFor="pack-description">{t("描述 ")}<span className="font-normal text-muted-foreground">{t("（可选）")}</span></label><textarea id="pack-description" name="description" className="pack-textarea" rows={3} maxLength={2048} value={description} onChange={(event) => setDescription(event.target.value)} placeholder={t("这个组合适合什么工作流？")} /></div>
      <section aria-labelledby="pick-skills-title"><h3 id="pick-skills-title" className="font-semibold">{t("从已收录仓库中选择")}</h3><p className="mt-2 text-sm text-muted-foreground">{t("可跨仓库选择；切换仓库不会清空已选技能。")}</p>{repositories.isPending ? <p role="status" className="mt-4">{t("正在读取仓库…")}</p> : repositories.isError ? <PackError error={repositories.error} retry={() => { void repositories.refetch() }} /> : choices.length === 0 ? <p className="mt-4 text-sm text-muted-foreground">{t("还没有已收录的仓库。请先到「仓库」添加内容。")}</p> : <>
        <div className="mt-4 space-y-2"><label htmlFor="pack-repository">{t("仓库与分支")}</label><select id="pack-repository" className="pack-select" value={chosenRepository} onChange={(event) => { setRepositoryId(event.target.value); setFilter("") }}>{choices.map((entry) => <option key={entry.id} value={entry.id}>{entry.nameWithOwner} · {entry.branch || t("默认分支")}</option>)}</select></div>
        <div className="filter-field mt-4 w-full"><Search size={16} aria-hidden="true" /><Input aria-label={t("筛选可选技能")} placeholder={t("按技能名称或路径筛选…")} value={filter} onChange={(event) => setFilter(event.target.value)} /></div>
        {detail.isPending ? <p className="mt-4 text-sm" role="status">{t("正在校验仓库并读取技能…")}</p> : detail.isError ? <PackError error={detail.error} retry={() => { void detail.refetch() }} /> : <div className="pack-choices mt-4">{visible.length === 0 ? <p role="status" className="p-4 text-sm text-muted-foreground">{t("没有匹配的技能。")}</p> : visible.map((entry) => {
          const checked = picked.some((item) => item.skillId === entry.id)
          return <label key={entry.id} className="pack-skill-choice" htmlFor={`pick-${entry.id}`}><Checkbox id={`pick-${entry.id}`} checked={checked} disabled={save.isPending || !checked && picked.length >= 256} onCheckedChange={(next) => setPicked((current) => next === true ? [...current, { skillId: entry.id, name: entry.name, repositoryName: detail.data?.repository?.nameWithOwner ?? "", slug: "" }] : current.filter((item) => item.skillId !== entry.id))} /><span className="min-w-0"><span className="block break-words text-sm font-medium">{entry.name}</span><span className="mt-1 block break-words text-xs text-muted-foreground">{entry.path}</span></span></label>
        })}</div>}
      </>}</section>
      <section aria-labelledby="picked-skills-title"><h3 id="picked-skills-title" className="font-semibold">{t("已选技能 ")}<span className="text-muted-foreground">{formatNumber(picked.length)}/256</span></h3><p className="mt-2 text-sm text-muted-foreground">{t("别名留空时自动生成。可用小写英文字母、数字、连字符和下划线，最多 80 字符；同名但内容不同的技能需要不同别名。")}</p>{picked.length === 0 ? <p role="status" className="mt-4 text-sm text-muted-foreground">{t("还未选择技能。")}</p> : <ul className="mt-4 space-y-3">{picked.map((item) => <li key={item.skillId} className="pack-picked"><div className="min-w-0"><p className="break-words text-sm font-medium">{item.name}</p><p className="mt-1 break-words text-xs text-muted-foreground">{item.repositoryName}</p></div><div className="min-w-0 space-y-1"><label className="text-xs" htmlFor={`alias-${item.skillId}`}>{t("安装别名")}</label><Input id={`alias-${item.skillId}`} aria-label={t("{{value1}}（{{value2}}）安装别名", { value1: item.name, value2: item.repositoryName })} autoComplete="off" spellCheck={false} maxLength={80} pattern="[a-z0-9_-]+" placeholder={t("自动生成")} value={item.slug} onChange={(event) => setPicked((current) => current.map((entry) => entry.skillId === item.skillId ? { ...entry, slug: event.target.value } : entry))} /></div><Button variant="ghost" aria-label={t("移除 {{value1}}（{{value2}}）", { value1: item.name, value2: item.repositoryName })} onClick={() => setPicked((current) => current.filter((entry) => entry.skillId !== item.skillId))}><X size={16} aria-hidden="true" /></Button></li>)}</ul>}</section>
    </fieldset>
    {validation && <p role="alert" className="error-notice">{validation}</p>}{save.isError && <PackError error={save.error} />}
    <div className="mt-6 flex flex-wrap items-center gap-3"><Button type="submit" disabled={save.isPending || picked.length === 0}><Check size={16} aria-hidden="true" />{save.isPending ? t("正在保存…") : t("保存组合")}</Button><Button variant="outline" disabled={save.isPending} onClick={onCancel}>{t("取消")}</Button><span className="text-sm text-muted-foreground" role="status">{save.isPending ? t("正在保存组合，请稍候。") : t("分享订阅跟随组合的最新版本。")}</span></div>
  </form>
}
