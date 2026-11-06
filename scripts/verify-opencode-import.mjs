import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" } } })
for (const key of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[key], `missing ${key}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestOpenCodeSelectionBrowserFixture$", "-test.v", "-test.timeout=300s"], { cwd: resolve("internal/server/httpapi"), env: { ...process.env, COGNATE_OPENCODE_SELECTION_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `opencode-import-${process.pid}`
const modern = "section.source-card:has([data-import-selection-heading])"
const layouts = []
let ready, failure
let previewCount = 0
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} rejected`)
  return parsed.data
}
const evaluate = (source) => browser(["eval", "--stdin"], source).result
async function until(check, label) {
  for (let count = 0; count < 80; count++) { if (await check()) return; await delay(100) }
  throw new Error(label)
}
async function control(path, method = "GET") {
  const response = await fetch(ready.controlURL + path, { method, headers: { "X-Fixture-Capability": ready.capability }, signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200)
  if (method === "GET") return response.json()
}
function role(name) {
  assert(evaluate(`(() => { document.querySelectorAll('[data-opencode-action]').forEach(node => node.removeAttribute('data-opencode-action')); const root = document.querySelector('[role=dialog]') ?? document.querySelector(${JSON.stringify(modern)}); const button = [...root.querySelectorAll('button')].find(node => node.textContent.trim() === ${JSON.stringify(name)}); if (!button || button.disabled) return false; button.dataset.opencodeAction = 'true'; return true })()`), "action missing or disabled")
  browser(["focus", "[data-opencode-action]"])
  browser(["press", "Enter"])
}
async function preferences(theme, locale) {
  browser(["focus", ".preferences [role=combobox]:last-child"])
  browser(["press", "Enter"])
  browser(["press", "Home"])
  if (locale === "en") browser(["press", "ArrowDown"])
  browser(["press", "Enter"])
  browser(["focus", ".preferences [role=combobox]:first-child"])
  browser(["press", "Enter"])
  browser(["press", "Home"])
  browser(["press", "ArrowDown"])
  if (theme === "dark") browser(["press", "ArrowDown"])
  browser(["press", "Enter"])
  assert.equal(evaluate("document.documentElement.lang"), locale)
  assert.equal(evaluate("document.documentElement.dataset.theme"), theme)
}
function checkReview(selector) {
  const text = evaluate(`document.querySelector(${JSON.stringify(selector)})?.innerText ?? ''`)
  for (const field of ["OpenCode imported", "providerID", "@ai-sdk/openai-compatible", "https://opencode.example/v1", "Authorization", "opencode.value.01", "Model A", "9007199254740991", "4096", "text", "image"]) assert(text.includes(field), `review omitted ${field}`)
  assert(!text.includes("SYNTHETIC-PRIVATE-MARKER"))
}
function expandDetails() {
  const count = evaluate(`(() => { const root = document.querySelector(${JSON.stringify(modern)}); const entries = [...root.querySelectorAll('details:not([open]) > summary')]; entries.forEach((node, index) => node.dataset.opencodeDetails = String(index)); return entries.length })()`)
  for (let index = 0; index < count; index++) { browser(["focus", `[data-opencode-details="${index}"]`]); browser(["press", "Enter"]) }
}
async function uploadPreview(locale) {
  browser(["upload", `${modern} input[type=file]`, join(directory, "source.db")])
  await until(() => evaluate(`!document.querySelector(${JSON.stringify(modern + ' button[data-slot=button]')})?.disabled && document.querySelector(${JSON.stringify(modern)})?.querySelector('input[type=file]')?.value === ''`), "file selection did not settle")
  role(locale === "en" ? "Upload and preview" : "上传并预览")
  previewCount++
  await until(() => evaluate(`document.querySelector(${JSON.stringify(modern)})?.textContent.includes('OpenCode imported')`), "OpenCode preview missing")
  expandDetails()
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture not ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["set", "viewport", "1440", "1000"])
  browser(["open", ready.url + "/repositories"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login missing")
  browser(["fill", "#username", "npx-owner"])
  browser(["fill", "#password", ready.password])
  browser(["click", "form button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('.repository-import summary')"), "library missing")
  await preferences("light", "zh-CN")
  browser(["click", ".repository-import summary"])
  browser(["click", "[data-import-selection-source]"])
  browser(["find", "role", "option", "click", "--name", "cc-switch schema23/24 SQLite · 64 MiB", "--exact"])
  browser(["upload", `${modern} input[type=file]`, join(directory, "source.db")])
  await until(() => evaluate(`document.querySelector(${JSON.stringify(modern)})?.textContent.includes('已选择导出文件')`), "file selection did not settle")
  assert.equal((await control("/state"))["/skillhub.import.v1.ImportService/PreviewImportSelection"] ?? 0, 0)
  role("上传并预览")
  previewCount++
  await until(() => evaluate(`document.querySelector(${JSON.stringify(modern)})?.textContent.includes('OpenCode imported')`), "OpenCode preview missing")
  expandDetails()
  checkReview(modern)
  assert.equal(evaluate(`document.querySelectorAll(${JSON.stringify(modern + ' [role=checkbox][aria-checked=true]')}).length`), 0)
  for (const width of [375, 1440]) for (const theme of ["light", "dark"]) for (const locale of ["zh-CN", "en"]) {
    browser(["set", "viewport", String(width), "1000"])
    const previousLocale = evaluate("document.documentElement.lang")
    await preferences(theme, locale)
    if (previousLocale !== locale) await uploadPreview(locale)
    checkReview(modern)
    const layout = evaluate("({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,theme:document.documentElement.dataset.theme,locale:document.documentElement.lang})")
    assert(layout.scrollWidth <= layout.width + 1, "preview overflow")
    layouts.push(layout)
    browser(["screenshot", join(output, `preview-${width}-${theme}-${locale}.png`)])
  }
  browser(["set", "viewport", "1440", "1000"])
  const previousLocale = evaluate("document.documentElement.lang")
  await preferences("light", "zh-CN")
  if (previousLocale !== "zh-CN") await uploadPreview("zh-CN")
  const configurationID = evaluate(`([...document.querySelector(${JSON.stringify(modern)}).querySelectorAll('label')].find(node => node.textContent.includes('OpenCode imported'))).htmlFor`)
  browser(["focus", `[id=${JSON.stringify(configurationID)}]`])
  browser(["press", "Space"])
  const noticeIDs = evaluate(`[...document.querySelectorAll(${JSON.stringify(modern + ' fieldset [role=checkbox]')})].map(node => node.id)`)
  for (const id of noticeIDs) { browser(["focus", `[id=${JSON.stringify(id)}]`]); browser(["press", "Space"]) }
  role("检查并确认本次选择")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "confirmation missing")
  checkReview("[role=dialog]")
  browser(["press", "Escape"])
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "Escape did not dismiss")
  assert.equal((await control("/state"))["/skillhub.import.v1.ImportService/CommitImportSelection"] ?? 0, 0)
  role("检查并确认本次选择")
  role("确认导入所选三类项目")
  await until(() => evaluate(`document.querySelector(${JSON.stringify(modern)})?.textContent.includes('导入已完成')`), "commit did not complete")
  const counts = await control("/state")
  assert.equal(counts["/skillhub.import.v1.ImportService/PreviewImportSelection"], previewCount)
  assert.equal(counts["/skillhub.import.v1.ImportService/CommitImportSelection"], 1)
  assert.equal(counts["/skillhub.library.v1.LibraryService/CollectRepository"] ?? 0, 0)
  browser(["screenshot", join(output, "completed.png")])
} catch (error) {
  failure = error
  if (ready) { try { writeFileSync(join(output, "requests.json"), JSON.stringify(await control("/state")), { mode: 0o600 }) } catch {} }
  try {
    if (evaluate("[...document.querySelectorAll('input[type=password]')].every(node => !node.value)")) {
      browser(["screenshot", join(output, "failure.png")])
      const safe = evaluate("document.body.textContent")
      if (!safe.includes("SYNTHETIC-PRIVATE-MARKER")) writeFileSync(join(output, "failure.txt"), safe, { mode: 0o600 })
    }
  } catch {}
}
finally {
  try { browser(["close"]) } catch (error) { failure ??= error }
  if (ready) { try { await control("/stop", "POST") } catch (error) { failure ??= error } }
  else fixture.kill("SIGTERM")
  const status = await Promise.race([terminal, delay(10000).then(() => null)])
  if (!status) { fixture.kill("SIGKILL"); failure ??= new Error("fixture failed to terminate") }
  else if (status.code !== 0 || status.signal !== null) failure ??= new Error("fixture failed")
  writeFileSync(join(output, "receipt.json"), JSON.stringify({ passed: !failure, layouts, fixture: status, error: failure?.message }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
console.log("OpenCode browser preview, full review, Escape, single commit and theme/layout checks passed")
