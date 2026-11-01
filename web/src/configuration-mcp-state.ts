import { clone, create, equals, toJsonString } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, type Asset } from "./gen/skillhub/configuration/v1/configuration_pb"
import { Action, DraftSchema, Readiness, SecretMutationSchema, ServerSchema, ViewSchema, Warning, type Document, type Draft, type PreviewResponse, type SecretMutation, type SlotStatus, type View } from "./gen/skillhub/configuration/mcp/v1/mcp_pb"

export type MCPSecretInput = { action: "keep" | "replace" | "clear"; value: string; confirmed: boolean }
export type MCPInputs = Record<string, MCPSecretInput>
const encoder = new TextEncoder()
const size = (value: string) => encoder.encode(value).length
const unicode = (value: string) => !/[\uD800-\uDFFF]/u.test(value)
const identifier = (value: string) => /^[a-z][a-z0-9_-]{0,63}$/.test(value)
const trim = (value: string) => value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "")
const name = (value: string) => unicode(value) && size(value) >= 1 && size(value) <= 128 && trim(value) === value && !/\p{Cc}/u.test(value)
const invalid = () => new ConnectError("", Code.DataLoss)

export function supportsMCP(asset: Pick<Asset, "agentId" | "kind" | "schemaVersion">) {
  return asset.agentId === "claude" && asset.kind === AssetKind.MCP && asset.schemaVersion === 1
}

export function requireMCPAsset(asset?: Asset) {
  if (!asset || !/^[A-Z2-7]{26}$/.test(asset.id) || asset.kind !== AssetKind.MCP || !asset.agentId || !name(asset.name) || asset.revision < 1n || asset.revision > 9223372036854775807n || asset.schemaVersion < 1 || asset.schemaVersion > 65535 || asset.createdAtUnix < 0n || asset.updatedAtUnix < 0n) throw invalid()
  return asset
}

export function mcpSlots(document?: Document) {
  const refs: string[] = []
  for (const server of document?.servers ?? []) {
    if (server.connection.case === "stdio") {
      for (const value of server.connection.value.args?.values ?? []) refs.push(value.slotRef)
      for (const value of server.connection.value.env?.values ?? []) refs.push(value.slotRef)
    } else if (server.connection.case === "http" || server.connection.case === "sse") {
      for (const value of server.connection.value.headers?.values ?? []) refs.push(value.slotRef)
    }
  }
  return refs
}

export function validateMCP(draft: Draft): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!name(draft.name)) errors.name = "nameError"
  if (draft.agentId !== "claude" || !draft.document || draft.document.servers.length > 32) errors.document = "documentError"
  const ids = new Set<string>(), names = new Set<string>(), refs = new Set<string>()
  for (const [index, server] of (draft.document?.servers ?? []).entries()) {
    const field = `server-${index}`
    if (!identifier(server.id) || !identifier(server.name) || ids.has(server.id) || names.has(server.name) || typeof server.enabled !== "boolean") errors[field] = "serverError"
    ids.add(server.id); names.add(server.name)
    const ref = (actual: string, expected: string) => {
      if (actual !== expected || size(actual) > 128 || refs.has(actual)) errors[field] = "referenceError"
      refs.add(actual)
    }
    const named = (values: { values: { name: string; slotRef: string }[] } | undefined, purpose: "env" | "header") => {
      if (!values) return
      if (values.values.length > (purpose === "env" ? 64 : 32)) errors[field] = "documentError"
      const seen = new Set<string>()
      for (const value of values.values) {
        const key = purpose === "env" ? value.name : value.name.toLowerCase()
        if (size(value.name) > 128 || !(purpose === "env" ? /^[A-Za-z_][A-Za-z0-9_]*$/ : /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/).test(value.name) || seen.has(key)) errors[field] = "referenceError"
        seen.add(key)
        ref(value.slotRef, `${purpose}/${server.id}/${value.name}`)
      }
    }
    if (server.connection.case === "stdio") {
      const value = server.connection.value
      if (!value.command || !unicode(value.command) || size(value.command) > 4096 || value.command.includes("\0")) errors[field] = "commandError"
      if ((value.args?.values.length ?? 0) > 64) errors[field] = "documentError"
      for (const arg of value.args?.values ?? []) {
        if (!identifier(arg.id)) errors[field] = "referenceError"
        ref(arg.slotRef, `arg/${server.id}/${arg.id}`)
      }
      named(value.env, "env")
    } else if (server.connection.case === "http" || server.connection.case === "sse") {
      const value = server.connection.value
      const authority = /^https?:\/\/([^/?#]+)/i.exec(value.url)?.[1]
      if (!authority || authority.includes("@") || !unicode(value.url) || size(value.url) > 4096 || /[\0?#]/.test(value.url)) errors[field] = "urlError"
      named(value.headers, "header")
    } else errors[field] = "documentError"
  }
  if (refs.size > 512 || size(toJsonString(DraftSchema, draft)) > 2 * 1024 * 1024) errors.document = "documentError"
  return errors
}

export function requireMCPView(view?: View) {
  if (!view?.asset) throw invalid()
  const asset = requireMCPAsset(view.asset)
  if (!supportsMCP(asset)) return { value: create(ViewSchema, { asset }), readOnly: true }
  if (!view.draft || view.draft.name !== asset.name || view.draft.agentId !== asset.agentId || Object.keys(validateMCP(view.draft)).length) throw invalid()
  const refs = mcpSlots(view.draft.document)
  if (view.secrets.length !== refs.length || view.secrets.some((slot, index) => slot.slotId !== refs[index] || typeof slot.present !== "boolean")) throw invalid()
  const readiness = view.secrets.every(slot => slot.present) ? Readiness.COMPLETE : Readiness.NEEDS_CREDENTIAL
  if (view.readiness !== readiness) throw invalid()
  return { value: clone(ViewSchema, view), readOnly: view.hasPreservedExtras }
}

export function mcpMutations(draft: Draft, previous: SlotStatus[], inputs: MCPInputs) {
  const refs = mcpSlots(draft.document)
  const all = [...refs, ...previous.map(slot => slot.slotId).filter(ref => !refs.includes(ref))]
  const mutations: SecretMutation[] = []
  let total = 0
  try {
    for (const ref of all) {
      const input = inputs[ref] ?? { action: "keep", value: "", confirmed: false }
      if (!refs.includes(ref) && input.action !== "clear") throw new ConnectError("", Code.InvalidArgument)
      if (input.action === "clear" && !input.confirmed) throw new ConnectError("", Code.InvalidArgument)
      if (input.action === "replace") {
        const value = encoder.encode(input.value)
        mutations.push(create(SecretMutationSchema, { slotId: ref, action: { case: "replace", value } }))
        total += value.length
        if (value.length > 8192 || total > 131072 || !unicode(input.value) || input.value.includes("\0") || ref.startsWith("header/") && /[\r\n]/.test(input.value)) throw new ConnectError("", Code.InvalidArgument)
      } else mutations.push(create(SecretMutationSchema, { slotId: ref, action: input.action === "clear" ? { case: "clear", value: { confirmed: true } } : { case: "keep", value: {} } }))
    }
    return mutations
  } catch (error) { clearMCPMutations(mutations); throw error }
}

export function clearMCPMutations(mutations: SecretMutation[]) {
  for (const mutation of mutations) {
    if (mutation.action.case === "replace") mutation.action.value.fill(0)
    mutation.action = { case: undefined }
  }
}

export function requireMCPPreview(response: PreviewResponse, draft: Draft) {
  const servers = draft.document?.servers ?? []
  if (response.canApply || response.applied || ![Readiness.COMPLETE, Readiness.NEEDS_CREDENTIAL].includes(response.readiness) || response.operations.length !== servers.length || !response.warnings.includes(Warning.NATIVE_NOT_QUALIFIED) || new Set(response.warnings).size !== response.warnings.length || response.warnings.some(warning => ![Warning.NATIVE_NOT_QUALIFIED, Warning.INSECURE_HTTP].includes(warning))) throw invalid()
  for (const [index, operation] of response.operations.entries()) {
    const server = servers[index]
    if (operation.serverId !== server.id || operation.nativeName !== server.name || !operation.requiresManagedProof || operation.action !== (server.enabled ? Action.UPSERT_MANAGED : Action.DISABLE_MANAGED) || (server.enabled ? !operation.desired || !equals(ServerSchema, operation.desired, server) : operation.desired !== undefined)) throw invalid()
  }
  return response
}

export function requireMCPWritten(view: View | undefined, draft: Draft, previous: Asset | undefined, copy: boolean, previousSlots: SlotStatus[], mutations: SecretMutation[]) {
  const parsed = requireMCPView(view)
  const value = parsed.value
  if (parsed.readOnly || !value.asset || !value.draft || value.asset.revision !== (previous && !copy ? previous.revision + 1n : 1n) || previous && !copy && value.asset.id !== previous.id || copy && previous && value.asset.id === previous.id || !equals(DraftSchema, value.draft, draft) || copy && value.secrets.some(slot => slot.present)) throw invalid()
  for (const slot of value.secrets) {
    const action = mutations.find(mutation => mutation.slotId === slot.slotId)?.action.case
    const expected = !copy && (action === "replace" || action !== "clear" && previousSlots.some(previous => previous.slotId === slot.slotId && previous.present))
    if (slot.present !== expected) throw invalid()
  }
  return value
}

export class MCPRequestGate {
  state: { pending: boolean; blocked: boolean; uncertain: boolean; failure?: Code } = { pending: false, blocked: false, uncertain: false }
  private active?: { controller: AbortController; write: boolean }
  private generation = 0
  private disposed = false
  constructor(private changed: () => void) {}
  async run<Result>(send: (signal: AbortSignal) => Promise<Result>, write = false): Promise<Result | undefined> {
    if (this.disposed || this.active || write && this.state.blocked) return
    const active = { controller: new AbortController(), write }
    const generation = ++this.generation
    this.active = active
    this.state = { ...this.state, pending: true, failure: undefined }
    this.changed()
    try {
      const result = await send(active.controller.signal)
      if (!this.disposed && generation === this.generation && !active.controller.signal.aborted) return result
    } catch (error) {
      if (!this.disposed && generation === this.generation) {
        const failure = ConnectError.from(error).code
        this.state = { ...this.state, failure, blocked: this.state.blocked || write, uncertain: this.state.uncertain || write && failure !== Code.Aborted }
      }
    } finally {
      if (!this.disposed && generation === this.generation) { this.active = undefined; this.state = { ...this.state, pending: false }; this.changed() }
    }
  }
  async readback<Result>(read: (signal: AbortSignal) => Promise<Result>) {
    const result = await this.run(read)
    if (result !== undefined) { this.state = { pending: false, blocked: false, uncertain: false }; this.changed() }
    return result
  }
  cancel() {
    if (this.active?.write) this.state = { ...this.state, blocked: true, uncertain: true }
    this.generation++
    this.active?.controller.abort()
    this.active = undefined
    this.state = { ...this.state, pending: false }
    if (!this.disposed) this.changed()
  }
  dispose() { this.disposed = true; this.cancel(); this.changed = () => {} }
}
