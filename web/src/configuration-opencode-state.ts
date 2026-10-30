import { clone, create, equals, type Message } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, CredentialReadiness, OpenCodeProviderSchema, type DraftView, type OpenCodeProvider, type OpenCodeValue, type OpenCodeV1Model, type OpenCodeV2Model, type OpenCodeSettings, type OpenCodeModelSettings, type OpenCodeCompaction, type OpenCodeLimit, type OpenCodeCost, type OpenCodeVariant } from "./gen/skillhub/configuration/v1/configuration_pb"
import type { AssetMetadata } from "./configuration-state"
import type { ConfigurationDraft } from "./configuration-form"

export type OptionalInput<Value> = { enabled: boolean; value: Value }
export type MapRow<Value> = { rowId: string; key: string; value: Value }
export type OrderedRow<Value> = { rowId: string; value: Value }
export type ValueFields = { kind: "choose" | "null" } | { kind: "boolean"; value: boolean } | { kind: "number" | "reference"; value: string } | { kind: "array"; value: OrderedRow<ValueFields>[] } | { kind: "object"; value: MapRow<ValueFields>[] }
export type ExtensionFields = OptionalInput<MapRow<ValueFields>[]>
export type HeaderFields = OptionalInput<MapRow<string>[]>
export type StringFields = OptionalInput<OrderedRow<string>[]>
export type LimitFields = { context: OptionalInput<string>; input: OptionalInput<string>; output: OptionalInput<string>; extensions: ExtensionFields }
export type CompactionFields = { type: string; extensions: ExtensionFields }
export type ModelSettingsFields = { compaction: OptionalInput<CompactionFields>; extensions: ExtensionFields }
export type SettingsFields = ModelSettingsFields & { timeout: OptionalInput<{ kind: "choose" | "number" | "disabled"; value: string }>; chunkTimeout: OptionalInput<string>; transport: OptionalInput<string> }
export type CostFields = { input: OptionalInput<string>; output: OptionalInput<string>; cache: OptionalInput<{ read: OptionalInput<string>; write: OptionalInput<string>; extensions: ExtensionFields }>; tier: OptionalInput<{ type: string; size: OptionalInput<string>; extensions: ExtensionFields }>; extensions: ExtensionFields }
export type VariantFields = { id: string; settings: OptionalInput<ModelSettingsFields>; headers: HeaderFields; body: ExtensionFields; extensions: ExtensionFields }
export type V1ModelFields = { name: OptionalInput<string>; limit: OptionalInput<LimitFields>; options: ExtensionFields; modalities: OptionalInput<{ input: StringFields; output: StringFields; extensions: ExtensionFields }>; thinking: OptionalInput<ValueFields>; cost: OptionalInput<ValueFields>; variants: OptionalInput<MapRow<ValueFields>[]>; extensions: ExtensionFields }
export type V2ModelFields = { modelId: OptionalInput<string>; family: OptionalInput<string>; name: OptionalInput<string>; package: OptionalInput<string>; disabled: OptionalInput<boolean | undefined>; settings: OptionalInput<ModelSettingsFields>; headers: HeaderFields; body: ExtensionFields; compatibility: OptionalInput<{ reasoningField: OptionalInput<ValueFields>; requireReasoning: OptionalInput<boolean | undefined>; maxTokensField: OptionalInput<string>; requireFinishReason: OptionalInput<boolean | undefined>; requireAssistantAfterTool: OptionalInput<boolean | undefined>; supportsPromptCacheKey: OptionalInput<boolean | undefined>; extensions: ExtensionFields }>; capabilities: OptionalInput<{ tools: OptionalInput<boolean | undefined>; input: StringFields; output: StringFields; extensions: ExtensionFields }>; limit: OptionalInput<LimitFields>; cost: OptionalInput<{ shape: "" | "single" | "tiers"; single: CostFields; tiers: OrderedRow<CostFields>[] }>; variants: OptionalInput<OrderedRow<VariantFields>[]>; extensions: ExtensionFields }
export type OpenCodeV1Fields = { npm: OptionalInput<string>; name: OptionalInput<string>; options: OptionalInput<{ baseUrl: OptionalInput<string>; apiKey: OptionalInput<string>; headers: HeaderFields; extensions: ExtensionFields }>; models: OptionalInput<MapRow<V1ModelFields>[]>; extensions: ExtensionFields }
export type OpenCodeV2Fields = { package: OptionalInput<string>; name: OptionalInput<string>; canonical: OptionalInput<string>; env: StringFields; settings: OptionalInput<SettingsFields>; headers: HeaderFields; body: ExtensionFields; models: OptionalInput<MapRow<V2ModelFields>[]>; extensions: ExtensionFields }
export type OpenCodeFields = { name: string; providerId: string; format?: "v1" | "v2"; v1: OpenCodeV1Fields; v2: OpenCodeV2Fields }
export type OpenCodeView = { mode: "editable" | "readonly"; asset: AssetMetadata; provider: OpenCodeProvider; slots: { slotId: string; present: boolean }[]; readiness: CredentialReadiness }
export const openCodeSlots = ["opencode.api-key", ...Array.from({ length: 63 }, (_, index) => `opencode.value.${String(index + 1).padStart(2, "0")}`)]
export const row = <Value,>(value: Value): OrderedRow<Value> => ({ rowId: crypto.randomUUID(), value })
export const mapRow = <Value,>(value: Value): MapRow<Value> => ({ ...row(value), key: "" })
const scalar = <Value,>(value: Value | undefined, fallback: Value): OptionalInput<Value> => ({ enabled: value !== undefined, value: value ?? fallback })
const nested = <Wire, Fields,>(value: Wire | undefined, convert: (value?: Wire) => Fields): OptionalInput<Fields> => ({ enabled: value !== undefined, value: convert(value) })
const maps = <Wire, Fields,>(value: { entries: Record<string, Wire> } | undefined, convert: (value: Wire) => Fields): OptionalInput<MapRow<Fields>[]> => ({ enabled: value !== undefined, value: Object.entries(value?.entries ?? {}).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => ({ ...row(convert(item)), key })) })
const strings = (value?: { values: string[] }): StringFields => ({ enabled: value !== undefined, value: (value?.values ?? []).map(row) })
const extensions = (value?: { entries: Record<string, OpenCodeValue> }): ExtensionFields => maps(value, valueFields)
const headers = (value?: { entries: Record<string, string> }): HeaderFields => maps(value, (item) => item)
export function valueFields(value?: OpenCodeValue): ValueFields {
  const branch = value?.value
  switch (branch?.case) {
    case "null": if (!branch.value) throw new ConnectError("", Code.DataLoss); return { kind: "null" }
    case "boolean": return { kind: "boolean", value: branch.value }
    case "number": return { kind: "number", value: branch.value }
    case "secretReference": return { kind: "reference", value: branch.value }
    case "array": return { kind: "array", value: branch.value.values.map((item) => row(valueFields(item))) }
    case "object": return { kind: "object", value: extensions(branch.value).value }
    case "literal": throw new ConnectError("", Code.DataLoss)
    default: return { kind: "choose" }
  }
}
export function limitFields(value?: OpenCodeLimit): LimitFields { return { context: scalar(value?.context, ""), input: scalar(value?.input, ""), output: scalar(value?.output, ""), extensions: extensions(value?.extensions) } }
export function compactionFields(value?: OpenCodeCompaction): CompactionFields { return { type: value?.type ?? "", extensions: extensions(value?.extensions) } }
export function modelSettingsFields(value?: Pick<OpenCodeModelSettings, "compaction" | "extensions">): ModelSettingsFields { return { compaction: nested(value?.compaction, compactionFields), extensions: extensions(value?.extensions) } }
export function settingsFields(value?: OpenCodeSettings): SettingsFields { return { ...modelSettingsFields(value), timeout: { enabled: value?.timeout !== undefined, value: { kind: value?.timeout?.value.case === "number" ? "number" : value?.timeout?.value.case === "disabled" && !value.timeout.value.value ? "disabled" : "choose", value: value?.timeout?.value.case === "number" ? value.timeout.value.value : "" } }, chunkTimeout: scalar(value?.chunkTimeout, ""), transport: scalar(value?.transport, "") } }
export function costFields(value?: OpenCodeCost): CostFields { return { input: scalar(value?.input, ""), output: scalar(value?.output, ""), cache: { enabled: value?.cache !== undefined, value: { read: scalar(value?.cache?.read, ""), write: scalar(value?.cache?.write, ""), extensions: extensions(value?.cache?.extensions) } }, tier: { enabled: value?.tier !== undefined, value: { type: value?.tier?.type ?? "context", size: scalar(value?.tier?.size, ""), extensions: extensions(value?.tier?.extensions) } }, extensions: extensions(value?.extensions) } }
export function variantFields(value?: OpenCodeVariant): VariantFields { return { id: value?.id ?? "", settings: nested(value?.settings, modelSettingsFields), headers: headers(value?.headers), body: extensions(value?.body), extensions: extensions(value?.extensions) } }
export function v1ModelFields(value?: OpenCodeV1Model): V1ModelFields { return { name: scalar(value?.name, ""), limit: nested(value?.limit, limitFields), options: extensions(value?.options), modalities: { enabled: value?.modalities !== undefined, value: { input: strings(value?.modalities?.input), output: strings(value?.modalities?.output), extensions: extensions(value?.modalities?.extensions) } }, thinking: nested(value?.thinking, valueFields), cost: nested(value?.cost, valueFields), variants: maps(value?.variants, valueFields), extensions: extensions(value?.extensions) } }
export function v2ModelFields(value?: OpenCodeV2Model): V2ModelFields {
  const compatibility = value?.compatibility
  const costs = value?.cost?.shape
  return { modelId: scalar(value?.modelId, ""), family: scalar(value?.family, ""), name: scalar(value?.name, ""), package: scalar(value?.package, ""), disabled: scalar(value?.disabled, undefined), settings: nested(value?.settings, modelSettingsFields), headers: headers(value?.headers), body: extensions(value?.body), compatibility: { enabled: compatibility !== undefined, value: { reasoningField: nested(compatibility?.reasoningField, valueFields), requireReasoning: scalar(compatibility?.requireReasoning, undefined), maxTokensField: scalar(compatibility?.maxTokensField, ""), requireFinishReason: scalar(compatibility?.requireFinishReason, undefined), requireAssistantAfterTool: scalar(compatibility?.requireAssistantAfterTool, undefined), supportsPromptCacheKey: scalar(compatibility?.supportsPromptCacheKey, undefined), extensions: extensions(compatibility?.extensions) } }, capabilities: { enabled: value?.capabilities !== undefined, value: { tools: scalar(value?.capabilities?.tools, undefined), input: strings(value?.capabilities?.input), output: strings(value?.capabilities?.output), extensions: extensions(value?.capabilities?.extensions) } }, limit: nested(value?.limit, limitFields), cost: { enabled: value?.cost !== undefined, value: { shape: costs?.case === "tiers" ? "tiers" : costs?.case === "single" ? "single" : "", single: costFields(costs?.case === "single" ? costs.value : undefined), tiers: costs?.case === "tiers" ? costs.value.values.map((item) => row(costFields(item))) : [] } }, variants: { enabled: value?.variants !== undefined, value: (value?.variants?.values ?? []).map((item) => row(variantFields(item))) }, extensions: extensions(value?.extensions) }
}
export function openCodeFields(provider?: OpenCodeProvider, name = ""): OpenCodeFields {
  const first = provider?.format.case === "v1" ? provider.format.value : undefined
  const second = provider?.format.case === "v2" ? provider.format.value : undefined
  return { name, providerId: provider?.providerId ?? "", format: provider?.format.case, v1: { npm: scalar(first?.npm, ""), name: scalar(first?.name, ""), options: { enabled: first?.options !== undefined, value: { baseUrl: scalar(first?.options?.baseUrl, ""), apiKey: scalar(first?.options?.apiKey, ""), headers: headers(first?.options?.headers), extensions: extensions(first?.options?.extensions) } }, models: maps(first?.models, v1ModelFields), extensions: extensions(first?.extensions) }, v2: { package: scalar(second?.package, ""), name: scalar(second?.name, ""), canonical: scalar(second?.canonical, ""), env: strings(second?.env), settings: nested(second?.settings, settingsFields), headers: headers(second?.headers), body: extensions(second?.body), models: maps(second?.models, v2ModelFields), extensions: extensions(second?.extensions) } }
}
class FieldError extends Error {
  readonly path: string
  constructor(path: string) { super("OpenCode 字段无效；不会回显输入。"); this.path = path }
}
const bytes = (value: string) => new TextEncoder().encode(value).length
const validText = (value: string, maximum = 256) => value.length > 0 && bytes(value) <= maximum && !/[\p{Cc}\uD800-\uDFFF]/u.test(value)
const identifier = (value: string) => /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)
function decimal(value: string, integer = false, nonnegative = false) {
  if (value.length > 8192 || !/^-?(0|[1-9]\d*)(\.\d*[1-9])?$/.test(value) || value === "-0") return false
  const number = Number(value)
  const shortest = String(number)
  const [mantissa, exponent] = shortest.split("e")
  let canonical = shortest
  if (exponent !== undefined) {
    const negative = mantissa.startsWith("-")
    const unsigned = negative ? mantissa.slice(1) : mantissa
    const digits = unsigned.replace(".", "")
    const position = (unsigned.includes(".") ? unsigned.indexOf(".") : unsigned.length) + Number(exponent)
    canonical = (negative ? "-" : "") + (position <= 0 ? `0.${"0".repeat(-position)}${digits}` : position >= digits.length ? digits + "0".repeat(position - digits.length) : `${digits.slice(0, position)}.${digits.slice(position)}`)
  }
  return Number.isFinite(number) && canonical === value && (!Number.isInteger(number) || Number.isSafeInteger(number)) && (!integer || Number.isInteger(number)) && (!nonnegative || number >= 0)
}
function packageName(value: string) { return bytes(value) <= 256 && /^(?:@[a-z0-9._-]+\/)?[a-z0-9._-]+$/.test(value) && value.replace(/^@/, "").split("/").every((part) => part !== "." && part !== "..") }
function endpoint(value: string) {
  try { const parsed = new URL(value); const decoded = decodeURIComponent(parsed.pathname); return validText(value, 2048) && value === value.trim() && /^https:\/\//.test(value) && parsed.protocol === "https:" && !!parsed.hostname && !parsed.username && !parsed.password && !/[?#\\]/.test(value) && !/^https:\/\/[^/]*@/.test(value) && !/^https:\/\/[^/]*:(?:0+)?(?:\/|$)/.test(value) && !/[\\\p{Cc}]/u.test(decoded) } catch { return false }
}
type Native = string | boolean | number | null | Native[] | { [key: string]: Native }
function native(value: unknown): Native {
  if (typeof value === "string" || typeof value === "boolean" || typeof value === "number" || value === null) return value
  if (Array.isArray(value)) return value.map(native)
  if (typeof value !== "object") throw new FieldError("provider")
  const object = value as Record<string, unknown>
  const type = typeof object.$typeName === "string" && object.$typeName.startsWith("skillhub.configuration.v1.OpenCode") ? object.$typeName.split(".").at(-1) : undefined
  if (["OpenCodeArray", "OpenCodeStrings", "OpenCodeVariants", "OpenCodeCostList"].includes(type ?? "")) return native(object.values)
  if (["OpenCodeObject", "OpenCodeHeaders", "OpenCodeV1Models", "OpenCodeV2Models"].includes(type ?? "")) return native(object.entries)
  const result: { [key: string]: Native } = Object.create(null)
  for (const [key, item] of Object.entries(object)) {
    if (type && key.startsWith("$") || item === undefined) continue
    if ((type === "OpenCodeValue" || type === "OpenCodeTimeout") && key === "value" || type === "OpenCodeCosts" && key === "shape" || type === "OpenCodeProvider" && key === "format") {
      const branch = item as { case: string; value: unknown }
      if (!branch.case) throw new FieldError("provider")
      const branchName = branch.case === "secretReference" ? "reference" : branch.case === "boolean" ? "bool" : branch.case
      result[branchName] = native(branch.value)
      if (key === "format") result.format = branch.case
    } else result[type && key === "providerId" ? "providerID" : type && key === "baseUrl" ? "baseURL" : type && key === "modelId" ? "modelID" : key] = native(item)
  }
  return result
}
function compile(fields: OpenCodeFields) {
  const fail = (path: string): never => { throw new FieldError(path) }
  const check = (condition: boolean, path: string) => { if (!condition) fail(path) }
  const stats = { nodes: 0, depth: 0, headers: 0, variants: 0, extensions: 0, providerBytes: 0, desiredBytes: 0 }
  const references = new Set<string>()
  const reference = (value: string, path: string) => { check(openCodeSlots.includes(value), path); references.add(value); return value }
  const text = (field: OptionalInput<string>, path: string, validate = validText) => { if (!field.enabled) return undefined; check(validate(field.value), path); return field.value }
  const boolean = (field: OptionalInput<boolean | undefined>, path: string) => { if (!field.enabled) return undefined; check(typeof field.value === "boolean", path); return field.value }
  const record = <Input, Output,>(rows: MapRow<Input>[], path: string, convert: (input: Input, path: string) => Output, keyValid = validText): Record<string, Output> => {
    const result: Record<string, Output> = Object.create(null)
    rows.forEach((item, index) => { const location = `${path}.${index}`; check(keyValid(item.key) && !Object.hasOwn(result, item.key), location); result[item.key] = convert(item.value, location) })
    return result
  }
  const value = (field: ValueFields, path: string, depth = 1): OpenCodeValue => {
    stats.nodes++; stats.depth = Math.max(stats.depth, depth)
    check(stats.nodes <= 2048 && depth <= 12, path)
    switch (field.kind) {
      case "null": return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "null", value: true } }
      case "boolean": return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "boolean", value: field.value } }
      case "number": check(decimal(field.value), path); return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "number", value: field.value } }
      case "reference": return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "secretReference", value: reference(field.value, path) } }
      case "array": return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "array", value: { $typeName: "skillhub.configuration.v1.OpenCodeArray", values: field.value.map((item, index) => value(item.value, `${path}.${index}`, depth + 1)) } } }
      case "object": return { $typeName: "skillhub.configuration.v1.OpenCodeValue", value: { case: "object", value: { $typeName: "skillhub.configuration.v1.OpenCodeObject", entries: record(field.value, path, (child, location) => value(child, location, depth + 1)) } } }
      default: return fail(path)
    }
  }
  const object = (field: ExtensionFields, path: string) => field.enabled ? value({ kind: "object", value: field.value }, path).value : undefined
  const objectWire = (field: ExtensionFields, path: string) => { const branch = object(field, path); return branch?.case === "object" ? branch.value : undefined }
  const extra = (field: ExtensionFields, path: string, reserved: string[]) => {
    if (!field.enabled) return undefined
    stats.extensions += field.value.length; check(stats.extensions <= 256, path)
    check(field.value.every((item) => !reserved.includes(item.key)), path)
    return { entries: record(field.value, path, (child, location) => value(child, location)) }
  }
  const header = (field: HeaderFields, path: string) => {
    if (!field.enabled) return undefined
    stats.headers += field.value.length; check(stats.headers <= 64, path)
    const lowered = field.value.map((item) => item.key.toLowerCase()); check(new Set(lowered).size === lowered.length, path)
    return { entries: record(field.value, path, reference, (key) => key.length <= 128 && /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key)) }
  }
  const stringList = (field: StringFields, path: string, names = false) => { if (!field.enabled) return undefined; check(field.value.length <= 256, path); check(field.value.every((item) => validText(item.value) && (!names || /^[A-Za-z_][A-Za-z0-9_]*$/.test(item.value))), path); return { values: field.value.map((item) => item.value) } }
  const numberText = (field: OptionalInput<string>, path: string, integer = false, nonnegative = false) => text(field, path, (item) => decimal(item, integer, nonnegative))
  const limit = (field: OptionalInput<LimitFields>, path: string, first = false) => { if (!field.enabled) return undefined; const item = field.value; check(!first || !item.input.enabled, path); return { context: numberText(item.context, `${path}.context`, true, first), input: numberText(item.input, `${path}.input`, true), output: numberText(item.output, `${path}.output`, true, first), extensions: extra(item.extensions, `${path}.extensions`, ["context", "input", "output"]) } }
  const compaction = (field: OptionalInput<CompactionFields>, path: string) => { if (!field.enabled) return undefined; check(["summary", "native"].includes(field.value.type), path); return { type: field.value.type, extensions: extra(field.value.extensions, `${path}.extensions`, ["type"]) } }
  const modelSettings = (field: OptionalInput<ModelSettingsFields>, path: string) => field.enabled ? { compaction: compaction(field.value.compaction, `${path}.compaction`), extensions: extra(field.value.extensions, `${path}.extensions`, ["compaction"]) } : undefined
  const cost = (item: CostFields, path: string) => {
    check(item.input.enabled && item.output.enabled, path)
    if (item.tier.enabled) check(item.tier.value.type === "context" && item.tier.value.size.enabled, path)
    return { input: numberText(item.input, `${path}.input`), output: numberText(item.output, `${path}.output`), cache: item.cache.enabled ? { read: numberText(item.cache.value.read, `${path}.cache.read`), write: numberText(item.cache.value.write, `${path}.cache.write`), extensions: extra(item.cache.value.extensions, `${path}.cache.extensions`, ["read", "write"]) } : undefined, tier: item.tier.enabled ? { type: "context", size: numberText(item.tier.value.size, `${path}.tier.size`, true), extensions: extra(item.tier.value.extensions, `${path}.tier.extensions`, ["type", "size"]) } : undefined, extensions: extra(item.extensions, `${path}.extensions`, ["input", "output", "cache", "tier"]) }
  }
  const firstModel = (item: V1ModelFields, path: string) => {
    if (item.variants.enabled) { stats.variants += item.variants.value.length; check(stats.variants <= 128 && item.variants.value.every((entry) => entry.value.kind === "object"), `${path}.variants`) }
    return { name: text(item.name, `${path}.name`), limit: limit(item.limit, `${path}.limit`, true), options: objectWire(item.options, `${path}.options`), modalities: item.modalities.enabled ? { input: stringList(item.modalities.value.input, `${path}.modalities.input`), output: stringList(item.modalities.value.output, `${path}.modalities.output`), extensions: extra(item.modalities.value.extensions, `${path}.modalities.extensions`, ["input", "output"]) } : undefined, thinking: item.thinking.enabled ? value(item.thinking.value, `${path}.thinking`) : undefined, cost: item.cost.enabled ? value(item.cost.value, `${path}.cost`) : undefined, variants: item.variants.enabled ? { entries: record(item.variants.value, `${path}.variants`, value, identifier) } : undefined, extensions: extra(item.extensions, `${path}.extensions`, ["name", "limit", "options", "modalities", "thinking", "cost", "variants"]) }
  }
  const secondModel = (item: V2ModelFields, path: string) => {
    const compatibility = item.compatibility.value
    const capabilities = item.capabilities.value
    if (item.compatibility.enabled && compatibility.reasoningField.enabled) check(compatibility.reasoningField.value.kind === "reference", `${path}.compatibility.reasoningField`)
    if (item.capabilities.enabled) check(capabilities.tools.enabled && capabilities.input.enabled && capabilities.output.enabled, `${path}.capabilities`)
    if (item.variants.enabled) { stats.variants += item.variants.value.length; const names = item.variants.value.map((entry) => entry.value.id); check(stats.variants <= 128 && names.every(identifier) && new Set(names).size === names.length, `${path}.variants`) }
    if (item.cost.enabled) check(item.cost.value.shape === "single" || item.cost.value.shape === "tiers", `${path}.cost`)
    if (item.cost.enabled && item.cost.value.shape === "tiers") check(item.cost.value.tiers.length <= 128, `${path}.cost`)
    return { modelId: text(item.modelId, `${path}.modelId`), family: text(item.family, `${path}.family`), name: text(item.name, `${path}.name`), package: text(item.package, `${path}.package`, packageName), disabled: boolean(item.disabled, `${path}.disabled`), settings: modelSettings(item.settings, `${path}.settings`), headers: header(item.headers, `${path}.headers`), body: objectWire(item.body, `${path}.body`), compatibility: item.compatibility.enabled ? { reasoningField: compatibility.reasoningField.enabled ? value(compatibility.reasoningField.value, `${path}.compatibility.reasoningField`) : undefined, requireReasoning: boolean(compatibility.requireReasoning, `${path}.compatibility.requireReasoning`), maxTokensField: text(compatibility.maxTokensField, `${path}.compatibility.maxTokensField`, (item) => ["max_tokens", "max_completion_tokens"].includes(item)), requireFinishReason: boolean(compatibility.requireFinishReason, `${path}.compatibility.requireFinishReason`), requireAssistantAfterTool: boolean(compatibility.requireAssistantAfterTool, `${path}.compatibility.requireAssistantAfterTool`), supportsPromptCacheKey: boolean(compatibility.supportsPromptCacheKey, `${path}.compatibility.supportsPromptCacheKey`), extensions: extra(compatibility.extensions, `${path}.compatibility.extensions`, ["reasoningField", "requireReasoning", "maxTokensField", "requireFinishReason", "requireAssistantAfterTool", "supportsPromptCacheKey"]) } : undefined, capabilities: item.capabilities.enabled ? { tools: boolean(capabilities.tools, `${path}.capabilities.tools`), input: stringList(capabilities.input, `${path}.capabilities.input`), output: stringList(capabilities.output, `${path}.capabilities.output`), extensions: extra(capabilities.extensions, `${path}.capabilities.extensions`, ["tools", "input", "output"]) } : undefined, limit: limit(item.limit, `${path}.limit`), cost: item.cost.enabled ? item.cost.value.shape === "single" ? { shape: { case: "single" as const, value: cost(item.cost.value.single, `${path}.cost.single`) } } : { shape: { case: "tiers" as const, value: { values: item.cost.value.tiers.map((entry, index) => cost(entry.value, `${path}.cost.tiers.${index}`)) } } } : undefined, variants: item.variants.enabled ? { values: item.variants.value.map((entry, index) => ({ id: entry.value.id, settings: modelSettings(entry.value.settings, `${path}.variants.${index}.settings`), headers: header(entry.value.headers, `${path}.variants.${index}.headers`), body: objectWire(entry.value.body, `${path}.variants.${index}.body`), extensions: extra(entry.value.extensions, `${path}.variants.${index}.extensions`, ["id", "settings", "headers", "body"]) })) } : undefined, extensions: extra(item.extensions, `${path}.extensions`, ["modelID", "family", "name", "package", "disabled", "settings", "headers", "body", "compatibility", "capabilities", "limit", "cost", "variants"]) }
  }
  check(validText(fields.name.trim(), 128), "name"); check(identifier(fields.providerId), "providerId"); check(fields.format === "v1" || fields.format === "v2", "format")
  const first = fields.v1
  const second = fields.v2
  const modelCount = fields.format === "v1" ? first.models : second.models
  check(!modelCount.enabled || modelCount.value.length <= 64, "models")
  const settings = second.settings.value
  if (fields.format === "v2" && second.settings.enabled && settings.timeout.enabled) check(settings.timeout.value.kind !== "choose", "v2.settings.timeout")
  const provider = create(OpenCodeProviderSchema, { providerId: fields.providerId, format: fields.format === "v1" ? { case: "v1", value: { npm: text(first.npm, "v1.npm", packageName), name: text(first.name, "v1.name"), options: first.options.enabled ? { baseUrl: text(first.options.value.baseUrl, "v1.options.baseUrl", endpoint), apiKey: first.options.value.apiKey.enabled ? reference(first.options.value.apiKey.value, "v1.options.apiKey") : undefined, headers: header(first.options.value.headers, "v1.options.headers"), extensions: extra(first.options.value.extensions, "v1.options.extensions", ["baseURL", "apiKey", "headers"]) } : undefined, models: first.models.enabled ? { entries: record(first.models.value, "v1.models", firstModel, identifier) } : undefined, extensions: extra(first.extensions, "v1.extensions", ["npm", "name", "options", "models"]) } } : { case: "v2", value: { package: text(second.package, "v2.package", packageName), name: text(second.name, "v2.name"), canonical: text(second.canonical, "v2.canonical"), env: stringList(second.env, "v2.env", true), settings: second.settings.enabled ? { timeout: settings.timeout.enabled ? settings.timeout.value.kind === "disabled" ? { value: { case: "disabled", value: false } } : { value: { case: "number", value: numberText({ enabled: true, value: settings.timeout.value.value }, "v2.settings.timeout")! } } : undefined, chunkTimeout: numberText(settings.chunkTimeout, "v2.settings.chunkTimeout"), compaction: compaction(settings.compaction, "v2.settings.compaction"), transport: text(settings.transport, "v2.settings.transport", (item) => ["http", "websocket"].includes(item)), extensions: extra(settings.extensions, "v2.settings.extensions", ["timeout", "chunkTimeout", "compaction", "transport"]) } : undefined, headers: header(second.headers, "v2.headers"), body: objectWire(second.body, "v2.body"), models: second.models.enabled ? { entries: record(second.models.value, "v2.models", secondModel, identifier) } : undefined, extensions: extra(second.extensions, "v2.extensions", ["package", "name", "canonical", "env", "settings", "headers", "body", "models"]) } } })
  const required = [...references].sort()
  stats.providerBytes = bytes(JSON.stringify(native(provider)))
  stats.desiredBytes = bytes(JSON.stringify({ provider: native(provider), slots: required.map((slot) => ({ slot, present: false })) }))
  check(stats.providerBytes <= 65536 && stats.desiredBytes <= 65536, "provider")
  return { provider, required, stats }
}
export function validateOpenCode(fields: OpenCodeFields): Record<string, string> { try { compile(fields); return {} } catch (error) { return { [error instanceof FieldError ? error.path : "provider"]: "OpenCode 字段无效；不会回显输入。" } } }
export function openCodeDraft(fields: OpenCodeFields): ConfigurationDraft { const { provider } = compile(fields); return { name: fields.name.trim(), payload: { case: "opencodeProvider", value: provider } } }
export function openCodeRequiredSlots(provider: OpenCodeProvider): string[] { return compile(openCodeFields(provider, "asset")).required }
export function openCodeFieldReferences(fields: OpenCodeFields): string[] {
  return Object.keys(openCodeReferenceLocations(fields)).sort()
}
export function openCodeReferenceLocations(fields: OpenCodeFields): Record<string, string[]> {
  const found: Record<string, string[]> = Object.create(null)
  function add(slot: string, path: string) { (found[slot] ??= []).push(path.replaceAll(".value.", ".").replace(/\.value$/, "")) }
  function visit(input: unknown, key = "", path = "") {
    if (!input || typeof input !== "object") return
    if (Array.isArray(input)) { input.forEach((item, index) => visit(item, "", `${path}.${index}`)); return }
    const object = input as Record<string, unknown>
    if (object.enabled === false) return
    if (object.kind === "reference" && typeof object.value === "string" && openCodeSlots.includes(object.value)) { add(object.value, path); return }
    if (key === "apiKey" && object.enabled && typeof object.value === "string" && openCodeSlots.includes(object.value)) { add(object.value, path); return }
    if (key === "headers" && object.enabled && Array.isArray(object.value)) { (object.value as MapRow<string>[]).forEach((item, index) => { if (openCodeSlots.includes(item.value)) add(item.value, `${path}.${index}`) }); return }
    for (const [childKey, child] of Object.entries(object)) visit(child, childKey, path ? `${path}.${childKey}` : childKey)
  }
  if (fields.format) visit(fields[fields.format], fields.format, fields.format)
  return found
}
export function openCodeNativeBudget(provider: OpenCodeProvider) { return compile(openCodeFields(provider, "asset")).stats }
export function supportsOpenCode(asset: AssetMetadata): boolean { return asset.agentId === "opencode" && asset.kind === AssetKind.PROVIDER && asset.schemaVersion === 1 }
export function requireOpenCodeView(view?: DraftView): OpenCodeView {
  if (!view?.asset || !supportsOpenCode(view.asset) || !view.draft || view.draft.payload.case !== "opencodeProvider") throw new ConnectError("", Code.FailedPrecondition)
  try {
    const provider = view.draft.payload.value
    const compiled = compile(openCodeFields(provider, view.asset.name))
    const slots = view.secrets.map(({ slotId, present }) => ({ slotId, present }))
    if (!equals(OpenCodeProviderSchema, provider, compiled.provider) || view.asset.name !== view.draft.name || !view.asset.id || view.asset.revision < 1n || view.asset.revision > 9223372036854775807n || view.asset.name !== view.asset.name.trim() || slots.length !== compiled.required.length || new Set(slots.map((slot) => slot.slotId)).size !== slots.length || slots.some((slot) => !compiled.required.includes(slot.slotId)) || view.credentialReadiness !== (slots.every((slot) => slot.present) ? CredentialReadiness.COMPLETE : CredentialReadiness.NEEDS_CREDENTIAL)) throw new Error("invalid view")
    const { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix } = view.asset
    return { mode: view.hasPreservedExtras ? "readonly" : "editable", asset: { id, agentId, kind, name, schemaVersion, revision, createdAtUnix, updatedAtUnix }, provider: clone(OpenCodeProviderSchema, provider), slots, readiness: view.credentialReadiness }
  } catch { throw new ConnectError("", Code.DataLoss) }
}
