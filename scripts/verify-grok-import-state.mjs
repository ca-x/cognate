import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "grok-import-state-test"
const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/import-selection-state.ts"))}; export { groupSelectionNotices } from ${JSON.stringify(resolve("web/src/import-selection-presentation.ts"))}; export { PreviewImportSelectionResponseSchema, ImportReceiptSchema } from ${JSON.stringify(resolve("web/src/gen/skillhub/import/v1/import_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
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

const noticeID = (index) => index.toString(16).padStart(64, "0")
const groupedResponse = state.create(state.PreviewImportSelectionResponseSchema, {
  ticket: new Uint8Array(290), expiresMs: BigInt(Date.now() + 240000),
  notices: [{ id: noticeID(1), code: 2, count: 3 }, { id: noticeID(2), code: 1, count: 1 }],
  skills: Array.from({ length: 60 }, (_, index) => ({
    id: noticeID(100 + index), safeName: `review-${index}`, status: 3,
    notices: [{ id: noticeID(200 + index), code: 2, count: 2 }],
  })),
  configurations: [{ id: response.configurations[0].id, knownAgentId: "grokbuild", kind: 1, status: 1, safeName: "Grok imported", credentialSlots: response.configurations[0].credentialSlots, draft: response.configurations[0].draft, notices: [
    { id: noticeID(3), code: 1, count: 2 }, { id: noticeID(4), code: 2, count: 1 },
  ] }],
})
let groupedState = state.withSelectionPreview(state.initialSelectionState(), groupedResponse)
groupedState = state.chooseSelection(groupedState, "configurations", groupedResponse.configurations[0].id, true)
const snapshot = structuredClone(groupedState.preview)
const groups = state.groupSelectionNotices(groupedState.preview)
assert.equal(groups.length, 2)
assert.deepEqual(groups.map((group) => [group.code, group.count, group.ids.length]), [[2, 124, 62], [1, 3, 2]])
assert.deepEqual(groups[0].contexts, [...groupedResponse.skills.map((entry) => entry.safeName), "Grok imported"])
assert.deepEqual(groups[1].contexts, ["Grok imported"])
const originalIDs = state.selectionNotices(groupedState.preview).map((notice) => notice.id)
assert.deepEqual(groups.flatMap((group) => group.ids).sort(), [...originalIDs].sort())
assert.equal(new Set(groups.flatMap((group) => group.ids)).size, originalIDs.length)
assert.deepEqual(groupedState.preview, snapshot, "presentation must not mutate protocol notices")
assert.equal(state.canConfirmSelection(groupedState), false)
groupedState = state.acknowledgeSelection(groupedState, "2", true)
assert.deepEqual(groupedState.acknowledged, [], "reason codes are not notice IDs")
for (const group of groups) for (const id of group.ids) groupedState = state.acknowledgeSelection(groupedState, id, true)
assert.equal(state.canConfirmSelection(groupedState), true)
assert.deepEqual(state.selectionPlan(groupedState).ackNoticeIds, originalIDs, "grouped acknowledgement must use the original protocol order")
for (const id of groups[0].ids) groupedState = state.acknowledgeSelection(groupedState, id, false)
assert.equal(state.canConfirmSelection(groupedState), false)
assert.deepEqual(groupedState.acknowledged, groups[1].ids, "clearing one reason must retain the other reason's IDs")
for (const id of groups[0].ids) groupedState = state.acknowledgeSelection(groupedState, id, true)
groupedState = state.acknowledgeSelection(groupedState, groups[0].ids[0], true)
assert.equal(state.selectionPlan(groupedState).ackNoticeIds.length, originalIDs.length, "repeat acknowledgement must not duplicate IDs")
assert.deepEqual(state.selectionPlan(groupedState).ackNoticeIds, originalIDs, "clearing and rechecking a reason must restore the original protocol order")
groupedState = state.acknowledgeSelection(groupedState, originalIDs[0], false)
assert.equal(state.canConfirmSelection(groupedState), false, "one missing acknowledgement must block confirmation")
assert.throws(() => state.selectionPlan(groupedState), "plan creation must not silently acknowledge a missing notice")
assert.equal(groupedState.acknowledged.includes(originalIDs[0]), false)
groupedState = state.acknowledgeSelection(groupedState, originalIDs[0], true)
assert.deepEqual(state.selectionPlan(groupedState).ackNoticeIds, originalIDs)
groupedState = state.withSelectionPreview(groupedState, groupedResponse)
assert.deepEqual(groupedState.acknowledged, [], "a fresh preview requires fresh explicit acknowledgements")
assert.deepEqual(state.groupSelectionNotices({ ...groupedState.preview, notices: [], skills: [], configurations: [] }), [])
console.log("Grouped notice counts, contexts, all 64 original IDs, independent acknowledgement and fresh-preview reset checks passed")
