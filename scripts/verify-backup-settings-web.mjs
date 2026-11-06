import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import https from "node:https"
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
async function click(name) {
  await until(() => evaluate(`(() => { const button=Array.from(document.querySelectorAll('button')).find(node=>node.textContent.trim()===${JSON.stringify(name)});if(!button||button.disabled)return false;button.scrollIntoView({block:'center',behavior:'instant'});const rect=button.getBoundingClientRect();const hit=document.elementFromPoint(rect.x+rect.width/2,rect.y+rect.height/2);return rect.width>0&&rect.height>0&&!!hit&&button.contains(hit) })()`), `button not ready: ${name}`)
  browser(["find", "role", "button", "click", "--name", name, "--exact"])
}
function controlSelector(label) {
  const selector = evaluate(`(() => { const label = Array.from(document.querySelectorAll('label')).find(node => node.textContent.trim().startsWith(${JSON.stringify(label)})); return label?.htmlFor ? '#' + CSS.escape(label.htmlFor) : '' })()`)
  assert(selector, "labeled control missing")
  return selector
}
function fill(label, value) {
  const selector = controlSelector(label)
  if (String(value) !== "") { browser(["fill", selector, String(value)]); return }
  browser(["focus", selector])
  browser(["press", "Control+A"])
  browser(["press", "Backspace"])
}
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
  await click("Save and apply")
  await until(() => revision() !== "" && revision() !== before, "settings save not confirmed")
  await until(() => evaluate("document.querySelector('[data-backup-settings-revision]')?.getAttribute('aria-busy')==='false'"), "settings still pending")
}
function rpc(service, method, body = {}, sessionToken) {
  const address = new URL(`/${service}/${method}`, ready.disabledURL)
  assert.equal(address.protocol, "https:", "fixture RPC must use HTTPS")
  assert(["127.0.0.1", "localhost"].includes(address.hostname), "fixture RPC must stay on loopback")
  return new Promise((done, reject) => {
    const request = https.request(address, { method: "POST", rejectUnauthorized: false, timeout: 10000, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...(sessionToken ? { Authorization: `Bearer ${sessionToken}` } : {}) } }, (response) => {
      let data = ""
      response.on("data", (chunk) => { data += chunk })
      response.on("error", reject)
      response.on("end", () => {
        try {
          assert.equal(response.statusCode, 200, `fixture ${method} failed`)
          done(JSON.parse(data))
        } catch { reject(new Error(`fixture ${method} did not return a successful JSON response`)) }
      })
    })
    request.on("error", reject)
    request.on("timeout", () => request.destroy(new Error("fixture RPC timeout")))
    request.end(JSON.stringify(body))
  })
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
  assert.equal(evaluate("Array.from(document.querySelectorAll('button')).find(node=>node.textContent.trim()==='Save and apply')?.disabled"), true, "unchanged settings must not create a new revision")
  evaluate("window.__backupSettingsTraffic={read:0,save:0,failRead:false};const fetchSettings=window.fetch.bind(window);window.fetch=(input,options)=>{const url=typeof input==='string'?input:input instanceof URL?input.href:input.url;if(url.endsWith('/GetBackupSettings')){window.__backupSettingsTraffic.read++;if(window.__backupSettingsTraffic.failRead){window.__backupSettingsTraffic.failRead=false;return Promise.resolve(new Response(JSON.stringify({code:'unavailable',message:'Synthetic read failure'}),{status:503,headers:{'Content-Type':'application/json'}}))}}if(url.endsWith('/UpdateBackupSettings'))window.__backupSettingsTraffic.save++;return fetchSettings(input,options)}")
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
  assert.deepEqual(evaluate("({read:window.__backupSettingsTraffic.read,save:window.__backupSettingsTraffic.save})"), { read: 0, save: 1 }, "accepted saves must update from the response without a second read")
  const savedRevision = revision()
  const destination = "form[data-backup-settings-revision] details > summary"
  browser(["click", destination])
  fill("Bucket", "unsaved-bucket")
  await until(() => evaluate("!!document.querySelector('[data-backup-settings-dirty=true]')"), "unsaved changes need a visible state")
  await click("Reload saved settings")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "reload must confirm before discarding edits")
  await click("Keep editing")
  await until(() => evaluate("document.querySelector('[role=dialog]')===null && document.activeElement?.textContent==='Reload saved settings'"), "cancel must restore focus to the reload button")
  assert.equal(evaluate(`document.querySelector(${JSON.stringify(controlSelector("Bucket"))}).value`), "unsaved-bucket")
  await click("Reload saved settings")
  await click("Discard and reload")
  await until(() => evaluate(`document.querySelector(${JSON.stringify(controlSelector("Bucket"))})?.value==='cognate-settings-browser'`), "same-revision reload must reset the draft")
  await until(() => evaluate("document.querySelector('form[data-backup-settings-revision]')?.getAttribute('aria-busy')==='false' && document.querySelector('[role=dialog]')===null"), "reload must finish and release its dialog before editing")
  assert.equal(revision(), savedRevision, "discarding a draft must not save it")
  assert.equal(evaluate("document.querySelector('[data-backup-settings-dirty=true]')===null"), true)
  evidence.push("unsaved-draft-confirm-cancel-and-same-revision-reset")
  fill("Bucket", "")
  await until(() => evaluate("document.querySelector('[data-backup-settings-dirty=true]')!==null"), "clearing a required field must create an unsaved change")
  assert.deepEqual(evaluate(`(() => {const input=document.querySelector(${JSON.stringify(controlSelector("Bucket"))});return {empty:input.value==='',required:input.required,valid:input.validity.valid}})()`), { empty: true, required: true, valid: false }, "clearing must invalidate the required bucket")
  browser(["click", destination])
  const invalidRevision = revision()
  await click("Save and apply")
  assert.equal(revision(), invalidRevision, "invalid fields must not save settings")
  assert.equal(evaluate("document.querySelector('form[data-backup-settings-revision] details').open"), true, "validation must reveal collapsed required fields")
  assert.equal(evaluate(`document.activeElement===document.querySelector(${JSON.stringify(controlSelector("Bucket"))})`), true)
  fill("Bucket", "cognate-settings-browser")
  evidence.push("collapsed-required-field-revealed-and-focused")
  fill("Bucket", "draft-kept-on-read-failure")
  evaluate("window.__backupSettingsTraffic.failRead=true")
  await click("Reload saved settings")
  await click("Discard and reload")
  await until(() => evaluate("document.querySelector('form[data-backup-settings-revision] fieldset')?.disabled===true && document.querySelector('[role=dialog]')===null"), "read failure must keep the form visible and lock uncertain settings")
  assert.equal(evaluate(`document.querySelector(${JSON.stringify(controlSelector("Bucket"))}).value`), "draft-kept-on-read-failure")
  const readsAfterFailure = evaluate("window.__backupSettingsTraffic.read")
  await click("Reload saved settings")
  await until(() => evaluate("document.querySelector('form[data-backup-settings-revision] fieldset')?.disabled===false"), "explicit reload must recover from a read failure")
  assert.equal(evaluate("window.__backupSettingsTraffic.read"), readsAfterFailure + 1, "read failure must not trigger automatic retries")
  assert.equal(evaluate(`document.querySelector(${JSON.stringify(controlSelector("Bucket"))}).value`), "cognate-settings-browser")
  evidence.push("failed-read-retains-draft-and-explicit-reload-recovers-without-retry")
  toggle("Back up automatically")
  fill("Backup interval (seconds)", "120")
  await save()
  assert.equal(evaluate("document.querySelector('[id$=interval]').value"), "120")
  evidence.push("automatic-schedule-hot-apply")
  fill("Backup interval (seconds)", "1.2e2")
  assert.equal(evaluate("Array.from(document.querySelectorAll('button')).find(node=>node.textContent.trim()==='Save and apply')?.disabled"), true, "equivalent numeric formatting is not a change")
  fill("Backup interval (seconds)", "1e2")
  await save()
  assert.equal(evaluate("document.querySelector('[id$=interval]').value"), "100")
  evidence.push("numeric-interval-exponent-no-throw-and-normalized-dirty-state")
  toggle("Enable server backups")
  await save()
  await until(() => evaluate("!document.querySelector('#backup-tasks-heading')"), "disable did not apply")
  evidence.push("disable-without-restart")
  toggle("Enable server backups")
  await save()
  await until(() => evaluate("!!document.querySelector('#backup-tasks-heading')"), "re-enable did not apply")
  evidence.push("re-enable-with-retained-credentials")
  {
    const revisionBeforeStaleReturn = revision()
    const readsBeforeStaleReturn = evaluate("window.__backupSettingsTraffic.read")
    await delay(31_000)
    browser(["click", 'nav a[href="/repositories"]'])
    await until(() => evaluate("location.pathname==='/repositories' && !document.querySelector('form[data-backup-settings-revision]')"), "stale-cache regression must leave the backup page through in-app navigation")
    browser(["click", 'nav a[href="/server-backup"]'])
    await until(() => revision() === revisionBeforeStaleReturn, "returning to stale cached settings must retain the accepted revision")
    assert.equal(evaluate("window.__backupSettingsTraffic.read"), readsBeforeStaleReturn, "returning to stale cached settings must not start a background GetBackupSettings")
    assert.equal(revision(), revisionBeforeStaleReturn, "stale cached return must not replace the settings revision")
    evidence.push("stale-settings-in-app-return-keeps-revision-without-background-read")
  }
  {
    const beforeExternalRevision = revision()
    browser(["click", destination])
    fill("Object prefix (optional)", "unsaved-before-external-revision")
    assert.equal(evaluate("document.querySelector('[data-backup-settings-dirty=true]')!==null"), true, "external update regression needs an unsaved draft")
    const { sessionToken } = await rpc("skillhub.auth.v1.AuthService", "Login", { username: ready.username, password: ready.password })
    assert(typeof sessionToken === "string" && sessionToken.length > 0, "second administrator session missing")
    const { settings } = await rpc("skillhub.management.v1.BackupManagementService", "GetBackupSettings", {}, sessionToken)
    assert.equal(String(settings.revision), beforeExternalRevision, "second administrator must read the current saved revision")
    const prefix = "settings-browser-second-session"
    const { settings: updated } = await rpc("skillhub.management.v1.BackupManagementService", "UpdateBackupSettings", { expectedRevision: settings.revision, settings: { ...settings, prefix }, credentialsAction: "keep" }, sessionToken)
    assert(BigInt(updated.revision) > BigInt(beforeExternalRevision), "second administrator must really advance the saved revision")
    assert.equal(revision(), beforeExternalRevision, "another administrator must not silently replace the local draft")
    assert.equal(evaluate(`document.querySelector(${JSON.stringify(controlSelector("Object prefix (optional)"))}).value`), "unsaved-before-external-revision")
    evaluate("window.__backupSettingsPreviousForm=document.querySelector('form[data-backup-settings-revision]'); true")
    const savesBeforeReload = evaluate("window.__backupSettingsTraffic.save")
    browser(["focus", "form[data-backup-settings-revision] .backup-actions > button:last-child"])
    browser(["press", "Enter"])
    await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "cross-revision reload must confirm before discarding the draft")
    assert.equal(evaluate("document.activeElement?.textContent"), "Keep editing", "cross-revision discard must start on the safe action")
    browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement?.textContent"), "Discard and reload", "keyboard must reach the discard action")
    browser(["press", "Enter"])
    await until(() => revision() === String(updated.revision) && evaluate("document.querySelector('[role=dialog]')===null && document.querySelector('form[data-backup-settings-revision]')?.getAttribute('aria-busy')==='false'"), "confirmed reload must show the second administrator's revision")
    assert.equal(evaluate("document.querySelector('form[data-backup-settings-revision]')!==window.__backupSettingsPreviousForm && !window.__backupSettingsPreviousForm.isConnected"), true, "a new revision must replace the old form")
    assert.equal(evaluate(`document.querySelector(${JSON.stringify(controlSelector("Object prefix (optional)"))}).value`), prefix, "confirmed reload must replace the draft with the other administrator's saved settings")
    assert.equal(evaluate("window.__backupSettingsTraffic.save"), savesBeforeReload, "discarding and reloading must not save the local draft")
    evidence.push("second-administrator-updated-revision-and-reload-replaced-form")
    await until(() => evaluate("(() => {const form=document.querySelector('form[data-backup-settings-revision]');const button=Array.from(form?.querySelectorAll('button')??[]).find(node=>node.textContent.trim()==='Reload saved settings');return !!button&&!button.disabled&&document.activeElement===button})()"), "cross-revision reload must restore focus to the new form's Reload saved settings button", 3000)
    evaluate("delete window.__backupSettingsPreviousForm")
    evidence.push("cross-revision-discard-reload-restores-new-form-focus")
  }
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
    browser(["set", "media", "reduced-motion"])
    evaluate("document.documentElement.style.fontSize='200%'")
    browser(["focus", "[id$=automatic][role=checkbox]"])
    browser(["press", "Space"])
    await click(locale === "en" ? "Reload saved settings" : "重新读取已保存设置")
    await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "discard dialog missing")
    assert.equal(evaluate("document.activeElement?.textContent"), locale === "en" ? "Keep editing" : "继续编辑", "discard must not be the default keyboard action")
    assert(evaluate("(() => {const dialog=document.querySelector('[role=dialog]');const rect=dialog.getBoundingClientRect();return rect.left>=0&&rect.right<=innerWidth+1&&getComputedStyle(dialog).animationName==='none'&&Array.from(dialog.querySelectorAll('button')).every(button=>{const box=button.getBoundingClientRect();return box.left>=rect.left&&box.right<=rect.right+1&&box.top>=0&&box.bottom<=innerHeight+1})})()"), "discard controls must remain visible at 200% with reduced motion")
    browser(["screenshot", join(output, `discard-${locale}-${theme}-${width}-200pct.png`)])
    await click(locale === "en" ? "Keep editing" : "继续编辑")
    await until(() => evaluate("document.querySelector('[role=dialog]')===null"), "discard dialog did not close")
    assert.equal(revision(), currentRevision, "canceling must never save settings")
    evidence.push(`discard-dialog-${locale}-${theme}-${width}-200pct-reduced-motion`)
    evaluate("document.documentElement.style.fontSize='100%'")
    browser(["set", "media", "light"])
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
