import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, writeFileSync } from "node:fs"
import https from "node:https"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const semanticScreenshots = ["invalid-safe-error", "129-skills-rejected-in-full", "17-repositories-rejected-in-full", "empty-lock", "other-provider-excluded", "unknown-no-replay", "completed-real-sqlite-status", "existing-repository-requires-explicit-binding", "session-clean-npx-other", "session-clean-npx-owner"]
const semanticLabels = [...semanticScreenshots, "keyboard-source-picker-npx", "real-owner-preview-repository-commit-status", "owner-isolation", "private-input-not-persisted", "unknown-single-commit", "keyboard-dialog-safe-focus", "owner-and-session-draft-cleanup", "legacy-exactly-two-sources", "no-implicit-skill-binding"]
const layoutCases = [320, 375].flatMap((width) => ["light", "dark"].flatMap((theme) => ["en", "zh-CN"].flatMap((locale) => ["preview", "source-menu"].map((surface) => ({ width, theme, locale, surface })))))
const layoutLabel = (entry) => `${entry.surface ?? "preview"}-${entry.width}-${entry.theme}-${entry.locale}`
const fixturePassLog = "=== RUN   TestNPXSelectionBrowserFixture\n--- PASS: TestNPXSelectionBrowserFixture (0.01s)\nPASS\n"

function assertExactly(actual, expected, label) {
  assert(Array.isArray(actual), `${label} missing`)
  assert.equal(new Set(actual).size, actual.length, `${label} duplicated`)
  assert.deepEqual([...actual].sort(), [...expected].sort(), `${label} missing or unexpected`)
}

function assertTerminal(snapshot) {
  if (snapshot.failure !== undefined) throw snapshot.failure instanceof Error ? snapshot.failure : new Error("browser verification failed")
  assert.equal(snapshot.completed, true, "browser verification did not complete")
  assert.equal(typeof snapshot.behaviorOnly, "boolean", "verification mode missing")
  const outcome = snapshot.fixtureExit
  assert(outcome && outcome.code === 0 && outcome.signal === null && !outcome.spawnError, "fixture did not exit cleanly")
  assert.equal(outcome.timedOut, false, "fixture shutdown timed out")
  assert.equal(outcome.stopFailed, false, "fixture stop failed")
  assert.equal(outcome.forced, false, "fixture required forced termination")
  assert.equal(outcome.reaped, true, "fixture was not reaped")
  assert.equal(typeof snapshot.fixtureLog, "string", "fixture log missing")
  assert(!/\b(?:FAIL|panic)\b/i.test(snapshot.fixtureLog), "fixture log contradicts PASS")
  for (const marker of [/^=== RUN   TestNPXSelectionBrowserFixture\r?$/gm, /^--- PASS: TestNPXSelectionBrowserFixture \(\d+(?:\.\d+)?s\)\r?$/gm, /^PASS\r?$/gm]) {
    assert.equal([...snapshot.fixtureLog.matchAll(marker)].length, 1, "fixture must run and pass exactly once with Go PASS summary")
  }
  assert.equal(semanticLabels.length, 19)
  const matrix = snapshot.behaviorOnly ? [] : layoutCases
  assertExactly(snapshot.evidence, [...semanticLabels, ...matrix.map(layoutLabel)], "semantic and matrix receipts")
  assert(Array.isArray(snapshot.layouts), "layouts missing")
  assertExactly(snapshot.layouts.map(layoutLabel), matrix.map(layoutLabel), "layout coverage")
  for (const entry of snapshot.layouts) {
    assert.deepEqual(entry.outside, [], "layout has overflowing content")
    if ((entry.surface ?? "preview") === "preview") {
      assert.equal(entry.fontSize, "32px", "layout text size contradicted")
      assert.equal(entry.reduced, true, "layout motion preference contradicted")
      assert(Number.isInteger(entry.scrollWidth) && entry.scrollWidth >= entry.width && entry.scrollWidth <= entry.width + 1, "layout page overflow")
      assert.deepEqual(entry.split, [], "layout has split words")
    }
  }
  assert(Array.isArray(snapshot.screenshots), "screenshots missing")
  assertExactly(snapshot.screenshots.map((entry) => entry.name), [...semanticScreenshots, ...matrix.map(layoutLabel)], "screenshot coverage")
  assert(snapshot.screenshots.every((entry) => entry.valid === true), "screenshot is not a nonempty PNG")
}

function observeFixture(child) {
  let spawnError
  child.on("error", (error) => { spawnError = error.code ?? "child_error" })
  return new Promise((done) => child.once("close", (code, signal) => done({ code, signal, spawnError })))
}

async function within(promise, duration) {
  let timer
  try {
    return await Promise.race([promise.then((value) => ({ expired: false, value })), new Promise((done) => { timer = setTimeout(() => done({ expired: true }), duration) })])
  } finally { clearTimeout(timer) }
}

async function finishFixture(child, terminal, stop, { stopMs = 5000, exitMs = 10000, termMs = 2000, killMs = 2000 } = {}) {
  let timedOut = false, stopFailed = !stop, forced = false
  if (stop) {
    try { const stopped = await within(Promise.resolve().then(stop), stopMs); timedOut = stopped.expired; stopFailed = stopped.expired } catch { stopFailed = true }
  }
  let observed = await within(terminal, exitMs)
  if (observed.expired) {
    timedOut = true
    forced = true
    child.kill("SIGTERM")
    observed = await within(terminal, termMs)
  }
  if (observed.expired) {
    child.kill("SIGKILL")
    observed = await within(terminal, killMs)
  }
  if (observed.expired) {
    child.unref()
    child.stdout?.destroy()
    child.stderr?.destroy()
  }
  return { ...(observed.expired ? { code: null, signal: null } : observed.value), timedOut, stopFailed, forced, reaped: !observed.expired }
}

function screenshotEvidence(directory) {
  return readdirSync(directory).filter((name) => name.endsWith(".png")).map((name) => {
    const bytes = readFileSync(join(directory, name))
    return { name: name.slice(0, -4), valid: bytes.length > 8 && bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])) }
  })
}

async function runSelfTests() {
  const checks = []
  function validSnapshot(behaviorOnly = false) {
    const matrix = behaviorOnly ? [] : layoutCases
    return { completed: true, behaviorOnly, evidence: [...semanticLabels, ...matrix.map(layoutLabel)], layouts: matrix.map((entry) => ({ ...entry, outside: [], split: [], reduced: true, fontSize: "32px", scrollWidth: entry.width })), screenshots: [...semanticScreenshots, ...matrix.map(layoutLabel)].map((name) => ({ name, valid: true })), fixtureLog: fixturePassLog, fixtureExit: { code: 0, signal: null, timedOut: false, stopFailed: false, forced: false, reaped: true } }
  }
  function reject(name, change) { const snapshot = validSnapshot(); change(snapshot); assert.throws(() => assertTerminal(snapshot), name); checks.push(name) }
  assertTerminal(validSnapshot()); checks.push("positive-full-35-16")
  assertTerminal(validSnapshot(true)); checks.push("positive-behavior-19-0")
  reject("nonzero-exit", (snapshot) => { snapshot.fixtureExit.code = 7 })
  reject("signal-exit", (snapshot) => { snapshot.fixtureExit.signal = "SIGTERM" })
  reject("timeout-cannot-be-masked-by-clean-exit", (snapshot) => { snapshot.fixtureExit.timedOut = true })
  reject("stop-failure", (snapshot) => { snapshot.fixtureExit.stopFailed = true })
  reject("forced-exit", (snapshot) => { snapshot.fixtureExit.forced = true })
  reject("unreaped-child", (snapshot) => { snapshot.fixtureExit.reaped = false })
  reject("incomplete-verification", (snapshot) => { snapshot.completed = false })
  reject("browser-failure-cannot-be-masked", (snapshot) => { snapshot.failure = new Error("synthetic browser failure") })
  reject("falsy-failure-cannot-be-masked", (snapshot) => { snapshot.failure = 0 })
  reject("ambiguous-mode", (snapshot) => { snapshot.behaviorOnly = "false" })
  reject("missing-semantic-receipt", (snapshot) => { snapshot.evidence.pop() })
  reject("duplicate-semantic-receipt", (snapshot) => { snapshot.evidence[0] = snapshot.evidence[1] })
  reject("unexpected-semantic-receipt", (snapshot) => { snapshot.evidence[0] = "invented-receipt" })
  reject("zero-semantic-receipts", (snapshot) => { snapshot.evidence = []; snapshot.layouts = []; snapshot.screenshots = [] })
  reject("missing-fixture-PASS", (snapshot) => { snapshot.fixtureLog = "=== RUN   TestNPXSelectionBrowserFixture\nPASS\n" })
  reject("missing-summary-PASS", (snapshot) => { snapshot.fixtureLog = fixturePassLog.replace(/\nPASS\n$/, "\n") })
  reject("zero-tests", (snapshot) => { snapshot.fixtureLog = "testing: warning: no tests to run\nPASS\n" })
  reject("duplicate-test-PASS", (snapshot) => { snapshot.fixtureLog += fixturePassLog })
  reject("contradictory-FAIL", (snapshot) => { snapshot.fixtureLog += "FAIL\n" })
  reject("contradictory-panic", (snapshot) => { snapshot.fixtureLog += "panic: synthetic panic\n" })
  reject("missing-matrix-receipt", (snapshot) => { snapshot.evidence = snapshot.evidence.filter((label) => label !== layoutLabel(layoutCases[0])) })
  reject("missing-layout", (snapshot) => { snapshot.layouts.pop() })
  reject("duplicate-layout", (snapshot) => { snapshot.layouts[0] = snapshot.layouts[1] })
  reject("contradictory-layout", (snapshot) => { snapshot.layouts[0].outside = ["synthetic overflow"] })
  reject("contradictory-motion", (snapshot) => { snapshot.layouts[0].reduced = false })
  reject("behavior-mode-with-layouts", (snapshot) => { snapshot.behaviorOnly = true; snapshot.evidence = [...semanticLabels]; snapshot.screenshots = snapshot.screenshots.slice(0, semanticScreenshots.length) })
  reject("missing-screenshot", (snapshot) => { snapshot.screenshots.pop() })
  reject("duplicate-screenshot", (snapshot) => { snapshot.screenshots[0] = snapshot.screenshots[1] })
  reject("invalid-PNG", (snapshot) => { snapshot.screenshots[0].valid = false })
  async function actualChild(script, { stop = () => {}, waitReady = false, timing = {} } = {}) {
    const child = spawn(process.execPath, ["--eval", script], { stdio: ["ignore", "pipe", "pipe"] })
    const terminal = observeFixture(child)
    let fixtureLog = "", started
    const prepared = new Promise((done) => { started = done })
    for (const stream of [child.stdout, child.stderr]) stream.on("data", (bytes) => { fixtureLog += bytes.toString(); if (fixtureLog.includes("SELFTEST_READY")) started() })
    let fixtureExit
    try { if (waitReady) assert.equal((await within(prepared, 1000)).expired, false, "self-test child did not start") } finally {
      fixtureExit = await finishFixture(child, terminal, stop, { stopMs: 40, exitMs: 1000, termMs: 40, killMs: 1000, ...timing })
    }
    return { ...validSnapshot(true), fixtureLog, fixtureExit }
  }
  const emitPass = `process.stdout.write(${JSON.stringify(fixturePassLog)})`
  assertTerminal(await actualChild(emitPass)); checks.push("actual-child-clean-PASS")
  for (const [name, script] of [["actual-child-nonzero", `${emitPass}; process.exitCode = 9`], ["actual-child-signal", "process.kill(process.pid, 'SIGTERM')"], ["actual-node-exception", "throw new Error('synthetic child exception')"], ["actual-child-FAIL-after-PASS", `${emitPass}; console.log('FAIL')`], ["actual-child-missing-PASS", "process.exitCode = 0"]]) {
    const snapshot = await actualChild(script)
    if (name === "actual-node-exception") { assert.equal(snapshot.fixtureExit.code, 1); assert.match(snapshot.fixtureLog, /synthetic child exception/) }
    assert.throws(() => assertTerminal(snapshot), name); checks.push(name)
  }
  const timeout = await actualChild("process.on('SIGTERM', () => {}); console.log('SELFTEST_READY'); setInterval(() => {}, 1000)", { waitReady: true, timing: { exitMs: 40 } })
  assert.equal(timeout.fixtureExit.timedOut, true)
  assert.equal(timeout.fixtureExit.signal, "SIGKILL")
  assert.equal(timeout.fixtureExit.reaped, true)
  assert.throws(() => assertTerminal(timeout)); checks.push("actual-timeout-SIGTERM-SIGKILL-reaped")
  const lateExit = await actualChild(`${emitPass}; console.log('SELFTEST_READY'); setTimeout(() => {}, 100)`, { waitReady: true, stop: () => new Promise(() => {}) })
  assert.equal(lateExit.fixtureExit.code, 0)
  assert.equal(lateExit.fixtureExit.signal, null)
  assert.equal(lateExit.fixtureExit.timedOut, true)
  assert.throws(() => assertTerminal(lateExit)); checks.push("actual-stop-timeout-clean-exit-still-fails")
  console.log(`PASS ${checks.length} terminal self-tests; no browser or upstream; actual abnormal children reaped`)
}

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, "behavior-only": { type: "boolean", default: false }, "self-test": { type: "boolean", default: false } } })
if (values["self-test"]) { await runSelfTests(); process.exit(0) }
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestNPXSelectionBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, COGNATE_NPX_SELECTION_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = observeFixture(fixture)
const session = `npx-selection-${process.pid}`
const evidence = [], layouts = []
let ready, failure, fixtureExit, completed = false
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]}${["click", "find"].includes(args[0]) ? ` ${args.slice(1).join(" ")}` : ""} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} rejected`)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 18000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
function modern() { return "document.querySelector('[data-import-selection-heading]').closest('section')" }
function text() { return evaluate(`${modern()}.textContent`) }
function capture(name) {
  assert.equal(evaluate("!!document.querySelector('input[type=password]')"), false, "never capture credentials")
  assert.equal(evaluate("document.body.innerText.includes('PRIVATE_NPX_MARKER')"), false, "raw private data visible")
  for (const secret of [ready.password, ready.capability]) assert.equal(evaluate(`document.body.innerText.includes(${JSON.stringify(secret)})`), false, "secret visible")
  assert.equal(evaluate("[...document.querySelectorAll('input[type=file]')].some(node => node.value)"), false, "filename visible")
  browser(["screenshot", join(output, `${name}.png`), "--full"])
  evidence.push(name)
}
async function control(path, method = "GET") {
  const response = await fetch(ready.controlURL + path, { method, headers: { "X-Fixture-Capability": ready.capability }, signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200)
  return method === "GET" ? response.json() : undefined
}
function rpc(service, method, body = {}, token) {
  return new Promise((done, reject) => {
    const request = https.request(`${ready.url}/skillhub.${service}/${method}`, { method: "POST", rejectUnauthorized: false, timeout: 10000, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", Origin: ready.url, ...(token ? { Authorization: `Bearer ${token}` } : {}) } }, (response) => {
      let data = ""
      response.on("data", (chunk) => { data += chunk })
      response.on("end", () => { try { done({ status: response.statusCode, body: JSON.parse(data) }) } catch { reject(new Error("invalid RPC receipt")) } })
    })
    request.on("error", reject)
    request.on("timeout", () => request.destroy(new Error("fixture RPC timeout")))
    request.end(JSON.stringify(body))
  })
}
async function preferences(theme, locale) {
  browser(["click", ".preferences [role=combobox]:last-child"])
  browser(["find", "role", "option", "click", "--name", locale === "en" ? "English" : "简体中文", "--exact"])
  browser(["click", ".preferences [role=combobox]:first-child"])
  browser(["find", "role", "option", "click", "--name", locale === "en" ? (theme === "dark" ? "Dark" : "Light") : (theme === "dark" ? "深色" : "浅色"), "--exact"])
}
function inputSelector() { return "section:has([data-import-selection-heading]) input[type=file]" }
async function choose(data) {
  const path = join(directory, "input.json")
  writeFileSync(path, typeof data === "string" ? data : JSON.stringify(data), { mode: 0o600 })
  browser(["upload", inputSelector(), path])
}
async function preview(data) {
  await choose(data)
  click("Upload and preview")
  await until(() => text().includes("Acknowledge exclusions") || evaluate(`${modern()}.querySelector('[role=alert]') !== null`), "preview did not resolve")
}
function clear() {
  click("Clear import draft")
  if (evaluate("!!document.querySelector('[role=dialog]')")) click("Discard import draft")
}
const lock = { version: 3, skills: { review: { sourceType: "github", source: "fixture/npx-review", sourceUrl: "https://github.com/fixture/npx-review", ref: "main", skillPath: "skills/review", skillFolderHash: "1".repeat(40), installedAt: "PRIVATE_NPX_MARKER" } } }
const selectionPath = "/skillhub.import.v1.ImportService/"
async function discardSourceFocus() {
  const source = "section:has([data-import-selection-heading]) [role=combobox]"
  const before = await control("/state")
  await choose(lock)
  await until(() => text().includes("Export selected"), "source discard requires a selected file")
  for (const confirm of [false, true]) {
    browser(["click", source])
    await until(() => evaluate("!!document.querySelector('[role=option]')"), "source menu did not open")
    browser(["find", "role", "option", "click", "--name", "Magpie JSON · 1 MiB", "--exact"])
    await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "source discard review did not open")
    if (confirm) click("Discard import draft")
    else browser(["press", "Escape"])
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "source discard review did not close")
    await delay(250)
    assert(evaluate(`${modern()}.querySelector('[role=combobox]') === document.activeElement`), `${confirm ? "confirming" : "canceling"} a source discard lost the stable source focus`)
    assert.equal(text().includes("Export selected"), !confirm, "source discard changed the wrong file lifecycle")
  }
  browser(["click", source])
  await until(() => evaluate("!!document.querySelector('[role=option]')"), "source menu did not reopen")
  browser(["press", "End"]); browser(["press", "Enter"])
  assert(text().includes("version 3"), "npx source did not resume after deliberate discard")
  const after = await control("/state")
  for (const method of ["PreviewImportSelection", "CommitImportSelection"]) assert.equal(after[selectionPath + method] ?? 0, before[selectionPath + method] ?? 0, "source discard dispatched an import")
}
async function verify() {
  await until(() => existsSync(join(directory, "ready.json")), "real TLS fixture not ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["set", "viewport", "1440", "1000"])
  browser(["open", ready.url + "/repositories"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login not ready")
  browser(["fill", "#username", "npx-owner"])
  browser(["fill", "#password", ready.password])
  browser(["click", "form button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('[data-import-selection-heading]')"), "owner Web not ready")
  await preferences("light", "en")
  browser(["click", "section:has([data-import-selection-heading]) [role=combobox]"])
  assert(evaluate("[...document.querySelectorAll('[role=option]')].some(node => node.textContent.includes('npx skills'))"), "RED: modern source picker lacks npx skills lock")
  browser(["press", "End"])
  browser(["press", "Enter"])
  assert(text().includes("GitHub"), "npx constraints absent")
  assert(text().includes("version 3 only") && text().includes("1 MiB") && text().includes("no client configuration") && text().includes("explicitly bind"), "npx safety boundaries absent")
  assert(text().includes("128 skill candidates") && text().includes("16 repositories") && text().includes("never truncated"), "batch limits absent")
  await discardSourceFocus()
  evidence.push("keyboard-source-picker-npx")
  await choose("")
  assert(evaluate(`${modern()}.querySelector('[role=alert]') !== null`), "empty file accepted")
  assert.equal((await control("/state"))[selectionPath + "PreviewImportSelection"] ?? 0, 0)
  clear()
  await choose("x".repeat((1 << 20) + 1))
  assert(evaluate(`${modern()}.querySelector('[role=alert]') !== null`), "oversized file accepted")
  clear()
  await preview("{invalid PRIVATE_NPX_MARKER")
  assert(evaluate(`${modern()}.querySelector('[role=alert]') !== null`), "invalid JSON accepted")
  capture("invalid-safe-error")
  clear()
  for (const [name, count] of [["129-skills", 129], ["17-repositories", 17]]) {
    const skills = Object.fromEntries(Array.from({ length: count }, (_, index) => {
      const entry = { ...lock.skills.review, skillPath: `skills/review-${index}` }
      if (name === "17-repositories") { entry.source = `fixture/npx-review-${index}`; entry.sourceUrl = `https://github.com/${entry.source}` }
      return [`review-${index}`, entry]
    }))
    const batch = { version: 3, skills }
    assert(Buffer.byteLength(JSON.stringify(batch)) < 1 << 20, "batch-limit case must be below file limit")
    await preview(batch)
    const alert = evaluate(`${modern()}.querySelector('[role=alert]')?.textContent ?? ''`)
    assert(alert.includes("ResourceExhausted") && alert.includes("128") && alert.includes("16") && alert.includes("never truncated"), "small oversized batch lacks actionable rejection")
    assert.equal(text().includes("Acknowledge exclusions"), false, "batch was silently truncated")
    capture(`${name}-rejected-in-full`)
    clear()
  }
  await preview({ version: 3, skills: {} })
  assert(text().includes("No candidates in this category"), "valid empty lock not empty")
  assert.equal(evaluate(`${modern()}.querySelectorAll('[role=checkbox][aria-checked=true]').length`), 0)
  capture("empty-lock")
  clear()
  await preview({ version: 3, skills: { private: { sourceType: "local", source: "PRIVATE_NPX_MARKER" } } })
  assert(text().includes("Other source data is excluded"), "other provider not excluded")
  assert.equal(evaluate(`${modern()}.querySelectorAll('label .font-mono').length`), 0)
  capture("other-provider-excluded")
  clear()
  await preview(lock)
  assert(text().includes("fixture/npx-review"), "real npx projection absent")
  assert(text().includes("Collect the repository first"), "unverified skill enabled")
  assert.equal(evaluate(`${modern()}.querySelectorAll('[role=checkbox][aria-checked=true]').length`), 0, "not default unselected")
  const ownerLogin = await rpc("auth.v1.AuthService", "Login", { username: "npx-owner", password: ready.password })
  assert.equal(ownerLogin.status, 200)
  const ownerToken = ownerLogin.body.sessionToken
  assert.equal((await rpc("library.v1.LibraryService", "ListRepositories", {}, ownerToken)).body.repositories?.length ?? 0, 0, "preview persisted")
  for (const width of values["behavior-only"] ? [] : [320, 375]) for (const theme of ["light", "dark"]) for (const locale of ["en", "zh-CN"]) {
    browser(["set", "viewport", "1440", "1000"])
    evaluate("document.documentElement.style.fontSize='100%'")
    await preferences(theme, locale)
    if (!text().includes("fixture/npx-review")) {
      await choose(lock)
      click(locale === "en" ? "Upload and preview" : "上传并预览")
      await until(() => text().includes("fixture/npx-review"), "localized preview failed")
    }
    browser(["set", "viewport", String(width), "1000"])
    browser(["set", "media", "reduced-motion"])
    evaluate("document.documentElement.style.fontSize='200%'")
    const result = evaluate(`(() => {
      const root = ${modern()}, outside = [], split = []
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
      while (walker.nextNode()) {
        const node = walker.currentNode, range = document.createRange(); range.selectNodeContents(node)
        if ([...range.getClientRects()].some(rect => rect.width && (rect.left < -1 || rect.right > innerWidth + 1))) outside.push(node.textContent.slice(0,80))
        if (getComputedStyle(node.parentElement).fontFamily.includes('mono')) continue
        for (const match of node.textContent.matchAll(/[A-Za-z]{3,}/g)) {
          const word = document.createRange(); word.setStart(node, match.index); word.setEnd(node, match.index + match[0].length)
          if (word.getClientRects().length > 1) split.push(match[0])
        }
      }
      return {width: innerWidth, scrollWidth: document.documentElement.scrollWidth, fontSize: getComputedStyle(document.documentElement).fontSize, reduced: matchMedia('(prefers-reduced-motion: reduce)').matches, outside, split}
    })()`)
    layouts.push({ theme, locale, ...result })
    assert(result.scrollWidth <= width + 1, "page overflow")
    assert.deepEqual(result.outside, [], "import text outside viewport")
    assert.deepEqual(result.split, [], "split English words")
    assert.equal(result.fontSize, "32px")
    assert.equal(result.reduced, true)
    capture(`preview-${width}-${theme}-${locale}`)
    browser(["click", "section:has([data-import-selection-heading]) [role=combobox]"])
    const menu = evaluate(`({ width: innerWidth, outside: [...document.querySelectorAll('[role=option]')].filter(node => { const rect = node.getBoundingClientRect(); return rect.left < -1 || rect.right > innerWidth + 1 }).map(node => node.textContent) })`)
    layouts.push({ theme, locale, surface: "source-menu", ...menu })
    assert.deepEqual(menu.outside, [], "source menu outside viewport")
    capture(`source-menu-${width}-${theme}-${locale}`)
    browser(["press", "Escape"])
    assert.equal(evaluate(`document.activeElement === ${modern()}.querySelector('[role=combobox]')`), true, "source menu focus not restored")
  }
  evaluate("document.documentElement.style.fontSize='100%'")
  browser(["set", "viewport", "1440", "1000"])
  await preferences("light", "en")
  if (!text().includes("fixture/npx-review")) await preview(lock)
  browser(["click", "section:has([data-import-selection-heading]) label:has(.font-mono)"])
  const acknowledgments = evaluate(`[...${modern()}.querySelectorAll('label[for*="-notice-"]')].map(node => node.htmlFor)`)
  for (const id of acknowledgments) browser(["click", `label[for='${id}']`])
  click("Review and confirm selection")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "confirmation absent")
  assert.equal(evaluate("document.activeElement.textContent"), "Cancel", "dialog focus not on safe cancel")
  browser(["press", "Escape"])
  assert.equal(evaluate("document.activeElement.textContent"), "Review and confirm selection", "dialog focus not restored")
  click("Review and confirm selection")
  await control("/lost", "POST")
  click("Import the selected items")
  await until(() => text().includes("Query import status"), "lost real receipt did not enter Unknown")
  capture("unknown-no-replay")
  const before = await control("/state")
  assert.equal(before[selectionPath + "CommitImportSelection"], 1)
  assert.equal(evaluate(`${modern()}.querySelector('[role=combobox]').disabled`), true)
  click("Query import status")
  await until(() => text().includes("Import completed"), "real persisted status missing")
  const after = await control("/state")
  assert.equal(after[selectionPath + "CommitImportSelection"], 1, "Unknown replayed commit")
  assert.equal(after[selectionPath + "GetImportCommitStatus"], 1)
  const listed = await rpc("library.v1.LibraryService", "ListRepositories", {}, ownerToken)
  assert.equal(listed.status, 200)
  assert.equal(listed.body.repositories.length, 1)
  const otherLogin = await rpc("auth.v1.AuthService", "Login", { username: "npx-other", password: ready.password })
  assert.equal(otherLogin.status, 200)
  const foreign = await rpc("library.v1.LibraryService", "ListRepositories", {}, otherLogin.body.sessionToken)
  assert.equal(foreign.status, 200)
  assert.equal(foreign.body.repositories?.length ?? 0, 0, "repository crossed owners")
  assert.equal(evaluate("Object.values(localStorage).some(value => value.includes('PRIVATE_NPX_MARKER'))"), false)
  assert.equal(evaluate("Object.values(sessionStorage).some(value => value.includes('PRIVATE_NPX_MARKER'))"), false)
  capture("completed-real-sqlite-status")
  evidence.push("real-owner-preview-repository-commit-status", "owner-isolation", "private-input-not-persisted", "unknown-single-commit", "keyboard-dialog-safe-focus")
  clear()
  await preview(lock)
  assert(text().includes("Explicitly bind a skill from the same repository"), "existing content was implicitly bound")
  assert.equal(evaluate(`${modern()}.querySelectorAll('[role=checkbox][aria-checked=true]').length`), 0)
  capture("existing-repository-requires-explicit-binding")
  clear()
  await choose(lock)
  for (const username of ["npx-other", "npx-owner"]) {
    click("Sign out")
    await until(() => evaluate("!!document.querySelector('#username')"), "sign out did not clear session")
    browser(["fill", "#username", username])
    browser(["fill", "#password", ready.password])
    browser(["click", "form button[type=submit]"])
    await until(() => evaluate("!!document.querySelector('[data-import-selection-heading]')"), "new owner Web not ready")
    assert.equal(text().includes("Acknowledge exclusions"), false, "preview crossed owner/session")
    assert.equal(text().includes("Export selected"), false, "file crossed owner/session")
    assert.equal(text().includes("Query import status"), false, "operation crossed owner/session")
    capture(`session-clean-${username}`)
  }
  click("Use the legacy repository-only workflow")
  browser(["click", "#import-source"])
  assert.equal(evaluate("document.querySelectorAll('[role=option]').length"), 2, "legacy source list changed")
  assert.equal(evaluate("[...document.querySelectorAll('[role=option]')].some(node => node.textContent.includes('npx'))"), false)
  browser(["press", "Escape"])
  evidence.push("owner-and-session-draft-cleanup", "legacy-exactly-two-sources", "no-implicit-skill-binding")
}
try { await verify(); completed = true } catch (error) { failure = error instanceof Error ? error : new Error("browser verification failed"); try { capture("failure") } catch {} } finally {
  fixtureExit = await finishFixture(fixture, terminal, ready ? () => control("/stop", "POST") : null)
  closeSync(log)
  try { browser(["close"]) } catch (error) { failure ??= error instanceof Error ? error : new Error("browser cleanup failed") }
  let screenshots = []
  try {
    screenshots = screenshotEvidence(output)
    assertTerminal({ completed, evidence, layouts, screenshots, fixtureExit, fixtureLog: readFileSync(join(output, "fixture.log"), "utf8"), behaviorOnly: values["behavior-only"], failure })
  } catch (error) { failure ??= error instanceof Error ? error : new Error("terminal verification failed") }
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ completed, behaviorOnly: values["behavior-only"], evidence, layouts, screenshots, fixtureExit, exit: failure ? 1 : 0, failure: failure?.message }, null, 2), { mode: 0o600 })
}
if (failure) { console.error(failure.message); process.exitCode = 1 } else console.log(`PASS ${evidence.length} receipts; ${layouts.length} layouts; fixture exit 0 signal null`)
