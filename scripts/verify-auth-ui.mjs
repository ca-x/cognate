import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { tmpdir } from "node:os"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" }, "self-test": { type: "boolean" }, "self-test-case": { type: "string" }, "focus-only": { type: "boolean" } } })
const terminalCases = { normal: { code: 0, signal: null }, nonzero: { code: 17, signal: null }, signal: { code: null, signal: "SIGABRT" }, timeout: null }
function finalizeReport(outcome, failure, details, directory) {
  if (!outcome || outcome.code !== 0 || outcome.signal !== null) failure ??= new Error(`fixture did not exit cleanly: ${JSON.stringify(outcome)}`)
  writeFileSync(join(directory, "report.json"), JSON.stringify({ ...details, passed: !failure, fixture: outcome, error: failure?.message }, null, 2), { mode: 0o600 })
  if (failure) throw failure
}
if (values["self-test-case"]) {
  const name = values["self-test-case"]
  assert(Object.hasOwn(terminalCases, name), "unknown terminal self-test case")
  assert(values.output, "terminal self-test needs output")
  mkdirSync(values.output, { recursive: true, mode: 0o700 })
  finalizeReport(terminalCases[name], undefined, { phase: "terminal-self-test" }, values.output)
  process.exit(0)
}
if (values["self-test"]) {
  const directory = values.output ? resolve(values.output) : mkdtempSync(join(tmpdir(), "auth-ui-terminal-self-test-"))
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  const results = []
  for (const [name, outcome] of Object.entries(terminalCases)) {
    const destination = mkdtempSync(join(directory, `${name}-`))
    const child = spawnSync(process.execPath, [resolve(process.argv[1]), "--self-test-case", name, "--output", destination], { encoding: "utf8", timeout: 5000 })
    const report = JSON.parse(readFileSync(join(destination, "report.json"), "utf8"))
    const expectedPass = name === "normal"
    results.push({ name, outcome, code: child.status, signal: child.signal, report })
    writeFileSync(join(directory, "self-test-report.json"), JSON.stringify(results, null, 2), { mode: 0o600 })
    assert.equal(child.signal, null, `${name}: self-test subprocess unexpectedly signaled`)
    assert.equal(child.status, expectedPass ? 0 : 1, `${name}: fixture termination must control top-level exit status`)
    assert.equal(report.passed, expectedPass, `${name}: fixture termination must control report.passed`)
    assert.deepEqual(report.fixture, outcome)
    if (!expectedPass) assert(report.error, `${name}: failed terminal outcome needs an error`)
  }
  console.log("PASS auth UI terminal self-test: 4 cases")
  process.exit(0)
}
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
assert(["before", "after"].includes(values.phase))
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestAuthUIBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_AUTH_UI_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `auth-ui-${process.pid}-${Date.now()}`
const checks = [], screenshots = [], layouts = [], keyboard = []
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure, locale = "en"
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} unsuccessful`)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 16000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
async function control(path, body) {
  const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body } : {}), signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200)
  return body === undefined ? response.json() : undefined
}
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
function fill(selector, value) { browser(["fill", selector, value]) }
function text() { return evaluate("document.body.innerText") }
function capture(name, full = true) {
  assert(evaluate("Array.from(document.querySelectorAll('input[type=password]')).every(input => input.value === '')"), "clear passwords before screenshots")
  browser(["screenshot", join(output, `${name}.png`), ...(full ? ["--full"] : [])])
  screenshots.push(name)
}
async function load(path = "/account") { browser(["open", ready.url + path]); await until(() => evaluate("!!document.querySelector('#auth-title')"), "auth screen did not mount") }
async function enterAccount() {
  fill("#username", "auth-fixture"); fill("#password", ready.password)
  click(locale === "en" ? "Sign in" : "登录")
  await until(() => evaluate("!!document.querySelector('.account-ui')"), "real login/GetSession did not reach account")
}
function geometry(context) {
  const result = evaluate(`(() => {
    const root = document.querySelector('.account-ui') || document.querySelector('main')
    const nodes = [...root.querySelectorAll('h1,h2,h3,p,button,input,dt,dd,a')]
    return {width: innerWidth, scrollWidth: document.documentElement.scrollWidth, outside: nodes.filter(node => {const box = node.getBoundingClientRect(); return box.width && box.height && (box.left < -1 || box.right > innerWidth + 1)}).map(node => node.tagName + ':' + node.textContent.trim().slice(0, 60))}
  })()`)
  layouts.push({ ...context, ...result })
  assert(result.scrollWidth <= result.width + 1, `horizontal overflow: ${JSON.stringify(context)}`)
  assert.deepEqual(result.outside, [], `clipped controls/text: ${JSON.stringify(context)}`)
}
async function preference(theme, language) {
  browser(["find", "role", "combobox", "click", "--name", locale === "en" ? "Theme" : "主题", "--exact"])
  browser(["find", "role", "option", "click", "--name", locale === "en" ? { dark: "Dark", light: "Light", system: "System" }[theme] : { dark: "深色", light: "浅色", system: "跟随系统" }[theme], "--exact"])
  browser(["find", "role", "combobox", "click", "--name", locale === "en" ? "Interface language" : "界面语言", "--exact"])
  browser(["find", "role", "option", "click", "--name", language === "en" ? "English" : "简体中文", "--exact"])
  locale = language
  assert.equal(evaluate("document.documentElement.lang"), language)
}
async function keyboardReveal(selector) {
  browser(["press", "Control+Home"])
  await until(() => evaluate("scrollY === 0"), "native Home scroll did not settle")
  for (let index = 0; index < 60; index++) {
    browser(["press", "Tab"])
    if (evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`)) {
      const visible = () => evaluate("(() => {const box = document.activeElement.getBoundingClientRect(); return box.top >= 6 && box.bottom <= innerHeight - 6 && getComputedStyle(document.activeElement).outlineStyle !== 'none'})()")
      let scrollSteps = 0
      while (!visible() && scrollSteps < 15) {
        browser(["press", evaluate("document.activeElement.getBoundingClientRect().top < 6") ? "PageUp" : "PageDown"])
        await delay(300)
        scrollSteps++
      }
      assert(visible(), "native Tab/PageDown must reveal focused action and outline")
      keyboard.push({ selector, scrollSteps })
      return
    }
  }
  assert.fail(`keyboard did not reach ${selector}`)
}
async function backgroundFocusChecks() {
  await control("mode", "enabled")
  await load()
  await until(() => text().includes("Sign in with OpenID Connect"), "focused auth options did not load")
  for (const screen of ["login", "account"]) {
    if (screen === "account") await enterAccount()
    for (const focusedAction of [true, false]) {
      await delay(31000)
      const selector = focusedAction ? screen === "login" ? '.auth-options > button[data-variant="outline"]' : '.account-method-action button[data-variant="default"]' : screen === "login" ? "#username" : '.topbar [role="combobox"]'
      if (!focusedAction && screen === "login") fill("#username", "background-draft")
      else await keyboardReveal(selector)
      assert(evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`), "background test did not acquire real keyboard focus")
      await control("mode", "options-error")
      evaluate("window.dispatchEvent(new Event('visibilitychange'))")
      await until(() => text().includes("Could not read sign-in methods"), "stale background error did not appear")
      assert(!text().includes(screen === "login" ? "Sign in with OpenID Connect" : "Link or verify OIDC identity"), "background failure must hide stale OIDC action")
      const expected = focusedAction ? ".auth-options-refresh" : selector
      assert(evaluate(`document.activeElement?.matches(${JSON.stringify(expected)})`), `${screen}: background settings error lost or stole keyboard focus`)
      if (!focusedAction && screen === "login") assert.equal(evaluate("document.querySelector('#username').value"), "background-draft")
      capture(`${screen}-background-error-${focusedAction ? "action" : "other"}-focus`)
      await control("mode", "enabled")
      if (focusedAction) browser(["press", "Enter"])
      else click("Retry sign-in methods")
      await until(() => text().includes(screen === "login" ? "Sign in with OpenID Connect" : "Link or verify OIDC identity"), "explicit retry did not restore known settings")
      assert(evaluate("document.activeElement?.matches('.auth-options-refresh')"), "background retry did not retain the stable trigger")
      checks.push(`${screen}: stale background failure ${focusedAction ? "returns removed action focus to refresh" : "does not steal unrelated focus"}; explicit retry recovers`)
    }
  }
  const receipts = await control("state")
  assert.equal(receipts.requests.filter(receipt => receipt.method === "StartOIDCLogin").length, 0, "focus recovery invoked or replayed OIDC")
  writeFileSync(join(output, "receipts.json"), JSON.stringify(receipts, null, 2), { mode: 0o600 })
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture did not become ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["set", "viewport", "1440", "1000"])
  await load()
  evaluate("localStorage.setItem('skillhub.locale','en'); localStorage.setItem('skillhub.theme','dark')")
  await load()
  if (values["focus-only"]) {
    await backgroundFocusChecks()
  } else {
  capture(`${values.phase}-login-dark`)
  if (values.phase === "before") {
    fill("#username", "auth-fixture"); fill("#password", ready.password); click("Sign in")
    await until(() => text().includes("Account and sign-in"), "baseline real account did not mount")
    capture("before-account-dark")
    browser(["set", "viewport", "320", "900"])
    evaluate("document.documentElement.style.fontSize = '200%'")
    capture("before-account-en-dark-320-200pct")
    click("Sign out")
    await until(() => evaluate("!!document.querySelector('#auth-title')"), "baseline logout failed")
    capture("before-login-en-dark-320-200pct")
    evaluate("document.documentElement.style.fontSize = '100%'")
    browser(["set", "viewport", "1440", "1000"])
  }
  await control("mode", "options-error")
  await load()
  await delay(6500)
  capture(`${values.phase}-login-options-error`)
  assert(text().includes("Could not read sign-in methods"), "login options failure must be explicit, not silently hide OIDC")
  await until(() => evaluate("!![...document.querySelectorAll('button')].find(node => node.textContent.includes('Retry sign-in methods'))"), "missing options retry")
  checks.push("RED→GREEN: explicit options failure and retry")
  await control("mode", "enabled")
  await keyboardReveal(".auth-options button")
  browser(["press", "Enter"])
  await until(() => text().includes("Sign in with OpenID Connect"), "retry did not read real enabled options")
  assert(evaluate("document.activeElement?.matches('.auth-options-refresh')"), "options retry lost keyboard focus")
  checks.push("keyboard retry reads production GetLoginOptions")
  await control("mode", "options-slow"); await load()
  await until(() => text().includes("Loading sign-in settings"), "missing real options loading state")
  capture("login-options-loading")
  await until(() => text().includes("Sign in with OpenID Connect"), "slow options did not finish")
  await control("mode", "oidc-error")
  click("Sign in with OpenID Connect")
  await until(() => evaluate("!!document.querySelector('.auth-options [role=alert]')"), "missing OIDC start error")
  capture("login-oidc-start-error")
  await control("mode", "enabled")
  click("Sign in with OpenID Connect")
  await until(() => text().includes("OpenID Connect sign-in did not finish"), "local provider cancellation did not return to login")
  capture("login-oidc-cancelled")
  checks.push("real OIDC Begin, local issuer cancellation and callback failure; no external provider")
  await load()
  fill("#username", "auth-fixture"); fill("#password", "wrong-fixture-password")
  click("Sign in")
  await until(() => evaluate("!!document.querySelector('form [role=alert]')"), "wrong password not rejected")
  fill("#password", ""); capture("login-wrong-password")
  await control("mode", "session-error")
  fill("#password", ready.password); click("Sign in")
  await until(() => evaluate("!!document.querySelector('form [role=alert]')"), "GetSession failure not shown")
  assert(evaluate("!!document.querySelector('#auth-title')"), "GetSession failure must not establish UI session")
  fill("#password", ""); capture("login-session-error")
  const failedLoginCount = (await control("state")).requests.filter(receipt => receipt.method === "Login").length
  await delay(500)
  assert.equal((await control("state")).requests.filter(receipt => receipt.method === "Login").length, failedLoginCount, "failed auth mutation automatically replayed")
  checks.push("wrong password rejected; failed GetSession never becomes authenticated UI")
  await control("mode", "enabled"); await load()
  click("Create account")
  const newUser = `auth-new-${Date.now()}`
  fill("#username", newUser); fill("#password", ready.password)
  click("Create account and sign in")
  await until(() => evaluate("!!document.querySelector('.account-ui')"), "real register→login→GetSession failed")
  assert(text().includes(newUser), "registered identity not shown")
  capture("registered-account")
  const registered = (await control("state")).requests.map(receipt => receipt.method)
  assert(registered.some((method, index) => method === "Register" && registered[index + 1] === "Login" && registered[index + 2] === "GetSession"), "registration RPC sequence changed")
  click("Sign out")
  await until(() => evaluate("!!document.querySelector('#auth-title')"), "sign out did not return to auth")
  await control("mode", "login-slow")
  fill("#username", "auth-fixture"); fill("#password", ready.password); click("Sign in")
  await until(() => evaluate("document.querySelector('form')?.getAttribute('aria-busy') === 'true'"), "login loading state missing")
  assert(evaluate("document.querySelector('form button[type=submit]').disabled"), "pending submit not locked")
  await until(() => evaluate("!!document.querySelector('.account-ui')"), "slow login did not complete")
  assert(text().includes((await control("state")).owner), "verified session user ID missing")
  checks.push("real SQLite registration, ordered login/GetSession, logout, pending submit and identity")
  await control("mode", "options-error")
  click("Refresh sign-in methods")
  await until(() => text().includes("Could not read sign-in methods"), "cached enabled options hid background error")
  assert(evaluate("document.activeElement?.matches('.auth-options-refresh')"), "account refresh failure lost focus")
  assert(!text().includes("Link or verify OIDC identity"), "binding must not use stale enabled options after error")
  capture("account-options-background-error")
  await control("mode", "disabled")
  click("Retry sign-in methods")
  await until(() => text().includes("has not enabled OpenID Connect"), "disabled account state missing")
  capture("account-oidc-disabled")
  await control("mode", "enabled")
  click("Refresh sign-in methods")
  await until(() => text().includes("Link or verify OIDC identity"), "enabled account did not refresh")
  await control("mode", "oidc-error")
  click("Link or verify OIDC identity")
  await until(() => evaluate("!!document.querySelector('.account-ui [role=alert]')"), "bind start failure missing")
  capture("account-bind-start-error")
  checks.push("cached options error is unknown, explicit retry recovers disabled, explicit binding failure")
  await control("mode", "oidc-error")
  for (const screen of ["account", "login"]) {
    if (screen === "login") { click(locale === "en" ? "Sign out" : "退出"); await until(() => evaluate("!!document.querySelector('#auth-title')"), "matrix logout failed") }
    for (const language of ["en", "zh-CN"]) for (const theme of ["dark", "light"]) {
      await preference(theme, language)
      for (const width of [375, 320]) for (const scale of [1, 2]) {
        browser(["set", "viewport", String(width), "900"])
        evaluate(`document.documentElement.style.fontSize = '${scale * 100}%'`)
        geometry({ screen, language, theme, width, textScale: scale })
        await keyboardReveal(screen === "account" ? '.account-method-action button[data-variant="default"]' : "form button[type=submit]")
        if (screen === "account") {
          browser(["press", "Enter"])
          await until(() => evaluate("!!document.querySelector('.account-method-action [role=alert]') && document.querySelector('.account-method-action button[data-variant=default]').getAttribute('aria-disabled') !== 'true'"), "keyboard binding failure did not settle")
          assert(evaluate("document.activeElement?.matches('.account-method-action button[data-variant=default]')"), "binding failure lost keyboard focus")
        }
        capture(`${screen}-${language}-${theme}-${width}-${scale * 100}pct-keyboard`, false)
        if (width === 320 && scale === 2 && theme === "light") capture(`${screen}-${language}-${theme}-${width}-200pct-overview`)
      }
    }
  }
  checks.push("32 real bilingual/theme/320+375/text-100+200% layouts; tab-revealed actions and focus rings")
  evaluate("document.documentElement.style.fontSize = '100%'")
  browser(["set", "viewport", "1440", "1000"])
  await preference("dark", "en")
  fill("#username", "draft-fixture")
  fill("#password", ready.password)
  await preference("light", "zh-CN")
  assert.equal(evaluate("document.querySelector('#username').value"), "draft-fixture", "preferences lost username draft")
  assert.equal(evaluate("document.querySelector('#password').value"), ready.password, "preferences lost password draft")
  assert(evaluate("document.activeElement?.getAttribute('role') === 'combobox'"), "preference selection did not restore trigger focus")
  await preference("system", "en")
  browser(["set", "media", "dark", "reduced-motion"])
  await until(() => evaluate("document.documentElement.dataset.theme === 'dark'"), "system dark theme did not update")
  browser(["set", "media", "light", "reduced-motion"])
  await until(() => evaluate("document.documentElement.dataset.theme === 'light'"), "system light theme did not update")
  assert(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), "reduced motion not active")
  await keyboardReveal("form button[type=submit]")
  assert(evaluate("getComputedStyle(document.activeElement).transitionDuration.split(',').every(value => parseFloat(value) === 0)"), "reduced motion still animates auth controls")
  fill("#username", ""); fill("#password", ""); capture("login-system-light-reduced-motion")
  await preference("dark", "en")
  await enterAccount()
  capture("account-dark-reduced-motion")
  const receipts = await control("state")
  assert(receipts.requests.some(receipt => receipt.method === "StartOIDCLogin" && receipt.linkCurrentAccount === true), "binding did not explicitly authorize current account")
  assert(receipts.requests.some(receipt => receipt.method === "StartOIDCLogin" && receipt.linkCurrentAccount === false), "normal OIDC login incorrectly links")
  assert(evaluate("Object.keys(localStorage).every(key => ['skillhub.theme','skillhub.locale'].includes(key))"), "unexpected persistent browser data")
  writeFileSync(join(output, "receipts.json"), JSON.stringify(receipts, null, 2), { mode: 0o600 })
  checks.push("draft survives preferences; live system theme; reduced motion; explicit binding bit; only preferences persist")
  }
} catch (error) {
  failure = error
  try { capture("failure") } catch {}
} finally {
  if (ready) { try { await control("stop", "") } catch {} }
  else fixture.kill("SIGTERM")
  const outcome = await Promise.race([terminal, delay(5000).then(() => null)])
  if (!outcome) fixture.kill("SIGKILL")
  try { browser(["close"]) } catch {}
  closeSync(log)
  finalizeReport(outcome, failure, { phase: values.phase, checks, screenshots, layouts, keyboard }, output)
}
if (failure) throw failure
console.log(`PASS auth UI: ${checks.length} checks, ${screenshots.length} screenshots, ${layouts.length} layouts`)
