import { clone, create, equals } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, ClaudeAuthMode, CodexAuthMode, CodexModality, CodexReasoningEffort, GeminiAuthMode, GeminiKeyTransport, type Draft } from "./gen/skillhub/configuration/v1/configuration_pb"
import { ImportCandidateOptionsSchema, ImportCandidateStatus, ImportCommitObservation, ImportConfigurationCandidateSchema, ImportNoticeCode, ImportReceiptPhase, ImportReceiptSchema, ImportRepositoryCandidateSchema, ImportRepositoryOutcomeCode, ImportSkillCandidateSchema, SkillBindingSchema, type ImportCandidateOptions, type ImportNotice, type ImportReceipt, type GetImportCommitStatusResponse, type PreviewImportSelectionResponse, type SkillBinding } from "./gen/skillhub/import/v1/import_pb"

export type SelectionPhase = "idle" | "reading" | "previewing" | "ready" | "confirming" | "committing" | "unknown" | "terminal"
export type SelectionGroup = "repositories" | "skills" | "configurations"
export type SelectionPreview = Pick<PreviewImportSelectionResponse, "repositories" | "skills" | "configurations" | "notices" | "expiresMs">
export type SelectionState = {
  phase: SelectionPhase
  preview?: SelectionPreview
  options: ImportCandidateOptions[]
  chosen: Record<SelectionGroup, string[]>
  acknowledged: string[]
  stale: boolean
  receipt?: ImportReceipt
  observation?: ImportCommitObservation
  error?: Code
}
export type SelectionPlan = { repositoryIds: string[]; skillIds: string[]; configurationIds: string[]; ackNoticeIds: string[]; options: ImportCandidateOptions[]; preview: SelectionPreview }

const hash = /^[a-f0-9]{64}$/
const statuses = [1, 2, 3, 4, 5, 6, 7, 8]
const agents = ["claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"]
const slots: Record<string, string[]> = { claude: ["claude.api-key", "claude.auth-token"], codex: ["codex.provider-key"], gemini: ["gemini.api-key", "gemini.legacy-api-key"] }
const maximum = { repositories: 16, skills: 128, configurations: 32 }

function requireSelection(valid: unknown): asserts valid {
  if (!valid) throw new ConnectError("", Code.DataLoss)
}

function closedMessages(value: unknown): boolean {
  if (value === null || typeof value !== "object" || value instanceof Uint8Array) return true
  if ("$unknown" in value && Array.isArray(value.$unknown) && value.$unknown.length) return false
  return Object.values(value).every(closedMessages)
}

function unique(values: string[]) { return new Set(values).size === values.length }
function validName(value: string) { return value.length > 0 && value.length <= 80 && value === value.trim() && !/[\u0000-\u001f\u007f]/u.test(value) }

export function initialSelectionState(): SelectionState {
  return { phase: "idle", options: [], chosen: { repositories: [], skills: [], configurations: [] }, acknowledged: [], stale: false }
}

export function validSkillBinding(binding?: SkillBinding): binding is SkillBinding {
  return !!binding && closedMessages(binding) && binding.repositoryId.length > 0 && hash.test(binding.archiveHash) && binding.skillId.length > 0 && hash.test(binding.contentHash) && /^[^/\s]+\/[^/\s]+$/u.test(binding.repository) && binding.logicalPath.length > 0 && !binding.logicalPath.startsWith("/") && !binding.logicalPath.split("/").some((part) => part === ".." || part === "" || part.includes("\\"))
}

function validateDraft(draft: Draft, agent: string) {
  requireSelection(closedMessages(draft) && validName(draft.name))
  const payload = draft.payload
  if (agent === "claude" && payload.case === "claudeProvider") {
    requireSelection([ClaudeAuthMode.NATIVE_LOGIN, ClaudeAuthMode.API_KEY, ClaudeAuthMode.AUTH_TOKEN].includes(payload.value.authMode))
  } else if (agent === "codex" && payload.case === "codexProvider") {
    const provider = payload.value
    requireSelection(provider.authMode === CodexAuthMode.PROVIDER_KEY && provider.endpoint && provider.model)
    if (provider.reasoningEffort !== undefined) requireSelection(provider.reasoningEffort >= CodexReasoningEffort.NONE && provider.reasoningEffort <= CodexReasoningEffort.XHIGH)
    for (const model of provider.catalog?.models ?? []) {
      requireSelection(model.model && (model.contextWindow === undefined || model.contextWindow > 0n && model.contextWindow <= 2147483647n))
      if (model.inputModalities) requireSelection(model.inputModalities.values.length && unique(model.inputModalities.values.map(String)) && model.inputModalities.values.every((value) => value === CodexModality.TEXT || value === CodexModality.IMAGE))
      if (model.reasoningLevels) requireSelection(model.reasoningLevels.values.length && unique(model.reasoningLevels.values.map(String)) && model.reasoningLevels.values.every((value) => value >= CodexReasoningEffort.NONE && value <= CodexReasoningEffort.XHIGH))
      if (model.defaultReasoningLevel !== undefined) requireSelection(model.reasoningLevels?.values.includes(model.defaultReasoningLevel))
    }
  } else if (agent === "gemini" && payload.case === "geminiProvider") {
    const provider = payload.value
    requireSelection(provider.authMode === GeminiAuthMode.NATIVE_LOGIN && provider.endpoint === undefined && provider.keyTransport === undefined || provider.authMode === GeminiAuthMode.API_KEY && [GeminiKeyTransport.NATIVE_DEFAULT, GeminiKeyTransport.BEARER].includes(provider.keyTransport!))
  } else requireSelection(false)
}

export function selectionNotices(preview: SelectionPreview): ImportNotice[] {
  return [...preview.notices, ...preview.skills.flatMap((entry) => entry.notices), ...preview.configurations.flatMap((entry) => entry.notices)]
}

export function acceptSelectionPreview(response: PreviewImportSelectionResponse, now = Date.now()): SelectionPreview {
  requireSelection(closedMessages(response) && response.ticket.length === 290 && response.expiresMs > BigInt(now) && response.expiresMs <= BigInt(now + 300000))
  requireSelection(response.repositories.length <= 1000 && response.skills.length <= 1000 && response.configurations.length <= 34)
  const all = [...response.repositories, ...response.skills, ...response.configurations]
  requireSelection(all.every((entry) => hash.test(entry.id) && statuses.includes(entry.status)) && unique(all.map((entry) => entry.id)))
  for (const entry of response.repositories) requireSelection(entry.hint && [ImportCandidateStatus.READY, ImportCandidateStatus.UNSUPPORTED_FIELD].includes(entry.status))
  for (const entry of response.skills) {
    requireSelection([ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_REPOSITORY_IMPORT, ImportCandidateStatus.UNRESOLVED, ImportCandidateStatus.UNSUPPORTED_AGENT, ImportCandidateStatus.UNSUPPORTED_KIND, ImportCandidateStatus.UNSUPPORTED_FIELD].includes(entry.status))
    requireSelection(entry.repositoryCandidateId === "" || response.repositories.some((repository) => repository.id === entry.repositoryCandidateId))
    if (entry.status === ImportCandidateStatus.READY) requireSelection(validSkillBinding(entry.binding))
    else requireSelection(entry.binding === undefined)
  }
  for (const entry of response.configurations) {
    requireSelection([ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_CHOICE, ImportCandidateStatus.UNSUPPORTED_AGENT, ImportCandidateStatus.UNSUPPORTED_KIND, ImportCandidateStatus.UNSUPPORTED_FIELD, ImportCandidateStatus.NAME_CONFLICT].includes(entry.status))
    requireSelection(entry.knownAgentId === "" || agents.includes(entry.knownAgentId))
    requireSelection([AssetKind.PROVIDER, AssetKind.MCP, AssetKind.PROMPT, AssetKind.AGENT_SETTINGS, AssetKind.WORKSPACE].includes(entry.kind))
    requireSelection(unique(entry.credentialSlots.map((slot) => slot.slotId)) && entry.credentialSlots.every((slot) => slots[entry.knownAgentId]?.includes(slot.slotId) && typeof slot.present === "boolean"))
    if (entry.status === ImportCandidateStatus.READY) {
      requireSelection(entry.kind === AssetKind.PROVIDER && entry.draft && entry.safeName === entry.draft.name)
      requireSelection(entry.credentialSlots.length === slots[entry.knownAgentId]?.length)
      validateDraft(entry.draft, entry.knownAgentId)
    } else requireSelection(entry.draft === undefined)
  }
  const notices = selectionNotices(response)
  requireSelection(unique(notices.map((entry) => entry.id)) && notices.every((entry) => hash.test(entry.id) && [1, 2, 3, 4, 5].includes(entry.code) && Number.isInteger(entry.count) && entry.count > 0))
  return { expiresMs: response.expiresMs, repositories: response.repositories.map((entry) => clone(ImportRepositoryCandidateSchema, entry)), skills: response.skills.map((entry) => clone(ImportSkillCandidateSchema, entry)), configurations: response.configurations.map((entry) => clone(ImportConfigurationCandidateSchema, entry)), notices: response.notices.map((entry) => ({ ...entry })) }
}

export function withSelectionPreview(state: SelectionState, response: PreviewImportSelectionResponse, now = Date.now()): SelectionState {
  return { ...initialSelectionState(), phase: "ready", options: state.options.map((entry) => clone(ImportCandidateOptionsSchema, entry)), preview: acceptSelectionPreview(response, now) }
}

export function chooseSelection(state: SelectionState, group: SelectionGroup, id: string, checked: boolean): SelectionState {
  if (state.phase !== "ready" || state.stale || !state.preview?.[group].some((entry) => entry.id === id && entry.status === ImportCandidateStatus.READY)) return state
  const remaining = state.chosen[group].filter((value) => value !== id)
  if (checked && remaining.length >= maximum[group]) return state
  return { ...state, chosen: { ...state.chosen, [group]: checked ? [...remaining, id] : remaining } }
}

export function acknowledgeSelection(state: SelectionState, id: string, checked: boolean): SelectionState {
  if (state.phase !== "ready" || state.stale || !state.preview || !selectionNotices(state.preview).some((entry) => entry.id === id)) return state
  return { ...state, acknowledged: checked ? [...state.acknowledged.filter((entry) => entry !== id), id] : state.acknowledged.filter((entry) => entry !== id) }
}

export function changeSelectionOption(state: SelectionState, option: ImportCandidateOptions): SelectionState {
  requireSelection(state.phase === "ready" && state.preview && closedMessages(option))
  const configuration = state.preview.configurations.find((entry) => entry.id === option.candidateId)
  const skill = state.preview.skills.find((entry) => entry.id === option.candidateId)
  requireSelection(configuration || skill)
  if (skill) requireSelection(option.name === undefined && option.auth.case === undefined && option.geminiKeyTransport === undefined && (option.skillBinding === undefined || validSkillBinding(option.skillBinding)))
  if (configuration) {
    requireSelection([ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_CHOICE, ImportCandidateStatus.NAME_CONFLICT].includes(configuration.status) && option.skillBinding === undefined)
    requireSelection(option.name === undefined || validName(option.name))
    requireSelection(option.auth.case === undefined || configuration.knownAgentId === "claude" && option.auth.case === "claudeAuthMode" && [1, 2, 3].includes(option.auth.value) || configuration.knownAgentId === "gemini" && option.auth.case === "geminiAuthMode" && [1, 2].includes(option.auth.value))
    requireSelection(option.geminiKeyTransport === undefined || configuration.knownAgentId === "gemini" && option.auth.case === "geminiAuthMode" && option.auth.value === GeminiAuthMode.API_KEY && [1, 2].includes(option.geminiKeyTransport))
  }
  return { ...state, stale: true, options: [...state.options.filter((entry) => entry.candidateId !== option.candidateId), clone(ImportCandidateOptionsSchema, option)], chosen: { repositories: [], skills: [], configurations: [] }, acknowledged: [] }
}

export function canConfirmSelection(state: SelectionState, now = Date.now()) {
  if (state.phase !== "ready" && state.phase !== "confirming" || state.stale || !state.preview || state.preview.expiresMs <= BigInt(now)) return false
  if (Object.values(state.chosen).every((values) => values.length === 0)) return false
  if (!Object.entries(state.chosen).every(([group, ids]) => unique(ids) && ids.length <= maximum[group as SelectionGroup] && ids.every((id) => state.preview![group as SelectionGroup].some((entry) => entry.id === id && entry.status === ImportCandidateStatus.READY)))) return false
  const notices = selectionNotices(state.preview)
  return unique(state.acknowledged) && state.acknowledged.length === notices.length && notices.every((entry) => state.acknowledged.includes(entry.id))
}

export function selectionPlan(state: SelectionState, now = Date.now()): SelectionPlan {
  requireSelection(canConfirmSelection(state, now))
  return { repositoryIds: [...state.chosen.repositories], skillIds: [...state.chosen.skills], configurationIds: [...state.chosen.configurations], ackNoticeIds: [...state.acknowledged], options: state.options.map((entry) => clone(ImportCandidateOptionsSchema, entry)), preview: state.preview! }
}

export function acceptSelectionReceipt(receipt: ImportReceipt | undefined, operation: Uint8Array, plan: SelectionPlan, previous?: ImportReceipt): ImportReceipt {
  requireSelection(receipt && closedMessages(receipt) && equalsBytes(receipt.operationId, operation) && receipt.revision > 0n && [1, 2, 3, 4, 5].includes(receipt.phase))
  requireSelection(!previous || receipt.revision >= previous.revision && (receipt.revision !== previous.revision || equals(ImportReceiptSchema, previous, receipt)))
  requireSelection(receipt.repositoryOutcomes.length === plan.repositoryIds.length && receipt.repositoryOutcomes.every((entry, index) => entry.candidateId === plan.repositoryIds[index] && [1, 2, 3, 4, 5, 6, 7, 8].includes(entry.code) && ([ImportRepositoryOutcomeCode.COLLECTED, ImportRepositoryOutcomeCode.ALREADY_COLLECTED].includes(entry.code) ? !!entry.repositoryId : entry.repositoryId === undefined)))
  requireSelection(receipt.createdAssets.length === 0 || receipt.createdAssets.length === plan.configurationIds.length)
  requireSelection(receipt.selectedSkillBindings.length === 0 || receipt.selectedSkillBindings.length === plan.skillIds.length)
  requireSelection(unique(receipt.createdAssets.map((entry) => entry.id)))
  receipt.createdAssets.forEach((asset, index) => {
    const candidate = plan.preview.configurations.find((entry) => entry.id === plan.configurationIds[index])
    requireSelection(candidate && asset.id && asset.revision > 0n && asset.schemaVersion === 1 && asset.agentId === candidate.knownAgentId && asset.kind === candidate.kind && asset.name === candidate.safeName)
  })
  receipt.selectedSkillBindings.forEach((binding, index) => requireSelection(validSkillBinding(binding) && equals(SkillBindingSchema, binding, plan.preview.skills.find((entry) => entry.id === plan.skillIds[index])!.binding!)))
  const terminal = [ImportReceiptPhase.COMPLETED, ImportReceiptPhase.PARTIAL, ImportReceiptPhase.FAILED].includes(receipt.phase)
  if (!terminal) requireSelection(receipt.createdAssets.length === 0 && receipt.selectedSkillBindings.length === 0)
  if (receipt.phase === ImportReceiptPhase.FAILED) requireSelection(receipt.createdAssets.length === 0 && receipt.selectedSkillBindings.length === 0)
  if (receipt.phase === ImportReceiptPhase.COMPLETED) requireSelection(receipt.createdAssets.length === plan.configurationIds.length && receipt.selectedSkillBindings.length === plan.skillIds.length && receipt.repositoryOutcomes.every((entry) => entry.code === ImportRepositoryOutcomeCode.COLLECTED || entry.code === ImportRepositoryOutcomeCode.ALREADY_COLLECTED))
  requireSelection(unique(receipt.notices.map((entry) => entry.id)) && receipt.notices.every((entry) => selectionNotices(plan.preview).some((notice) => notice.id === entry.id && notice.code === entry.code && notice.count === entry.count)))
  return clone(ImportReceiptSchema, receipt)
}

export function acceptSelectionStatus(response: GetImportCommitStatusResponse, operation: Uint8Array, plan: SelectionPlan, previous?: ImportReceipt) {
  requireSelection(closedMessages(response) && [1, 2, 3, 4].includes(response.observation))
  if (response.observation === ImportCommitObservation.NOT_OBSERVED) { requireSelection(!response.receipt && !previous); return undefined }
  const receipt = acceptSelectionReceipt(response.receipt, operation, plan, previous)
  const terminal = [ImportReceiptPhase.COMPLETED, ImportReceiptPhase.PARTIAL, ImportReceiptPhase.FAILED].includes(receipt.phase)
  requireSelection(terminal === (response.observation === ImportCommitObservation.TERMINAL))
  return receipt
}

export function equalsBytes(left: Uint8Array, right: Uint8Array) { return left.length === right.length && left.every((value, index) => value === right[index]) }

export function selectionOption(state: SelectionState, candidateId: string) {
  return state.options.find((entry) => entry.candidateId === candidateId) ?? create(ImportCandidateOptionsSchema, { candidateId })
}

export const selectionStatusLabels: Record<ImportCandidateStatus, string> = { 0: "未知导入状态", 1: "可选择", 2: "需要明确选项", 3: "请先收录仓库，再重新预览", 4: "请显式绑定同仓技能", 5: "此 agent 尚不支持导入", 6: "此配置类别尚不支持导入", 7: "含不支持的功能字段，整项不导入", 8: "名称冲突，请改名后重新预览" }
export const selectionNoticeLabels: Record<ImportNoticeCode, string> = { 0: "未知导入提示", 1: "凭据仅显示存在状态，本次不会迁移", 2: "来源展示元数据不导入", 3: "其它来源数据不导入", 4: "来源内容未比对，仅绑定现有同仓技能", 5: "未知来源部分不导入" }
