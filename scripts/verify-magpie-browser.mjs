import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { createCipheriv, pbkdf2Sync } from "node:crypto"
import { existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"

const [fixtureBinary, browserBinary, chromeBinary, outputPath] = process.argv.slice(2)
assert(fixtureBinary && browserBinary && chromeBinary && outputPath)
const output = resolve(outputPath)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(fixtureBinary), ["-test.run=^TestNPXSelectionBrowserFixture$", "-test.v", "-test.timeout=300s"], { env: { ...process.env, HOME: home, COGNATE_NPX_SELECTION_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `magpie-fix-${process.pid}-${Date.now()}`
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure
function browser(args, input) {
  const result = spawnSync(resolve(browserBinary), ["--session", session, "--executable-path", resolve(chromeBinary), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]}${args[0] === "find" ? ` ${args[args.indexOf("--name") + 1]}` : ""} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { if (check()) return; await delay(100) }; assert.fail(message) }
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
const password = "synthetic-magpie-backup-password"
const salt = Buffer.alloc(16, 7), nonce = Buffer.alloc(12, 9)
const cipher = createCipheriv("aes-256-gcm", pbkdf2Sync(password, salt, 600000, 32, "sha256"), nonce)
cipher.setAAD(Buffer.from(`magpie-backup/1/pbkdf2-sha256/600000/${salt.toString("hex")}`))
const bundle = { version: 2, providers: [{ key: "SYNTHETIC-PRIVATE-KEY" }], library: { skills: Array.from({ length: 60 }, (_, index) => ({ name: `review-${String(index).padStart(2, "0")}`, source: { kind: "github", repo: "example/toolkit", path: `skills/review-${index}` }, agents: [], files: { "SKILL.md": "SYNTHETIC-FILE-CONTENT" } })) } }
const data = Buffer.concat([cipher.update(JSON.stringify(bundle)), cipher.final(), cipher.getAuthTag()])
const envelope = JSON.stringify({ format: "magpie-backup", version: 1, kdf: "pbkdf2-sha256", iterations: 600000, salt: salt.toString("base64"), nonce: nonce.toString("base64"), data: data.toString("base64") })
function selectFile() {
  evaluate(`(() => { const input = document.querySelector('input[type=file]'); const transfer = new DataTransfer(); transfer.items.add(new File([${JSON.stringify(envelope)}], 'magpie-2026-11-06.magpie-backup', {type:'application/json'})); input.files = transfer.files; input.dispatchEvent(new Event('change', {bubbles:true})); return true })()`)
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture not ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["open", ready.url + "/repositories"])
  evaluate("localStorage.setItem('skillhub.locale','en')")
  browser(["reload"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login absent")
  browser(["fill", "#username", "npx-owner"])
  browser(["fill", "#password", ready.password])
  click("Sign in")
  await until(() => evaluate("!!document.querySelector('input[type=file]')"), "import view absent")
  const point = evaluate("(() => { const node = document.querySelector('.repository-import > summary'); node.scrollIntoView({block:'center'}); const rect = node.getBoundingClientRect(); return {x:rect.x+rect.width/2,y:rect.y+rect.height/2} })()")
  browser(["mouse", "move", String(Math.round(point.x)), String(Math.round(point.y))])
  browser(["mouse", "down"])
  browser(["mouse", "up"])
  assert(evaluate("document.querySelector('.repository-import').open"), "pointer must open import disclosure")
  browser(["focus", ".repository-import > summary"])
  browser(["press", "Enter"])
  assert.equal(evaluate("document.querySelector('.repository-import').open"), false, "keyboard must close import disclosure")
  browser(["focus", ".repository-import > summary"])
  browser(["press", "Enter"])
  assert(evaluate("document.querySelector('.repository-import').open"), "import disclosure did not open")
  assert.equal(evaluate("document.querySelectorAll('[role=checkbox][aria-checked=true]').length"), 0, "no import category may be selected by default")
  evaluate(`window.__importBodies=[]; const originalFetch=window.fetch; window.fetch=async function(input,init){const request=new Request(input,init); if(request.url.endsWith('/PreviewImportSelection')) {let text=new TextDecoder().decode(await request.clone().arrayBuffer()); if(request.headers.get('Content-Type')?.includes('json')) text=atob(JSON.parse(text).sourceData ?? ''); window.__importBodies.push(text)} return originalFetch.call(this,input,init)}`)
  selectFile()
  assert(evaluate("document.querySelector('input[type=file]').accept.includes('.magpie-backup')"), "native Magpie extension must be selectable")
  assert(evaluate("document.body.innerText.includes('magpie-2026-11-06.magpie-backup')"), "selected filename must be visible locally")
  assert(evaluate("document.querySelector('.repository-import').open"), "file selection closed import disclosure")
  browser(["fill", "input[type=password]", "wrong-password"])
  assert(evaluate("document.querySelector('.repository-import').open"), "password input closed import disclosure")
  click("Upload and preview")
  await until(() => evaluate("!!document.querySelector('.source-card [role=alert]')"), "wrong password did not fail")
  assert.equal(evaluate("window.__importBodies.length"), 0, "wrong password transmitted a source")
  selectFile()
  browser(["fill", "input[type=password]", password])
  click("Upload and preview")
  await until(() => evaluate("document.body.innerText.includes('example/toolkit')"), "encrypted backup did not reach real server preview")
  assert(evaluate(`window.__importBodies.length===1 && window.__importBodies.every(body=>!body.includes(${JSON.stringify(password)})&&!body.includes('SYNTHETIC-')&&!body.includes('magpie-backup')&&!body.includes('magpie-2026-11-06'))`), "excluded backup data, filename or password transmitted")
  assert(evaluate("document.querySelector('input[type=password]').value === ''"), "export password retained in input")
  assert.equal(evaluate("document.querySelectorAll('[role=checkbox][aria-checked=true]').length"), 0, "preview must not preselect items or acknowledge exclusions")
  assert(evaluate("[...document.querySelectorAll('[role=region]')].some(node=>node.getAttribute('aria-label')==='Skill references candidates' && node.scrollHeight>node.clientHeight && node.clientHeight<=420)"), "large skill list must have bounded independent scrolling")
  assert(evaluate("document.querySelectorAll('button[id*=notice-group]').length>0 && document.querySelectorAll('button[id*=notice-group]').length<=5"), "repeated notices must be grouped by reason")
  browser(["fill", 'input[placeholder="Filter Skill references"]', "review-59"])
  await until(() => evaluate("document.querySelector('[aria-label=\"Skill references candidates\"]').innerText.includes('review-59') && !document.querySelector('[aria-label=\"Skill references candidates\"]').innerText.includes('review-00')"), "skill filter must distinguish candidates")
  assert(evaluate("document.querySelector('[aria-label=\"Skill references candidates\"]').closest('fieldset').innerText.includes('Showing 1 of 60 items')"), "filter must show matching count and stable total")
  evaluate("document.querySelector('input[placeholder=\"Filter Skill references\"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,isComposing:true}))")
  assert.equal(evaluate("document.querySelector('input[placeholder=\"Filter Skill references\"]').value"), "review-59", "IME composition must not be cleared")
  browser(["focus", 'input[placeholder="Filter Skill references"]'])
  browser(["press", "Escape"])
  await until(() => evaluate("document.querySelector('[aria-label=\"Skill references candidates\"]').querySelectorAll('[role=checkbox]').length===60"), "clearing the skill filter must restore all candidates")
  assert.equal(evaluate("document.activeElement?.getAttribute('placeholder')"), "Filter Skill references", "Escape clear must retain input focus")
  browser(["fill", 'input[placeholder="Filter Skill references"]', "no-matching-skills"])
  assert(evaluate("Array.from(document.querySelector('[aria-label=\"Skill references candidates\"]').closest('fieldset').querySelectorAll('button')).find(node=>node.textContent==='Select visible eligible items').disabled"), "empty results must not offer a bulk action")
  click("Clear Skill references search")
  await until(() => evaluate("document.querySelector('[aria-label=\"Skill references candidates\"]').querySelectorAll('[role=checkbox]').length===60"), "clear button must restore results")
  assert.equal(evaluate("document.activeElement?.getAttribute('placeholder')"), "Filter Skill references", "pointer clear must restore input focus")
  for (const width of [375, 1440]) {
    browser(["set", "viewport", String(width), "1000"])
    assert(evaluate("document.documentElement.scrollWidth<=innerWidth+1"), "import view overflow")
    browser(["screenshot", join(output, `magpie-preview-${width}.png`), "--full"])
  }
  evaluate("document.querySelector('[aria-label=\"Repositories candidates\"]').closest('fieldset').querySelector('button').setAttribute('data-test-bulk','repositories')")
  browser(["focus", '[data-test-bulk="repositories"]'])
  browser(["press", "Enter"])
  await until(() => evaluate("document.querySelector('[aria-label=\"Repositories candidates\"]').querySelectorAll('[role=checkbox][aria-checked=true]').length===1 && document.querySelector('[aria-label=\"Skill references candidates\"]').querySelectorAll('[role=checkbox][aria-checked=true]').length===0 && [...document.querySelectorAll('[role=region]')].filter(node=>node.getAttribute('aria-label')?.includes('configuration')).every(node=>node.querySelectorAll('[role=checkbox][aria-checked=true]').length===0)"), "repository-only bulk selection must remain independent")
  assert(evaluate("[...document.querySelectorAll('button')].find(node=>node.textContent==='Review and confirm selection').disabled"), "exclusions must still require explicit acknowledgement")
  const noticeIds = evaluate("[...document.querySelectorAll('button[id*=notice-group]')].map(node=>node.id)")
  for (const id of noticeIds) { browser(["focus", `[id=${JSON.stringify(id)}]`]); browser(["press", "Space"]) }
  await until(() => evaluate("![...document.querySelectorAll('button')].find(node=>node.textContent==='Review and confirm selection').disabled"), "all grouped notices must acknowledge their original IDs")
  browser(["focus", `[id=${JSON.stringify(noticeIds[0])}]`])
  browser(["press", "Space"])
  assert(evaluate("[...document.querySelectorAll('button')].find(node=>node.textContent==='Review and confirm selection').disabled"), "clearing a notice group must block confirmation")
  assert(evaluate(`(${JSON.stringify(noticeIds.slice(1))}).every(id=>document.getElementById(id).getAttribute('aria-checked')==='true')`), "clearing one notice group must preserve other acknowledgements")
  browser(["press", "Space"])
  click("Review and confirm selection")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "confirmation missing")
  click("Import the selected items")
  await until(() => evaluate("document.body.innerText.includes('Import completed (not installed or applied to a device)')"), "real repository import did not finish")
  assert(evaluate("document.body.innerText.includes('Collected and scanned')"), "receipt must explain repository outcome")
  assert(evaluate("!document.body.innerText.includes('magpie-2026-11-06.magpie-backup')"), "completed import must release local file metadata")
  const requests = await fetch(ready.controlURL + "/state", { headers: { "X-Fixture-Capability": ready.capability } }).then(response => response.json())
  assert.equal(requests['/skillhub.import.v1.ImportService/CommitImportSelection'], 1, "group acknowledgement must preserve one commit")
  console.log("PASS: native extension and local filename, 60-item bounded/filterable preview, grouped explicit acknowledgements, repository-only bulk selection and real single commit, visible receipt, wrong-password and secret boundaries")
} catch (error) {
  failure = error
  try {
    if (evaluate("[...document.querySelectorAll('input[type=password]')].every(node=>!node.value) && !document.body.innerText.includes('SYNTHETIC-')")) browser(["screenshot", join(output, "failure.png"), "--full"])
    const status = evaluate("({statuses:[...document.querySelectorAll('.source-card [role=status],.source-card [role=alert]')].map(node=>node.innerText),dialog:!!document.querySelector('[role=dialog]')})")
    writeFileSync(join(output, "failure-state.json"), JSON.stringify(status, null, 2), { mode: 0o600 })
    if (ready) {
      const response = await fetch(ready.controlURL + "/state", { headers: { "X-Fixture-Capability": ready.capability }, signal: AbortSignal.timeout(5000) })
      assert.equal(response.status, 200)
      writeFileSync(join(output, "requests.json"), JSON.stringify(await response.json()), { mode: 0o600 })
    }
  } catch {}
}
finally {
  try { browser(["close"]) } catch (error) { failure ??= error }
  if (ready) {
    try { const response = await fetch(ready.controlURL + "/stop", { method: "POST", headers: { "X-Fixture-Capability": ready.capability }, signal: AbortSignal.timeout(5000) }); assert.equal(response.status, 200) } catch (error) { failure ??= error }
  } else { fixture.kill("SIGTERM") }
  const result = await terminal
  if (result.code !== 0 || result.signal !== null) failure ??= new Error("fixture did not terminate successfully")
  writeFileSync(join(output, "report.json"), JSON.stringify({ passed: !failure, fixture: result, error: failure?.message }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
