import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, openSync, closeSync, existsSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { pathToFileURL } from "node:url"

const { values } = parseArgs({ options: {
  "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" },
  suite: { type: "string", default: "all" }, output: { type: "string" },
  "cache-observer": { type: "boolean", default: false },
} })
for (const option of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[option], `missing ${option}`)
assert(["preflight", "state", "boundaries", "display", "pending", "copy-integrity", "forms", "privacy", "lifecycle", "regression", "all"].includes(values.suite))
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const fixtureLog = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestOpenCodeAssetsBrowserFixture$", "-test.v", "-test.timeout=270s"], {
  env: { ...process.env, COGNATE_OPENCODE_BROWSER_DIR: directory }, stdio: ["ignore", fixtureLog, fixtureLog],
})
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const delay = (milliseconds) => new Promise((done) => setTimeout(done, milliseconds))
let ready
let browserStarted = false
const session = `oc-web-${process.pid}-${Date.now()}`
const evidence = []
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], {
    env: process.env, input, encoding: "utf8", timeout: 30000, killSignal: "SIGKILL", maxBuffer: 2 << 20,
  })
  if (result.status !== 0) {
    let diagnostic = `${result.stdout}\n${result.stderr}`
    for (const secret of [ready?.password, ready?.replacement, ready?.capability]) if (secret) diagnostic = diagnostic.replaceAll(secret, "[REDACTED]")
    writeFileSync(join(output, "browser-error.log"), diagnostic, { mode: 0o600 })
  }
  assert.equal(result.status, 0, `browser command failed: ${args.slice(0, 3).join(" ")}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser operation rejected: ${args[0]}`)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, description) {
  const deadline = Date.now() + 20000
  while (Date.now() < deadline) { if (await check()) return; await delay(150) }
  assert.fail(`bounded wait failed: ${description}`)
}
async function control(action, assetID) {
  const response = await fetch(`${ready.controlURL}/command`, { method: "POST", headers: { "X-Fixture-Capability": ready.capability, "Content-Type": "application/json", Connection: "close" }, body: JSON.stringify({ action, ...(assetID ? { assetID } : {}) }), signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200, "fixture control rejected")
}
async function state() {
  const response = await fetch(`${ready.controlURL}/state`, { headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200, "fixture state unavailable")
  return response.json()
}
function cacheProof(name) {
  if (!values["cache-observer"]) return
  const proof = evaluate("window.__ocCacheProof ?? null")
  assert(proof && proof.metadataOnly && proof.sentinelFree && !proof.foreignOwnerVisible && proof.count > 0, `private metadata observer failed: ${name}`)
  evidence.push({ name: `private-cache-${name}`, pass: true })
}
async function stateCases() {
  const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
  const input = "opencode-state-test-entry"
  const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/configuration-opencode-state.ts"))}; export { OpenCodeProviderSchema, CredentialReadiness, DraftViewSchema } from ${JSON.stringify(resolve("web/src/gen/skillhub/configuration/v1/configuration_pb.ts"))}; export {fromJson, create, equals} from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
  const destination = join(output, "state-test.mjs")
  await build.write({ file: destination, format: "esm" }); await build.close()
  const module = await import(pathToFileURL(destination))
  const expectations = JSON.parse(readFileSync(join(directory, "expectations.json"), "utf8"))
  const fields = {}
  const valid = (value) => Object.keys(module.validateOpenCode(value)).length === 0
  for (const [name, vector] of Object.entries(expectations)) {
    const provider = module.fromJson(module.OpenCodeProviderSchema, vector.provider)
    fields[name] = module.openCodeFields(provider, name)
    assert(valid(fields[name]), `Go-validated boundary rejected by form: ${name}`)
    assert(module.equals(module.OpenCodeProviderSchema, provider, module.openCodeDraft(fields[name]).payload.value), `typed presence/order roundtrip: ${name}`)
    assert.equal(module.openCodeNativeBudget(provider).providerBytes, vector.nativeBytes, `native byte accounting parity: ${name}`)
  }
  const reject = (name, mutate) => { const candidate = structuredClone(fields[name]); mutate(candidate); assert(!valid(candidate), `N+1 or invalid form accepted: ${name}`) }
  reject("depth12", (candidate) => { let item = candidate.v1.models.value[0].value.thinking.value; while (item.kind === "array") item = item.value[0].value; Object.assign(item, { kind: "array", value: [module.row({ kind: "null" })] }) })
  reject("nodes2048", (candidate) => candidate.v2.body.value[0].value.value.push(module.row({ kind: "null" })))
  reject("models64", (candidate) => candidate.v1.models.value.push({ ...structuredClone(candidate.v1.models.value[0]), key: "extra" }))
  reject("variants128", (candidate) => candidate.v2.models.value[0].value.variants.value.push({ ...structuredClone(candidate.v2.models.value[0].value.variants.value[0]), value: { ...candidate.v2.models.value[0].value.variants.value[0].value, id: "extra" } }))
  reject("headers64-slots64", (candidate) => candidate.v2.headers.value.push({ ...structuredClone(candidate.v2.headers.value[0]), key: "X-Extra" }))
  reject("headers64-slots64", (candidate) => { candidate.v2.headers.value[1].key = candidate.v2.headers.value[0].key.toLowerCase() })
  reject("headers64-slots64", (candidate) => { candidate.v2.headers.value[0].value = "opencode.value.64" })
  reject("extensions256", (candidate) => candidate.v2.extensions.value.push({ ...structuredClone(candidate.v2.extensions.value[0]), key: "extra" }))
  reject("large-native", (candidate) => candidate.v2.body.value.push(...Array.from({ length: 12 }, (_, index) => ({ ...module.mapRow({ kind: "null" }), key: `extra${index}`.padEnd(240, "x") }))))
  reject("full-v1", (candidate) => { candidate.v1.options.value.baseUrl.value = "https://example.invalid/%0a" })
  reject("full-v1", (candidate) => { candidate.v1.models.value.push(structuredClone(candidate.v1.models.value[0])) })
  reject("full-v2", (candidate) => { candidate.v2.models.value[0].value.variants.value[1].value.id = candidate.v2.models.value[0].value.variants.value[0].value.id })
  const number = structuredClone(fields["full-v1"])
  for (const value of ["0", "0.5", "-1", "9007199254740991", "0.0000001"]) { number.v1.models.value[0].value.cost.value = { kind: "number", value }; assert(valid(number), `valid canonical decimal rejected: ${value}`) }
  for (const value of ["01", "-0", "1.0", "1e3", "9007199254740992", "0.10000000000000001", "NaN", "Infinity"]) { number.v1.models.value[0].value.cost.value = { kind: "number", value }; assert(!valid(number), `noncanonical decimal accepted: ${value}`) }
  const unicode = structuredClone(fields["full-v1"]); unicode.v1.name.value = "供应商🌍"; assert(valid(unicode))
  reject("full-v1", (candidate) => { candidate.v1.name.value = "\ud800" })
  const preserved = module.openCodeDraft(fields["full-v2"]).payload.value
  const slots = module.openCodeRequiredSlots(preserved).map((slotId) => ({ slotId, present: false }))
  const view = module.create(module.DraftViewSchema, { asset: { id: "synthetic-id", agentId: "opencode", kind: 1, schemaVersion: 1, name: "full-v2", revision: 1n }, draft: { name: "full-v2", payload: { case: "opencodeProvider", value: preserved } }, secrets: slots, credentialReadiness: module.CredentialReadiness.NEEDS_CREDENTIAL })
  const loaded = module.requireOpenCodeView(view); loaded.provider.providerId = "changed"; assert.notEqual(view.draft.payload.value.providerId, "changed", "read state aliases wire")
  for (const mutate of [(entry) => entry.secrets.pop(), (entry) => entry.secrets.push(entry.secrets[0]), (entry) => { entry.credentialReadiness = module.CredentialReadiness.COMPLETE }, (entry) => { entry.asset.agentId = "claude" }, (entry) => { entry.asset.revision = 0n }]) { const invalid = structuredClone(view); mutate(invalid); assert.throws(() => module.requireOpenCodeView(invalid)) }
  view.hasPreservedExtras = true; assert.equal(module.requireOpenCodeView(view).mode, "readonly")
  const unchosenCost = structuredClone(fields["full-v2"])
  unchosenCost.v2.models.value[0].value.cost = module.v2ModelFields().cost
  unchosenCost.v2.models.value[0].value.cost.enabled = true
  unchosenCost.v2.models.value[0].value.cost.value.single.input = { enabled: true, value: "0" }
  unchosenCost.v2.models.value[0].value.cost.value.single.output = { enabled: true, value: "0" }
  assert(!valid(unchosenCost), "cost shape silently defaulted without explicit choice")
  evidence.push({ name: "independent-go-vectors-boundaries-byte-parity-and-strict-view", pass: true })
}
function center(expression) { evaluate(`new Promise(done => requestAnimationFrame(() => requestAnimationFrame(() => { (${expression})?.scrollIntoView({block:'center'}); done(true) })))`) }
function click(name) {
  const found = evaluate(`(() => { document.querySelectorAll('[data-oc-click-target]').forEach(node=>node.removeAttribute('data-oc-click-target')); const dialogs=Array.from(document.querySelectorAll('[role=dialog]')); const scope=dialogs.at(-1) ?? document; const button=Array.from(scope.querySelectorAll('button')).find(node=>node.textContent === ${JSON.stringify(name)} || node.getAttribute('aria-label') === ${JSON.stringify(name)}); if(!button)return false; button.setAttribute('data-oc-click-target','true'); return true })()`)
  assert(found, `visible button missing: ${name}`)
  center("document.querySelector('[data-oc-click-target]')")
  browser(["click", "[data-oc-click-target]"])
}
function fill(label, value) { browser(["find", "label", label, "fill", value]) }
function field(path, value) { const selector = `div[data-field-path="${path}"] > input`; center(`document.querySelector(${JSON.stringify(selector)})`); if (value === "") { browser(["click", selector]); browser(["press", "Control+a"]); browser(["press", "Backspace"]) } else browser(["fill", selector, value]) }
function choose(path, value) {
  const selector = `div[data-field-path="${path}"] > button[role=combobox]`
  evaluate(`new Promise(done => requestAnimationFrame(() => requestAnimationFrame(() => { document.querySelector(${JSON.stringify(selector)})?.scrollIntoView({block:'center'}); done(true) })))`)
  browser(["click", selector]); browser(["find", "role", "option", "click", "--name", value, "--exact"])
}
function enable(path) { const label = `设置 ${path}`; center(`Array.from(document.querySelectorAll('label')).find(node => node.textContent === ${JSON.stringify(label)})`); browser(["find", "label", label, "click"]) }
function optionalText(path, value) { enable(path); field(path, value) }
function optionalChoice(path, value) { enable(path); choose(path, value) }
function optionalBoolean(path, value) { optionalChoice(path, String(value)) }
function addMap(path, index, key) { click(`新增 ${path} 行`); field(`${path}.${index}.key`, key) }
function stringList(path, values) { enable(path); values.forEach((value, index) => { click(`新增 ${path} 行`); field(`${path}.${index}`, value) }) }
function typedValue(path, value) {
  choose(`${path}.type`, value.kind)
  if (value.kind === "number") field(path, value.value)
  if (value.kind === "reference" || value.kind === "boolean") choose(path, String(value.value))
  if (value.kind === "array") value.value.forEach((child, index) => { click(`新增 ${path} 行`); typedValue(`${path}.${index}`, child) })
  if (value.kind === "object") value.value.forEach(([key, child], index) => { addMap(path, index, key); typedValue(`${path}.${index}`, child) })
}
function objectFields(path, entries) { enable(path); entries.forEach(([key, value], index) => { addMap(path, index, key); typedValue(`${path}.${index}`, value) }) }
function headerFields(path, entries) { enable(path); entries.forEach(([key, slot], index) => { addMap(path, index, key); choose(`${path}.${index}`, slot) }) }
const numeric = (value) => ({ kind: "number", value })
const boolean = (value) => ({ kind: "boolean", value })
const reference = (value) => ({ kind: "reference", value })
const nullValue = { kind: "null" }
function compaction(path, kind, entries) { enable(path); choose(`${path}.type`, kind); if (entries) objectFields(`${path}.extensions`, entries) }
function fillFullV1() {
  optionalText("v1.npm", "@ai-sdk/openai-compatible")
  optionalText("v1.name", "Provider One")
  enable("v1.options")
  optionalText("v1.options.baseUrl", "https://synthetic.invalid/custom")
  optionalChoice("v1.options.apiKey", "opencode.api-key")
  headerFields("v1.options.headers", [["X-Trace", "opencode.value.01"]])
  objectFields("v1.options.extensions", [["flag", nullValue]])
  enable("v1.models"); addMap("v1.models", 0, "alpha")
  const model = "v1.models.0"
  optionalText(`${model}.name`, "Alpha")
  enable(`${model}.limit`)
  optionalText(`${model}.limit.context`, "8192")
  optionalText(`${model}.limit.output`, "0")
  objectFields(`${model}.limit.extensions`, [["flag", boolean(false)]])
  objectFields(`${model}.options`, [["switch", boolean(false)]])
  enable(`${model}.modalities`)
  stringList(`${model}.modalities.input`, [])
  stringList(`${model}.modalities.output`, ["text"])
  objectFields(`${model}.modalities.extensions`, [["number", numeric("0")]])
  enable(`${model}.thinking`); typedValue(`${model}.thinking`, boolean(false))
  enable(`${model}.cost`); typedValue(`${model}.cost`, numeric("0"))
  enable(`${model}.variants`); addMap(`${model}.variants`, 0, "fast"); typedValue(`${model}.variants.0`, { kind: "object", value: [] })
  objectFields(`${model}.extensions`, [["proof", reference("opencode.value.02")]])
  objectFields("v1.extensions", [["object", { kind: "object", value: [["array", { kind: "array", value: [nullValue, boolean(false), numeric("0.5"), reference("opencode.value.03")] }]] }], ["__proto__", numeric("0")], ["value", boolean(true)]])
}
function fillFullV2() {
  optionalText("v2.package", "@ai-sdk/openai-compatible")
  optionalText("v2.name", "Provider Two")
  optionalText("v2.canonical", "vendor")
  stringList("v2.env", ["API_TOKEN", "REGION"])
  enable("v2.settings")
  enable("v2.settings.timeout"); choose("v2.settings.timeout.type", "disabled")
  optionalText("v2.settings.chunkTimeout", "0")
  compaction("v2.settings.compaction", "native", [["flag", boolean(false)]])
  optionalChoice("v2.settings.transport", "websocket")
  objectFields("v2.settings.extensions", [])
  headerFields("v2.headers", [["X-API", "opencode.api-key"]])
  objectFields("v2.body", [["apiKey", reference("opencode.value.01")]])
  enable("v2.models"); addMap("v2.models", 0, "alpha")
  const model = "v2.models.0"
  optionalText(`${model}.modelId`, "remote")
  optionalText(`${model}.family`, "family")
  optionalText(`${model}.name`, "Alpha")
  optionalText(`${model}.package`, "@ai-sdk/openai-compatible")
  optionalBoolean(`${model}.disabled`, false)
  enable(`${model}.settings`); compaction(`${model}.settings.compaction`, "summary", []); objectFields(`${model}.settings.extensions`, [])
  headerFields(`${model}.headers`, [])
  objectFields(`${model}.body`, [])
  enable(`${model}.compatibility`)
  enable(`${model}.compatibility.reasoningField`); typedValue(`${model}.compatibility.reasoningField`, reference("opencode.value.02"))
  for (const key of ["requireReasoning", "requireFinishReason", "requireAssistantAfterTool", "supportsPromptCacheKey"]) optionalBoolean(`${model}.compatibility.${key}`, false)
  optionalChoice(`${model}.compatibility.maxTokensField`, "max_tokens")
  objectFields(`${model}.compatibility.extensions`, [["flag", nullValue]])
  enable(`${model}.capabilities`)
  optionalBoolean(`${model}.capabilities.tools`, false)
  stringList(`${model}.capabilities.input`, ["text", "image"])
  stringList(`${model}.capabilities.output`, [])
  objectFields(`${model}.capabilities.extensions`, [["number", numeric("0")]])
  enable(`${model}.limit`)
  for (const [key, value] of [["context", "0"], ["input", "4096"], ["output", "0"]]) optionalText(`${model}.limit.${key}`, value)
  objectFields(`${model}.limit.extensions`, [["flag", boolean(true)]])
  enable(`${model}.cost`); choose(`${model}.cost.shape`, "tiers")
  click(`新增 ${model}.cost.tiers 行`)
  const tier = `${model}.cost.tiers.0`
  optionalText(`${tier}.input`, "0"); optionalText(`${tier}.output`, "0.25")
  enable(`${tier}.cache`); optionalText(`${tier}.cache.read`, "0"); optionalText(`${tier}.cache.write`, "0"); objectFields(`${tier}.cache.extensions`, [["flag", nullValue]])
  enable(`${tier}.tier`); optionalText(`${tier}.tier.size`, "8192"); objectFields(`${tier}.tier.extensions`, [])
  objectFields(`${tier}.extensions`, [["flag", boolean(false)]])
  click(`新增 ${model}.cost.tiers 行`); optionalText(`${model}.cost.tiers.1.input`, "1"); optionalText(`${model}.cost.tiers.1.output`, "2")
  enable(`${model}.variants`); click(`新增 ${model}.variants 行`)
  const variant = `${model}.variants.0`
  field(`${variant}.id`, "fast"); enable(`${variant}.settings`); compaction(`${variant}.settings.compaction`, "native"); objectFields(`${variant}.settings.extensions`, [])
  headerFields(`${variant}.headers`, [["X-Mode", "opencode.value.03"]])
  objectFields(`${variant}.body`, [["flag", boolean(false)]]); objectFields(`${variant}.extensions`, [])
  click(`新增 ${model}.variants 行`); field(`${model}.variants.1.id`, "slow")
  objectFields(`${model}.extensions`, [["named", reference("opencode.value.04")]])
  objectFields("v2.extensions", [["nullable", nullValue]])
}
async function createFull(format, index) {
  click("新建配置资产"); browser(["find", "label", "创建目标", "click"])
  browser(["find", "role", "option", "click", "--name", "OpenCode provider 资产", "--exact"])
  choose("format", format); field("name", `Web full ${format}`); field("providerId", "web-full")
  if (format === "v1") fillFullV1(); else fillFullV2()
  click("保存配置资产")
  await until(async () => (await state()).assets.length === index, "full typed tree actual SQLite commit")
  await until(() => evaluate("document.querySelector('[role=dialog]') === null"), "full typed acknowledgment")
  const snapshot = await state()
  const saved = snapshot.assets.find((asset) => asset.name === `Web full ${format}`)
  assert.equal(saved?.treeEqual, true, "all user-entered typed fields must match independent Go vector")
  evidence.push({ name: `full-${format}-dom-to-real-db-exact`, pass: true })
}
function rowAction(name, action) {
  const found = evaluate(`(() => { document.querySelectorAll('[data-oc-review-action]').forEach(node => node.removeAttribute('data-oc-review-action')); const item=Array.from(document.querySelectorAll('li')).find(node => node.querySelector('h3')?.textContent === ${JSON.stringify(name)}); const button=Array.from(item?.querySelectorAll('button') ?? []).find(node => node.textContent === ${JSON.stringify(action)}); if (!button) return false; button.setAttribute('data-oc-review-action', 'true'); button.scrollIntoView({block:'center'}); return true })()`)
  assert.equal(found, true, `missing real metadata action: ${action}`)
  browser(["click", "[data-oc-review-action]"])
}
async function edit(name) { rowAction(name, "编辑配置"); await until(() => evaluate("!!document.querySelector('div[data-field-path=providerId] > input')"), "real typed draft read") }
async function closeEditor() {
  click("关闭对话框")
  if (evaluate("Array.from(document.querySelectorAll('button')).some(node => node.textContent === '放弃更改')")) click("放弃更改")
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "editor closed normally")
}
function secretInput(slot, large = false) {
  choose(`secrets.${slot}`, "replace")
  const filled = evaluate(`(() => { const group = document.querySelector('div[data-field-path="secrets.${slot}"]')?.closest('fieldset'); const input = group?.querySelector('input[type=password]'); if(!input) return false; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${large ? "'s'.repeat(8192)" : JSON.stringify(ready.replacement)}); input.dispatchEvent(new Event('input',{bubbles:true})); return true })()`)
  assert.equal(filled, true, "private replacement input missing")
}
async function saveRevision(id, revision, button = "保存配置资产") {
  await until(() => evaluate("document.querySelectorAll('[role=dialog]').length === 1"), "all nested confirmations closed before explicit save")
  click(button)
  await until(async () => (await state()).assets.find((asset) => asset.id === id)?.revision === revision, "real CAS revision")
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "validated write acknowledgment")
  const saved = (await state()).assets.find((asset) => asset.id === id)
  await until(() => evaluate(`Array.from(document.querySelectorAll('li')).some(node => node.querySelector('h3')?.textContent === ${JSON.stringify(saved.name)} && Array.from(node.querySelectorAll('dd')).some(value => value.textContent === ${JSON.stringify(`1 / ${revision}`)}))`), "metadata invalidation after real write")
}
async function privacyCases() {
  let snapshot = await state()
  let source = snapshot.assets.find((asset) => asset.name === "Web full v1")
  assert(source)
  await edit(source.name)
  secretInput("opencode.api-key")
  cacheProof("replacement-not-cached")
  assert.equal(evaluate(`!document.body.textContent.includes(${JSON.stringify(ready.replacement)}) && !location.href.includes(${JSON.stringify(ready.replacement)}) && !Object.values(localStorage).some(value => value.includes(${JSON.stringify(ready.replacement)})) && !Object.values(sessionStorage).some(value => value.includes(${JSON.stringify(ready.replacement)}))`), true, "new secret escaped its password/ref boundary")
  await saveRevision(source.id, 2)
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  assert.equal(source.expectedSecretEqual, true)
  assert.equal(snapshot.plaintextAbsent, true)
  await edit(source.name); field("name", "Web full v1 kept"); await saveRevision(source.id, 3)
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  assert.equal(source.expectedSecretEqual, true, "KEEP changed the actual stored secret")
  rowAction(source.name, "复制（不带凭据）")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('button')).find(node => node.textContent === '复制为无凭据资产' && !node.disabled)"), "source draft guarded copy readiness")
  fill("新副本名称", "Copied full v1")
  click("复制为无凭据资产")
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "real copy acknowledgment")
  snapshot = await state()
  const copied = snapshot.assets.find((asset) => asset.name === "Copied full v1")
  assert(copied && copied.id !== source.id && copied.revision === 1 && copied.treeEqual)
  assert(copied.slots.every((slot) => !slot.present) && !copied.expectedSecretEqual, "COPY migrated an actual secret")
  await edit(source.name)
  const before = (await state()).counters.commits
  choose("secrets.opencode.api-key", "clear"); click("继续编辑")
  assert.equal((await state()).counters.commits, before, "CLEAR cancellation wrote data")
  choose("secrets.opencode.api-key", "clear"); click("确认 CLEAR")
  await saveRevision(source.id, 4)
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  assert(source.slots.every((slot) => !slot.present) && !source.expectedSecretEqual)
  await edit(source.name); secretInput("opencode.api-key"); await saveRevision(source.id, 5)
  await edit(source.name); enable("v1.options"); click("继续编辑")
  assert.equal(evaluate("document.querySelector('fieldset[data-field-path=\"v1.options\"] button[role=checkbox]').getAttribute('aria-checked')"), "true", "cancelled ref removal changed draft")
  enable("v1.options"); click("确认移除引用并 CLEAR"); await saveRevision(source.id, 6)
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  assert(!source.expectedSecretEqual && !source.slots.some((slot) => slot.slotId === "opencode.api-key"), "last-ref CLEAR did not remove orphan credential in same CAS")
  assert.equal(snapshot.plaintextAbsent, true)
  evidence.push({ name: "replace-keep-copy-clear-and-last-ref-real-db", pass: true })
  let second = snapshot.assets.find((asset) => asset.name === "Web full v2")
  await edit(second.name); field("name", "Web full v2 localized"); secretInput("opencode.api-key")
  browser(["find", "label", "界面语言", "click"]); browser(["find", "role", "option", "click", "--name", "English", "--exact"])
  await until(() => evaluate("document.documentElement.lang === 'en'"), "locale change")
  assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).every(input => input.value === '')"), true, "locale retained new secret")
  assert.equal(evaluate("document.querySelector('div[data-field-path=name] > input').value"), "Web full v2 localized", "locale lost non-secret draft")
  browser(["find", "label", "Interface language", "click"]); browser(["find", "role", "option", "click", "--name", "简体中文", "--exact"])
  await saveRevision(second.id, second.revision + 1)
  second = (await state()).assets.find((asset) => asset.id === second.id)
  assert(second.slots.every((slot) => !slot.present) && !second.expectedSecretEqual, "locale cleared UI but reused stale replacement")
  evidence.push({ name: "locale-clears-new-secret-keeps-nonsecret-real-save", pass: true })
  cacheProof("locale-and-write-response-not-cached")
  await edit(copied.name); secretInput("opencode.api-key"); await saveRevision(copied.id, 2)
  await edit(copied.name); choose("v1.options.headers.0", "opencode.api-key"); enable("v1.options.apiKey")
  assert.equal(evaluate("document.querySelectorAll('[role=dialog]').length"), 1, "one shared-reference removal demanded destructive CLEAR")
  await saveRevision(copied.id, 3)
  const shared = (await state()).assets.find((asset) => asset.id === copied.id)
  assert(shared.expectedSecretEqual && shared.slots.some((slot) => slot.slotId === "opencode.api-key" && slot.present), "removing one of shared refs cleared stored secret")
  await edit(copied.name); choose("secrets.opencode.api-key", "clear"); click("确认 CLEAR")
  browser(["find", "label", "界面语言", "click"]); browser(["find", "role", "option", "click", "--name", "English", "--exact"])
  await until(() => evaluate("document.documentElement.lang === 'en'"), "CLEAR confirmation language reset")
  const clearBefore = (await state()).counters.commits
  click("Save configuration asset")
  await until(() => evaluate("document.querySelector('[role=alert]') !== null"), "unconfirmed CLEAR rejected locally")
  assert.equal((await state()).counters.commits, clearBefore, "language retained stale CLEAR approval")
  browser(["find", "label", "Interface language", "click"]); browser(["find", "role", "option", "click", "--name", "简体中文", "--exact"])
  click("确认 CLEAR")
  await until(() => evaluate("document.querySelectorAll('[role=dialog]').length === 2"), "explicit CLEAR reconfirmation dialog")
  click("确认 CLEAR"); await saveRevision(copied.id, 4)
  assert(!(await state()).assets.find((asset) => asset.id === copied.id).expectedSecretEqual)
  evidence.push({ name: "shared-ref-preservation-and-locale-requires-clear-reconfirmation", pass: true })
}
async function lifecycleCases() {
  let snapshot = await state()
  let source = snapshot.assets.find((asset) => asset.format === "v2" && asset.providerID === "web-full")
  await edit(source.name); field("name", "Unknown committed name")
  const before = snapshot.counters.commits
  await control("drop-after-commit"); click("保存配置资产")
  await until(async () => (await state()).counters.commits === before + 1, "actual durable commit before lost receipt")
  await until(() => evaluate("document.body.textContent.includes('配置写入结果待确认')"), "unknown outcome UI")
  assert.equal(evaluate("document.querySelector('form button[type=submit]')?.disabled"), true)
  assert.equal((await state()).counters.commits, before + 1, "unknown write was replayed")
  await closeEditor(); click("刷新配置元数据")
  await until(() => evaluate("Array.from(document.querySelectorAll('h3')).some(node => node.textContent === 'Unknown committed name')"), "explicit metadata refresh")
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  await edit(source.name); field("name", "Rejected stale name"); secretInput("opencode.api-key")
  await control("external-update", source.id)
  const external = (await state()).counters.commits
  click("保存配置资产")
  await until(() => evaluate("document.body.textContent.includes('配置版本已改变')"), "CAS conflict UI")
  assert.equal((await state()).counters.commits, external, "stale CAS wrote through")
  assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).every(input => input.value === '')"), true, "conflict retained replacement")
  assert.equal(evaluate("document.querySelector('div[data-field-path=name] > input').value"), "Rejected stale name")
  await closeEditor(); click("刷新配置元数据")
  snapshot = await state(); source = snapshot.assets.find((asset) => asset.id === source.id)
  await until(() => evaluate(`Array.from(document.querySelectorAll('h3')).some(node => node.textContent === ${JSON.stringify(source.name)})`), "new metadata after conflict")
  await edit(source.name); field("name", "Held before cancelled write"); secretInput("opencode.api-key")
  const heldBefore = (await state()).counters.commits
  await control("hold-before"); click("保存配置资产")
  await until(async () => (await state()).counters.held, "real before-store barrier")
  browser(["find", "label", "界面语言", "click"]); browser(["find", "role", "option", "click", "--name", "English", "--exact"])
  await until(() => evaluate("document.body.textContent.includes('write outcome is unknown')"), "cancelled writer conservatively blocked")
  await control("release")
  assert.equal((await state()).counters.commits, heldBefore, "cancel-before barrier committed")
  assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).every(input => input.value === '')"), true)
  browser(["find", "label", "Interface language", "click"]); browser(["find", "role", "option", "click", "--name", "简体中文", "--exact"])
  await closeEditor()
  evidence.push({ name: "real-commit-unknown-cas-cancel-and-no-replay", pass: true })
}
async function readonlyCases() {
  let snapshot = await state()
  const source = snapshot.assets.find((asset) => asset.format === "v2" && asset.providerID === "web-full")
  await control("seed-preserved-extras", source.id)
  click("刷新配置元数据")
  await until(async () => (await state()).assets.find((asset) => asset.id === source.id)?.extras === true, "actual preserved envelope")
  await until(() => evaluate(`Array.from(document.querySelectorAll('li')).some(node => node.querySelector('h3')?.textContent === ${JSON.stringify(source.name)} && Array.from(node.querySelectorAll('dd')).some(value => value.textContent === ${JSON.stringify(`1 / ${source.revision + 1}`)}))`), "fresh readonly metadata revision")
  await edit(source.name)
  assert.equal(evaluate("document.querySelector('form button[type=submit]')?.disabled"), true, "preserved extras editor was writable")
  await closeEditor()
  rowAction(source.name, "复制（不带凭据）")
  await until(() => evaluate("document.body.textContent.includes('此资产保留未知字段')"), "copy readonly guard")
  assert.equal(evaluate("document.querySelector('form button[type=submit]')?.disabled"), true)
  await closeEditor()
  rowAction(source.name, "删除配置资产")
  browser(["find", "label", "确认删除此配置资产及其保存的凭据。", "click"])
  click("确认删除配置资产")
  await until(async () => !(await state()).assets.some((asset) => asset.id === source.id), "metadata CAS delete of readonly asset")
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "readonly deletion acknowledgment")
  evidence.push({ name: "preserved-extras-readonly-copy-reject-cas-delete", pass: true })
}
async function copyIntegrityCase() {
  const snapshot = await state()
  const source = snapshot.assets.find((asset) => asset.slots.length > 0 && !asset.extras)
  assert(source, "copy integrity needs actual referenced asset")
  await until(() => evaluate(`Array.from(document.querySelectorAll('li')).some(node => node.querySelector('h3')?.textContent === ${JSON.stringify(source.name)} && Array.from(node.querySelectorAll('dd')).some(value => value.textContent === ${JSON.stringify(`1 / ${source.revision}`)}))`), "copy integrity fresh source metadata")
  rowAction(source.name, "复制（不带凭据）")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('button')).find(node => node.textContent === '复制为无凭据资产' && !node.disabled)"), "guarded copy source loaded")
  fill("新副本名称", "Corrupt reply real copy")
  await control("corrupt-copy")
  click("复制为无凭据资产")
  await until(() => evaluate("document.body.textContent.includes('配置写入结果待确认') || document.body.textContent.includes('已复制非秘密配置，未选择或应用。')"), "corrupt copy terminal UI")
  const copied = (await state()).assets.find((asset) => asset.name === "Corrupt reply real copy")
  assert.equal((await state()).corruptReplies, snapshot.corruptReplies + 1, "fault must deliver HTTP200 structurally valid all-present response, not a transport failure")
  assert(copied && copied.id !== source.id && copied.slots.length > 0 && copied.slots.every((slot) => !slot.present), "fault must not fake actual DB credential copying")
  assert.equal(evaluate("document.body.textContent.includes('配置写入结果待确认')"), true, "structurally valid all-present Copy response accepted as success")
  await closeEditor()
  evidence.push({ name: "copy-structural-valid-present-reply-rejected-real-db-empty", pass: true })
}
async function removedPendingCase() {
  const source = (await state()).assets.find((asset) => asset.name === "Web minimal v1")
  await edit(source.name)
  enable("v1.options"); optionalChoice("v1.options.apiKey", "opencode.api-key")
  secretInput("opencode.api-key")
  enable("v1.options.apiKey"); enable("v1.options.apiKey")
  await saveRevision(source.id, 2)
  const saved = (await state()).assets.find((asset) => asset.id === source.id)
  assert(!saved.expectedSecretEqual && saved.slots.length === 1 && !saved.slots[0].present, "removed pending reference retained and published stale replacement")
  evidence.push({ name: "removed-new-reference-erases-replacement-before-readd", pass: true })
}
async function legacyCases() {
  for (const [agent, target] of [["claude", "Claude"], ["codex", "Codex API provider"], ["gemini", "Gemini provider 资产"]]) {
    const name = `Legacy ${agent}`
    click("新建配置资产"); browser(["find", "label", "创建目标", "click"])
    browser(["find", "role", "option", "click", "--name", target, "--exact"])
    fill("配置资产名称", name)
    if (agent === "codex") { fill("Codex HTTPS 端点", "https://synthetic.invalid/codex"); fill("Codex 主模型 ID", "synthetic-model") }
    click("仅保存配置资产")
    await until(() => evaluate("!document.querySelector('[role=dialog]')"), `legacy ${agent} create`)
    let asset = (await state()).assets.find((item) => item.name === name)
    assert(asset && asset.agent === agent && asset.revision === 1)
    await until(() => evaluate(`Array.from(document.querySelectorAll('h3')).some(node => node.textContent === ${JSON.stringify(name)})`), "legacy metadata invalidation")
    rowAction(name, "编辑配置")
    await until(() => evaluate("Array.from(document.querySelectorAll('label')).some(node => node.textContent === '配置资产名称')"), `legacy ${agent} typed read`)
    fill("配置资产名称", `${name} edited`); await saveRevision(asset.id, 2, "仅保存配置资产")
    asset = (await state()).assets.find((item) => item.id === asset.id)
    rowAction(asset.name, "复制（不带凭据）")
    await until(() => evaluate("!!Array.from(document.querySelectorAll('button')).find(node => node.textContent === '复制为无凭据资产' && !node.disabled)"), "legacy copy readiness")
    fill("新副本名称", `${name} copy`); click("复制为无凭据资产")
    await until(() => evaluate("!document.querySelector('[role=dialog]')"), `legacy ${agent} copy`)
    const copied = (await state()).assets.find((item) => item.name === `${name} copy`)
    assert(copied && copied.agent === agent && copied.slots.every((slot) => !slot.present))
    await until(() => evaluate(`Array.from(document.querySelectorAll('h3')).some(node => node.textContent === ${JSON.stringify(copied.name)})`), "legacy copy metadata")
    rowAction(copied.name, "删除配置资产"); browser(["find", "label", "确认删除此配置资产及其保存的凭据。", "click"]); click("确认删除配置资产")
    await until(() => evaluate("!document.querySelector('[role=dialog]')"), `legacy ${agent} delete`)
    assert(!(await state()).assets.some((item) => item.id === copied.id))
    evidence.push({ name: `legacy-${agent}-real-ui-crud-copy-no-key`, pass: true })
  }
}
async function navigationAndOwnerCases() {
  const source = (await state()).assets.find((asset) => asset.format === "v1" && !asset.extras && asset.slots.length > 0)
  await edit(source.name); field("name", "Unsaved navigation draft"); secretInput("opencode.api-key")
  browser(["press", "Escape"])
  await until(() => evaluate("Array.from(document.querySelectorAll('button')).some(node => node.textContent === '继续编辑')"), "dirty escape confirmation")
  assert.equal(evaluate("document.activeElement.textContent"), "继续编辑", "discard cancellation not focused")
  click("继续编辑")
  assert.equal(evaluate("document.querySelector('div[data-field-path=name] > input').value"), "Unsaved navigation draft")
  assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).some(input => input.value.length > 0)"), true, "cancel discarded live replacement")
  browser(["back"])
  await until(() => evaluate("Array.from(document.querySelectorAll('button')).some(node => node.textContent === '放弃更改')"), "actual router dirty blocker")
  click("继续编辑")
  assert.equal(evaluate("location.pathname"), "/configuration")
  await closeEditor()
  assert.equal(evaluate("document.querySelectorAll('input[type=password]').length"), 0)
  await edit(source.name); field("name", "Late owner old draft"); secretInput("opencode.api-key")
  const before = (await state()).counters.commits
  await control("hold-before"); click("保存配置资产")
  await until(async () => (await state()).counters.held, "owner-switch write barrier")
  click("关闭对话框")
  if (evaluate("Array.from(document.querySelectorAll('button')).some(node => node.textContent === '放弃更改')")) click("放弃更改")
  click("退出")
  await until(() => evaluate("!!document.querySelector('#username')"), "actual logout session teardown")
  await control("release")
  fill("用户名", ready.otherUsername)
  evaluate(`(() => { const input = document.querySelector('#password'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, ${JSON.stringify(ready.password)}); input.dispatchEvent(new Event('input', {bubbles:true})); return true })()`)
  click("登录")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('a')).find(node => node.getAttribute('href') === '/configuration')"), "second real owner login")
  browser(["find", "role", "link", "click", "--name", "AI 配置", "--exact"])
  await until(() => evaluate("document.body.textContent.includes('还没有配置资产')"), "second owner private empty inventory")
  assert.equal(evaluate("document.querySelectorAll('input[type=password]').length"), 0)
  assert.equal(evaluate("document.body.textContent.includes('Late owner old draft') || document.body.textContent.includes('配置资产已保存，未应用到设备。')"), false, "late response contaminated new owner")
  assert.equal((await state()).counters.commits, before, "cancelled pre-store owner write committed")
  cacheProof("owner-switch")
  evidence.push({ name: "dirty-escape-router-cancel-and-owner-logout-late-write", pass: true })
}
async function displayCases() {
  browser(["set", "viewport", "375", "812"])
  const baselineLayout = evaluate("({page:document.documentElement.scrollWidth,viewport:innerWidth,sidebar:document.querySelector('.sidebar')?.scrollWidth})")
  writeFileSync(join(output, "host-shell-baseline-layout.json"), JSON.stringify(baselineLayout), { mode: 0o600 })
  await edit("Web full v2")
  for (const [width, height, theme] of [[1440, 900, "light"], [375, 812, "light"], [375, 812, "dark"]]) {
    browser(["set", "viewport", String(width), String(height)])
    browser(["set", "media", theme, "reduced-motion"])
    await until(() => evaluate(`document.documentElement.dataset.theme === ${JSON.stringify(theme)}`), "real theme preference propagation")
    center("document.querySelector('[role=dialog] h2')")
    const layout = evaluate("({page:document.documentElement.scrollWidth,viewport:innerWidth,dialogs:Array.from(document.querySelectorAll('[role=dialog]')).map(node=>{const box=node.getBoundingClientRect();return {left:box.left,right:box.right,scroll:node.scrollWidth,width:node.clientWidth}}),reduced:matchMedia('(prefers-reduced-motion: reduce)').matches})")
    writeFileSync(join(output, `layout-${width}-${theme}.json`), JSON.stringify(layout), { mode: 0o600 })
    assert(layout.page <= Math.max(layout.viewport, baselineLayout.page) + 1 && layout.dialogs.every((dialog) => dialog.left >= 0 && dialog.right <= layout.viewport + 1 && dialog.scroll <= dialog.width + 1), `new horizontal overflow at ${width}/${theme}`)
    assert(layout.reduced)
    assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).every(input=>!input.value)"), true)
    browser(["screenshot", join(output, `opencode-${width}-${theme}.png`)])
  }
  for (let count = 0; count < 12; count++) { browser(["press", "Tab"]); assert.equal(evaluate("document.querySelector('[role=dialog]').contains(document.activeElement)"), true, "dialog keyboard focus escaped") }
  field("providerId", ""); click("保存配置资产")
  await until(() => evaluate("document.activeElement.getAttribute('role') === 'alert'"), "rendered error summary focus")
  assert.equal(evaluate("document.activeElement.getAttribute('role')"), "alert", "error summary not focused")
  const errorLink = evaluate("(() => {const button=document.querySelector('[role=alert] button'); if(!button)return false;button.setAttribute('data-oc-error-link','true');return true})()")
  assert(errorLink); center("document.querySelector('[data-oc-error-link]')"); browser(["click", "[data-oc-error-link]"])
  assert.equal(evaluate("document.activeElement.closest('[data-field-path]')?.getAttribute('data-field-path')"), "providerId")
  field("providerId", "web-full")
  center("document.querySelector('details > summary')")
  browser(["click", "details > summary"])
  assert.equal(evaluate("document.querySelector('details').open"), false)
  browser(["click", "details > summary"])
  assert.equal(evaluate("document.querySelector('div[data-field-path=\"v2.models.0.modelId\"] input').value"), "remote", "collapse erased model fields")
  evaluate("document.documentElement.style.zoom='2'; true")
  assert.equal(evaluate("document.querySelector('[role=dialog]').scrollWidth <= document.querySelector('[role=dialog]').clientWidth + 1"), true, "CSS200% dialog layout overflow")
  evaluate("document.documentElement.style.zoom=''; true")
  await closeEditor()
  browser(["set", "viewport", "1440", "900"]); browser(["set", "media", "light"])
  evidence.push({ name: "desktop-mobile-light-dark-reduced-keyboard-error-summary-css200", pass: true })
}
async function boundaryCases() {
  await control("seed-boundaries"); click("刷新配置元数据")
  await until(() => evaluate("Array.from(document.querySelectorAll('h3')).some(node=>node.textContent === 'Boundary depth12')"), "real boundary metadata")
  await edit("Boundary depth12")
  const path = "v1.models.0.thinking" + ".0".repeat(11)
  choose(`${path}.type`, "array")
  assert.equal(evaluate(`Array.from(document.querySelectorAll('button')).find(node=>node.textContent === ${JSON.stringify(`新增 ${path} 行`)})?.disabled`), true, "native depth12 UI permits another child")
  choose(`${path}.type`, "null")
  const deep = (await state()).assets.find((asset) => asset.name === "Boundary depth12")
  field("name", "Boundary depth12 saved"); await saveRevision(deep.id, 2)
  await edit("Boundary models64")
  assert.equal(evaluate("Array.from(document.querySelectorAll('button')).find(node=>node.textContent === '新增 v1.models 行')?.disabled"), true, "models64 UI permits65th row")
  await closeEditor()
  await edit("Boundary wire96")
  for (let index = 1; index <= 7; index++) secretInput(`opencode.value.${String(index).padStart(2, "0")}`, true)
  const before = (await state()).counters.writes
  click("保存配置资产")
  await until(() => evaluate("document.querySelector('form[aria-busy=false]') !== null && document.querySelectorAll('input[type=password]').length > 0 && Array.from(document.querySelectorAll('input[type=password]')).every(input => input.value === '')"), "bounded wire rejection cleans all replacements")
  assert.equal((await state()).counters.writes, before, "over96KiB JSON request reached server")
  assert.equal(evaluate("document.querySelectorAll('[role=alert]').length > 0"), true, "over96KiB request lacked fixed error")
  await closeEditor()
  evidence.push({ name: "real-depth12-model64-and-wire96-pre-send-no-requests", pass: true })
}
async function singleCostCase() {
  const source = (await state()).assets.find((asset) => asset.name === "Web full v2")
  await edit(source.name)
  choose("v2.models.0.cost.shape", "single")
  optionalText("v2.models.0.cost.single.input", "0")
  optionalText("v2.models.0.cost.single.output", "0.25")
  await saveRevision(source.id, source.revision + 1)
  assert.equal((await state()).assets.find((asset) => asset.id === source.id).singleCostEqual, true, "single cost DOM fields did not match independent Go tree")
  await edit(source.name)
  assert.equal(evaluate("document.querySelector('div[data-field-path=\"v2.models.0.cost.single.output\"] input').value"), "0.25")
  await closeEditor()
  evidence.push({ name: "single-cost-dom-to-real-db-and-reload", pass: true })
}
async function createMinimal(format, index) {
  browser(["find", "role", "option", "click", "--name", "OpenCode provider 资产", "--exact"])
  choose("format", format)
  field("name", `Web minimal ${format}`)
  field("providerId", `web-minimal-${format}`)
  click("保存配置资产")
  await until(async () => (await state()).assets.length === index, "real OpenCode SQLite publication")
  await until(() => evaluate("document.querySelector('[role=dialog]') === null"), "successful editor closure")
  const snapshot = await state()
  const saved = snapshot.assets.find((asset) => asset.name === `Web minimal ${format}`)
  assert(saved && saved.agent === "opencode" && saved.format === format && saved.revision === 1)
  assert.equal(saved.slots.length, 0)
  evidence.push({ name: `real-minimal-${format}-create`, pass: true })
}

let failure
try {
  await until(() => existsSync(join(directory, "ready.json")), "real fixture readiness")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  if (["state", "all"].includes(values.suite)) await stateCases()
  assert(ready.url.startsWith("https://127.0.0.1:"))
  const baseline = await state()
  assert.equal(baseline.assets.length, 0)
  assert.equal(baseline.sideEffectsZero, true)
  if (values.suite !== "state") {
  browserStarted = true
  browser(["open", ready.url])
  evaluate("localStorage.setItem('skillhub.locale', 'zh-CN'); location.reload(); true")
  await until(() => evaluate("!!document.querySelector('#username') && Array.from(document.querySelectorAll('label')).some(node => node.textContent === '用户名')"), "real login form")
  fill("用户名", ready.username)
  evaluate(`(() => { const input = document.querySelector('#password'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, ${JSON.stringify(ready.password)}); input.dispatchEvent(new Event('input', {bubbles:true})); return true })()`)
  click("登录")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('a')).find(node => node.getAttribute('href') === '/configuration')"), "authenticated workspace")
  browser(["find", "role", "link", "click", "--name", "AI 配置", "--exact"])
  await until(() => evaluate("!!Array.from(document.querySelectorAll('button')).find(node => node.textContent === '新建配置资产')"), "real configuration page")
  evidence.push({ name: "real-auth-tls-store", pass: true })
  if (values.suite !== "preflight") {
    click("新建配置资产")
    browser(["find", "label", "创建目标", "click"])
    assert.equal(evaluate("!!Array.from(document.querySelectorAll('[role=option]')).find(node => node.textContent.includes('OpenCode'))"), true, "dedicated OpenCode creation target is missing")
    evidence.push({ name: "opencode-creation-target", pass: true })
    await createMinimal("v1", 1)
    click("新建配置资产")
    browser(["find", "label", "创建目标", "click"])
    await createMinimal("v2", 2)
    await createFull("v1", 3)
    await createFull("v2", 4)
    if (["forms", "all"].includes(values.suite)) await singleCostCase()
    if (["display", "all"].includes(values.suite)) await displayCases()
    if (["boundaries", "all"].includes(values.suite)) await boundaryCases()
    if (["privacy", "all"].includes(values.suite)) await privacyCases()
    if (["pending", "all"].includes(values.suite)) await removedPendingCase()
    if (["lifecycle", "all"].includes(values.suite)) await lifecycleCases()
    if (["regression", "all"].includes(values.suite)) await readonlyCases()
    if (["copy-integrity", "all"].includes(values.suite)) await copyIntegrityCase()
    if (["regression", "all"].includes(values.suite)) await legacyCases()
    if (values.suite === "all") await navigationAndOwnerCases()
  }
  }
} catch (error) {
  failure = error
  if (browserStarted) {
    try { writeFileSync(join(output, "safe-failure-state.json"), JSON.stringify({ state: await state(), ui: evaluate("({titles:Array.from(document.querySelectorAll('[role=dialog] h2')).map(node=>node.textContent),buttons:Array.from(document.querySelectorAll('[role=dialog] button')).map(node=>({text:node.textContent,disabled:node.disabled})),alerts:Array.from(document.querySelectorAll('[role=alert]')).map(node=>({unknown:node.textContent.includes('配置写入结果待确认'),conflict:node.textContent.includes('配置版本已改变'),invalid:node.textContent.includes('配置字段无效')}))})") }), { mode: 0o600 }) } catch {}
    try { writeFileSync(join(output, "layout-diagnostic.json"), JSON.stringify(evaluate("Array.from(document.querySelectorAll('[role=dialog], [data-field-path=format], [data-field-path=format] button')).map(node => { const box=node.getBoundingClientRect(); const hit=document.elementFromPoint(box.x+box.width/2,box.y+box.height/2); return {tag:node.tagName, role:node.getAttribute('role'), path:node.getAttribute('data-field-path'), x:box.x,y:box.y,width:box.width,height:box.height,cover:hit?.tagName,coverClass:hit?.className} })")), { mode: 0o600 }) } catch {}
  }
}
finally {
  if (browserStarted) { try { browser(["close"]) } catch (error) { failure ??= error } }
  if (ready) { try { await control("release"); await control("stop") } catch (error) { failure ??= error } }
  const joined = await Promise.race([terminal, delay(10000).then(() => null)])
  if (!joined) { fixture.kill("SIGKILL"); await terminal; failure ??= new Error("fixture failed to drain normally") }
  else if (joined.code !== 0 || joined.signal) failure ??= new Error("fixture terminal was not zero")
  closeSync(fixtureLog)
  if (existsSync(join(directory, "stopped.json"))) {
    const stopped = JSON.parse(readFileSync(join(directory, "stopped.json"), "utf8"))
    if (!stopped.closed || stopped.active !== 0 || !stopped.sideEffectsZero) failure ??= new Error("fixture cleanup receipt failed")
    evidence.push({ name: "real-close-drain", pass: stopped.closed && stopped.active === 0 && stopped.sideEffectsZero })
  } else failure ??= new Error("missing actual stopped receipt")
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ suite: values.suite, evidence, exit: failure ? 1 : 0 }, null, 2), { mode: 0o600 })
}
if (failure) { console.error(failure.message); process.exitCode = 1 }
else console.log(`suite=${values.suite} PASS`)
