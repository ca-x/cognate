import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import https from "node:https"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { fileURLToPath } from "node:url"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" }, "rpc-only": { type: "boolean" }, "negative-gate": { type: "boolean" }, "self-test-gate": { type: "boolean" }, "audit-state": { type: "string" }, rounds: { type: "string", default: "30" } } })
const allowances = [
  { stage: "token-invalid", mode: "normal", method: "ConnectToken", code: "unavailable" },
  { stage: "subscription-error", mode: "upstream-error", method: "Sync", code: "unavailable" },
  { stage: "sync-error", mode: "upstream-error", method: "Sync", code: "unavailable" },
  { stage: "query-error", mode: "error", method: "GetCatalog", code: "unavailable" },
  { stage: "generation-conflict", mode: "normal", method: "ConnectToken", code: "aborted" },
  ...["loading", "device-flow", "query-error", "client-expiry", "server-expiry", "generation-refresh"].map(stage => ({ stage, mode: "normal", method: "GetDeviceAuthorization", code: "not_found" })),
]
function classifyFailures(state) {
  if (!Object.hasOwn(state, "failures") || !Object.hasOwn(state, "failureEvents")) return [{ code: "missing-ledger" }]
  const failures = state.failures ?? [], events = state.failureEvents ?? []
  if (!Array.isArray(failures) || !Array.isArray(events)) return [{ code: "invalid-ledger" }]
  const unexpected = []
  if (failures.length !== events.length || events.some((event, index) => event.sequence !== index + 1 || failures[index] !== `${event.method}:${event.code}`)) unexpected.push({ code: "incomplete-ledger" })
  for (const event of events) {
    if (event.code === "internal" || !allowances.some(rule => Object.entries(rule).every(([key, value]) => event[key] === value))) unexpected.push(event)
  }
  return unexpected
}
if (values["self-test-gate"]) {
  let checks = 0
  for (const rule of allowances) {
    const event = { sequence: 1, ...rule }
    assert.equal(classifyFailures({ failures: [`${event.method}:${event.code}`], failureEvents: [event] }).length, 0); checks++
    for (const code of ["internal", "unknown", "permission_denied"]) {
      const changed = { ...event, code }
      assert(classifyFailures({ failures: [`${changed.method}:${code}`], failureEvents: [changed] }).length > 0); checks++
    }
    assert(classifyFailures({ failures: [`${event.method}:${event.code}`], failureEvents: [{ ...event, stage: "wrong-stage" }] }).length > 0); checks++
  }
  assert(classifyFailures({ failures: ["GetCatalog:internal"] }).length > 0); checks++
  assert(classifyFailures({}).length > 0); checks++
  assert(classifyFailures({ failures: [] }).length > 0); checks++
  assert(classifyFailures({ failures: [], failureEvents: [{ sequence: 1, stage: "complete", mode: "normal", method: "Sync", code: "internal" }] }).length > 0); checks++
  console.log(JSON.stringify({ gateSelfTest: "pass", checks }))
  process.exit(0)
}
if (values["audit-state"]) {
  const unexpected = classifyFailures(JSON.parse(readFileSync(resolve(values["audit-state"]), "utf8")))
  console.log(JSON.stringify({ ledgerAuditExit: unexpected.length ? 1 : 0, unexpectedCount: unexpected.length }))
  process.exit(unexpected.length ? 1 : 0)
}
for (const name of ["fixture-bin", "output", ...(!values["rpc-only"] ? ["browser-bin", "chrome-bin"] : [])]) assert(values[name], `missing ${name}`)
assert(!values["negative-gate"] || values["rpc-only"], "negative gate requires RPC-only mode")
const rounds = Number(values.rounds)
assert(Number.isSafeInteger(rounds) && rounds >= 1 && rounds <= 1000, "invalid round count")
const identity = {
  scriptSHA256: createHash("sha256").update(readFileSync(fileURLToPath(import.meta.url))).digest("hex"),
  fixtureSHA256: createHash("sha256").update(readFileSync(resolve(values["fixture-bin"]))).digest("hex"),
  node: process.version,
}
assert(["before", "after"].includes(values.phase))
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestSourcesBrowserFixture$", "-test.v", "-test.timeout=900s"], { env: { ...process.env, CGO_ENABLED: "0", HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_SOURCES_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `sources-${process.pid}-${Date.now()}`
const evidence = [], regressions = [], geometry = [], unexpectedBackendFailures = []
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure, rpcSession, started = false
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed (details withheld)`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} rejected (details withheld)`)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message, timeout = 15000) { const deadline = Date.now() + timeout; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
function capture(name) { assert(evaluate("!document.querySelector('input[type=password]')?.value"), "screenshot blocked: password field nonempty"); browser(["screenshot", join(output, `${name}.png`), "--full"]); evidence.push(name) }
async function control(path, body) { const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body } : {}), signal: AbortSignal.timeout(5000) }); assert.equal(response.status, 200, "fixture control rejected"); return body === undefined ? response.json() : undefined }
function click(text) { browser(["find", "role", "button", "click", "--name", text, "--exact"]) }
function input(selector, value) { evaluate(`(() => { const node = document.querySelector(${JSON.stringify(selector)}); if (!node) return false; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(node, ${JSON.stringify(value)}); node.dispatchEvent(new Event('input', {bubbles:true})); return true })()`) }
function check(condition, name) { if (condition) evidence.push(name); else if (values.phase === "before") regressions.push(name); else assert.fail(name) }
async function login() {
  await until(() => evaluate("!!document.querySelector('#username')"), "login missing")
  input("#username", ready.username); input("#password", ready.password)
  browser(["click", "button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('.source-card')"), "sources missing")
}
async function load() { browser(["open", ready.url + "/"]); await login() }
async function lists(count = 28) { await until(() => evaluate(`document.querySelectorAll('.source-list-row').length === ${count}`), "real lists missing") }
async function stage(name) { await control("stage", name) }
async function rpc(service, method, body = {}) {
  const address = new URL(`/skillhub.${service}.v1.${service === "auth" ? "Auth" : "GitHub"}Service/${method}`, ready.url)
  assert.equal(address.hostname, "127.0.0.1")
  return new Promise((done, reject) => {
    const request = https.request(address, { method: "POST", rejectUnauthorized: false, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...(rpcSession ? { Authorization: `Bearer ${rpcSession}` } : {}) }, timeout: 10000 }, response => {
      let text = ""
      response.setEncoding("utf8")
      response.on("data", chunk => { text += chunk })
      response.on("end", () => {
        try { done({ status: response.statusCode, data: JSON.parse(text) }) }
        catch { reject(new Error("invalid local RPC JSON")) }
      })
    })
    request.on("error", () => reject(new Error("local RPC transport failure")))
    request.on("timeout", () => request.destroy())
    request.end(JSON.stringify(body))
  })
}
async function runRPC() {
  await stage("rpc-initial")
  const login = await rpc("auth", "Login", { username: ready.username, password: ready.password })
  assert.equal(login.status, 200, "RPC login failed")
  rpcSession = login.data.sessionToken
  assert.equal((await rpc("github", "ConnectToken", { accessToken: ready.token })).status, 200, "RPC initial connection failed")
  assert.equal((await rpc("github", "Sync")).status, 200, "RPC initial sync failed")
  if (values["negative-gate"]) {
    const procedures = ["GetCatalog", "Sync", "ConnectToken", "SetListSubscription", "GetDeviceAuthorization"].map(method => ({ service: "github", method }))
    procedures.push({ service: "auth", method: "GetSession" })
    for (const phase of ["loading", "connected", "subscription-error", "query-error", "reauthorization"]) for (const { service, method } of procedures) {
      await stage(phase)
      await control("mode", "internal-once")
      assert.equal((await rpc(service, method)).status, 500, "negative injection did not execute")
      assert.equal((await rpc("github", "GetCatalog")).status, 200, "post-injection observation failed")
      evidence.push({ phase, service, method, injectedCode: "internal", laterCatalogOK: true })
    }
    return
  }
  for (let iteration = 0; iteration < rounds; iteration++) {
    await stage(`rpc-round-${iteration}`)
    await control("expire", "")
    const expired = await rpc("github", "GetCatalog")
    assert.equal(expired.status, 200, "expired catalog lookup failed")
    assert(expired.data.catalog.authorizationRequired && !(expired.data.catalog.lists ?? []).length, "RPC expiry exposed observations")
    const connected = await rpc("github", "ConnectToken", { accessToken: ready.token, expectedGeneration: expired.data.catalog.credentialGeneration })
    assert.equal(connected.status, 200, "RPC reauthorization failed")
    const responses = await Promise.all([rpc("github", "GetCatalog"), rpc("github", "Sync")])
    evidence.push({ iteration, codes: responses.map((response, index) => ({ method: index === 0 ? "GetCatalog" : "Sync", status: response.status, code: response.status === 200 ? "ok" : response.data.code })) })
  }
}
function textContains(text) { return evaluate(`document.querySelector('main').textContent.includes(${JSON.stringify(text)})`) }
async function preferences(theme, locale, width) {
  browser(["set", "viewport", String(width), width <= 375 ? "812" : "960"])
  const current = evaluate("document.documentElement.lang")
  browser(["find", "label", current === "en" ? "Theme" : "主题", "click", "--exact"])
  browser(["find", "role", "option", "click", "--name", current === "en" ? (theme === "dark" ? "Dark" : "Light") : (theme === "dark" ? "深色" : "浅色"), "--exact"])
  if (current !== locale) {
    browser(["find", "label", current === "en" ? "Interface language" : "界面语言", "click", "--exact"])
    browser(["find", "role", "option", "click", "--name", locale === "en" ? "English" : "简体中文", "--exact"])
  }
  assert.equal(evaluate("document.documentElement.dataset.theme"), theme)
  assert.equal(evaluate("document.documentElement.lang"), locale)
}
function reflow(name) {
  const result = evaluate(`(() => {
    const root = document.querySelector('main');
    const outside = rectangle => rectangle.width > 0 && (rectangle.left < -1 || rectangle.right > innerWidth + 1);
    const outsideElements = Array.from(root.querySelectorAll('*')).filter(node => getComputedStyle(node).visibility !== 'hidden' && outside(node.getBoundingClientRect()));
    const elements = outsideElements.length;
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT); let text = 0;
    while (walker.nextNode()) { const node = walker.currentNode; if (!node.textContent.trim() || !node.parentElement.checkVisibility()) continue; const range = document.createRange(); range.selectNodeContents(node); text += Array.from(range.getClientRects()).filter(outside).length; }
    const brokenControlWords = [];
    for (const control of root.querySelectorAll('.connection-state, .sources-state, .token-input-row .button')) {
      if (!control.checkVisibility()) continue;
      const words = document.createTreeWalker(control, NodeFilter.SHOW_TEXT);
      while (words.nextNode()) for (const match of words.currentNode.textContent.matchAll(/[A-Za-z]{3,}/g)) {
        const range = document.createRange(); range.setStart(words.currentNode, match.index); range.setEnd(words.currentNode, match.index + match[0].length);
        if (new Set(Array.from(range.getClientRects()).filter(rectangle => rectangle.width > 0).map(rectangle => Math.round(rectangle.top))).size > 1) brokenControlWords.push(match[0]);
      }
    }
    return {elements, text, brokenControlWords, page: document.documentElement.scrollWidth > innerWidth + 1, details: outsideElements.map(node => ({tag:node.tagName, class:node.getAttribute('class'), left:node.getBoundingClientRect().left, right:node.getBoundingClientRect().right}))};
  })()`)
  geometry.push({ name, ...result })
  check(!result.page && !result.elements && !result.text, `reflow-${name}`)
  check(!result.brokenControlWords.length, `control-words-${name}`)
}
async function matrix(prefix) {
  for (const theme of ["dark", "light"]) for (const locale of ["zh-CN", "en"]) for (const width of [1440, 375, 320]) {
    await preferences(theme, locale, width)
    const name = `${prefix}-${theme}-${locale}-${width}`
    reflow(name); capture(name)
    evaluate("document.documentElement.style.fontSize = '200%'; true")
    reflow(`${name}-text200`); capture(`${name}-text200`)
    evaluate("document.documentElement.style.fontSize = ''; true")
  }
  await preferences("dark", "zh-CN", 1440)
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture readiness missing")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  started = true
  if (values["rpc-only"]) {
    await runRPC()
  } else {
  await stage("loading")
  browser(["open", ready.url])
  evaluate("localStorage.setItem('skillhub.locale','zh-CN'); localStorage.setItem('skillhub.theme','dark'); true")
  await control("mode", "slow")
  browser(["set", "viewport", "1440", "960"])
  await load()
  await until(() => textContains("正在读取连接状态…"), "loading state missing")
  capture("loading-dark-zh-1440")
  assert(!evaluate("!!document.querySelector('.list-section')"), "loading became empty lists")
  await control("mode", "normal")
  await until(() => evaluate("!!document.querySelector('.device-flow button')"), "authorization form missing")
  capture("disconnected-dark-zh-1440")
  await matrix("disconnected")
  await stage("device-flow")
  click("通过 GitHub 授权")
  await until(() => evaluate("document.querySelector('.device-code')?.textContent === 'TEST-ONLY'"), "real device flow missing")
  capture("device-pending-dark-zh-1440")
  click("取消本次授权")
  await until(() => textContains("已取消本次授权。"), "device cancellation missing")
  assert(!evaluate("!!document.querySelector('.device-code')"), "canceled device code retained")
  evidence.push("device-start-cancel-real-connect")
  browser(["click", ".source-card summary"])
  assert.equal(evaluate("document.querySelector('#github-token').type"), "password")
  await stage("token-invalid")
  input("#github-token", "synthetic-invalid")
  click("连接 GitHub")
  await until(() => evaluate("!!document.querySelector('.source-card [role=alert]')"), "invalid token was not rejected")
  assert.equal(evaluate("document.querySelector('#github-token').value"), "")
  capture("token-error-dark-zh-1440")
  await stage("connect")
  input("#github-token", ready.token)
  click("连接 GitHub")
  await lists()
  await until(() => textContains("来源已同步。"), "connect sync feedback missing")
  capture("connected-dark-zh-1440")
  let state = await control("state")
  assert(state.validated > 0 && state.wireSafe && state.plaintextAbsent, "credential validation/storage/readback invariant failed")
  evidence.push("password-cleared-validated-encrypted-no-readback")
  await stage("subscription")
  const beforeSelection = state.requests.length
  browser(["click", ".source-list-row:first-child [role=checkbox]"])
  await until(() => textContains("订阅已保存并同步。"), "subscription completion missing")
  state = await control("state")
  const selected = state.requests.slice(beforeSelection)
  assert(selected.indexOf("SetListSubscription") >= 0 && selected.indexOf("Sync") > selected.indexOf("SetListSubscription"), "subscription sync order changed")
  assert.equal(evaluate("document.querySelector('.source-list-row:first-child [role=checkbox]').getAttribute('aria-checked')"), "true")
  evidence.push("subscription-then-sync-real-sqlite")
  await stage("sync-pending")
  await control("mode", "slow-upstream")
  click("立即同步")
  await until(() => evaluate("document.querySelector('.list-section').getAttribute('aria-busy') === 'true'"), "real sync pending state missing")
  assert(evaluate("Array.from(document.querySelectorAll('.source-list-row [role=checkbox]')).every(node=>node.disabled) && document.querySelector('.page-heading button').disabled"), "sync pending controls enabled")
  capture("sync-pending-dark-zh-1440")
  await until(() => textContains("来源已同步。"), "slow upstream sync never completed")
  await control("mode", "normal")
  evidence.push("real-sync-pending-controls-disabled-no-fake-progress")
  await stage("subscription-error")
  await control("mode", "upstream-error")
  browser(["click", ".source-list-row:nth-child(2) [role=checkbox]"])
  await until(() => evaluate("!!document.querySelector('.list-section [role=alert]')"), "subscription sync failure missing")
  await until(() => evaluate("document.querySelectorAll('.source-list-row').length === 0"), "invalidated observations were not hidden")
  assert(!textContains("订阅已保存并同步。"), "failed subscription sync reported success")
  check(!textContains("还没有收藏列表"), "failed-subscription-is-not-empty-lists")
  capture("subscription-error-dark-zh-1440")
  await control("mode", "normal")
  await stage("subscription-recovery")
  click("立即同步"); await lists()
  await until(() => evaluate("!document.querySelector('.source-list-row:nth-child(2) [role=checkbox]')?.disabled"), "subscription never settled")
  browser(["click", ".source-list-row:nth-child(2) [role=checkbox]"])
  await until(() => textContains("订阅已保存并同步。"), "subscription recovery missing")
  evidence.push("subscription-sync-error-no-false-success-and-recovery")
  browser(["fill", ".filter-field input", "no-such-list"])
  await until(() => textContains("没有匹配的列表"), "filter empty state missing")
  capture("filter-no-match-dark-zh-1440")
  input(".filter-field input", "")
  await lists()
  await stage("sync-error")
  await control("mode", "upstream-error")
  click("立即同步")
  await until(() => evaluate("!!document.querySelector('.list-section [role=alert]')"), "sync error missing")
  capture("sync-error-dark-zh-1440")
  await control("mode", "normal")
  await stage("sync-recovery")
  click("立即同步")
  await until(() => textContains("来源已同步。"), "sync retry missing")
  evidence.push("manual-sync-error-and-retry")
  await matrix("connected")
  await preferences("dark", "zh-CN", 375)
  for (let index = 0; index < 30 && !evaluate("document.activeElement === document.querySelector('.source-list-row:first-child [role=checkbox]')"); index++) browser(["press", "Tab"])
  assert(evaluate("document.activeElement === document.querySelector('.source-list-row:first-child [role=checkbox]')"), "Tab did not reach first list")
  assert(evaluate("document.activeElement.matches(':focus-visible') && getComputedStyle(document.activeElement).outlineStyle !== 'none'"), "keyboard focus not visible")
  capture("keyboard-focus-dark-zh-375")
  browser(["press", "Space"])
  await until(() => textContains("订阅已保存并同步。"), "keyboard subscription missing")
  assert.equal(evaluate("document.querySelector('.source-list-row:first-child [role=checkbox]').getAttribute('aria-checked')"), "false")
  evidence.push("keyboard-space-checkbox-and-visible-focus")
  browser(["set", "media", "reduced-motion"])
  assert(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), "reduced motion emulation missing")
  check(evaluate("Array.from(document.querySelectorAll('.source-list-row')).every(node => getComputedStyle(node).transitionDuration.split(',').every(value => parseFloat(value) === 0))"), "reduced-motion-no-row-transition")
  capture("reduced-motion-dark-zh-375")
  await preferences("dark", "zh-CN", 1440)
  await control("mode", "empty")
  click("立即同步")
  await until(() => textContains("还没有收藏列表"), "real empty lists missing")
  capture("empty-dark-zh-1440")
  await matrix("empty")
  await control("mode", "long")
  click("立即同步"); await lists()
  await stage("query-error")
  await control("mode", "error")
  await until(() => evaluate("!!document.querySelector('.source-card [role=alert]')"), "cached query error missing", 40000)
  check(!evaluate("!!document.querySelector('.list-section')"), "cached-error-hides-list-content")
  capture("cached-error-dark-zh-1440")
  await load()
  await until(() => evaluate("!!document.querySelector('.source-card [role=alert]')"), "query error missing")
  assert(!evaluate("!!document.querySelector('.list-section')"), "initial error became empty lists")
  capture("query-error-dark-zh-1440")
  await matrix("query-error")
  await stage("query-recovery")
  await control("mode", "normal"); click("重试"); await lists()
  await stage("unknown")
  await control("mode", "absent"); await load()
  await until(() => !textContains("正在读取连接状态…"), "absent response unresolved")
  check(evaluate("!!document.querySelector('.source-card [role=alert]')"), "unknown-catalog-is-error-not-disconnected")
  capture("unknown-dark-zh-1440")
  await stage("client-expiry")
  await control("mode", "short-expiry"); await load()
  await lists()
  await until(() => evaluate("!!document.querySelector('.source-card [role=alert]')"), "client catalog expiry missing")
  assert(!evaluate("!!document.querySelector('.list-section')"), "expired lists exposed")
  capture("client-expired-dark-zh-1440")
  await control("mode", "normal"); click("重试"); await lists()
  await stage("server-expiry")
  await control("expire", "")
  await load()
  await until(() => textContains("GitHub 凭据已过期，请重新授权。旧来源内容已隐藏。"), "server credential expiry missing")
  assert(!evaluate("!!document.querySelector('.list-section')"), "server expired lists exposed")
  capture("credential-expired-dark-zh-1440")
  await matrix("credential-expired")
  await stage("reauthorization")
  browser(["click", ".source-card summary"])
  input("#github-token", ready.token); click("更新连接"); await lists()
  evidence.push("server-expiry-reauthorization-generation-and-hidden-old-content")
  browser(["click", ".source-card summary"])
  browser(["click", ".source-card details details summary"])
  await stage("generation-conflict")
  await control("rotate", "")
  const beforeConflict = (await control("state")).requests.length
  input("#github-token", ready.token); click("更新连接")
  await until(() => textContains("来源已改变，请刷新后重试。"), "stale generation not rejected")
  assert.equal(evaluate("document.querySelector('#github-token').value"), "")
  assert(!(await control("state")).requests.slice(beforeConflict).includes("Sync"), "conflict started sync")
  capture("generation-conflict-dark-zh-1440")
  await stage("generation-refresh")
  await load()
  await until(() => textContains("立即同步"), "latest connection state missing")
  assert.equal(evaluate("document.querySelectorAll('.source-list-row').length"), 0, "changed generation exposed stale observations")
  click("立即同步"); await lists()
  evidence.push("stale-generation-browser-rejected-and-input-cleared")
  await preferences("light", "en", 320)
  capture("reauthorized-light-en-320")
  browser(["click", ".source-card summary"])
  browser(["click", ".source-card details details summary"])
  evaluate("document.documentElement.style.fontSize = '200%'; true")
  reflow("token-form-light-en-320-text200")
  capture("token-form-light-en-320-text200")
  evaluate("document.documentElement.style.fontSize = ''; true")
  state = await control("state")
  assert(state.wireSafe && state.plaintextAbsent, "secret boundary failed")
  writeFileSync(join(output, "safe-state.json"), JSON.stringify(state, null, 2), { mode: 0o600 })
  }
} catch (error) {
  failure = error
  if (started && !values["rpc-only"]) {
    try {
      capture("failure-state")
      writeFileSync(join(output, "failure-state.json"), JSON.stringify(evaluate("({rows:document.querySelectorAll('.source-list-row').length,filter:document.querySelector('.filter-field input')?.value,alertCount:document.querySelectorAll('main [role=alert]').length})"), null, 2), { mode: 0o600 })
      writeFileSync(join(output, "failure-safe-state.json"), JSON.stringify(await control("state"), null, 2), { mode: 0o600 })
    } catch {}
  }
} finally {
  if (started && !values["rpc-only"]) { try { browser(["close"]) } catch {} }
  if (ready) {
    try {
      const state = await control("state")
      writeFileSync(join(output, "safe-state.json"), JSON.stringify(state, null, 2), { mode: 0o600 })
      unexpectedBackendFailures.push(...classifyFailures(state))
      assert(state.validated > 0 && state.wireSafe && state.plaintextAbsent, "final credential boundary failed")
    } catch { failure ??= new Error("final failure ledger unavailable or unsafe") }
  }
  if (ready) { try { await control("stop", "") } catch {} }
  const result = await Promise.race([terminal, delay(5000).then(() => null)])
  if (!result) { fixture.kill("SIGTERM"); failure ??= new Error("fixture did not stop cleanly") }
  else if (result.code !== 0) failure ??= new Error("fixture exited unsuccessfully")
  closeSync(log)
  rmSync(directory, { recursive: true, force: true })
  let safeFailure = failure ? (failure instanceof assert.AssertionError ? failure.message : "verification failed (details withheld)") : undefined
  for (const secret of [ready?.token, ready?.password, ready?.capability, rpcSession].filter(Boolean)) safeFailure = safeFailure?.replaceAll(secret, "[redacted]")
  const report = { identity, phase: values.phase, uiLoaded: started && !values["rpc-only"], backendRecoveryRetries: 0, negativeGateProbe: !!values["negative-gate"], exit: failure || unexpectedBackendFailures.length ? 1 : 0, evidence, regressions, geometry, unexpectedBackendFailures, ...(safeFailure ? { failure: safeFailure } : {}) }
  writeFileSync(join(output, "receipts.json"), JSON.stringify(report, null, 2), { mode: 0o600 })
  console.log(JSON.stringify({ phase: values.phase, exit: report.exit, checks: evidence.length, regressions, unexpectedBackendFailures, ...(report.failure ? { failure: report.failure } : {}) }))
  if (report.exit) process.exitCode = 1
}
