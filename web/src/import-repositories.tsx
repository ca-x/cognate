import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ChangeEvent } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { useBlocker } from "@tanstack/react-router"
import { create } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { FileUp } from "lucide-react"
import { useApi } from "./api"
import { t, formatNumber } from "./i18n"
import { ExportFormat, ImportSelectionSource, ImportReceiptPhase, ImportCandidateStatus, ImportCandidateOptionsSchema, SkillBindingSchema, type RepositoryCandidate, type ImportCandidateOptions } from "./gen/skillhub/import/v1/import_pb"
import { ImportSelectionForm, ImportSelectionDiscard, type BindingChoice, type BindingRepositoryChoice, type ImportSource } from "./import-selection-form"
import { acceptSelectionReceipt, acceptSelectionStatus, acknowledgeSelection, canConfirmSelection, changeSelectionOption, chooseSelection, initialSelectionState, selectionPlan, withSelectionPreview, type SelectionPlan, type SelectionState } from "./import-selection-state"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"

type Outcome = "pending" | "in_flight" | "collected" | "failed" | "unknown" | "not_attempted"
type Candidate = Pick<RepositoryCandidate, "repository" | "branch" | "existingRepositoryId" | "sourceEnabled"> & { outcome: Outcome; code?: Code }
type Phase = "idle" | "reading" | "previewing" | "collecting"
type Source = "magpie" | "cc-switch"

const sourceOptions = {
  magpie: { maximumBytes: 1024 * 1024, label: "Magpie JSON 导出（最大 1 MiB）", accept: ".json,application/json", invalidFile: "请选择非空 JSON 导出，最大 1 MiB。" },
  "cc-switch": { maximumBytes: 16 * 1024 * 1024, label: "cc-switch SQLite 快照（最大 16 MiB）", accept: ".db,.sqlite,.sqlite3,application/vnd.sqlite3,application/x-sqlite3", invalidFile: "请选择非空、自包含的 SQLite 快照，最大 16 MiB。" },
}
const itemFailures = new Set([Code.InvalidArgument, Code.NotFound, Code.FailedPrecondition, Code.DataLoss])
const outcomeLabels: Record<Outcome, string> = {
  pending: "待选择",
  in_flight: "正在收录并验证…",
  collected: "已收录并验证",
  failed: "此仓库收录失败",
  unknown: "结果待确认",
  not_attempted: "未尝试",
}

function previewError(code: Code, source: Source) {
  switch (code) {
    case Code.InvalidArgument:
    case Code.FailedPrecondition: return source === "magpie" ? "无法预览此导出。请使用有效的 Magpie library.json。" : "无法预览此快照。请提供有效 cc-switch schema 的独立 rollback-mode SQLite 快照；不接受 WAL 模式数据库。"
    case Code.ResourceExhausted: return source === "magpie" ? "导出超过限制或预览请求较多，请检查 1 MiB 上限后稍后重试。" : "快照超过限制或预览请求较多，请检查 16 MiB 上限后稍后重试。"
    case Code.Unauthenticated:
    case Code.PermissionDenied: return "会话已失效或没有权限，请重新登录后再试。"
    default: return "预览未完成，请检查连接后重新选择文件。未收录任何仓库。"
  }
}

function collectionError(code?: Code) {
  switch (code) {
    case Code.InvalidArgument: return "仓库或分支格式无效。"
    case Code.NotFound: return "无法获取公开仓库或分支；暂不支持私有来源。"
    case Code.FailedPrecondition: return "归档中没有可用技能，或归档格式不安全。"
    case Code.DataLoss: return "已有缓存缺失或损坏；未验证成功，不会覆盖快照。"
    case Code.ResourceExhausted: return "达到容量限制或被限流，已停止后续收录。"
    case Code.Unauthenticated:
    case Code.PermissionDenied: return "会话已失效或没有权限，已停止后续收录。"
    default: return "等待中断或服务异常，已停止后续收录。"
  }
}

function readExport(file: File, signal: AbortSignal): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    const abort = () => { reader.abort(); reject(new ConnectError("canceled", Code.Canceled)) }
    reader.onload = () => { if (reader.result instanceof ArrayBuffer) resolve(new Uint8Array(reader.result)); else reject(new Error("read failed")) }
    reader.onerror = () => reject(new Error("read failed"))
    reader.onabort = () => reject(new ConnectError("canceled", Code.Canceled))
    reader.onloadend = () => signal.removeEventListener("abort", abort)
    if (signal.aborted) { abort(); return }
    signal.addEventListener("abort", abort, { once: true })
    reader.readAsArrayBuffer(file)
  })
}

function LegacyImportRepositories({ collectionBusy, onCollectingChange }: { collectionBusy: boolean; onCollectingChange: (collecting: boolean) => void }) {
  useTranslation()
  const { imports, library, session } = useApi()
  const queryClient = useQueryClient()
  const file = useRef<File | null>(null)
  const active = useRef<AbortController | null>(null)
  const generation = useRef(0)
  const heading = useRef<HTMLHeadingElement>(null)
  const [source, setSource] = useState<Source>("magpie")
  const [hasFile, setHasFile] = useState(false)
  const [phase, setPhase] = useState<Phase>("idle")
  const [candidates, setCandidates] = useState<Candidate[] | null>(null)
  const [selection, setSelection] = useState<number[]>([])
  const [referenceCount, setReferenceCount] = useState<number | null>(null)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const busy = phase !== "idle"
  const options = sourceOptions[source]

  useLayoutEffect(() => () => {
    generation.current++
    active.current?.abort()
    file.current = null
  }, [])

  useLayoutEffect(() => {
    onCollectingChange(phase === "collecting")
    return () => onCollectingChange(false)
  }, [phase, onCollectingChange])

  useEffect(() => {
    if (candidates !== null && phase === "idle") heading.current?.focus()
  }, [candidates, phase])

  function invalidate() {
    void queryClient.invalidateQueries({ queryKey: ["library-repositories", session?.userId] })
    void queryClient.invalidateQueries({ queryKey: ["repository-skills", session?.userId] })
    void queryClient.invalidateQueries({ queryKey: ["collected-repositories", session?.userId] })
  }

  function clear() {
    if (phase === "collecting") invalidate()
    generation.current++
    active.current?.abort()
    active.current = null
    file.current = null
    setHasFile(false)
    setPhase("idle")
    setCandidates(null)
    setSelection([])
    setReferenceCount(null)
    setNotice("")
    setError("")
  }

  function chooseFile(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.target.files?.[0]
    event.target.value = ""
    if (!selected) return
    clear()
    if (selected.size === 0 || selected.size > options.maximumBytes) {
      setError(options.invalidFile)
      return
    }
    file.current = selected
    setHasFile(true)
  }

  async function preview() {
    if (active.current || !file.current || !session) return
    const selectedFile = file.current
    file.current = null
    setHasFile(false)
    const controller = new AbortController()
    active.current = controller
    const attempt = ++generation.current
    let bytes: Uint8Array | undefined
    setError("")
    setNotice("")
    setPhase("reading")
    try {
      bytes = await readExport(selectedFile, controller.signal)
      if (attempt !== generation.current || controller.signal.aborted) return
      if (!bytes.length || bytes.length > options.maximumBytes) { setError(options.invalidFile); return }
      setPhase("previewing")
      const result = source === "magpie"
        ? await imports.previewLibraryExport({ format: ExportFormat.MAGPIE_LIBRARY_JSON, exportData: bytes }, { signal: controller.signal })
        : await imports.previewCCSwitchSnapshot({ snapshotData: bytes }, { signal: controller.signal })
      if (attempt !== generation.current || controller.signal.aborted) return
      setCandidates(result.repositories.map((entry) => ({ repository: entry.repository, branch: entry.branch, existingRepositoryId: entry.existingRepositoryId, sourceEnabled: entry.sourceEnabled, outcome: "pending" })))
      setReferenceCount("referencedSkillCount" in result ? result.referencedSkillCount : null)
      setSelection([])
    } catch (failure) {
      if (attempt === generation.current) setError(previewError(ConnectError.from(failure).code, source))
    } finally {
      bytes?.fill(0)
      if (attempt === generation.current) { active.current = null; setPhase("idle") }
    }
  }

  function cancel() {
    const collecting = phase === "collecting"
    generation.current++
    active.current?.abort()
    active.current = null
    file.current = null
    setHasFile(false)
    setPhase("idle")
    setCandidates((rows) => rows?.map((row) => row.outcome === "in_flight" ? { ...row, outcome: "unknown", code: Code.Canceled } : row) ?? null)
    setNotice(collecting ? "已停止等待及后续收录。已完成的仓库保留；当前项可能已提交，请重新验证。" : "已取消预览，未收录任何仓库。请重新选择文件。")
    if (collecting) invalidate()
  }

  async function collect() {
    if (active.current || collectionBusy || !candidates || !selection.length || !session) return
    const chosen = [...selection]
    const snapshot = candidates
    const controller = new AbortController()
    active.current = controller
    const attempt = ++generation.current
    setPhase("collecting")
    setError("")
    setNotice("")
    setCandidates((rows) => rows?.map((row, index) => chosen.includes(index) ? { ...row, outcome: "not_attempted", code: undefined } : row) ?? null)
    const update = (index: number, outcome: Outcome, code?: Code) => setCandidates((rows) => rows?.map((row, position) => position === index ? { ...row, outcome, code } : row) ?? null)
    try {
      for (const index of chosen) {
        if (controller.signal.aborted || attempt !== generation.current) break
        update(index, "in_flight")
        const candidate = snapshot[index]
        try {
          const result = await library.collectRepository({ repository: candidate.repository, branch: candidate.branch }, { signal: controller.signal })
          if (attempt !== generation.current || controller.signal.aborted) break
          if (!result.repository) throw new ConnectError("missing result", Code.Unknown)
          update(index, "collected")
        } catch (failure) {
          if (attempt !== generation.current || controller.signal.aborted) break
          const code = ConnectError.from(failure).code
          const definite = itemFailures.has(code)
          update(index, definite ? "failed" : "unknown", code)
          if (!definite) { setNotice("收录已停止。先前成功项保留，后续项未尝试；结果待确认项可显式重新验证。"); break }
        }
      }
    } finally {
      if (attempt === generation.current) {
        active.current = null
        setPhase("idle")
        invalidate()
      }
    }
  }

  const unknownCount = candidates?.filter((candidate) => candidate.outcome === "unknown").length ?? 0
  const completedCount = candidates?.filter((candidate) => candidate.outcome === "collected").length ?? 0
  return <section className="source-card my-6" aria-labelledby="magpie-import-title">
    <div className="source-card-heading"><span className="service-icon"><FileUp size={22} aria-hidden="true" /></span><div className="min-w-0"><h2 id="magpie-import-title" className="font-semibold">{t("从导出文件收录仓库来源")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("选择来源及导出文件，仅在点击上传并预览后发送到此 Cognate 服务端。预览不会收录仓库。")}</p></div></div>
    <div className="source-card-body space-y-4">
      <div className="space-y-2"><label htmlFor="import-source" className="block text-sm font-medium">{t("导入来源")}</label><Select value={source} onValueChange={(value) => { if (value !== "magpie" && value !== "cc-switch") return; clear(); setSource(value) }}><SelectTrigger id="import-source" className="w-full sm:w-auto" aria-describedby="import-source-help"><SelectValue /></SelectTrigger><SelectContent>{Object.entries(sourceOptions).map(([value, option]) => <SelectItem key={value} value={value}>{t(option.label)}</SelectItem>)}</SelectContent></Select><p id="import-source-help" className="text-sm text-muted-foreground">{t("切换来源会停止等待及后续操作，并清除文件、候选、选择和进度；已提交的收录不会撤销。")}</p></div>
      <p id="magpie-scope" className="text-sm leading-6 text-muted-foreground">{t("仅支持公开 GitHub 仓库。确认后整仓扫描全部技能，不限于导出中列出的技能；不迁移本地修改、技能内容或客户端配置。新收录默认私有，不自动安装或发布。")}</p>
      {source === "cc-switch" && <div id="snapshot-warning" className="space-y-2 rounded-lg border border-border p-3 text-sm leading-6"><p className="font-medium">{t("整个数据库可能包含 API 密钥、token 等凭据。只有点击「上传并预览」才会发送完整快照，请仅上传到你信任的 Cognate 服务器。")}</p><p>{t("必须通过 SQLite backup API 或一致性导出生成 rollback-mode、自包含的独立快照。不能直接复制运行中的主数据库并忽略 WAL 文件。")}</p><p className="text-muted-foreground">{t("服务端仅在内存中 Deserialize，拒绝 WAL 模式或无效 schema，不将原始数据库写入磁盘。浏览器不执行 SQL、moejs 或来源适配器。")}</p></div>}
      <div className="space-y-2"><label htmlFor="magpie-export" className="text-sm font-medium">{t(options.label)}</label><Input id="magpie-export" type="file" accept={options.accept} disabled={busy} onChange={chooseFile} aria-describedby={`magpie-scope magpie-file-help${source === "cc-switch" ? " snapshot-warning" : ""}`} className="h-auto min-h-11 py-2" /><p id="magpie-file-help" className="text-sm text-muted-foreground">{hasFile ? t("已选择导出文件。点击上传并预览后才会发送。") : t("原文可能含密钥，不显示原文或文件名，不保存导出或凭据。")}</p></div>
      <div className="flex flex-wrap gap-3"><Button onClick={() => void preview()} disabled={busy || !hasFile}>{t("上传并预览")}</Button>{busy ? <Button variant="outline" onClick={cancel}>{t("停止等待及后续操作")}</Button> : (hasFile || candidates || error || notice) && <Button variant="ghost" onClick={clear}>{t("清除导入草稿")}</Button>}</div>
      {error && <p role="alert" className="text-sm text-destructive">{t(error)}</p>}
      <p role="status" aria-live="polite" className="min-h-6 text-sm leading-6 text-muted-foreground">{phase === "reading" ? t("正在读取导出…") : phase === "previewing" ? t("正在上传并预览，不会收录仓库…") : phase === "collecting" ? t("正在按选择顺序逐仓收录，同时最多一个请求。") : t(notice)}</p>
      <div hidden={candidates === null}>
        <h3 ref={heading} tabIndex={-1} className="font-medium">{t("预览候选仓库")}</h3>
        <p className="mt-2 text-sm text-muted-foreground">{referenceCount === null ? t("{{repositories}} 个候选仓库（不提供技能引用计数）", { repositories: formatNumber(candidates?.length ?? 0) }) : t("{{repositories}} 个候选仓库 · {{skills}} 个导出技能引用（不是将收录的技能数量）", { repositories: formatNumber(candidates?.length ?? 0), skills: formatNumber(referenceCount) })}</p>
        {source === "cc-switch" && <p className="mt-2 text-sm text-muted-foreground">{t("此预览仅包含仓库候选，确认后整仓收录；本地技能内容和客户端配置尚未上传或应用，不代表完整迁移。")}</p>}
        {candidates?.length === 0 ? <p role="status" className="py-5 text-sm">{t(source === "magpie" ? "没有可收录的 GitHub 仓库。导出可能为空，或仅包含 git / folder 等暂不支持的来源。" : "快照中没有可收录的 GitHub 仓库候选。")}</p> : <>
          <p id="magpie-selection-help" className="my-3 text-sm text-muted-foreground">{t("默认不选。按勾选顺序收录；取消勾选再选择会排到末尾。已收录项仍验证缓存，不更新既有快照。")}</p>
          <ul className="my-4 max-h-96 overflow-y-auto rounded-lg border border-border" aria-describedby="magpie-selection-help">
            {candidates?.map((candidate, index) => <li key={index} className="border-b border-border p-3 last:border-b-0">
              <div className="flex items-start gap-3"><Checkbox id={`magpie-candidate-${index}`} className="mt-3" disabled={busy} checked={selection.includes(index)} onCheckedChange={(checked) => setSelection((current) => checked === true ? [...current.filter((entry) => entry !== index), index] : current.filter((entry) => entry !== index))} aria-describedby={`magpie-result-${index}`} /><label htmlFor={`magpie-candidate-${index}`} className="min-h-11 min-w-0 flex-1 cursor-pointer py-2"><span className="block break-all font-mono text-sm font-medium">{candidate.repository}</span><span className="mt-1 block break-all text-xs text-muted-foreground">{t("分支：{{branch}}", { branch: candidate.branch || t("默认分支") })}{selection.includes(index) && ` · ${t("顺序 {{order}}", { order: formatNumber(selection.indexOf(index) + 1) })}`}</span></label></div>
              {candidate.existingRepositoryId && <p className="mt-1 text-xs text-muted-foreground">{t("已收录；仍需验证缓存，不更新快照。")}</p>}
              {candidate.sourceEnabled !== undefined && <p className="mt-1 text-xs text-muted-foreground">{t(candidate.sourceEnabled ? "来源中已启用；仍需显式选择收录，不会自动启用来源。" : "来源中已禁用；仍可显式选择收录，不会启用原来源。")}</p>}
              <p id={`magpie-result-${index}`} className="mt-2 text-sm"><span className="font-medium">{t(outcomeLabels[candidate.outcome])}</span>{candidate.code !== undefined && <span className="block text-muted-foreground">{t(collectionError(candidate.code))}</span>}</p>
            </li>)}
          </ul>
          <p role="status" className="text-sm text-muted-foreground">{t("已选 {{selected}} · 已验证 {{completed}} · 待确认 {{unknown}}", { selected: formatNumber(selection.length), completed: formatNumber(completedCount), unknown: formatNumber(unknownCount) })}</p>
          {unknownCount > 0 && <p className="mt-3 text-sm leading-6 text-muted-foreground">{t("待确认不代表未入库。可保留待确认项勾选并再次收录以验证缓存；重复收录不会新增重复记录。请先检查连接、权限或限流，系统不会自动重试。")}</p>}
          <div className="mt-4 flex flex-wrap gap-3"><Button disabled={busy || collectionBusy || !selection.length} onClick={() => void collect()}>{t("收录并验证所选仓库")}</Button><Button variant="ghost" disabled={busy || !selection.length} onClick={() => setSelection([])}>{t("取消所有选择")}</Button></div>
          {collectionBusy && <p role="status" className="mt-3 text-sm text-muted-foreground">{t("另一个仓库正在收录，请等待完成后再开始。")}</p>}
          <p className="mt-3 text-sm leading-6 text-muted-foreground">{t("每仓独立提交。明确单项失败可继续；取消、断线、鉴权、限流或未知错误停止后续。先前成功不会整批回滚。")}</p>
        </>}
      </div>
    </div>
  </section>
}

function SelectionImportRepositories({ collectionBusy, onCollectingChange, onLegacy }: { collectionBusy: boolean; onCollectingChange: (collecting: boolean) => void; onLegacy: () => void }) {
  const { i18n } = useTranslation()
  const { imports, library, session } = useApi()
  const queryClient = useQueryClient()
  const [source, setSource] = useState<ImportSource>("magpie")
  const [state, updateState] = useState<SelectionState>(initialSelectionState)
  const current = useRef(state)
  const setState = useCallback((next: SelectionState | ((previous: SelectionState) => SelectionState)) => {
    const value = typeof next === "function" ? next(current.current) : next
    current.current = value
    updateState(value)
  }, [])
  const file = useRef<File | null>(null)
  const bytes = useRef<Uint8Array | null>(null)
  const ticket = useRef<Uint8Array | null>(null)
  const active = useRef<{ controller: AbortController; write: boolean } | null>(null)
  const bindingRequest = useRef<AbortController | null>(null)
  const generation = useRef(0)
  const operation = useRef<{ id: Uint8Array; plan: SelectionPlan } | null>(null)
  const expiry = useRef<ReturnType<typeof setTimeout> | null>(null)
  const discardAction = useRef<(() => void) | null>(null)
  const [hasFile, setHasFile] = useState(false)
  const [statusPending, setStatusPending] = useState(false)
  const [bindingPending, setBindingPending] = useState(false)
  const [bindingChoices, setBindingChoices] = useState<Record<string, BindingChoice[]>>({})
  const [bindingRepositories, setBindingRepositories] = useState<Record<string, BindingRepositoryChoice[]>>({})
  const [bindingRepositoryIds, setBindingRepositoryIds] = useState<Record<string, string>>({})
  const [discardOpen, setDiscardOpen] = useState(false)
  const origin = window.location.origin
  const owner = session?.userId
  const identity = `${owner ?? ""}\u0000${session?.token ?? ""}\u0000${origin}`
  const latestIdentity = useRef(identity)
  const previousIdentity = useRef(identity)
  latestIdentity.current = identity
  const dirty = hasFile || !!state.preview || state.phase === "reading" || state.phase === "previewing" || state.phase === "committing" || state.phase === "unknown"
  const blocker = useBlocker({ shouldBlockFn: () => dirty, withResolver: true, enableBeforeUnload: dirty })

  const invalidate = useCallback(() => {
    if (!owner) return
    for (const key of ["configuration-assets", "library-repositories", "repository-skills", "collected-repositories"]) void queryClient.invalidateQueries({ queryKey: [key, owner] })
  }, [owner, queryClient])

  function releaseSource() {
    file.current = null
    bytes.current?.fill(0)
    bytes.current = null
    ticket.current?.fill(0)
    ticket.current = null
    if (expiry.current !== null) clearTimeout(expiry.current)
    expiry.current = null
  }

  function interrupt() {
    generation.current++
    const uncertain = !!operation.current && (active.current?.write || current.current.phase === "committing" || current.current.phase === "unknown")
    active.current?.controller.abort()
    active.current = null
    bindingRequest.current?.abort()
    bindingRequest.current = null
    releaseSource()
    return uncertain
  }

  function reset() {
    const uncertain = interrupt()
    setHasFile(false)
    setStatusPending(false)
    setBindingPending(false)
    setBindingChoices({})
    setBindingRepositories({})
    setBindingRepositoryIds({})
    setDiscardOpen(false)
    discardAction.current = null
    if (uncertain) { setState((previous) => ({ ...previous, phase: "unknown", stale: true })); invalidate() }
    else { operation.current = null; setState(initialSelectionState()) }
  }

  useLayoutEffect(() => {
    if (previousIdentity.current !== identity) {
      interrupt()
      operation.current = null
      setState(initialSelectionState())
      setHasFile(false)
      setStatusPending(false)
      setBindingPending(false)
      setBindingChoices({})
      setBindingRepositories({})
      setBindingRepositoryIds({})
      setDiscardOpen(false)
      previousIdentity.current = identity
    } else reset()
    return () => { interrupt(); discardAction.current = null }
  }, [identity, i18n.resolvedLanguage])

  useLayoutEffect(() => {
    onCollectingChange(state.phase === "committing")
    return () => onCollectingChange(false)
  }, [state.phase, onCollectingChange])

  function isCurrent(attempt: number, captured: string, controller: AbortController) {
    return attempt === generation.current && latestIdentity.current === captured && !controller.signal.aborted
  }

  function requestReset(action: () => void) {
    if (current.current.phase === "unknown" || current.current.phase === "committing") return
    if (dirty) { discardAction.current = action; setDiscardOpen(true) }
    else action()
  }

  function chooseNewFile(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.target.files?.[0]
    event.target.value = ""
    if (!selected || current.current.phase === "unknown" || current.current.phase === "committing") return
    requestReset(() => {
      reset()
      if (selected.size === 0 || selected.size > sourceOptions[source].maximumBytes) { setState((previous) => ({ ...previous, error: Code.ResourceExhausted })); return }
      file.current = selected
      setHasFile(true)
    })
  }

  async function previewSelection() {
    if (active.current || !session || current.current.phase === "unknown" || current.current.phase === "committing" || !file.current && !bytes.current) return
    const controller = new AbortController()
    active.current = { controller, write: false }
    const attempt = ++generation.current
    const captured = identity
    const previous = current.current
    bindingRequest.current?.abort()
    bindingRequest.current = null
    setBindingPending(false)
    ticket.current?.fill(0)
    ticket.current = null
    if (expiry.current !== null) clearTimeout(expiry.current)
    expiry.current = null
    setState({ ...previous, phase: bytes.current ? "previewing" : "reading", stale: true, error: undefined, acknowledged: [] })
    try {
      if (!bytes.current) {
        const selected = file.current!
        file.current = null
        const read = await readExport(selected, controller.signal)
        if (!isCurrent(attempt, captured, controller)) { read.fill(0); return }
        bytes.current = read
      }
      const raw = bytes.current
      if (!raw.length || raw.length > sourceOptions[source].maximumBytes) throw new ConnectError("", Code.ResourceExhausted)
      setState((value) => ({ ...value, phase: "previewing" }))
      const response = await imports.previewImportSelection({ source: source === "magpie" ? ImportSelectionSource.MAGPIE_LIBRARY_JSON : ImportSelectionSource.CCSWITCH_SCHEMA24, sourceData: raw, options: previous.options }, { signal: controller.signal, timeoutMs: source === "magpie" ? 5000 : 15000 })
      if (!isCurrent(attempt, captured, controller)) { response.ticket.fill(0); return }
      let next: SelectionState
      try { next = withSelectionPreview(previous, response) } catch (failure) { response.ticket.fill(0); throw failure }
      ticket.current = response.ticket
      setState(next)
      setHasFile(true)
      setBindingChoices({})
      setBindingRepositories({})
      setBindingRepositoryIds({})
      expiry.current = setTimeout(() => {
        if (!isCurrent(attempt, captured, controller)) return
        if (active.current?.write) return
        reset()
        setState((value) => ({ ...value, error: Code.FailedPrecondition }))
      }, Math.max(0, Number(response.expiresMs - BigInt(Date.now()))))
    } catch (failure) {
      if (isCurrent(attempt, captured, controller)) { releaseSource(); setHasFile(false); setState({ ...initialSelectionState(), options: previous.options, error: ConnectError.from(failure).code }) }
    } finally {
      if (isCurrent(attempt, captured, controller)) active.current = null
    }
  }

  function updateOption(option: ImportCandidateOptions) {
    if (active.current || current.current.phase !== "ready") return
    try {
      const next = changeSelectionOption(current.current, option)
      ticket.current?.fill(0)
      ticket.current = null
      setState(next)
    } catch { setState((value) => ({ ...value, error: Code.InvalidArgument })) }
  }

  async function loadBindings(candidateId: string, repositoryId?: string) {
    if (active.current || current.current.phase !== "ready" || !session) return
    const skill = current.current.preview?.skills.find((candidate) => candidate.id === candidateId)
    const hint = current.current.preview?.repositories.find((candidate) => candidate.id === skill?.repositoryCandidateId)?.hint
    if (!skill || ![ImportCandidateStatus.READY, ImportCandidateStatus.UNRESOLVED].includes(skill.status) || skill.repositoryCandidateId && !hint) return
    const selectedRepository = repositoryId === undefined ? undefined : bindingRepositories[candidateId]?.find((entry) => entry.id === repositoryId)
    if (repositoryId !== undefined && (hint || !selectedRepository)) return
    bindingRequest.current?.abort()
    const controller = new AbortController()
    bindingRequest.current = controller
    setBindingChoices((value) => { const next = { ...value }; delete next[candidateId]; return next })
    setBindingRepositoryIds((value) => ({ ...value, [candidateId]: repositoryId ?? "" }))
    updateOption(create(ImportCandidateOptionsSchema, { candidateId }))
    setBindingPending(true)
    const attempt = generation.current
    const captured = identity
    try {
      let matches: BindingRepositoryChoice[]
      if (selectedRepository) matches = [selectedRepository]
      else {
        const result = await library.listRepositories({}, { signal: controller.signal, timeoutMs: 15000 })
        if (!isCurrent(attempt, captured, controller)) return
        if (!hint) {
          setBindingRepositories((value) => ({ ...value, [candidateId]: result.repositories.map((entry) => ({ id: entry.id, repository: entry.nameWithOwner, branch: entry.branch, archiveHash: entry.archiveHash })) }))
          return
        }
        matches = result.repositories.filter((entry) => entry.nameWithOwner === hint.repository && entry.branch === hint.branch).map((entry) => ({ id: entry.id, repository: entry.nameWithOwner, branch: entry.branch, archiveHash: entry.archiveHash }))
      }
      const choices: BindingChoice[] = []
      for (const repository of matches) {
        const detail = await library.getRepository({ repositoryId: repository.id }, { signal: controller.signal, timeoutMs: 15000 })
        if (!isCurrent(attempt, captured, controller)) return
        if (!detail.repository || detail.repository.id !== repository.id || detail.repository.nameWithOwner !== repository.repository || detail.repository.branch !== repository.branch || detail.repository.archiveHash !== repository.archiveHash) throw new ConnectError("", Code.Aborted)
        for (const skill of detail.skills) choices.push({ label: `${repository.repository} · ${repository.branch || t("默认分支")} · ${skill.name} · ${skill.path}`, binding: create(SkillBindingSchema, { repository: repository.repository, branch: repository.branch, repositoryId: repository.id, archiveHash: detail.repository.archiveHash, skillId: skill.id, logicalPath: skill.path, contentHash: skill.contentHash }) })
      }
      setBindingChoices((value) => ({ ...value, [candidateId]: choices }))
    } catch (failure) {
      if (isCurrent(attempt, captured, controller)) setState((value) => ({ ...value, error: ConnectError.from(failure).code }))
    } finally {
      if (isCurrent(attempt, captured, controller)) { bindingRequest.current = null; setBindingPending(false) }
    }
  }

  async function commitSelection() {
    if (active.current || operation.current || collectionBusy || !session || !bytes.current || !ticket.current || current.current.phase !== "confirming" || !canConfirmSelection(current.current)) return
    const plan = selectionPlan(current.current)
    const id = crypto.getRandomValues(new Uint8Array(16))
    operation.current = { id, plan }
    const controller = new AbortController()
    active.current = { controller, write: true }
    const attempt = ++generation.current
    const captured = identity
    if (expiry.current !== null) clearTimeout(expiry.current)
    expiry.current = null
    setState((value) => ({ ...value, phase: "committing", error: undefined }))
    try {
      const response = await imports.commitImportSelection({ ticket: ticket.current, source: source === "magpie" ? ImportSelectionSource.MAGPIE_LIBRARY_JSON : ImportSelectionSource.CCSWITCH_SCHEMA24, sourceData: bytes.current, options: plan.options, operationId: id, repositoryIds: plan.repositoryIds, skillIds: plan.skillIds, configurationIds: plan.configurationIds, ackNoticeIds: plan.ackNoticeIds }, { signal: controller.signal, timeoutMs: 120000 })
      if (!isCurrent(attempt, captured, controller)) return
      const receipt = acceptSelectionReceipt(response.receipt, id, plan)
      const terminal = [ImportReceiptPhase.COMPLETED, ImportReceiptPhase.PARTIAL, ImportReceiptPhase.FAILED].includes(receipt.phase)
      setState((value) => ({ ...value, receipt, phase: terminal ? "terminal" : "unknown", stale: true }))
    } catch (failure) {
      if (isCurrent(attempt, captured, controller)) setState((value) => ({ ...value, phase: "unknown", stale: true, error: ConnectError.from(failure).code }))
    } finally {
      if (isCurrent(attempt, captured, controller)) { active.current = null; releaseSource(); setHasFile(false); invalidate() }
    }
  }

  async function queryStatus() {
    if (active.current || !operation.current || !session || current.current.phase !== "unknown") return
    const controller = new AbortController()
    active.current = { controller, write: false }
    const attempt = ++generation.current
    const captured = identity
    const expected = operation.current
    setStatusPending(true)
    setState((value) => ({ ...value, error: undefined }))
    try {
      const response = await imports.getImportCommitStatus({ operationId: expected.id }, { signal: controller.signal, timeoutMs: 5000 })
      if (!isCurrent(attempt, captured, controller)) return
      const receipt = acceptSelectionStatus(response, expected.id, expected.plan, current.current.receipt)
      setState((value) => ({ ...value, receipt, observation: response.observation, phase: receipt && [ImportReceiptPhase.COMPLETED, ImportReceiptPhase.PARTIAL, ImportReceiptPhase.FAILED].includes(receipt.phase) ? "terminal" : "unknown" }))
      invalidate()
    } catch (failure) {
      if (isCurrent(attempt, captured, controller)) setState((value) => ({ ...value, phase: "unknown", error: ConnectError.from(failure).code }))
    } finally {
      if (isCurrent(attempt, captured, controller)) { active.current = null; setStatusPending(false) }
    }
  }

  return <>
    <ImportSelectionForm source={source} state={state} hasFile={hasFile} collectionBusy={collectionBusy} statusPending={statusPending} bindingChoices={bindingChoices} bindingRepositories={bindingRepositories} bindingRepositoryIds={bindingRepositoryIds} bindingPending={bindingPending}
      onSource={(value) => requestReset(() => { reset(); setSource(value) })} onFile={chooseNewFile} onPreview={() => void previewSelection()}
      onChoose={(group, id, checked) => setState((value) => chooseSelection(value, group, id, checked))} onAcknowledge={(id, checked) => setState((value) => acknowledgeSelection(value, id, checked))} onOption={updateOption} onBindings={(id) => void loadBindings(id)} onBindingRepository={(id, repositoryId) => void loadBindings(id, repositoryId)}
      onConfirm={() => { if (canConfirmSelection(current.current) && !collectionBusy) setState((value) => ({ ...value, phase: "confirming" })) }} onConfirmClose={() => { if (current.current.phase === "confirming") setState((value) => ({ ...value, phase: "ready" })) }} onCommit={() => void commitSelection()}
      onCancel={reset} onClear={() => requestReset(reset)} onStatus={() => void queryStatus()} />
    <Button variant="ghost" className="mt-3" disabled={state.phase === "unknown" || state.phase === "committing"} onClick={() => requestReset(() => { reset(); onLegacy() })}>{t("使用旧版仅仓库收录流程")}</Button>
    <ImportSelectionDiscard open={discardOpen || blocker.status === "blocked"} onCancel={() => { setDiscardOpen(false); discardAction.current = null; if (blocker.status === "blocked") blocker.reset() }} onDiscard={() => { const action = discardAction.current; reset(); if (blocker.status === "blocked") blocker.proceed(); else action?.() }} />
  </>
}

export function ImportRepositories(props: { collectionBusy: boolean; onCollectingChange: (collecting: boolean) => void }) {
  const [legacy, setLegacy] = useState(false)
  const [legacyBusy, setLegacyBusy] = useState(false)
  const notify = useCallback((collecting: boolean) => { setLegacyBusy(collecting); props.onCollectingChange(collecting) }, [props.onCollectingChange])
  return legacy ? <><LegacyImportRepositories {...props} onCollectingChange={notify} /><Button className="mt-3" variant="ghost" disabled={legacyBusy} onClick={() => setLegacy(false)}>{t("切换到技能、仓库与配置三类导入")}</Button></> : <SelectionImportRepositories {...props} onLegacy={() => setLegacy(true)} />
}
