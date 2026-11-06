import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" } } })
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestBackupBrowserFixture$", "-test.v", "-test.timeout=360s"], { env: { ...process.env, COGNATE_BACKUP_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `backup-settings-${process.pid}`
const evidence = []
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure

function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1,localhost", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 8 << 20 })
  assert.equal(result.status, 0, `browser command failed: ${args[0]}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, JSON.stringify(parsed))
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message, timeout = 20000) { const deadline = Date.now() + timeout; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
function controlSelector(label) {
  const selector = evaluate(`(() => { const label = Array.from(document.querySelectorAll('label')).find(node => node.textContent.trim().startsWith(${JSON.stringify(label)})); return label?.htmlFor ? '#' + CSS.escape(label.htmlFor) : '' })()`)
  assert(selector, "labeled control missing")
  return selector
}
function fill(label, value) { browser(["fill", controlSelector(label), String(value)]) }
function toggle(label) { browser(["focus", controlSelector(label)]); browser(["press", "Space"]) }
async function login(url, ordinary = false) {
  browser(["open", `${url}/server-backup`])
  await until(() => evaluate("!!document.querySelector('#username')"), "login form missing")
  browser(["fill", "#username", ordinary ? ready.ordinaryUsername : ready.username])
  browser(["fill", "#password", ordinary ? ready.ordinaryPassword : ready.password])
  browser(["press", "Enter"])
  await until(() => evaluate("!!document.querySelector('.backup-page h1')"), "backup page missing")
}
function revision() { return evaluate("document.querySelector('[data-backup-settings-revision]')?.getAttribute('data-backup-settings-revision') || ''") }
async function save() {
  const before = revision()
  click("Save and apply")
  await until(() => revision() !== "" && revision() !== before, "settings save not confirmed")
  await until(() => evaluate("document.querySelector('[data-backup-settings-revision]')?.getAttribute('aria-busy')==='false'"), "settings still pending")
}
async function stop() {
  const response = await fetch(`${ready.controlURL}/stop`, { method: "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, body: "{}", signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 204)
}

try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture did not start")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["open", `${ready.disabledURL}/server-backup`])
  evaluate("localStorage.setItem('skillhub.locale','en'); localStorage.setItem('skillhub.theme','dark')")
  await login(ready.disabledURL)
  await until(() => revision() !== "", "settings form missing")
  assert.equal(evaluate("document.querySelector('[id$=enabled][role=checkbox]').getAttribute('data-state')"), "unchecked")
  toggle("Enable server backups")
  fill("S3 endpoint (HTTPS)", ready.s3Endpoint)
  fill("Region", "test-1")
  fill("Bucket", "cognate-settings-browser")
  fill("Object prefix (optional)", "settings-browser")
  fill("Recovery key ID (UUID)", "00000000-0000-4000-8000-000000000001")
  fill("Recovery key file on the server", ready.recoveryKeyFile)
  fill("Access key ID", "synthetic-backup-access")
  fill("Secret access key", "synthetic-backup-secret")
  await save()
  await until(() => evaluate("!!document.querySelector('#backup-tasks-heading')"), "initially-disabled service did not hot enable")
  assert.equal(evaluate("Array.from(document.querySelectorAll('input[type=password]')).some(input=>input.value!== '')"), false)
  evidence.push("initially-disabled-hot-enable-with-new-credentials")
  toggle("Back up automatically")
  fill("Backup interval (seconds)", "120")
  await save()
  assert.equal(evaluate("document.querySelector('[id$=interval]').value"), "120")
  evidence.push("automatic-schedule-hot-apply")
  toggle("Enable server backups")
  await save()
  await until(() => evaluate("!document.querySelector('#backup-tasks-heading')"), "disable did not apply")
  evidence.push("disable-without-restart")
  toggle("Enable server backups")
  await save()
  await until(() => evaluate("!!document.querySelector('#backup-tasks-heading')"), "re-enable did not apply")
  evidence.push("re-enable-with-retained-credentials")
  const currentRevision = revision()
  for (const locale of ["en", "zh-CN"]) for (const theme of ["dark", "light"]) for (const width of [375, 1440]) {
    evaluate(`localStorage.setItem('skillhub.locale',${JSON.stringify(locale)});localStorage.setItem('skillhub.theme',${JSON.stringify(theme)})`)
    browser(["set", "viewport", String(width), "960"])
    await login(ready.disabledURL)
    await until(() => revision() === currentRevision, "saved settings missing after page reload")
    const geometry = evaluate("({width:innerWidth,content:document.documentElement.scrollWidth,form:document.querySelector('[data-backup-settings-revision]').getBoundingClientRect().toJSON()})")
    assert(geometry.content <= geometry.width + 1, `page overflows at ${locale}/${theme}/${width}`)
    assert(geometry.form.left >= 0 && geometry.form.right <= geometry.width + 1)
    browser(["screenshot", join(output, `${locale}-${theme}-${width}.png`), "--full"])
    evidence.push(`layout-${locale}-${theme}-${width}`)
  }
  await login(ready.url, true)
  await until(() => evaluate("/Administrator access required|需要管理员权限/.test(document.querySelector('.backup-page')?.textContent || '')"), "ordinary account restriction missing")
  assert.equal(revision(), "", "ordinary account must not see the settings form")
  evidence.push("ordinary-account-no-settings")
  assert.equal(fixture.exitCode, null, "fixture unexpectedly restarted or stopped")
} catch (error) {
  failure = error
  try { browser(["screenshot", join(output, "failure.png"), "--full"]) } catch {}
} finally {
  try { browser(["close"]) } catch {}
  if (ready) { try { await stop() } catch {} }
  const result = await Promise.race([terminal, delay(10000).then(() => null)])
  if (!result) fixture.kill("SIGTERM")
  const finished = result ?? await terminal
  closeSync(log)
  if (!failure && finished.code !== 0) failure = new Error("fixture did not stop normally")
  writeFileSync(join(output, "receipt.json"), JSON.stringify({ exit: failure ? 1 : 0, evidence, failure: failure?.message, fixture: finished }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
console.log(`backup settings web: ${evidence.length} checks passed`)
