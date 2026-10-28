import { useEffect, useLayoutEffect, useRef, useState, type ChangeEvent } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { Code, ConnectError } from "@connectrpc/connect"
import { FileUp } from "lucide-react"
import { useApi } from "./api"
import { t, formatNumber } from "./i18n"
import { ExportFormat, type RepositoryCandidate } from "./gen/skillhub/import/v1/import_pb"
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

export function ImportRepositories({ collectionBusy, onCollectingChange }: { collectionBusy: boolean; onCollectingChange: (collecting: boolean) => void }) {
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
    <div className="source-card-heading"><span className="service-icon"><FileUp size={22} aria-hidden="true" /></span><div className="min-w-0"><h2 id="magpie-import-title" className="font-semibold">{t("从导出文件收录仓库来源")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("选择来源及导出文件，仅在点击上传并预览后发送到此 SkillHub 服务端。预览不会收录仓库。")}</p></div></div>
    <div className="source-card-body space-y-4">
      <div className="space-y-2"><label htmlFor="import-source" className="block text-sm font-medium">{t("导入来源")}</label><Select value={source} onValueChange={(value) => { if (value !== "magpie" && value !== "cc-switch") return; clear(); setSource(value) }}><SelectTrigger id="import-source" className="w-full sm:w-auto" aria-describedby="import-source-help"><SelectValue /></SelectTrigger><SelectContent>{Object.entries(sourceOptions).map(([value, option]) => <SelectItem key={value} value={value}>{t(option.label)}</SelectItem>)}</SelectContent></Select><p id="import-source-help" className="text-sm text-muted-foreground">{t("切换来源会停止等待及后续操作，并清除文件、候选、选择和进度；已提交的收录不会撤销。")}</p></div>
      <p id="magpie-scope" className="text-sm leading-6 text-muted-foreground">{t("仅支持公开 GitHub 仓库。确认后整仓扫描全部技能，不限于导出中列出的技能；不迁移本地修改、技能内容或客户端配置。新收录默认私有，不自动安装或发布。")}</p>
      {source === "cc-switch" && <div id="snapshot-warning" className="space-y-2 rounded-lg border border-border p-3 text-sm leading-6"><p className="font-medium">{t("整个数据库可能包含 API 密钥、token 等凭据。只有点击「上传并预览」才会发送完整快照，请仅上传到你信任的 SkillHub 服务器。")}</p><p>{t("必须通过 SQLite backup API 或一致性导出生成 rollback-mode、自包含的独立快照。不能直接复制运行中的主数据库并忽略 WAL 文件。")}</p><p className="text-muted-foreground">{t("服务端仅在内存中 Deserialize，拒绝 WAL 模式或无效 schema，不将原始数据库写入磁盘。浏览器不执行 SQL、moejs 或来源适配器。")}</p></div>}
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
