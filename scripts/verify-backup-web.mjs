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
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestBackupBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_BACKUP_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `backup-${process.pid}-${Date.now()}`
const evidence = []
const layouts = []
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure

function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed: ${result.stderr || result.stdout}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, JSON.stringify(parsed))
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message, timeout = 20000) { const deadline = Date.now() + timeout; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
async function control(path, body) {
  const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(path === "seed" ? 90000 : 5000) })
  assert.equal(response.status, body === undefined ? 200 : 204, `fixture ${path} failed`)
  return body === undefined ? response.json() : undefined
}
const mode = (value = "") => control("mode", { mode: value })
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
function capture(name, full = true) { browser(["screenshot", join(output, `${name}.png`), ...(full ? ["--full"] : [])]); evidence.push({ check: name }) }
const pageText = () => evaluate("document.querySelector('.backup-page')?.textContent ?? ''")
async function login(url = ready.url, ordinary = false) {
  browser(["open", `${url}/server-backup`])
  await until(() => evaluate("!!document.querySelector('#username')"), "real login form missing")
  browser(["fill", "#username", ordinary ? ready.ordinaryUsername : ready.username])
  browser(["fill", "#password", ordinary ? ready.ordinaryPassword : ready.password])
  browser(["press", "Enter"])
  await until(() => evaluate("!!document.querySelector('.backup-page h1')"), "authenticated backup page missing")
  await until(() => !pageText().includes("Reading backup availability") && !pageText().includes("正在读取备份可用状态"), "backup status did not settle")
}
function refresh() { browser(["click", ".backup-page .page-heading button"]) }
async function confirmStart(continuing = false) {
  click(continuing ? "Continue original request" : "Start backup")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "Radix confirmation missing")
  const label = continuing ? "Continue with the same ID" : "Confirm and start"
  assert.equal(evaluate(`Array.from(document.querySelectorAll('[role=dialog] button')).find(node=>node.textContent===${JSON.stringify(label)}).disabled`), true, "scope must not be acknowledged by default")
  browser(["click", "[role=dialog] [role=checkbox]"])
  click(label)
  await until(() => evaluate("!document.querySelector('[role=dialog]')"), "confirmation did not close")
  await until(() => evaluate("document.activeElement.id==='backup-tasks-heading'"), "confirmed action must return focus to stable task heading")
}
async function committed(count) {
  refresh()
  await until(async () => (await control("state")).completes >= count, "real S3 complete marker missing")
  refresh()
  await until(() => evaluate(`document.querySelectorAll('.backup-task-list [data-phase=remote_committed]').length >= ${Math.min(count, 20)}`), "real committed state not displayed")
}
function pendingID() { return evaluate("document.querySelector('.backup-pending code')?.textContent ?? ''") }
async function recoverLost(kind) {
  const before = await control("state")
  await mode(kind)
  await confirmStart()
  await until(() => pageText().includes("The server did not confirm the request"), "uncertain Start feedback missing")
  const original = pendingID()
  assert.match(original, /^[0-9a-f-]{36}$/)
  await committed(before.completes + 1)
  await login()
  await until(() => pendingID() === original, "reload did not preserve the same request identity")
  const savedKey = evaluate("Object.keys(sessionStorage).find(key=>key.startsWith('cognate.backup.intent.v1.'))")
  const beforeCompare = await control("state")
  click("Continue original request")
  browser(["click", "[role=dialog] [role=checkbox]"])
  evaluate(`sessionStorage.removeItem(${JSON.stringify(savedKey)})`)
  click("Continue with the same ID")
  await until(() => pageText().includes("cannot safely keep its request identity"), "missing original request must not silently create a new one")
  assert.deepEqual((await control("state")).startIDs, beforeCompare.startIDs)
  evaluate(`sessionStorage.setItem(${JSON.stringify(savedKey)},${JSON.stringify(original)})`)
  await login()
  await until(() => pendingID() === original, "synthetic request identity was not restored")
  await confirmStart(true)
  await until(() => pageText().includes("The request's job is confirmed"), "same-ID continuation not confirmed")
  const after = await control("state")
  assert.deepEqual(after.startIDs.slice(-2), [original, original])
  assert.equal(after.startJobs.at(-1), after.startJobs.at(-2), "same-ID continuation registered a second job")
  assert.equal(after.completes, before.completes + 1, "same-ID continuation repeated capture/publication")
  assert.equal(pendingID(), "")
  evidence.push({ check: `${kind}-reload-same-request-no-replay`, starts: 2, jobs: 1, publications: 1 })
}
function geometry(context) {
  const result = evaluate(`(() => {
    const root=document.querySelector('[role=dialog]') ?? document.querySelector('.backup-page');
    const nodes=Array.from(root.querySelectorAll('button,input,[role=checkbox],code,h1,h2,h3,p,dt,dd,label'));
    const elements=nodes.flatMap(node=>{const box=node.getBoundingClientRect();return box.width && (box.left < -1 || box.right > innerWidth+1 || node.scrollWidth > node.clientWidth+2) ? [{tag:node.tagName,role:node.getAttribute('role'),text:node.textContent.slice(0,80),left:box.left,right:box.right,scroll:node.scrollWidth,client:node.clientWidth}] : []});
    const text=[];const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
    while(walker.nextNode()){const node=walker.currentNode;if(!node.textContent.trim() || node.parentElement.closest('svg'))continue;const range=document.createRange();range.selectNodeContents(node);for(const box of range.getClientRects())if(box.width&&(box.left < -1 || box.right > innerWidth+1))text.push({text:node.textContent.slice(0,80),left:box.left,right:box.right})}
    return {width:innerWidth,scrollWidth:document.documentElement.scrollWidth,fontSize:getComputedStyle(document.documentElement).fontSize,elements,text,overflowX:getComputedStyle(root).overflowX};
  })()`)
  layouts.push({ ...context, ...result })
  assert.equal(result.scrollWidth <= result.width + 1, true, `horizontal overflow: ${JSON.stringify(context)}`)
  assert.deepEqual(result.elements, [], `clipped controls or content: ${JSON.stringify(context)}`)
  assert.deepEqual(result.text, [], `text outside viewport: ${JSON.stringify(context)}`)
  assert.equal(result.fontSize, context.scale === 2 ? "32px" : "16px")
  assert(!["hidden", "clip"].includes(result.overflowX), "do not mask overflow")
}
function focusedControl(context) {
  const result = evaluate(`(() => {const node=document.activeElement;const box=node.getBoundingClientRect();const dialog=node.closest('[role=dialog]').getBoundingClientRect();return {label:node.textContent,left:box.left,right:box.right,top:box.top,bottom:box.bottom,dialogTop:dialog.top,dialogBottom:dialog.bottom,height:innerHeight}})()`)
  layouts.push({ ...context, focus: result })
  assert(result.top >= Math.max(0, result.dialogTop) - 1 && result.bottom <= Math.min(result.height, result.dialogBottom) + 1, `focused action must be fully revealed by real dialog scrolling: ${JSON.stringify(context)}`)
  assert(result.left >= -1 && result.right <= context.width + 1)
  evidence.push({ check: "200pct-keyboard-action-revealed", ...context, label: result.label })
}

try {
  await until(() => existsSync(join(directory, "ready.json")), "fresh isolated fixture did not start")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  assert.equal((await fetch(`${ready.controlURL}/state`)).status, 401, "control endpoint must reject missing capability")
  browser(["open", `${ready.url}/server-backup`])
  evaluate("localStorage.setItem('skillhub.locale','en'); localStorage.setItem('skillhub.theme','dark')")
  browser(["set", "viewport", "1440", "960"])
  await login()
  await until(() => pageText().includes("No backup tasks yet"), "new real instance not empty")
  capture("empty-dark-en")
  browser(["focus", ".backup-section-heading button"])
  browser(["press", "Enter"])
  assert.equal(evaluate("!!document.activeElement.closest('[role=dialog]')"), true, "Radix must move keyboard focus into dialog")
  browser(["press", "Escape"])
  assert.equal(evaluate("document.activeElement.matches('.backup-section-heading button')"), true, "Radix must restore trigger focus")
  await confirmStart()
  await committed(1)
  capture("committed-dark-en")
  await recoverLost("lost-start")
  await recoverLost("malformed-start")

  const storageBefore = await control("state")
  evaluate("Storage.prototype.setItem = function(){throw new DOMException('synthetic storage denial','SecurityError')}")
  await confirmStart()
  await until(() => pageText().includes("cannot safely keep its request identity"), "storage denial must stop Start before network")
  assert.deepEqual((await control("state")).startIDs, storageBefore.startIDs)
  evidence.push({ check: "storage-write-denied-no-start-request" })
  await login()

  await mode("lost-start")
  await confirmStart()
  await until(() => pageText().includes("The server did not confirm the request"), "forget setup missing")
  const forgetBefore = await control("state")
  click("Forget browser record")
  click("Forget this browser record")
  await until(() => pageText().includes("Browser record removed"), "forget acknowledgement missing")
  assert.equal(evaluate("document.activeElement.id"), "backup-tasks-heading", "forgotten trigger must not leave focus on body")
  const forgetAfter = await control("state")
  assert.deepEqual(forgetAfter.startIDs, forgetBefore.startIDs)
  assert.equal(pendingID(), "")
  await committed(4)
  evidence.push({ check: "forget-only-browser-record-no-cancel-or-replay" })

  for (const [fault, text] of [["status-error", "Backup state could not be read"], ["malformed-status", "incomplete or unsupported backup state"], ["list-error", "Task history could not be read"], ["malformed-page", "incomplete or unsupported backup state"]]) {
    await mode(fault)
    refresh()
    await until(() => pageText().includes(text), `${fault} boundary missing`)
    assert.equal(evaluate("Array.from(document.querySelectorAll('.backup-page button')).some(node=>node.textContent==='Start backup'&&!node.disabled)"), false, `${fault} must disable new Start`)
    capture(fault)
    await mode()
    refresh()
    await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===4 && !document.querySelector('.backup-page [role=alert]')"), "valid state did not recover after explicit refresh")
  }
  await mode("malformed-job")
  browser(["click", ".backup-task-list>li:first-child button:first-child"])
  await until(() => evaluate("document.querySelector('[role=dialog]')?.textContent.includes('current task could not be confirmed')"), "malformed Get must not show committed state")
  browser(["press", "Escape"])
  await mode()

  const reconcileBefore = await control("state")
  browser(["click", ".backup-task-list>li:first-child button:last-child"])
  await until(() => pageText().includes("Exact read-only verification returned"), "read-only reconciliation feedback missing")
  const reconcileAfter = await control("state")
  assert.equal(reconcileAfter.puts, reconcileBefore.puts)
  assert(reconcileAfter.reads > reconcileBefore.reads)
  evidence.push({ check: "reconcile-real-exact-reads-no-put" })

  await mode("hold-upload")
  const cancelBefore = await control("state")
  await confirmStart()
  await until(async () => (await control("state")).uploadsHeld > cancelBefore.uploadsHeld, "real upload was not held")
  refresh()
  await until(() => pageText().includes("Request cancellation"), "active task cancellation missing")
  click("Request cancellation")
  browser(["click", "[role=dialog] .backup-actions button:last-child"])
  await until(() => pageText().includes("Cancellation never means"), "cancellation acknowledgement missing")
  await mode()
  refresh()
  await until(() => evaluate("!!document.querySelector('.backup-task-list [data-phase=canceled]')"), "canceled task not shown")
  assert.equal((await control("state")).completes, cancelBefore.completes, "canceled held upload published complete")
  evidence.push({ check: "held-real-upload-cancel-no-rollback-claim" })

  await mode("hold-start")
  const lateBefore = await control("state")
  await confirmStart()
  await until(async () => (await control("state")).rpcsHeld > lateBefore.rpcsHeld, "actual registered Start response not held")
  click("Sign out")
  await until(() => evaluate("!!document.querySelector('#username')"), "logout did not clear workspace")
  await login(ready.url, true)
  await until(() => pageText().includes("Administrator access required"), "ordinary account not denied")
  await mode()
  assert.equal(evaluate("document.querySelectorAll('.backup-task-list>li,.backup-pending').length"), 0, "old owner data appeared in ordinary workspace")
  capture("ordinary-admin-denied")
  assert((await control("state")).rpcsCanceled > lateBefore.rpcsCanceled, "owner change did not abort held Start response")
  evidence.push({ check: "owner-change-aborts-old-start-no-data-leak" })

  await login(ready.disabledURL)
  await until(() => pageText().includes("Backups are not enabled"), "real disabled App boundary missing")
  assert.equal(evaluate("document.querySelectorAll('.backup-task-list>li,.backup-section-heading button').length"), 0)
  capture("real-disabled-app")

  await login()
  await until(() => pendingID() !== "", "original administrator's uncertain request not preserved")
  await confirmStart(true)
  await until(() => pendingID() === "", "same-ID recovery after owner change not confirmed")
  await control("seed", {})
  refresh()
  await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===20"), "first real history page not bounded to 20")
  const firstIDs = evaluate("Array.from(document.querySelectorAll('[data-backup-job]'),node=>node.dataset.backupJob)")
  click("Older tasks")
  await until(() => pageText().includes("Page 2"), "second history page missing")
  await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===7"), "real second history page missing")
  const secondIDs = evaluate("Array.from(document.querySelectorAll('[data-backup-job]'),node=>node.dataset.backupJob)")
  assert.equal(secondIDs.some((id) => firstIDs.includes(id)), false)
  click("Newer tasks")
  await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===20"), "previous real history page missing")
  evidence.push({ check: "real-27-job-history-two-disjoint-pages" })
  for (const [fault, text] of [["list-error-page2", "Task history could not be read"], ["malformed-page2", "incomplete or unsupported backup state"], ["empty-page2", "No tasks on this page"]]) {
    const before = await control("state")
    await mode(fault)
    click("Older tasks")
    await until(() => pageText().includes(text), `${fault} negative boundary missing`)
    assert.equal(pageText().includes("No backup tasks yet"), false, "empty later page must not masquerade as a new instance")
    assert.equal(evaluate("Array.from(document.querySelectorAll('.backup-pagination button')).find(node=>node.textContent==='Newer tasks')?.disabled"), false, "safe newer-page navigation must survive a bad current page")
    capture(fault)
    click("Newer tasks")
    await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===20"), "bad subsequent page must allow return to real first-page history")
    assert.deepEqual(evaluate("Array.from(document.querySelectorAll('[data-backup-job]'),node=>node.dataset.backupJob)"), firstIDs)
    const after = await control("state")
    assert.equal(after.listCursors.at(-1), "")
    assert.deepEqual(after.startIDs, before.startIDs)
    await mode()
    evidence.push({ check: `${fault}-returns-newest-page-no-mutation` })
  }

  for (const theme of ["light", "dark"]) for (const locale of ["en", "zh-CN"]) for (const width of [375, 320]) {
    browser(["set", "viewport", String(width), "812"])
    evaluate(`localStorage.setItem('skillhub.locale',${JSON.stringify(locale)});localStorage.setItem('skillhub.theme',${JSON.stringify(theme)})`)
    await login()
    await until(() => evaluate("document.querySelectorAll('.backup-task-list>li').length===20"), "matrix history did not settle")
    evaluate("document.documentElement.style.fontSize='200%'")
    const context = { theme, locale, width, scale: 2 }
    geometry({ ...context, surface: "history" })
    capture(`${theme}-${locale}-${width}-200pct-history`, false)
    const startLabel = locale === "en" ? "Start backup" : "开始备份"
    click(startLabel)
    geometry({ ...context, surface: "start-dialog" })
    capture(`${theme}-${locale}-${width}-200pct-start`, false)
    assert.equal(evaluate("document.activeElement.matches('[role=dialog] h2')"), true, "large confirmation must initially focus its stable heading")
    focusedControl({ ...context, surface: "start-heading" })
    browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement.getAttribute('role')"), "checkbox")
    browser(["press", "Space"])
    browser(["press", "Tab"])
    browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement.textContent"), locale === "en" ? "Confirm and start" : "确认并开始")
    focusedControl({ ...context, surface: "start-confirm" })
    capture(`${theme}-${locale}-${width}-200pct-confirm-revealed`, false)
    browser(["press", "Escape"])
    browser(["click", ".backup-task-list>li:first-child button:first-child"])
    await until(() => evaluate("!!document.querySelector('[role=dialog] .backup-metadata')"), "real detail not readable")
    geometry({ ...context, surface: "detail-dialog" })
    capture(`${theme}-${locale}-${width}-200pct-detail`, false)
    assert.equal(evaluate("document.activeElement.matches('[role=dialog] h2')"), true, "loading detail must not push initial keyboard focus offscreen")
    focusedControl({ ...context, surface: "detail-heading" })
    browser(["press", "Tab"])
    browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement.textContent"), locale === "en" ? "Not now" : "暂不操作")
    focusedControl({ ...context, surface: "detail-dismiss" })
    browser(["press", "Enter"])
    await until(() => evaluate("!document.querySelector('[role=dialog]')"), "keyboard dismiss did not activate the actual dialog action")
    assert.equal(evaluate("document.documentElement.dataset.theme"), theme)
    assert.equal(evaluate("document.documentElement.lang"), locale)
  }
  browser(["set", "media", "reduced-motion"])
  assert.equal(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true)
  browser(["click", ".backup-section-heading button"])
  assert.equal(evaluate("getComputedStyle(document.querySelector('[role=dialog]')).animationName"), "none")
  browser(["press", "Escape"])
  evidence.push({ check: "reduced-motion-no-dialog-animation" })
  const final = await control("state")
  assert.equal(final.violation, false, "S3 fixture contract violated")
  assert(final.objects > 0 && final.completes === 26, "real complete-last publication count inaccurate")
  evidence.push({ check: "synthetic-tls-s3-real-ciphertext-publication", publications: final.completes, objects: final.objects })
} catch (error) {
  failure = error
  if (ready) { try { writeFileSync(join(output, "failure-state.json"), JSON.stringify(await control("state"), null, 2), { mode: 0o600 }) } catch {} }
  try { capture("failure", false) } catch {}
} finally {
  try { browser(["close"]) } catch {}
  if (ready) { try { await control("stop", {}) } catch {} }
  const result = await Promise.race([terminal, delay(10000).then(() => null)])
  if (!result) fixture.kill("SIGTERM")
  const finished = result ?? await terminal
  closeSync(log)
  if (!failure && finished.code !== 0) failure = new Error("fixture did not stop normally")
  writeFileSync(join(output, "layouts.json"), JSON.stringify(layouts, null, 2), { mode: 0o600 })
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ exit: failure ? 1 : 0, evidence, failure: failure?.message, fixture: finished }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
console.log(`backup web: ${evidence.length} receipts and ${layouts.length} geometry checks passed`)
