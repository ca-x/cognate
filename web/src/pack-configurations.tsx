import "./pack-configurations-i18n"
import "./pack-configurations.css"
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { Code } from "@connectrpc/connect"
import { ArrowDown, ArrowUp, Link2, RefreshCw, ShieldCheck } from "lucide-react"
import { packConfigurationRefusal, useApi } from "./api"
import { t } from "./i18n"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { AssetKind, type Asset } from "./gen/skillhub/configuration/v1/configuration_pb"
import { AttachmentState, type AttachmentSnapshot } from "./gen/skillhub/pack/configuration/v1/configuration_pb"

type Reference = { assetId: string; expectedAssetRevision: bigint; targetId: string }
type Phase = "idle" | "editing" | "review" | "checking" | "sending" | "blocked" | "unknown"
type Draft = { base: AttachmentSnapshot; references: Reference[] }
const agents = new Set(["claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"])
const kindNames = new Map([[AssetKind.PROVIDER, "provider"], [AssetKind.MCP, "mcp"], [AssetKind.PROMPT, "prompt"], [AssetKind.AGENT_SETTINGS, "agent-settings"], [AssetKind.WORKSPACE, "workspace"]])
const canonical = /^[A-Z2-7]{26}$/
const positive = (revision: bigint) => revision > 0n && revision < 9223372036854775807n
const eligible = (asset: Asset) => agents.has(asset.agentId) && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1
const pair = (reference: Reference) => `${reference.assetId}/${reference.targetId}`
const referencesOf = (snapshot: AttachmentSnapshot): Reference[] => snapshot.entries.map(entry => ({ assetId: entry.assetId, expectedAssetRevision: entry.assetRevision, targetId: entry.targetId }))

async function snapshotOf(snapshot: AttachmentSnapshot | undefined, packId: string) {
  if (!snapshot || snapshot.packId !== packId || !positive(snapshot.packRevision) || !positive(snapshot.configurationRevision) || snapshot.snapshotDigest.length !== 32 || snapshot.entries.length > 16) throw new Error("invalid metadata snapshot")
  const pairs = new Set<string>()
  const identities = new Set<string>()
  for (const [ordinal, entry] of snapshot.entries.entries()) {
    const key = `${entry.assetId}/${entry.targetId}`
    if (!canonical.test(entry.assetId) || !canonical.test(entry.attachmentId) || !positive(entry.assetRevision) || entry.ordinal !== ordinal || !entry.agentId || !kindNames.has(entry.kind) || !entry.targetId || entry.targetId.length > 128 || entry.schemaVersion < 1 || entry.schemaVersion > 65535 || ![AttachmentState.CURRENT, AttachmentState.STALE, AttachmentState.UNSUPPORTED].includes(entry.state) || pairs.has(key) || identities.has(entry.attachmentId)) throw new Error("invalid metadata entry")
    if (entry.state === AttachmentState.CURRENT && (entry.kind !== AssetKind.PROVIDER || entry.schemaVersion !== 1 || !agents.has(entry.agentId) || entry.targetId !== `${entry.agentId}.provider.user`)) throw new Error("invalid current metadata")
    pairs.add(key)
    identities.add(entry.attachmentId)
  }
  const encoder = new TextEncoder()
  const encoded = Array.from(encoder.encode("cognate/pack-configuration-snapshot/v1"))
  const integer = (value: bigint, size = 8) => {
    const buffer = new DataView(new ArrayBuffer(size))
    if (size === 4) buffer.setUint32(0, Number(value)); else buffer.setBigUint64(0, value)
    encoded.push(...new Uint8Array(buffer.buffer))
  }
  const text = (value: string) => { const bytes = encoder.encode(value); integer(BigInt(bytes.length), 4); encoded.push(...bytes) }
  text(snapshot.packId)
  integer(snapshot.packRevision)
  integer(snapshot.configurationRevision)
  integer(BigInt(snapshot.entries.length))
  for (const entry of snapshot.entries) {
    text(entry.attachmentId); text(entry.assetId); integer(entry.assetRevision)
    text(entry.agentId); text(kindNames.get(entry.kind)!); integer(BigInt(entry.schemaVersion)); text(entry.targetId)
  }
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new Uint8Array(encoded)))
  if (!digest.every((value, index) => value === snapshot.snapshotDigest[index])) throw new Error("invalid snapshot digest")
  return snapshot
}

function signature(snapshot: AttachmentSnapshot) {
  return JSON.stringify([snapshot.packId, String(snapshot.packRevision), String(snapshot.configurationRevision), Array.from(snapshot.snapshotDigest), snapshot.entries.map(entry => [entry.attachmentId, entry.assetId, String(entry.assetRevision), entry.agentId, entry.kind, entry.schemaVersion, entry.targetId, entry.ordinal, entry.state])])
}

function refusalText(error: unknown) {
  switch (packConfigurationRefusal(error)) {
    case Code.Aborted: return "附件或资产版本冲突。请只读刷新，再重新编辑和审阅。"
    case Code.PermissionDenied: case Code.Unauthenticated: case Code.NotFound: return "当前所有者无权访问此组合或资产，或资源已删除。"
    case Code.InvalidArgument: case Code.FailedPrecondition: case Code.ResourceExhausted: return "服务端拒绝了引用、目标或数量。请只读刷新并重新审阅。"
    default: return "提交结果不明（Unknown）。不会重放；匹配的当前状态也不能证明哪次提交成功。"
  }
}

function NativeBoundary() {
  return <p className="pc-boundary"><ShieldCheck size={18} aria-hidden="true" /><span><code>NativeUnavailable</code>: {t("仅维护私有引用，不读取配置正文或密钥，不授予分享访问，也不自动应用到设备。")}</span></p>
}

export function PackConfigurations({ packId, packRevision, readFailed }: { packId: string; packRevision: bigint; readFailed: boolean }) {
  useTranslation()
  const api = useApi()
  const queryClient = useQueryClient()
  const [phase, setPhase] = useState<Phase>("idle")
  const [draft, setDraft] = useState<Draft | null>(null)
  const [candidate, setCandidate] = useState("")
  const [approved, setApproved] = useState(false)
  const [notice, setNotice] = useState("")
  const heading = useRef<HTMLHeadingElement>(null)
  const dialogHeading = useRef<HTMLHeadingElement>(null)
  const reviewTrigger = useRef<HTMLButtonElement>(null)
  const section = useRef<HTMLElement>(null)
  const candidateTrigger = useRef<HTMLButtonElement>(null)
  const editTrigger = useRef<HTMLButtonElement>(null)
  const editFocus = useRef<{ previous: HTMLElement; target: () => HTMLButtonElement | null } | null>(null)
  const alive = useRef(false)
  const current = useRef({ session: api.session, packRevision, readFailed })
  current.current = { session: api.session, packRevision, readFailed }
  const active = useRef<{ controller: AbortController; dispatched: boolean; revision: bigint; session: typeof api.session } | null>(null)
  const key = ["pack-configurations", api.session?.userId, packId]
  const query = useQuery({
    queryKey: key, enabled: !!api.session && phase !== "unknown", retry: false, refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => {
      const [response, metadata] = await Promise.all([api.packConfigurations.getAttachments({ packId }, { signal }), api.configuration.listAssets({}, { signal })])
      if (metadata.assets.length > 250 || metadata.assets.some(asset => !canonical.test(asset.id) || !positive(asset.revision) || !asset.agentId || asset.schemaVersion < 1 || asset.schemaVersion > 65535) || new Set(metadata.assets.map(asset => asset.id)).size !== metadata.assets.length) throw new Error("invalid asset metadata")
      return { snapshot: await snapshotOf(response.snapshot, packId), assets: metadata.assets }
    },
  })
  const data = query.data
  const unverified = readFailed || query.isError || query.isFetching || !data || data.snapshot.packRevision !== packRevision
  const changed = !!draft && !!data && signature(draft.base) !== signature(data.snapshot)
  const busy = phase === "checking" || phase === "sending"
  const dialogOpen = phase === "review" || busy
  const canEdit = !unverified && !busy && phase !== "unknown"
  const canReview = phase === "editing" && !unverified && !changed && !!draft && draft.references.every(reference => data?.assets.some(asset => asset.id === reference.assetId))

  useEffect(() => {
    alive.current = true
    return () => { alive.current = false; active.current?.controller.abort() }
  }, [])
  useEffect(() => {
    const operation = active.current
    if (operation && (readFailed || operation.revision !== packRevision || operation.session !== api.session)) operation.controller.abort()
  }, [packRevision, readFailed, api.session])

  useLayoutEffect(() => {
    const pending = editFocus.current
    editFocus.current = null
    if (!pending || document.activeElement !== document.body && document.activeElement !== pending.previous) return
    const target = pending.target()
    if (target?.isConnected && !target.disabled) target.focus()
    else heading.current?.focus()
  }, [draft, phase, candidate])

  function keepEditFocus(previous: HTMLElement, target: () => HTMLButtonElement | null) {
    if (document.activeElement === previous) editFocus.current = { previous, target }
  }

  function referenceButton(reference: Reference, action: string) {
    const row = section.current?.querySelector<HTMLElement>(`.pc-editor [data-reference="${CSS.escape(pair(reference))}"]`)
    return row?.querySelector<HTMLButtonElement>(`[data-pc-action="${action}"]:not(:disabled)`) ?? row?.querySelector<HTMLButtonElement>("button:not(:disabled)") ?? null
  }

  async function refresh() {
    if (active.current || query.isFetching) return
    const result = await query.refetch()
    if (!alive.current) return
    if (result.isSuccess) {
      if (phase === "unknown") setPhase("blocked")
      setNotice(phase === "unknown" ? "只读核对完成；旧草稿仅供检查。后续替换必须重新编辑和审阅。" : "已重新读取元数据。不会覆盖草稿或提交替换。")
      void queryClient.invalidateQueries({ queryKey: ["pack", api.session?.userId, packId] })
      void queryClient.invalidateQueries({ queryKey: ["packs", api.session?.userId] })
    }
  }

  function beginEdit() {
    if (!canEdit || !data) return
    setDraft({ base: data.snapshot, references: referencesOf(data.snapshot) })
    setCandidate("")
    setNotice("")
    setPhase("editing")
  }

  function cancelDialog() {
    const operation = active.current
    operation?.controller.abort()
    setApproved(false)
    if (operation?.dispatched) {
      setPhase("unknown")
      setNotice("提交结果不明（Unknown）。不会重放；匹配的当前状态也不能证明哪次提交成功。")
    } else {
      setPhase("editing")
      setNotice("已取消审阅；未发送替换。")
    }
  }

  async function replace() {
    if (active.current || phase !== "review" || !approved || !draft || unverified || changed || !api.session) return
    const capture = draft
    const operation = { controller: new AbortController(), dispatched: false, revision: capture.base.packRevision, session: api.session }
    active.current = operation
    setPhase("checking")
    const validContext = () => alive.current && !operation.controller.signal.aborted && current.current.session === operation.session && !current.current.readFailed && current.current.packRevision === capture.base.packRevision
    try {
      const session = await api.auth.getSession({}, { signal: operation.controller.signal })
      if (session.userId !== operation.session.userId || !validContext()) throw new Error("owner changed")
      const fresh = await snapshotOf((await api.packConfigurations.getAttachments({ packId }, { signal: operation.controller.signal })).snapshot, packId)
      if (signature(fresh) !== signature(capture.base) || !validContext()) throw new Error("review changed")
      operation.dispatched = true
      setPhase("sending")
      const response = await api.packConfigurations.replaceAttachments({ packId, expectedPackRevision: capture.base.packRevision, expectedConfigurationRevision: capture.base.configurationRevision, references: capture.references }, { signal: operation.controller.signal })
      const result = await snapshotOf(response.snapshot, packId)
      if (!validContext() || result.packRevision !== capture.base.packRevision + 1n || result.configurationRevision !== capture.base.configurationRevision + 1n || result.entries.length !== capture.references.length || result.entries.some((entry, index) => {
        const reference = capture.references[index]
        const previous = capture.base.entries.find(old => old.assetId === entry.assetId && old.targetId === entry.targetId)
        const asset = data?.assets.find(asset => asset.id === reference.assetId)
        return !asset || entry.agentId !== asset.agentId || entry.kind !== asset.kind || entry.schemaVersion !== asset.schemaVersion || entry.assetId !== reference.assetId || entry.targetId !== reference.targetId || entry.assetRevision !== reference.expectedAssetRevision || entry.state !== AttachmentState.CURRENT || (previous && previous.attachmentId !== entry.attachmentId)
      })) throw new Error("unconfirmed receipt")
      if (alive.current) {
        setPhase("idle")
        setDraft(null)
        setNotice("附件元数据已替换。未应用设备配置，技能与分享保持不变。")
        queryClient.setQueryData(key, data ? { ...data, snapshot: result } : undefined)
        void queryClient.invalidateQueries({ queryKey: ["pack", api.session?.userId, packId] })
        void queryClient.invalidateQueries({ queryKey: ["packs", api.session?.userId] })
      }
    } catch (error) {
      if (alive.current) {
        const unknown = operation.dispatched && (operation.controller.signal.aborted || packConfigurationRefusal(error) === undefined)
        setPhase(unknown ? "unknown" : "blocked")
        setNotice(operation.dispatched ? unknown ? "提交结果不明（Unknown）。不会重放；匹配的当前状态也不能证明哪次提交成功。" : refusalText(error) : "审阅已失效或读取失败；未发送替换。请只读刷新后重新编辑。")
      }
    } finally {
      if (active.current === operation) active.current = null
      if (alive.current) setApproved(false)
    }
  }

  function changeReferences(references: Reference[]) {
    if (phase === "editing" && draft && !busy) setDraft({ ...draft, references })
  }

  function move(index: number, offset: number, trigger: HTMLButtonElement) {
    if (!draft) return
    const references = [...draft.references]
    const destination = index + offset
    if (destination < 0 || destination >= references.length) return
    const moved = references[index]
    keepEditFocus(trigger, () => referenceButton(moved, offset < 0 ? "up" : "down"))
    ;[references[index], references[destination]] = [references[destination], references[index]]
    changeReferences(references)
  }

  const assets = data?.assets ?? []
  const choices = assets.filter(asset => eligible(asset) && !draft?.references.some(reference => reference.assetId === asset.id && reference.targetId === `${asset.agentId}.provider.user`))
  const assetName = (assetId: string) => assets.find(asset => asset.id === assetId)?.name ?? t("资产元数据缺失，引用保留")
  function referenceList(references: Reference[], editing = false) {
    return references.length === 0 ? <p className="pc-empty">{t("没有配置附件。空列表只清空附件，不删除资产或技能。")}</p> : <ol className="pc-references">{references.map((reference, index) => {
      const asset = assets.find(entry => entry.id === reference.assetId)
      const original = data?.snapshot.entries.find(entry => entry.assetId === reference.assetId && entry.targetId === reference.targetId)
      const status = !asset ? "资产元数据缺失，引用保留" : !eligible(asset) || original?.state === AttachmentState.UNSUPPORTED ? "不支持的元数据" : reference.expectedAssetRevision !== asset.revision || original?.state === AttachmentState.STALE && reference.expectedAssetRevision === original.assetRevision ? "旧资产版本，不自动追随" : "当前元数据"
      return <li key={pair(reference)} data-reference={pair(reference)} className="pc-reference"><div className="pc-reference-body"><strong>{index + 1}. {assetName(reference.assetId)}</strong><span className="pc-status">{t(status)}</span><code>{reference.assetId}</code><span><code>{reference.targetId}</code> · {t("资产版本")} {String(reference.expectedAssetRevision)}</span>{asset && <span>{asset.agentId} · {t("格式版本")} {asset.schemaVersion}</span>}</div>{editing && <div className="pc-actions">
        <Button data-pc-action="up" variant="outline" disabled={phase !== "editing" || index === 0} aria-label={t("上移第 {{number}} 项", { number: index + 1 })} onClick={event => move(index, -1, event.currentTarget)}><ArrowUp size={16} aria-hidden="true" />{t("上移")}</Button>
        <Button data-pc-action="down" variant="outline" disabled={phase !== "editing" || index === references.length - 1} aria-label={t("下移第 {{number}} 项", { number: index + 1 })} onClick={event => move(index, 1, event.currentTarget)}><ArrowDown size={16} aria-hidden="true" />{t("下移")}</Button>
        {asset && eligible(asset) && reference.expectedAssetRevision !== asset.revision && <Button variant="outline" disabled={phase !== "editing"} onClick={() => changeReferences(references.map((entry, position) => position === index ? { ...entry, expectedAssetRevision: asset.revision } : entry))}>{t("使用当前资产版本")}</Button>}
        <Button data-pc-action="remove" variant="ghost" disabled={phase !== "editing"} aria-label={t("移除第 {{number}} 项", { number: index + 1 })} onClick={event => {
          const remaining = references.filter((_, position) => position !== index)
          const successor = remaining[Math.min(index, remaining.length - 1)]
          keepEditFocus(event.currentTarget, () => successor ? referenceButton(successor, "remove") : candidateTrigger.current)
          changeReferences(remaining)
        }}>{t("移除引用")}</Button>
      </div>}</li>
    })}</ol>
  }

  return <section ref={section} className="pack-configurations" aria-labelledby={`pc-heading-${packId}`} aria-busy={busy || query.isFetching}>
    <div className="pc-heading"><div><p className="packs-eyebrow"><Link2 size={15} aria-hidden="true" />{t("所有者元数据")}</p><h3 ref={heading} tabIndex={-1} id={`pc-heading-${packId}`}>{t("配置附件")}</h3></div><Button className="pc-refresh" variant="outline" disabled={busy} aria-disabled={query.isFetching} onClick={() => { void refresh() }}><RefreshCw size={15} aria-hidden="true" />{phase === "unknown" ? t("只读核对当前状态") : t("读取附件元数据")}</Button></div>
    <NativeBoundary />
    {data && <p className="pc-revisions">{t("组合版本")} <strong>{String(data.snapshot.packRevision)}</strong> · {t("配置组版本")} <strong>{String(data.snapshot.configurationRevision)}</strong> · {data.snapshot.entries.length}/16</p>}
    {query.isPending && <p role="status">{t("正在读取附件与资产元数据…")}</p>}
    {unverified && !query.isPending && <p role="alert" className="error-notice">{t("读取失败或组合版本未核实。保留上次元数据和草稿，禁止写入。")}</p>}
    <p className="pc-notice" role={phase === "unknown" || phase === "blocked" ? "alert" : "status"}>{t(notice)}</p>
    {data && <>{referenceList(referencesOf(data.snapshot))}<div className="pc-actions"><Button className="pc-edit" ref={editTrigger} variant="outline" disabled={!canEdit} onClick={beginEdit}>{draft ? t("放弃旧审阅，重新编辑") : t("编辑配置附件")}</Button></div></>}
    {draft && <div className="pc-editor"><h4>{t("待提交的有序引用")} · {draft.references.length}/16</h4><p>{t("从空选择开始添加。旧引用保留原版本；更新、移除和清空都需要明确审阅。")}</p>{referenceList(draft.references, phase === "editing")}
      {phase === "editing" && <><div className="pc-candidate"><label htmlFor={`pc-asset-${packId}`}>{t("可附加的所有者资产")}</label><Select value={candidate} onValueChange={setCandidate} disabled={unverified || draft.references.length >= 16}><SelectTrigger ref={candidateTrigger} id={`pc-asset-${packId}`}><SelectValue placeholder={t("明确选择一个资产")} /></SelectTrigger><SelectContent className="pc-select">{choices.map(asset => <SelectItem key={asset.id} value={asset.id}>{asset.name} · {asset.agentId} · {String(asset.revision)}</SelectItem>)}</SelectContent></Select><Button className="pc-add" variant="outline" disabled={unverified || !candidate || draft.references.length >= 16 || !choices.some(asset => asset.id === candidate)} onClick={event => {
        const asset = choices.find(entry => entry.id === candidate)
        if (asset) {
          const reference = { assetId: asset.id, expectedAssetRevision: asset.revision, targetId: `${asset.agentId}.provider.user` }
          keepEditFocus(event.currentTarget, () => candidateTrigger.current?.disabled ? referenceButton(reference, "remove") : candidateTrigger.current)
          changeReferences([...draft.references, reference]); setCandidate("")
        }
      }}>{t("添加引用")}</Button></div><p className="pc-help">{t("只列出 schema 1 provider 的十四类元数据目标；服务端校验为准，不代表原生支持。")}</p><div className="pc-actions"><Button variant="outline" onClick={event => { keepEditFocus(event.currentTarget, () => candidateTrigger.current); changeReferences([]) }} disabled={!draft.references.length}>{t("清空待提交引用")}</Button><Button className="pc-review" ref={reviewTrigger} disabled={!canReview} onClick={() => { setApproved(false); setPhase("review") }}>{t("审阅替换")}</Button><Button variant="ghost" onClick={event => { keepEditFocus(event.currentTarget, () => editTrigger.current); setPhase("idle"); setDraft(null); setNotice("已放弃草稿；未发送替换。") }}>{t("放弃草稿")}</Button></div></>}
      {changed && <p role="alert" className="error-notice">{t("审阅基线已变化。草稿保留；请重新编辑，不重用旧确认。")}</p>}
    </div>}
    <Dialog open={dialogOpen} onOpenChange={open => { if (!open) cancelDialog() }}><DialogContent className="pc-dialog" onOpenAutoFocus={event => { event.preventDefault(); dialogHeading.current?.focus() }} onCloseAutoFocus={event => { event.preventDefault(); const target = reviewTrigger.current; if (target?.isConnected && !target.disabled) target.focus(); else heading.current?.focus() }}>
      <DialogTitle ref={dialogHeading} tabIndex={-1}>{t("确认整组替换附件元数据")}</DialogTitle><DialogDescription>{t("审阅不是服务端预验证。确认后重验所有者与完整快照，只发送一次双 CAS 替换。")}</DialogDescription>
      <dl className="pc-context"><dt>{t("服务来源")}</dt><dd><code>{window.location.origin}</code></dd><dt>{t("当前所有者")}</dt><dd>{api.session?.username}<code>{api.session?.userId}</code></dd><dt>{t("组合标识")}</dt><dd><code>{packId}</code></dd><dt>{t("组合版本 / 配置组版本")}</dt><dd>{String(draft?.base.packRevision)} / {String(draft?.base.configurationRevision)}</dd></dl>
      <h4>{t("旧的有序引用")}</h4>{draft && referenceList(referencesOf(draft.base))}<h4>{t("新的有序引用")}</h4>{draft && referenceList(draft.references)}
      <NativeBoundary />
      <label className="pc-approval"><Checkbox checked={approved} disabled={busy} onCheckedChange={value => setApproved(value === true)} />{t("我已审阅顺序、版本和清空后果，只替换元数据。")}</label>
      <div className="pc-actions"><Button variant="outline" onClick={cancelDialog}>{busy ? t("停止等待并关闭") : t("取消审阅")}</Button><Button className="pc-confirm" disabled={busy || !approved || unverified || changed} onClick={() => { void replace() }}>{busy ? t("正在核验或提交…") : t("确认替换一次")}</Button></div>
    </DialogContent></Dialog>
  </section>
}
