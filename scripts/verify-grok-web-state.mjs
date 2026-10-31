import assert from "node:assert/strict"
import { mkdtempSync, readFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "grok-web-state-test"
const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/configuration-grok-state.ts"))}; export { english } from ${JSON.stringify(resolve("web/src/locales/en.ts"))}; export { requireConfigurationView } from ${JSON.stringify(resolve("web/src/configuration-state.ts"))}; export { DraftViewSchema, AssetKind, GrokBuildMode, CredentialReadiness } from ${JSON.stringify(resolve("web/src/gen/skillhub/configuration/v1/configuration_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
const destination = join(mkdtempSync(join(tmpdir(), "grok-web-state-")), "state.mjs")
await build.write({ file: destination, format: "esm" })
await build.close()
const state = await import(pathToFileURL(destination))
const view = state.create(state.DraftViewSchema, { asset: { id: "A".repeat(26), agentId: "grokbuild", kind: state.AssetKind.PROVIDER, schemaVersion: 1, revision: 1n, name: "Native Grok" }, draft: { name: "Native Grok", payload: { case: "grokbuildProvider", value: { mode: state.GrokBuildMode.NATIVE_LOGIN } } }, secrets: [{ slotId: "grokbuild.api-key", present: false }], credentialReadiness: state.CredentialReadiness.COMPLETE })
assert.equal(state.requireConfigurationView(view).kind, "grok", "valid Grok provider must have a dedicated Web view")
view.hasPreservedExtras = true
assert.equal(state.requireConfigurationView(view).view.mode, "readonly")
view.hasPreservedExtras = false
const fields = { ...state.grokFields(), name: " Custom ", mode: state.GrokBuildMode.CUSTOM_MODEL, profile: "本地 profile", upstreamModel: "x-ai/model", endpoint: "https://ExAmPle.invalid:443/a%2Fb", modelName: "Model name", apiBackend: "responses", contextWindow: "9007199254740991", envEnabled: true, envKey: "GROK_KEY" }
assert.deepEqual(state.validateGrok(fields), {})
const draft = state.grokDraft(fields)
assert.equal(draft.name, "Custom")
assert.equal(draft.payload.value.model.contextWindow, 9007199254740991n)
assert.equal(draft.payload.value.model.profile, fields.profile)
assert.equal(draft.payload.value.model.endpoint, fields.endpoint)
view.asset.name = view.draft.name = "Custom"
view.draft.payload = { case: "grokbuildProvider", value: { $typeName: "skillhub.configuration.v1.GrokBuildProvider", mode: state.GrokBuildMode.CUSTOM_MODEL, model: { $typeName: "skillhub.configuration.v1.GrokBuildModel", ...draft.payload.value.model } } }
assert.equal(state.requireConfigurationView(view).view.readiness, state.CredentialReadiness.COMPLETE)
view.draft.payload.value.model.envKey = undefined
view.credentialReadiness = state.CredentialReadiness.NEEDS_CREDENTIAL
assert.equal(state.requireConfigurationView(view).view.present, false)
view.secrets[0].present = true
view.credentialReadiness = state.CredentialReadiness.COMPLETE
assert.equal(state.requireConfigurationView(view).view.present, true)
function reject(field, values) {
  for (const value of values) assert(state.validateGrok({ ...fields, [field]: value })[field], `invalid ${field} admitted`)
}
reject("contextWindow", ["0", "01", "+1", "1.0", "1e3", "9007199254740992", "1".repeat(100), " 1", "١"])
reject("profile", ["", " padded", "padded ", "a\u0085", "\uD800", "x".repeat(129)])
reject("upstreamModel", ["x".repeat(257), "model\n"])
reject("modelName", ["x".repeat(129), "name\0"])
reject("apiBackend", ["", "openai", "responses "])
reject("endpoint", ["http://example.invalid", "https://user:pass@example.invalid", "https://@example.invalid", "https://example.invalid?", "https://example.invalid#", "https://example.invalid:0", "https://example.invalid:65536", "https://example.invalid:", "https://example.invalid/a%0d", "https://example.invalid/%FF", "https://example.invalid\\path", "https://%65xample.invalid"])
reject("envKey", ["", "1KEY", "KEY-ONE", "x".repeat(129)])
assert.deepEqual(state.validateGrok({ ...fields, contextWindow: "1", profile: "x".repeat(128), upstreamModel: "x".repeat(256), modelName: "x".repeat(128), envKey: "_".repeat(128) }), {})
assert.equal(state.grokDraft({ ...fields, envEnabled: false, envKey: "bad-value" }).payload.value.model.envKey, undefined)
assert.equal(state.grokDraft({ ...fields, name: "\uFEFFname" }).name, "\uFEFFname", "do not trim a character Go preserves")
for (const mutate of [
  (candidate) => { candidate.secrets = [] },
  (candidate) => { candidate.secrets.push(candidate.secrets[0]) },
  (candidate) => { candidate.secrets[0].slotId = "other.api-key" },
  (candidate) => { candidate.asset.revision = 0n },
  (candidate) => { candidate.asset.id = "invalid" },
  (candidate) => { candidate.draft.name = "different" },
  (candidate) => { candidate.draft.payload.value.model.contextWindow = 9007199254740992n },
  (candidate) => { candidate.draft.payload.value.mode = state.GrokBuildMode.NATIVE_LOGIN },
  (candidate) => { candidate.draft.payload.value.model = undefined },
  (candidate) => { candidate.credentialReadiness = state.CredentialReadiness.NEEDS_CREDENTIAL },
]) {
  const candidate = structuredClone(view)
  mutate(candidate)
  assert.throws(() => state.requireConfigurationView(candidate), "corrupt receipt must not become editable")
}
for (const path of ["web/src/configuration-grok-state.ts", "web/src/configuration-grok-form.tsx"]) {
  for (const [, message] of readFileSync(path, "utf8").matchAll(/"([^"\n]*[\u4e00-\u9fff][^"\n]*)"/g)) assert(state.english[message], "Grok English translation missing")
}
console.log("Grok Web state PASS")
