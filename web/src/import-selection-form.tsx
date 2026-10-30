import { useId, useRef, useState, type ChangeEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { clone } from "@bufbuild/protobuf"
import { t, formatNumber, savePreference } from "./i18n"
import { ClaudeAuthMode, CodexAuthMode, GeminiAuthMode, GeminiKeyTransport, CodexModality, CodexReasoningEffort, type Draft } from "./gen/skillhub/configuration/v1/configuration_pb"
import { ImportCandidateOptionsSchema, ImportCandidateStatus, ImportCommitObservation, ImportReceiptPhase, type ImportCandidateOptions, type ImportConfigurationCandidate, type SkillBinding } from "./gen/skillhub/import/v1/import_pb"
import { canConfirmSelection, selectionNotices, selectionNoticeLabels, selectionOption, selectionStatusLabels, type SelectionGroup, type SelectionState } from "./import-selection-state"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Input } from "./components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./components/ui/dialog"

export type ImportSource = "magpie" | "cc-switch"
export type BindingChoice = { label: string; binding: SkillBinding }
export type ImportSelectionFormProps = {
  source: ImportSource
  state: SelectionState
  hasFile: boolean
  collectionBusy: boolean
  statusPending: boolean
  bindingChoices: Record<string, BindingChoice[]>
  bindingPending: boolean
  onSource: (source: ImportSource) => void
  onFile: (event: ChangeEvent<HTMLInputElement>) => void
  onPreview: () => void
  onChoose: (group: SelectionGroup, id: string, checked: boolean) => void
  onAcknowledge: (id: string, checked: boolean) => void
  onOption: (option: ImportCandidateOptions) => void
  onBindings: (candidateId: string) => void
  onConfirm: () => void
  onConfirmClose: () => void
  onCommit: () => void
  onCancel: () => void
  onClear: () => void
  onStatus: () => void
}

function ImportLanguage() {
  const { i18n } = useTranslation()
  const id = useId()
  return <div className="mt-4 space-y-2"><label htmlFor={id}>{t("界面语言")}</label><Select value={i18n.resolvedLanguage ?? "zh-CN"} onValueChange={(value) => { if (value === "zh-CN" || value === "en") { savePreference("locale", value); void i18n.changeLanguage(value) } }}><SelectTrigger id={id}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="zh-CN"><span lang="zh-CN">简体中文</span></SelectItem><SelectItem value="en"><span lang="en">English</span></SelectItem></SelectContent></Select></div>
}

function ReadField({ label, value }: { label: string; value: string | boolean | bigint | undefined }) {
  return <div className="grid gap-1 border-b border-border py-2 last:border-0"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="whitespace-pre-wrap break-all text-sm">{value === undefined ? t("未设置") : typeof value === "boolean" ? String(value) : String(value)}</dd></div>
}

function ProviderPreview({ draft }: { draft: Draft }) {
  const payload = draft.payload
  return <dl className="mt-3 text-sm">
    <ReadField label={t("名称")} value={draft.name} />
    {payload.case === "claudeProvider" && <>
      <ReadField label="endpoint" value={payload.value.endpoint} /><ReadField label="authMode" value={ClaudeAuthMode[payload.value.authMode]} />
      <ReadField label="defaultModel" value={payload.value.models?.defaultModel} /><ReadField label="haiku" value={payload.value.models?.haiku} /><ReadField label="sonnet" value={payload.value.models?.sonnet} /><ReadField label="opus" value={payload.value.models?.opus} />
    </>}
    {payload.case === "geminiProvider" && <>
      <ReadField label="authMode" value={GeminiAuthMode[payload.value.authMode]} /><ReadField label="endpoint" value={payload.value.endpoint} /><ReadField label="keyTransport" value={payload.value.keyTransport === undefined ? undefined : GeminiKeyTransport[payload.value.keyTransport]} /><ReadField label="env.model" value={payload.value.envModel} /><ReadField label="settings.model.name" value={payload.value.settingsModel} />
    </>}
    {payload.case === "codexProvider" && <>
      <ReadField label="authMode" value={CodexAuthMode[payload.value.authMode]} /><ReadField label="endpoint" value={payload.value.endpoint} /><ReadField label="model" value={payload.value.model} /><ReadField label="reasoningEffort" value={payload.value.reasoningEffort === undefined ? undefined : CodexReasoningEffort[payload.value.reasoningEffort]} />
      <div className="mt-3"><dt className="font-medium">catalog</dt><dd>{payload.value.catalog === undefined ? t("未设置") : <ol className="space-y-3">{payload.value.catalog.models.map((model, index) => <li key={index} className="border-t border-border pt-2"><dl>
        <ReadField label="model" value={model.model} /><ReadField label="displayName" value={model.displayName} /><ReadField label="contextWindow" value={model.contextWindow} /><ReadField label="supportsParallelToolCalls" value={model.supportsParallelToolCalls} /><ReadField label="inputModalities" value={model.inputModalities === undefined ? undefined : JSON.stringify(model.inputModalities.values.map((value) => CodexModality[value]))} /><ReadField label="baseInstructions" value={model.baseInstructions} /><ReadField label="reasoningLevels" value={model.reasoningLevels === undefined ? undefined : JSON.stringify(model.reasoningLevels.values.map((value) => CodexReasoningEffort[value]))} /><ReadField label="defaultReasoningLevel" value={model.defaultReasoningLevel === undefined ? undefined : CodexReasoningEffort[model.defaultReasoningLevel]} />
      </dl></li>)}</ol>}</dd></div>
    </>}
  </dl>
}

function ConfigurationChoices({ candidate, option, disabled, onOption }: { candidate: ImportConfigurationCandidate; option: ImportCandidateOptions; disabled: boolean; onOption: (option: ImportCandidateOptions) => void }) {
  const id = useId()
  const [name, setName] = useState(option.name ?? candidate.safeName)
  const [auth, setAuth] = useState(option.auth.case ? String(option.auth.value) : candidate.draft?.payload.case === "claudeProvider" || candidate.draft?.payload.case === "geminiProvider" ? String(candidate.draft.payload.value.authMode) : "unset")
  const [transport, setTransport] = useState(option.geminiKeyTransport === undefined ? candidate.draft?.payload.case === "geminiProvider" && candidate.draft.payload.value.keyTransport !== undefined ? String(candidate.draft.payload.value.keyTransport) : "unset" : String(option.geminiKeyTransport))
  const editable = [ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_CHOICE, ImportCandidateStatus.NAME_CONFLICT].includes(candidate.status)
  if (!editable) return null
  const apply = () => {
    const next = clone(ImportCandidateOptionsSchema, option)
    next.name = name
    if (candidate.knownAgentId === "claude") next.auth = auth === "unset" ? { case: undefined } : { case: "claudeAuthMode", value: Number(auth) }
    if (candidate.knownAgentId === "gemini") { next.auth = auth === "unset" ? { case: undefined } : { case: "geminiAuthMode", value: Number(auth) }; next.geminiKeyTransport = auth === "2" && transport !== "unset" ? Number(transport) : undefined }
    onOption(next)
  }
  return <div className="mt-3 space-y-3">
    <div className="space-y-1"><label htmlFor={`${id}-name`} className="text-sm">{t("导入名称（不自动修正）")}</label><Input id={`${id}-name`} disabled={disabled} value={name} maxLength={80} onChange={(event) => setName(event.target.value)} /></div>
    {(candidate.knownAgentId === "claude" || candidate.knownAgentId === "gemini") && <div className="space-y-1"><label htmlFor={`${id}-auth`}>{t("认证模式")}</label><Select value={auth} disabled={disabled} onValueChange={setAuth}><SelectTrigger id={`${id}-auth`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unset">{t("请选择，不推断认证")}</SelectItem><SelectItem value="1">{t("原生登录（不证明设备已登录）")}</SelectItem><SelectItem value="2">API key</SelectItem>{candidate.knownAgentId === "claude" && <SelectItem value="3">Auth token</SelectItem>}</SelectContent></Select></div>}
    {candidate.knownAgentId === "gemini" && auth === "2" && <div className="space-y-1"><label htmlFor={`${id}-transport`}>{t("Gemini key 传输方式")}</label><Select value={transport} disabled={disabled} onValueChange={setTransport}><SelectTrigger id={`${id}-transport`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unset">{t("请明确选择传输方式")}</SelectItem><SelectItem value="1">native-default</SelectItem><SelectItem value="2">bearer</SelectItem></SelectContent></Select></div>}
    <Button type="button" variant="outline" disabled={disabled} onClick={apply}>{t("更新选项，需重新预览")}</Button>
  </div>
}

function Column({ title, children }: { title: string; children: ReactNode }) {
  return <fieldset className="min-w-0 space-y-3 rounded-lg border border-border p-4"><legend className="px-1 font-semibold">{title}</legend>{children}</fieldset>
}

function ConfirmSelectionDetails({ state }: { state: SelectionState }) {
  const preview = state.preview
  if (!preview) return null
  return <div className="mt-4 max-h-80 space-y-4 overflow-y-auto rounded-lg border border-border p-3 text-sm" aria-label={t("本次确认的完整安全字段")}>
    {preview.skills.filter((entry) => state.chosen.skills.includes(entry.id)).map((entry) => <div key={entry.id}><h3 className="font-medium">{t("技能引用")} · {entry.safeName}</h3><dl>{entry.binding && <><ReadField label="repository" value={entry.binding.repository} /><ReadField label="ref" value={entry.binding.branch} /><ReadField label="repositoryId" value={entry.binding.repositoryId} /><ReadField label="archiveHash" value={entry.binding.archiveHash} /><ReadField label="skillId" value={entry.binding.skillId} /><ReadField label="logicalPath" value={entry.binding.logicalPath} /><ReadField label="contentHash" value={entry.binding.contentHash} /></>}</dl></div>)}
    {preview.repositories.filter((entry) => state.chosen.repositories.includes(entry.id)).map((entry) => <div key={entry.id}><h3 className="font-medium">{t("仓库")}</h3><dl><ReadField label="repository" value={entry.hint?.repository} /><ReadField label="ref" value={entry.hint?.branch} /><ReadField label="sourceEnabled" value={entry.hint?.sourceEnabled} /></dl></div>)}
    {preview.configurations.filter((entry) => state.chosen.configurations.includes(entry.id)).map((entry) => <div key={entry.id}><h3 className="font-medium">{entry.knownAgentId} · {entry.safeName}</h3>{entry.draft && <ProviderPreview draft={entry.draft} />}{entry.credentialSlots.map((slot) => <p key={slot.slotId} className="mt-2 break-all">{slot.slotId}: {t(slot.present ? "来源存在，本次不迁移" : "来源未设置")}</p>)}</div>)}
    {selectionNotices(preview).map((notice) => <p key={notice.id}>{t(selectionNoticeLabels[notice.code])} · {formatNumber(notice.count)}</p>)}
  </div>
}

export function ImportSelectionForm(props: ImportSelectionFormProps) {
  useTranslation()
  const { state, source } = props
  const id = useId()
  const confirmTrigger = useRef<HTMLButtonElement>(null)
  const confirmCancel = useRef<HTMLButtonElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const busy = ["reading", "previewing", "committing"].includes(state.phase)
  const locked = busy || state.phase === "unknown" || state.phase === "terminal" || props.statusPending
  const preview = state.preview
  const notices = preview ? selectionNotices(preview) : []
  const chooseBox = (group: SelectionGroup, candidateId: string, status: ImportCandidateStatus, label: ReactNode) => <div className="flex items-start gap-3"><Checkbox id={`${id}-${candidateId}`} disabled={locked || state.stale || status !== ImportCandidateStatus.READY} checked={state.chosen[group].includes(candidateId)} onCheckedChange={(value) => props.onChoose(group, candidateId, value === true)} className="mt-3" /><label htmlFor={`${id}-${candidateId}`} className="min-h-11 min-w-0 flex-1 py-2 break-words">{label}</label></div>
  return <section className="source-card" aria-labelledby={`${id}-heading`}>
    <div className="source-card-heading"><div><h2 id={`${id}-heading`} data-import-selection-heading ref={heading} tabIndex={-1} className="font-semibold">{t("选择导入技能、仓库与配置")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("三类独立选择，默认不选；保存资产不等于设备应用。")}</p></div></div>
    <div className="source-card-body space-y-4">
      <p className="text-sm leading-6">{t("整个导出可能含密钥。仅点击上传后发送到当前受信任服务端；原文、文件名和密钥不会显示或持久保存在浏览器。")}</p>
      <div className="space-y-2"><label htmlFor={`${id}-source`}>{t("导入来源")}</label><Select value={source} disabled={state.phase === "unknown" || state.phase === "committing"} onValueChange={(value) => { if (value === "magpie" || value === "cc-switch") props.onSource(value) }}><SelectTrigger id={`${id}-source`} className="sm:max-w-md"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="magpie">Magpie JSON · 1 MiB</SelectItem><SelectItem value="cc-switch">cc-switch schema24 SQLite · 16 MiB</SelectItem></SelectContent></Select></div>
      {source === "cc-switch" && <p className="text-sm text-muted-foreground">{t("仅接受自包含 rollback-mode SQLite 一致性快照，不接受 WAL 主数据库副本。")}</p>}
      <div className="space-y-2"><label htmlFor={`${id}-file`}>{t("授权导出文件")}</label><Input id={`${id}-file`} type="file" disabled={locked} accept={source === "magpie" ? ".json,application/json" : ".db,.sqlite,.sqlite3"} onChange={props.onFile} className="h-auto min-h-11 py-2" /><p className="text-sm text-muted-foreground">{props.hasFile ? t("已选择导出文件。点击上传并预览后才会发送。") : t("预览不收录、不创建资产、不下载技能。")}</p></div>
      <div className="flex flex-wrap gap-3"><Button disabled={locked || !props.hasFile} onClick={props.onPreview}>{state.stale ? t("重新预览同一快照") : t("上传并预览")}</Button>{busy && <Button variant="outline" onClick={props.onCancel}>{t("停止等待及后续操作")}</Button>}{!busy && state.phase !== "unknown" && (preview || props.hasFile || state.error !== undefined) && <Button variant="ghost" onClick={props.onClear}>{t("清除导入草稿")}</Button>}</div>
      {state.error !== undefined && <p role="alert" className="text-sm text-destructive">{t("导入请求未完成或未通过校验；不会回显输入或自动重试。")}</p>}
      {busy && <p role="status" aria-live="polite" className="text-sm">{t(state.phase === "committing" ? "正在提交选择；停止等待不代表撤销写入。" : "正在读取并预览；尚未确认导入。")}</p>}
      {state.stale && <p role="status" className="text-sm">{t("选项已变更，旧票据不可提交。请重新预览并再次选择、确认提示。")}</p>}
      {preview && <>
        <p className="text-sm text-muted-foreground">{t("仅在票据有效期内确认同一快照；过期或目标版本变化需要重新预览。")}</p>
        <div className="grid gap-4 xl:grid-cols-3">
          <Column title={t("技能引用")}>
            {!preview.skills.length && <p className="text-sm text-muted-foreground">{t("此列没有候选项。")}</p>}
            {preview.skills.map((candidate) => <div key={candidate.id} className="border-b border-border pb-3 last:border-0">
              {chooseBox("skills", candidate.id, candidate.status, candidate.safeName)}<p className="text-xs text-muted-foreground">{t(selectionStatusLabels[candidate.status])}</p>
              {candidate.binding && <dl><ReadField label="repository / ref" value={`${candidate.binding.repository} / ${candidate.binding.branch}`} /><ReadField label="path" value={candidate.binding.logicalPath} /><ReadField label="contentHash" value={candidate.binding.contentHash} /></dl>}
              {[ImportCandidateStatus.UNRESOLVED, ImportCandidateStatus.READY].includes(candidate.status) && <div className="mt-3 space-y-2"><Button variant="outline" disabled={locked || props.bindingPending} onClick={() => props.onBindings(candidate.id)}>{t("读取同仓已收录技能供绑定")}</Button>{props.bindingChoices[candidate.id] && <><label htmlFor={`${id}-binding-${candidate.id}`} className="block text-sm">{t("显式选择现有技能绑定")}</label><Select value={selectionOption(state, candidate.id).skillBinding?.skillId ?? "unset"} disabled={locked} onValueChange={(value) => { const binding = props.bindingChoices[candidate.id]?.find((entry) => entry.binding.skillId === value)?.binding; if (binding) { const option = clone(ImportCandidateOptionsSchema, selectionOption(state, candidate.id)); option.skillBinding = binding; props.onOption(option) } }}><SelectTrigger id={`${id}-binding-${candidate.id}`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unset">{t("请选择绑定，不按名称猜测")}</SelectItem>{props.bindingChoices[candidate.id].map((choice) => <SelectItem key={choice.binding.skillId} value={choice.binding.skillId}>{choice.label}</SelectItem>)}</SelectContent></Select>{props.bindingChoices[candidate.id].length === 0 && <p className="text-xs">{t("没有同仓可绑定技能；请先收录仓库，再重新预览。")}</p>}</>}</div>}
            </div>)}
          </Column>
          <Column title={t("仓库")}>
            {!preview.repositories.length && <p className="text-sm text-muted-foreground">{t("此列没有候选项。")}</p>}
            {preview.repositories.map((candidate) => <div key={candidate.id} className="border-b border-border pb-3 last:border-0">{chooseBox("repositories", candidate.id, candidate.status, <><span className="block font-mono text-sm">{candidate.hint?.repository}</span><span className="block text-xs text-muted-foreground">{candidate.hint?.branch || t("默认分支")}</span></>)}<p className="text-xs text-muted-foreground">{t(selectionStatusLabels[candidate.status])}</p>{candidate.hint?.sourceEnabled !== undefined && <ReadField label="sourceEnabled" value={candidate.hint.sourceEnabled} />}</div>)}
          </Column>
          <Column title={t("客户端配置资产")}>
            {!preview.configurations.length && <p className="text-sm text-muted-foreground">{t("此列没有候选项。")}</p>}
            {preview.configurations.map((candidate) => <div key={candidate.id} className="border-b border-border pb-3 last:border-0">{chooseBox("configurations", candidate.id, candidate.status, <><span className="block">{candidate.safeName || t("不支持的来源配置")}</span><span className="block text-xs">{candidate.knownAgentId}</span></>)}<p className="text-xs text-muted-foreground">{t(selectionStatusLabels[candidate.status])}</p>{candidate.draft && <details className="mt-2"><summary className="cursor-pointer text-sm">{t("查看完整安全字段与 presence")}</summary><ProviderPreview draft={candidate.draft} /></details>}{candidate.credentialSlots.map((slot) => <p key={slot.slotId} className="mt-2 break-all text-xs">{slot.slotId}: {t(slot.present ? "来源存在，本次不迁移" : "来源未设置")}</p>)}<ConfigurationChoices candidate={candidate} option={selectionOption(state, candidate.id)} disabled={locked} onOption={props.onOption} /></div>)}
          </Column>
        </div>
        <fieldset className="space-y-3"><legend className="mb-2 font-medium">{t("明确确认排除项")}</legend>{notices.length === 0 && <p className="text-sm text-muted-foreground">{t("没有需要确认的排除提示。")}</p>}{notices.map((notice) => <div key={notice.id} className="flex items-start gap-3"><Checkbox id={`${id}-notice-${notice.id}`} disabled={locked || state.stale} checked={state.acknowledged.includes(notice.id)} onCheckedChange={(value) => props.onAcknowledge(notice.id, value === true)} className="mt-3" /><label htmlFor={`${id}-notice-${notice.id}`} className="min-h-11 py-2 text-sm">{t(selectionNoticeLabels[notice.code])} · {formatNumber(notice.count)}</label></div>)}</fieldset>
        <p className="text-sm">{t("已选：技能 {{skills}} · 仓库 {{repositories}} · 配置 {{configurations}}", { skills: formatNumber(state.chosen.skills.length), repositories: formatNumber(state.chosen.repositories.length), configurations: formatNumber(state.chosen.configurations.length) })}</p>
        <Button ref={confirmTrigger} disabled={props.collectionBusy || !canConfirmSelection(state)} onClick={props.onConfirm}>{t("检查并确认本次选择")}</Button>
      </>}
      {state.phase === "unknown" && <div className="space-y-3" role="status"><p>{t("提交结果待确认。不会重放提交；请显式查询同一 operation。")}</p>{state.observation === ImportCommitObservation.NOT_OBSERVED && <p>{t("当前未观察到 receipt，不代表未提交；原请求可能仍在执行。")}</p>}<Button variant="outline" disabled={props.statusPending} onClick={props.onStatus}>{props.statusPending ? t("正在查询状态…") : t("查询导入状态")}</Button></div>}
      {state.receipt && <div className="space-y-2 border-t border-border pt-4" role="status"><h3 className="font-medium">{t(state.receipt.phase === ImportReceiptPhase.COMPLETED ? "导入已完成（未安装或应用到设备）" : state.receipt.phase === ImportReceiptPhase.PARTIAL ? "导入部分完成，请检查逐项结果" : state.receipt.phase === ImportReceiptPhase.FAILED ? "导入未完成，先前成功仓库可能保留" : "已有 receipt，尚未终态")}</h3><p>revision {String(state.receipt.revision)}</p>{state.receipt.repositoryOutcomes.map((entry) => <p key={entry.candidateId} className="break-all text-sm">{preview?.repositories.find((candidate) => candidate.id === entry.candidateId)?.hint?.repository} · {String(entry.code)} {entry.repositoryId}</p>)}{state.receipt.createdAssets.map((asset) => <p key={asset.id} className="break-all text-sm">{asset.name} · {asset.id} · revision {String(asset.revision)}</p>)}{state.receipt.selectedSkillBindings.map((binding) => <p key={binding.skillId} className="break-all text-sm">{t("持久技能引用（不是已安装）")} · {binding.repository} / {binding.logicalPath} · {binding.skillId}</p>)}</div>}
    </div>
    <Dialog open={state.phase === "confirming"} onOpenChange={(open) => { if (!open) props.onConfirmClose() }}><DialogContent className="sm:max-w-2xl" onOpenAutoFocus={(event) => { event.preventDefault(); confirmCancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); const target = confirmTrigger.current; if (target && !target.disabled) target.focus(); else heading.current?.focus() }}><DialogTitle>{t("确认同一快照的导入选择")}</DialogTitle><DialogDescription>{t("配置仅创建私有资产；技能仅保存明确引用；仓库逐项收录。凭据不迁移，客户端不会自动应用。")}</DialogDescription><p className="mt-4 text-sm">{t("已选：技能 {{skills}} · 仓库 {{repositories}} · 配置 {{configurations}}", { skills: formatNumber(state.chosen.skills.length), repositories: formatNumber(state.chosen.repositories.length), configurations: formatNumber(state.chosen.configurations.length) })}</p><ConfirmSelectionDetails state={state} /><p className="mt-2 text-sm">{t("确认基于上方完整字段与提示；改选项必须重新预览。")}</p><ImportLanguage /><div className="mt-5 flex flex-wrap gap-3"><Button ref={confirmCancel} variant="outline" onClick={props.onConfirmClose}>{t("取消")}</Button><Button disabled={props.collectionBusy || !canConfirmSelection(state)} onClick={props.onCommit}>{t("确认导入所选三类项目")}</Button></div></DialogContent></Dialog>
  </section>
}

export function ImportSelectionDiscard({ open, onCancel, onDiscard }: { open: boolean; onCancel: () => void; onDiscard: () => void }) {
  const cancel = useRef<HTMLButtonElement>(null)
  const previous = useRef<HTMLElement | null>(null)
  return <Dialog open={open} onOpenChange={(value) => { if (!value) onCancel() }}><DialogContent onOpenAutoFocus={(event) => { event.preventDefault(); previous.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; cancel.current?.focus() }} onCloseAutoFocus={(event) => { event.preventDefault(); if (previous.current?.isConnected) previous.current.focus(); else document.querySelector<HTMLElement>("[data-import-selection-heading]")?.focus(); previous.current = null }}><DialogTitle>{t("放弃当前导入草稿？")}</DialogTitle><DialogDescription>{t("将清除文件、票据与选择。停止等待不能撤销已经提交的操作。")}</DialogDescription><ImportLanguage /><div className="mt-5 flex flex-wrap gap-3"><Button ref={cancel} variant="outline" onClick={onCancel}>{t("取消")}</Button><Button onClick={onDiscard}>{t("放弃导入草稿")}</Button></div></DialogContent></Dialog>
}
