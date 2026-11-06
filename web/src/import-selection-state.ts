import { clone, create, equals } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, ClaudeAuthMode, CodexAuthMode, CodexModality, CodexReasoningEffort, GeminiAuthMode, GeminiKeyTransport, GrokBuildMode, type Draft } from "./gen/skillhub/configuration/v1/configuration_pb"
import { grokFields, grokName, validGrokUnicode, validateGrok } from "./configuration-grok-state"
import { openCodeFields, openCodeRequiredSlots, validateOpenCode } from "./configuration-opencode-state"
import { ImportCandidateOptionsSchema, ImportCandidateStatus, ImportCommitObservation, ImportConfigurationCandidateSchema, ImportNoticeCode, ImportReceiptPhase, ImportReceiptSchema, ImportRepositoryCandidateSchema, ImportRepositoryOutcomeCode, ImportSkillCandidateSchema, SkillBindingSchema, SkillSourceReferenceSchema, type ImportCandidateOptions, type ImportNotice, type ImportReceipt, type GetImportCommitStatusResponse, type PreviewImportSelectionResponse, type SkillBinding, type SkillSourceReference } from "./gen/skillhub/import/v1/import_pb"

export type SelectionPhase = "idle" | "reading" | "previewing" | "ready" | "confirming" | "committing" | "unknown" | "terminal"
export type SelectionGroup = "repositories" | "skills" | "configurations"
export type SelectionPreview = Pick<PreviewImportSelectionResponse, "repositories" | "skills" | "configurations" | "notices" | "expiresMs">
export type SelectionState = {
  phase: SelectionPhase
  registerSources?: boolean
  preview?: SelectionPreview
  options: ImportCandidateOptions[]
  chosen: Record<SelectionGroup, string[]>
  acknowledged: string[]
  stale: boolean
  receipt?: ImportReceipt
  observation?: ImportCommitObservation
  error?: Code
}
export type SelectionPlan = { registerSources?: boolean; repositoryIds: string[]; skillIds: string[]; configurationIds: string[]; ackNoticeIds: string[]; options: ImportCandidateOptions[]; preview: SelectionPreview }

const hash = /^[a-f0-9]{64}$/
const statuses = [1, 2, 3, 4, 5, 6, 7, 8, 9]
const agents = ["claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"]
const slots: Record<string, string[]> = { claude: ["claude.api-key", "claude.auth-token"], codex: ["codex.provider-key"], gemini: ["gemini.api-key", "gemini.legacy-api-key"], grokbuild: ["grokbuild.api-key"] }
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
function validName(value: string) { return value.length > 0 && validGrokUnicode(value) && new TextEncoder().encode(value).length <= 128 && value === grokName(value) && !/\p{Cc}/u.test(value) }

export function initialSelectionState(registerSources = false): SelectionState {
  return { phase: "idle", registerSources, options: [], chosen: { repositories: [], skills: [], configurations: [] }, acknowledged: [], stale: false }
}

export function validSkillBinding(binding?: SkillBinding): binding is SkillBinding {
  return !!binding && closedMessages(binding) && binding.repositoryId.length > 0 && hash.test(binding.archiveHash) && binding.skillId.length > 0 && hash.test(binding.contentHash) && /^[^/\s]+\/[^/\s]+$/u.test(binding.repository) && binding.logicalPath.length > 0 && !binding.logicalPath.startsWith("/") && !binding.logicalPath.split("/").some((part) => part === ".." || part === "" || part.includes("\\"))
}

export function validSkillSourceReference(reference?: SkillSourceReference, committed = false): reference is SkillSourceReference {
  return !!reference && closedMessages(reference)
    && (committed ? reference.sourceId.length > 0 && !/[\s\p{Cc}]/u.test(reference.sourceId) : reference.sourceId === "")
    && /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(reference.repository) && !reference.repository.split("/").some((part) => part === "." || part === "..")
    && validGrokUnicode(reference.branch) && !/[\s\\\p{Cc}]/u.test(reference.branch)
    && reference.safeName.length > 0 && validGrokUnicode(reference.safeName) && new TextEncoder().encode(reference.safeName).length <= 256 && !/\p{Cc}/u.test(reference.safeName)
    && validGrokUnicode(reference.logicalPath) && !/[\\\p{Cc}]/u.test(reference.logicalPath) && !reference.logicalPath.startsWith("/")
    && (reference.logicalPath === "" || reference.logicalPath === "." || !reference.logicalPath.split("/").some((part) => part === ".." || part === "." || part === ""))
}

export function selectableSelection(group: SelectionGroup, status: ImportCandidateStatus, registerSources = false) {
  return status === ImportCandidateStatus.READY || registerSources && group === "skills" && status === ImportCandidateStatus.SOURCE_REFERENCE_READY
}

function cloneSelectionPreview(preview: SelectionPreview): SelectionPreview {
  return { expiresMs: preview.expiresMs, repositories: preview.repositories.map((entry) => clone(ImportRepositoryCandidateSchema, entry)), skills: preview.skills.map((entry) => clone(ImportSkillCandidateSchema, entry)), configurations: preview.configurations.map((entry) => clone(ImportConfigurationCandidateSchema, entry)), notices: preview.notices.map((entry) => ({ ...entry })) }
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
  } else if (agent === "grokbuild" && payload.case === "grokbuildProvider") {
    requireSelection(payload.value.mode === GrokBuildMode.CUSTOM_MODEL && payload.value.model && !Object.keys(validateGrok(grokFields(payload.value, draft.name))).length)
  } else if (agent === "opencode" && payload.case === "opencodeProvider") {
    requireSelection(payload.value.format.case === "v1" && !Object.keys(validateOpenCode(openCodeFields(payload.value, draft.name))).length)
    const provider = payload.value.format.value
    requireSelection(!provider.extensions && !provider.options?.extensions && Object.values(provider.models?.entries ?? {}).every((model) => !model.extensions && !model.options && !model.thinking && !model.cost && !model.variants && !model.limit?.extensions && !model.modalities?.extensions))
  } else requireSelection(false)
}

export function selectionNotices(preview: SelectionPreview): ImportNotice[] {
  return [...preview.notices, ...preview.skills.flatMap((entry) => entry.notices), ...preview.configurations.flatMap((entry) => entry.notices)]
}

export function acceptSelectionPreview(response: PreviewImportSelectionResponse, now = Date.now(), registerSources = false): SelectionPreview {
  requireSelection(closedMessages(response) && response.ticket.length === 290 && response.expiresMs > BigInt(now) && response.expiresMs <= BigInt(now + 300000))
  requireSelection(response.repositories.length <= 1000 && response.skills.length <= 1000 && response.configurations.length <= 34)
  const all = [...response.repositories, ...response.skills, ...response.configurations]
  requireSelection(all.every((entry) => hash.test(entry.id) && statuses.includes(entry.status)) && unique(all.map((entry) => entry.id)))
  for (const entry of response.repositories) requireSelection(entry.hint && [ImportCandidateStatus.READY, ImportCandidateStatus.UNSUPPORTED_FIELD].includes(entry.status))
  for (const entry of response.skills) {
    requireSelection([ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_REPOSITORY_IMPORT, ImportCandidateStatus.UNRESOLVED, ImportCandidateStatus.UNSUPPORTED_AGENT, ImportCandidateStatus.UNSUPPORTED_KIND, ImportCandidateStatus.UNSUPPORTED_FIELD, ImportCandidateStatus.SOURCE_REFERENCE_READY].includes(entry.status))
    requireSelection(entry.repositoryCandidateId === "" || response.repositories.some((repository) => repository.id === entry.repositoryCandidateId))
    if (entry.status === ImportCandidateStatus.READY) requireSelection(validSkillBinding(entry.binding) && entry.sourceReference === undefined)
    else if (entry.status === ImportCandidateStatus.SOURCE_REFERENCE_READY) {
      const repository = response.repositories.find((candidate) => candidate.id === entry.repositoryCandidateId)
      requireSelection(registerSources && entry.binding === undefined && validSkillSourceReference(entry.sourceReference) && entry.sourceReference.safeName === entry.safeName)
      requireSelection(repository?.status === ImportCandidateStatus.READY && repository.hint && repository.hint.repository === entry.sourceReference.repository && repository.hint.branch === entry.sourceReference.branch)
    } else requireSelection(entry.binding === undefined && entry.sourceReference === undefined)
  }
  for (const entry of response.configurations) {
    requireSelection([ImportCandidateStatus.READY, ImportCandidateStatus.NEEDS_CHOICE, ImportCandidateStatus.UNSUPPORTED_AGENT, ImportCandidateStatus.UNSUPPORTED_KIND, ImportCandidateStatus.UNSUPPORTED_FIELD, ImportCandidateStatus.NAME_CONFLICT].includes(entry.status))
    requireSelection(entry.knownAgentId === "" || agents.includes(entry.knownAgentId))
    requireSelection([AssetKind.PROVIDER, AssetKind.MCP, AssetKind.PROMPT, AssetKind.AGENT_SETTINGS, AssetKind.WORKSPACE].includes(entry.kind))
    let allowedSlots = slots[entry.knownAgentId] ?? []
    if (entry.knownAgentId === "opencode") {
      if (entry.status === ImportCandidateStatus.READY) {
        requireSelection(entry.draft)
        validateDraft(entry.draft, entry.knownAgentId)
        requireSelection(entry.draft.payload.case === "opencodeProvider")
        allowedSlots = openCodeRequiredSlots(entry.draft.payload.value)
      } else allowedSlots = ["opencode.api-key", ...Array.from({ length: 63 }, (_, index) => `opencode.value.${String(index + 1).padStart(2, "0")}`)]
    }
    requireSelection(unique(entry.credentialSlots.map((slot) => slot.slotId)) && entry.credentialSlots.every((slot) => allowedSlots.includes(slot.slotId) && typeof slot.present === "boolean"))
    if (entry.status === ImportCandidateStatus.READY) {
      requireSelection(entry.kind === AssetKind.PROVIDER && entry.draft && entry.safeName === entry.draft.name)
      requireSelection(entry.credentialSlots.length === allowedSlots.length)
      validateDraft(entry.draft, entry.knownAgentId)
    } else requireSelection(entry.draft === undefined)
  }
  const notices = selectionNotices(response)
  requireSelection(unique(notices.map((entry) => entry.id)) && notices.every((entry) => hash.test(entry.id) && [1, 2, 3, 4, 5].includes(entry.code) && Number.isInteger(entry.count) && entry.count > 0))
  return cloneSelectionPreview(response)
}

export function withSelectionPreview(state: SelectionState, response: PreviewImportSelectionResponse, now = Date.now()): SelectionState {
  return { ...initialSelectionState(state.registerSources), phase: "ready", options: state.options.map((entry) => clone(ImportCandidateOptionsSchema, entry)), preview: acceptSelectionPreview(response, now, state.registerSources) }
}

export function chooseSelection(state: SelectionState, group: SelectionGroup, id: string, checked: boolean): SelectionState {
  if (state.phase !== "ready" || state.stale || !state.preview?.[group].some((entry) => entry.id === id && selectableSelection(group, entry.status, state.registerSources))) return state
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
  if (!Object.entries(state.chosen).every(([group, ids]) => unique(ids) && ids.length <= maximum[group as SelectionGroup] && ids.every((id) => state.preview![group as SelectionGroup].some((entry) => entry.id === id && selectableSelection(group as SelectionGroup, entry.status, state.registerSources))))) return false
  const notices = selectionNotices(state.preview)
  return unique(state.acknowledged) && state.acknowledged.length === notices.length && notices.every((entry) => state.acknowledged.includes(entry.id))
}

export function selectionPlan(state: SelectionState, now = Date.now()): SelectionPlan {
  requireSelection(canConfirmSelection(state, now))
  const ackNoticeIds = selectionNotices(state.preview!).filter((entry) => state.acknowledged.includes(entry.id)).map((entry) => entry.id)
  return { registerSources: state.registerSources ?? false, repositoryIds: [...state.chosen.repositories], skillIds: [...state.chosen.skills], configurationIds: [...state.chosen.configurations], ackNoticeIds, options: state.options.map((entry) => clone(ImportCandidateOptionsSchema, entry)), preview: cloneSelectionPreview(state.preview!) }
}

export function acceptSelectionReceipt(receipt: ImportReceipt | undefined, operation: Uint8Array, plan: SelectionPlan, previous?: ImportReceipt): ImportReceipt {
  requireSelection(receipt && closedMessages(receipt) && equalsBytes(receipt.operationId, operation) && receipt.revision > 0n && [1, 2, 3, 4, 5].includes(receipt.phase))
  requireSelection(!previous || receipt.revision >= previous.revision && (receipt.revision !== previous.revision || equals(ImportReceiptSchema, previous, receipt)))
  const successfulCodes = plan.registerSources ? [ImportRepositoryOutcomeCode.REGISTERED, ImportRepositoryOutcomeCode.ALREADY_REGISTERED] : [ImportRepositoryOutcomeCode.COLLECTED, ImportRepositoryOutcomeCode.ALREADY_COLLECTED]
  const allowedCodes = [...successfulCodes, ImportRepositoryOutcomeCode.NOT_ATTEMPTED, ImportRepositoryOutcomeCode.CANCELLED, ImportRepositoryOutcomeCode.AUTHORIZATION_LOST, ImportRepositoryOutcomeCode.SOURCE_REJECTED, ImportRepositoryOutcomeCode.CAPACITY_EXHAUSTED, ImportRepositoryOutcomeCode.UNKNOWN]
  requireSelection(receipt.repositoryOutcomes.length === plan.repositoryIds.length && receipt.repositoryOutcomes.every((entry, index) => entry.candidateId === plan.repositoryIds[index] && allowedCodes.includes(entry.code) && (successfulCodes.includes(entry.code) ? !!entry.repositoryId : entry.repositoryId === undefined)))
  const skills = plan.skillIds.map((id) => plan.preview.skills.find((entry) => entry.id === id))
  requireSelection(skills.every((entry) => entry && selectableSelection("skills", entry.status, plan.registerSources)))
  const expectedBindings = skills.filter((entry) => entry!.status === ImportCandidateStatus.READY)
  const expectedSources = skills.filter((entry) => entry!.status === ImportCandidateStatus.SOURCE_REFERENCE_READY)
  const sources = receipt.selectedSkillSources ?? []
  requireSelection(receipt.createdAssets.length === 0 || receipt.createdAssets.length === plan.configurationIds.length)
  requireSelection(receipt.selectedSkillBindings.length === 0 || receipt.selectedSkillBindings.length === expectedBindings.length)
  requireSelection(sources.length === 0 || sources.length === expectedSources.length)
  requireSelection(unique(receipt.createdAssets.map((entry) => entry.id)))
  receipt.createdAssets.forEach((asset, index) => {
    const candidate = plan.preview.configurations.find((entry) => entry.id === plan.configurationIds[index])
    requireSelection(candidate && asset.id && asset.revision > 0n && asset.schemaVersion === 1 && asset.agentId === candidate.knownAgentId && asset.kind === candidate.kind && asset.name === candidate.safeName)
  })
  receipt.selectedSkillBindings.forEach((binding, index) => requireSelection(validSkillBinding(binding) && equals(SkillBindingSchema, binding, expectedBindings[index]!.binding!)))
  const sourceIds = new Map<string, string>()
  const sourceRepositories = new Map<string, string>()
  function matchSource(repository: string, branch: string, sourceId: string) {
    const key = JSON.stringify([repository, branch])
    requireSelection(sourceId && (!sourceIds.has(key) || sourceIds.get(key) === sourceId) && (!sourceRepositories.has(sourceId) || sourceRepositories.get(sourceId) === key))
    sourceIds.set(key, sourceId)
    sourceRepositories.set(sourceId, key)
  }
  if (plan.registerSources) for (const outcome of receipt.repositoryOutcomes) if (successfulCodes.includes(outcome.code)) {
    const repository = plan.preview.repositories.find((entry) => entry.id === outcome.candidateId)?.hint
    requireSelection(repository)
    matchSource(repository.repository, repository.branch, outcome.repositoryId!)
  }
  sources.forEach((reference, index) => {
    const expected = expectedSources[index]?.sourceReference
    requireSelection(plan.registerSources && expected && validSkillSourceReference(reference, true) && equals(SkillSourceReferenceSchema, reference, create(SkillSourceReferenceSchema, { ...expected, sourceId: reference.sourceId })))
    matchSource(reference.repository, reference.branch, reference.sourceId)
  })
  const terminal = [ImportReceiptPhase.COMPLETED, ImportReceiptPhase.PARTIAL, ImportReceiptPhase.FAILED].includes(receipt.phase)
  if (plan.registerSources) requireSelection(receipt.phase !== ImportReceiptPhase.PARTIAL && (receipt.phase === ImportReceiptPhase.COMPLETED || receipt.repositoryOutcomes.every((entry) => !successfulCodes.includes(entry.code))))
  if (!terminal) requireSelection(receipt.createdAssets.length === 0 && receipt.selectedSkillBindings.length === 0 && sources.length === 0)
  if (receipt.phase === ImportReceiptPhase.FAILED) requireSelection(receipt.createdAssets.length === 0 && receipt.selectedSkillBindings.length === 0 && sources.length === 0)
  if (receipt.phase === ImportReceiptPhase.COMPLETED) requireSelection(receipt.createdAssets.length === plan.configurationIds.length && receipt.selectedSkillBindings.length === expectedBindings.length && sources.length === expectedSources.length && receipt.repositoryOutcomes.every((entry) => successfulCodes.includes(entry.code)))
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

export const selectionStatusLabels: Record<ImportCandidateStatus, string> = { 0: "未知导入状态", 1: "可选择", 2: "需要明确选项", 3: "请先收录仓库，再重新预览", 4: "请显式绑定同仓技能", 5: "此 agent 尚不支持导入", 6: "此配置类别尚不支持导入", 7: "含不支持的功能字段，整项不导入", 8: "名称冲突，请改名后重新预览", 9: "可保存来源，尚未下载或验证" }
export const selectionNoticeLabels: Record<ImportNoticeCode, string> = { 0: "未知导入提示", 1: "凭据仅显示存在状态，本次不会迁移", 2: "来源展示元数据不导入", 3: "其它来源数据不导入", 4: "来源内容未比对，仅绑定现有同仓技能", 5: "未知来源部分不导入" }
