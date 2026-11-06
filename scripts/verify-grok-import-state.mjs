import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "grok-import-state-test"
const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/import-selection-state.ts"))}; export { PreviewImportSelectionResponseSchema, ImportReceiptSchema } from ${JSON.stringify(resolve("web/src/gen/skillhub/import/v1/import_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
const destination = join(mkdtempSync(join(tmpdir(), "grok-import-state-")), "state.mjs")
await build.write({ file: destination, format: "esm" })
await build.close()
const state = await import(pathToFileURL(destination))
const response = state.create(state.PreviewImportSelectionResponseSchema, {
  ticket: new Uint8Array(290), expiresMs: BigInt(Date.now() + 240000),
  configurations: [{ id: "c".repeat(64), knownAgentId: "grokbuild", kind: 1, status: 1, safeName: "Grok imported", credentialSlots: [{ slotId: "grokbuild.api-key", present: true }], draft: { name: "Grok imported", payload: { case: "grokbuildProvider", value: { mode: 2, model: { profile: "work", upstreamModel: "grok-model", endpoint: "https://grok.example/v1", name: "Work model", apiBackend: "responses", contextWindow: 9007199254740991n, envKey: "GROK_API_KEY" } } } } }],
})
const preview = state.acceptSelectionPreview(response)
assert.equal(preview.configurations[0].draft.payload.value.model.contextWindow, 9007199254740991n)
for (const name of ["a".repeat(81), "a".repeat(128), "界".repeat(42) + "ab"]) {
  const named = structuredClone(response)
  named.configurations[0].safeName = named.configurations[0].draft.name = name
  assert.equal(state.acceptSelectionPreview(named).configurations[0].safeName, name)
}
for (const name of ["a".repeat(129), "界".repeat(43), "bad\u0085", "\uD800", " padded", "padded "]) {
  const named = structuredClone(response)
  named.configurations[0].safeName = named.configurations[0].draft.name = name
  assert.throws(() => state.acceptSelectionPreview(named))
}
for (const mutate of [
  (candidate) => { candidate.credentialSlots[0].slotId = "claude.api-key" },
  (candidate) => { candidate.draft.payload.value.model.envKey = "" },
  (candidate) => { candidate.draft.payload.value.model.contextWindow = 9007199254740992n },
  (candidate) => { candidate.draft.payload.value.model.endpoint = "http://grok.example/v1" },
  (candidate) => { candidate.draft.payload.value.model.apiBackend = "unknown" },
  (candidate) => { candidate.draft.payload.value.model = undefined },
  (candidate) => { candidate.draft.payload.value.mode = 1 },
]) {
  const invalid = structuredClone(response)
  mutate(invalid.configurations[0])
  assert.throws(() => state.acceptSelectionPreview(invalid))
}
const operation = new Uint8Array(16).fill(1)
const plan = { repositoryIds: [], skillIds: [], configurationIds: ["c".repeat(64)], ackNoticeIds: [], options: [], preview }
const receipt = state.create(state.ImportReceiptSchema, { operationId: operation, revision: 1n, phase: 3, createdAssets: [{ id: "A".repeat(26), agentId: "grokbuild", kind: 1, schemaVersion: 1, revision: 1n, name: "Grok imported" }] })
assert.equal(state.acceptSelectionReceipt(receipt, operation, plan).createdAssets[0].agentId, "grokbuild")
receipt.createdAssets[0].agentId = "claude"
assert.throws(() => state.acceptSelectionReceipt(receipt, operation, plan))
console.log("Grok import preview and receipt checks passed")

const openCode = state.create(state.PreviewImportSelectionResponseSchema, {
  ticket: new Uint8Array(290), expiresMs: BigInt(Date.now() + 240000),
  configurations: [{ id: "d".repeat(64), knownAgentId: "opencode", kind: 1, status: 1, safeName: "OpenCode imported", credentialSlots: [{ slotId: "opencode.api-key", present: true }, { slotId: "opencode.value.01", present: true }], draft: { name: "OpenCode imported", payload: { case: "opencodeProvider", value: { providerId: "work", format: { case: "v1", value: { npm: "@ai-sdk/openai-compatible", options: { baseUrl: "https://opencode.example/v1", apiKey: "opencode.api-key", headers: { entries: { Authorization: "opencode.value.01" } } }, models: { entries: { a: { limit: { context: "9007199254740991", output: "4096" } } } } } } } } } }],
})
const openCodePreview = state.acceptSelectionPreview(openCode)
assert.equal(openCodePreview.configurations[0].draft.payload.value.format.value.models.entries.a.limit.context, "9007199254740991")
for (const mutate of [
  (candidate) => { candidate.credentialSlots.pop() },
  (candidate) => { candidate.credentialSlots[1].slotId = "opencode.value.02" },
  (candidate) => { candidate.credentialSlots.push(candidate.credentialSlots[0]) },
  (candidate) => { candidate.draft.payload.value.format.value.options.apiKey = "SYNTHETIC-PRIVATE-MARKER" },
  (candidate) => { candidate.draft.payload.value.format.value.models.entries.a.limit.context = "9007199254740992" },
  (candidate) => { candidate.draft.payload.value.format = { case: "v2", value: {} } },
  (candidate) => { candidate.draft.payload.value.format.value.options.headers.entries.authorization = "opencode.value.01" },
]) {
  const invalid = structuredClone(openCode)
  mutate(invalid.configurations[0])
  assert.throws(() => state.acceptSelectionPreview(invalid))
}
const openCodePlan = { repositoryIds: [], skillIds: [], configurationIds: ["d".repeat(64)], ackNoticeIds: [], options: [], preview: openCodePreview }
const openCodeReceipt = state.create(state.ImportReceiptSchema, { operationId: operation, revision: 1n, phase: 3, createdAssets: [{ id: "B".repeat(26), agentId: "opencode", kind: 1, schemaVersion: 1, revision: 1n, name: "OpenCode imported" }] })
assert.equal(state.acceptSelectionReceipt(openCodeReceipt, operation, openCodePlan).createdAssets[0].agentId, "opencode")
openCodeReceipt.createdAssets[0].agentId = "claude"
assert.throws(() => state.acceptSelectionReceipt(openCodeReceipt, operation, openCodePlan))
console.log("OpenCode import preview, dynamic slots and receipt checks passed")
