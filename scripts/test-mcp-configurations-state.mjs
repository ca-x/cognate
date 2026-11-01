import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "mcp-configuration-state-test"
const red = process.argv.includes("--red-allow-replay")
let mutated = 0
const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/configuration-mcp-state.ts"))}; export * as wire from ${JSON.stringify(resolve("web/src/gen/skillhub/configuration/mcp/v1/mcp_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}; export { AssetKind } from ${JSON.stringify(resolve("web/src/gen/skillhub/configuration/v1/configuration_pb.ts"))};` : undefined, transform(code, path) {
  if (!red || path !== resolve("web/src/configuration-mcp-state.ts")) return
  const guard = "if (this.active?.write) this.state ="
  assert(code.includes(guard), "negative-control boundary absent")
  mutated++
  return code.replace(guard, "if (false && this.active?.write) this.state =")
} }] })
const destination = join(mkdtempSync(join(tmpdir(), "mcp-web-state-")), "state.mjs")
await build.write({ file: destination, format: "esm" })
await build.close()
assert.equal(mutated, red ? 1 : 0)
const state = await import(pathToFileURL(destination))
const { create, wire } = state
const draft = create(wire.DraftSchema, { name: "Private tools", agentId: "claude", document: { servers: [{ id: "local", name: "local-tools", enabled: true, connection: { case: "stdio", value: { command: "private-command", args: { values: [{ id: "first", slotRef: "arg/local/first" }] }, env: { values: [] } } } }, { id: "remote", name: "remote-tools", enabled: false, connection: { case: "sse", value: { url: "https://example.invalid/mcp", headers: { values: [{ name: "Authorization", slotRef: "header/remote/Authorization" }] } } } }] } })
assert.deepEqual(state.validateMCP(draft), {})
const uppercaseURL = structuredClone(draft)
uppercaseURL.document.servers[1].connection.value.url = "HTTPS://example.invalid/mcp"
assert.deepEqual(state.validateMCP(uppercaseURL), {}, "Go URL parsing accepts uppercase schemes without rewriting the declaration")
assert.deepEqual(state.mcpSlots(draft.document), ["arg/local/first", "header/remote/Authorization"])
const view = create(wire.ViewSchema, { asset: { id: "A".repeat(26), agentId: "claude", kind: state.AssetKind.MCP, schemaVersion: 1, revision: 1n, name: draft.name }, draft, secrets: [{ slotId: "arg/local/first", present: true }, { slotId: "header/remote/Authorization", present: false }], readiness: wire.Readiness.NEEDS_CREDENTIAL })
assert.equal(state.requireMCPView(view).readOnly, false)
const owned = state.requireMCPView(view)
owned.value.asset.name = "must-not-alias"
assert.equal(view.asset.name, draft.name, "owned view must not alias the RPC draft snapshot")
for (const mutate of [candidate => { candidate.asset.revision = 0n }, candidate => { candidate.secrets.reverse() }, candidate => { candidate.secrets[0].present = undefined }, candidate => { candidate.secrets.push(candidate.secrets[0]) }, candidate => { candidate.draft.name = "different" }, candidate => { candidate.readiness = wire.Readiness.COMPLETE }]) {
  const candidate = structuredClone(view)
  mutate(candidate)
  assert.throws(() => state.requireMCPView(candidate))
}
const extra = structuredClone(view)
extra.hasPreservedExtras = true
assert.equal(state.requireMCPView(extra).readOnly, true)
assert.equal(state.supportsMCP({ ...view.asset, schemaVersion: 2 }), false)
for (const mutate of [candidate => { candidate.document.servers.push(candidate.document.servers[0]) }, candidate => { candidate.document.servers[0].enabled = undefined }, candidate => { candidate.document.servers[0].connection.value.args.values[0].slotRef = "foreign/ref" }, candidate => { candidate.document.servers[0].connection.value.command = "x\0y" }, candidate => { candidate.document.servers[1].connection.value.url += "?token=x" }]) {
  const candidate = structuredClone(draft)
  mutate(candidate)
  assert(Object.keys(state.validateMCP(candidate)).length)
}
const removed = structuredClone(draft)
removed.document.servers.shift()
assert.throws(() => state.mcpMutations(removed, view.secrets, {}), "last reference removal requires explicit clear even when missing")
const replacements = { "arg/local/first": { action: "replace", value: "SYNTHETIC", confirmed: false } }
const changes = state.mcpMutations(draft, view.secrets, replacements)
assert.equal(changes[0].action.case, "replace")
state.clearMCPMutations(changes)
assert(changes[0].action.case === undefined)
assert.equal(state.mcpMutations(removed, view.secrets, { "arg/local/first": { action: "clear", confirmed: true, value: "" } })[1].action.case, "clear")
const empty = create(wire.DraftSchema, { name: "Empty", agentId: "claude", document: { servers: [] } })
assert.deepEqual(state.validateMCP(empty), {})
const emptyReplacement = state.mcpMutations(draft, view.secrets, { "arg/local/first": { action: "replace", value: "", confirmed: false } })
assert.equal(emptyReplacement[0].action.value.length, 0, "empty replacement is supported by the existing contract")
state.clearMCPMutations(emptyReplacement)
const preview = create(wire.PreviewResponseSchema, { operations: [{ serverId: "local", nativeName: "local-tools", action: wire.Action.UPSERT_MANAGED, desired: draft.document.servers[0], requiresManagedProof: true }, { serverId: "remote", nativeName: "remote-tools", action: wire.Action.DISABLE_MANAGED, requiresManagedProof: true }], canApply: false, applied: false, readiness: wire.Readiness.NEEDS_CREDENTIAL, warnings: [wire.Warning.NATIVE_NOT_QUALIFIED] })
assert.equal(state.requireMCPPreview(preview, draft), preview)
for (const mutate of [candidate => { candidate.canApply = true }, candidate => { candidate.applied = true }, candidate => { candidate.operations.reverse() }, candidate => { candidate.operations[0].requiresManagedProof = false }, candidate => { candidate.operations[0].desired.connection.value.command = "different" }]) {
  const candidate = structuredClone(preview)
  mutate(candidate)
  assert.throws(() => state.requireMCPPreview(candidate, draft))
}
const updated = structuredClone(view)
updated.asset.revision = 2n
assert.equal(state.requireMCPWritten(updated, draft, view.asset, false, view.secrets, []).asset.revision, 2n)
assert.throws(() => state.requireMCPWritten(updated, draft, view.asset, false, view.secrets, [{ slotId: "arg/local/first", action: { case: "clear", value: { confirmed: true } } }]), "ignored secret Clear must not be accepted")
let notify = 0, sends = 0, finish
const gate = new state.MCPRequestGate(() => notify++)
const first = gate.run(async () => { sends++; return new Promise(resolve => { finish = resolve }) }, true)
assert.equal(await gate.run(async () => { sends++ }, true), undefined)
gate.cancel()
finish("late result")
assert.equal(await first, undefined)
assert(gate.state.blocked && gate.state.uncertain)
assert.equal(await gate.run(async () => { sends++ }, true), undefined)
assert.equal(sends, 1)
await gate.readback(async () => "fresh state")
assert.equal(gate.state.blocked, false)
assert.equal(await gate.run(async () => "new reviewed operation", true), "new reviewed operation")
gate.dispose()
assert.equal(await gate.run(async () => { sends++ }, true), undefined)
assert.equal(sends, 1)
assert(notify > 0)
console.log("MCP configurations state PASS: shape, ordered refs, optional presence, readonly, explicit Clear, secret disposal, Unknown, no replay, readback, disposal")
