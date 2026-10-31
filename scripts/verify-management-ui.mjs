import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: {
  "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" },
  "self-test": { type: "boolean" }, red: { type: "string" }, "skip-matrix": { type: "boolean" },
} })
const delay = (duration) => new Promise((done) => setTimeout(done, duration))

async function terminalResult(child, timeout) {
  let timer
  try {
    return await Promise.race([
      new Promise((done, reject) => {
        child.once("error", () => reject(new Error("fixture spawn failed")))
        child.once("close", (code, signal) => done({ code, signal }))
      }),
      new Promise((done, reject) => { timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error("fixture terminal timeout")) }, timeout) }),
    ])
  } finally { clearTimeout(timer) }
}

function acceptFixture(result, output) {
  assert.equal(result.signal, null, "fixture terminated by signal")
  assert.equal(result.code, 0, "fixture exited nonzero")
  assert.match(output, /MANAGEMENT_UI_STOP_RECEIPT/, "fixture stop receipt missing")
  assert.match(output, /--- PASS: TestManagementUIBrowserFixture /, "fixture test receipt missing")
  assert.doesNotMatch(output, /--- FAIL:|panic:|DATA RACE/, "fixture failure in output")
}

async function selfTest() {
  const good = "MANAGEMENT_UI_STOP_RECEIPT\n--- PASS: TestManagementUIBrowserFixture (0.01s)"
  const cases = [
    ["normal", "console.log(process.env.RECEIPT)", good, true],
    ["nonzero-after-receipt", "console.log(process.env.RECEIPT);process.exit(7)", good, false],
    ["signal-after-receipt", "console.log(process.env.RECEIPT);process.kill(process.pid,'SIGTERM')", good, false],
    ["missing-stop", "console.log('--- PASS: TestManagementUIBrowserFixture (0.01s)')", "", false],
    ["missing-pass", "console.log('MANAGEMENT_UI_STOP_RECEIPT')", "", false],
    ["failed-after-receipt", "console.log(process.env.RECEIPT);console.log('--- FAIL: fixture')", good, false],
    ["timeout", "setInterval(()=>{},1000)", "", false],
  ]
  const receipts = []
  for (const [name, source, receipt, expected] of cases) {
    const child = spawn(process.execPath, ["-e", source], { env: { ...process.env, RECEIPT: receipt }, stdio: ["ignore", "pipe", "ignore"] })
    let output = "", accepted = false
    child.stdout.on("data", (chunk) => { output += chunk })
    try { acceptFixture(await terminalResult(child, 400), output); accepted = true } catch { accepted = false }
    assert.equal(accepted, expected, `terminal self-test: ${name}`)
    receipts.push({ name, accepted, expected })
  }
  if (values.output) { mkdirSync(resolve(values.output), { recursive: true, mode: 0o700 }); writeFileSync(join(resolve(values.output), "terminal-self-tests.json"), JSON.stringify(receipts, null, 2), { mode: 0o600 }) }
  console.log(`fixture terminal self-tests: ${receipts.length}/${cases.length}`)
}

async function run() {
  assert.equal(process.versions.node, "26.12.0", "use the pinned Node runtime")
  for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
  const output = resolve(values.output)
  mkdirSync(output, { recursive: true, mode: 0o700 })
  const directory = mkdtempSync(join(output, "fixture-"))
  const home = join(directory, "home")
  mkdirSync(home, { mode: 0o700 })
  const logPath = join(output, "fixture.log")
  const log = openSync(logPath, "w", 0o600)
  const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestManagementUIBrowserFixture$", "-test.v", "-test.timeout=540s"], {
    env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_MANAGEMENT_UI_DIR: directory }, stdio: ["ignore", log, log],
  })
  const terminal = terminalResult(fixture, 550_000)
  let exited = false
  fixture.once("exit", () => { exited = true })
  terminal.catch(() => {})
  const session = `management-${process.pid}-${Date.now()}`
  const evidence = []
  let ready, failure, fixtureResult, browserStarted = false
  function browser(args, input) {
    const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 2 << 20 })
    assert.equal(result.status, 0, `browser command failed: ${args[0]}`)
    let parsed
    try { parsed = JSON.parse(result.stdout) } catch { assert.fail(`browser response invalid: ${args[0]}`) }
    assert.notEqual(parsed.success, false, `browser operation rejected: ${args[0]}`)
    return parsed.data
  }
  function evaluate(source) { const data = browser(["eval", "--stdin"], source); return data && Object.hasOwn(data, "result") ? data.result : data }
  async function until(check, label) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { assert(!exited, "fixture exited during browser work"); if (await check()) return; await delay(90) }; assert.fail(label) }
  function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
  async function saveLog() {
    click("Save settings")
    if (evaluate("!!document.querySelector('[role=dialog]')")) click("Save and allow cleanup")
  }
  function press(key) { browser(["press", key]) }
  async function tabTo(selector) {
    for (let count = 0; count < 90; count++) {
      if (evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`)) return
      press("Tab")
    }
    assert.fail("Tab did not reach the requested control")
  }
  function capture(name) {
    assert.equal(evaluate("!!document.querySelector('textarea[autocomplete=off]') || !!document.querySelector('input[type=password]')?.value"), false, "refusing screenshot with secret material")
    evaluate("document.scrollingElement.scrollTo({top:0,behavior:'instant'});true")
    browser(["screenshot", join(output, `${name}.png`), ...(evaluate("!!document.querySelector('[role=dialog]')") ? [] : ["--full"])])
    evidence.push({ screenshot: `${name}.png`, secretFree: true })
  }
  async function control(path, body) {
    const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(4000) })
    assert.equal(response.status, 200, `fixture control ${path}`)
    return body === undefined ? response.json() : undefined
  }
  async function mode(rpc = "", kind = "") { await control("mode", { RPC: rpc, Mode: kind }) }
  async function navigate(path) {
    browser(["focus", `a[href='${path}']`]); press("Enter")
    await until(() => evaluate(`location.pathname === ${JSON.stringify(path)} && !!document.querySelector('main h1')`), `route did not mount: ${path}`)
  }
  function textContains(text) { return evaluate(`document.body.textContent.includes(${JSON.stringify(text)})`) }
  function noOverflow(selector) {
    return evaluate(`(() => {
      const root = document.querySelector(${JSON.stringify(selector)}); if (!root) return false
      return document.documentElement.scrollWidth <= innerWidth + 1 && [...root.querySelectorAll('*')].every(node => {
        const style = getComputedStyle(node); const rect = node.getBoundingClientRect()
        if (style.display === 'none' || style.visibility === 'hidden' || !rect.width || !rect.height || node.closest('[data-radix-popper-content-wrapper]')) return true
        return rect.left >= -1 && rect.right <= innerWidth + 1 && (!['BUTTON','P','LABEL','LEGEND','H1','H2','H3'].includes(node.tagName) || node.scrollWidth <= node.clientWidth + 1)
      })
    })()`)
  }
  function wholeWords(selector) {
    return evaluate(`(() => {
      const root=document.querySelector(${JSON.stringify(selector)}); if(!root) return false
      const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT)
      while(walker.nextNode()) {
        const node=walker.currentNode, parent=node.parentElement
        if(!parent || parent.closest('code,textarea,[class*=font-mono]') || !parent.getBoundingClientRect().width || parent.closest('details:not([open])') && !parent.closest('summary')) continue
        for(const word of node.textContent.matchAll(/[A-Za-z]{4,}/g)) {
          const range=document.createRange(); range.setStart(node,word.index);range.setEnd(node,word.index+word[0].length)
          if([...range.getClientRects()].filter(rect=>rect.width>0).length>1) return {word:word[0],tag:parent.tagName,class:parent.className}
        }
      }
      return true
    })()`)
  }
  async function choose(label, option) {
    browser(["find", "role", "combobox", "click", "--name", label, "--exact"])
    browser(["find", "role", "option", "click", "--name", option, "--exact"])
  }
  async function login(username = "management-admin") {
    await until(() => evaluate("!!document.querySelector('#username')"), "login form did not mount")
    browser(["fill", "#username", username]); browser(["fill", "#password", "Synthetic-browser-only-42"]); browser(["click", "form button[type=submit]"])
    await until(() => !evaluate("!!document.querySelector('#password')"), "real login did not complete")
    if (!evaluate("location.pathname === '/mcp-settings'")) await navigate("/mcp-settings")
    await until(() => textContains("Your credentials"), "MCP page unavailable")
  }
  try {
    await until(() => existsSync(join(directory, "ready.json")), "fixture readiness timeout")
    ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
    assert.equal(new URL(ready.url).hostname, "127.0.0.1")
    browser(["open", `${ready.url}/mcp-settings`]); browserStarted = true
    evaluate("localStorage.setItem('skillhub.locale','en');localStorage.setItem('skillhub.theme','dark')")
    browser(["open", `${ready.url}/mcp-settings`])
    await login()
    await until(() => textContains("No MCP credentials yet"), "real empty state missing")
    capture(values.red ? "before-mcp-empty" : "mcp-empty-dark-en")
    if (values.red === "log-health") {
      await navigate("/server-logging"); await until(() => textContains("File retention"), "log policy missing")
      capture("before-logging-policy")
      browser(["fill", "input[id$=-maxBackups]", "9"])
      await mode("GetLogSettings", "error"); click("Reload and discard draft")
      if (evaluate("!!document.querySelector('[role=dialog]')")) click("Discard and reload")
      await until(() => textContains("Logging settings could not be read"), "log read error missing")
      assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "9")
      assert.equal(textContains("No logging failures recorded in this process."), false, "failed query must not claim current healthy status")
      return
    }
    await mode("GetLogSettings", "error"); await navigate("/server-logging")
    await until(() => textContains("Logging settings could not be read"), "initial log query failure missing")
    await mode("GetLogSettings", "slow"); click("Reload settings")
    await until(() => textContains("Loading logging settings…"), "real log loading state missing")
    await until(() => textContains("File retention"), "real log loading did not resolve")
    assert.equal(evaluate("document.activeElement !== document.body"), true)
    await mode("ListCredentials", "slow"); await navigate("/mcp-settings")
    await until(() => textContains("Loading credentials…"), "real token loading missing")
    await until(() => textContains("No MCP credentials yet"), "token loading did not resolve")
    await mode()
    evidence.push({ check: "real delayed reads, initial query failure and recovery with stable focus" })
    click("Create token")
    browser(["fill", "input[name=mcp-credential-name]", "Synthetic desktop assistant"])
    await tabTo("[role=checkbox]"); press("Space")
    assert.equal(evaluate("document.querySelector('[role=checkbox]').getAttribute('aria-checked')"), "true")
    await tabTo("form button[type=submit]"); press("Enter")
    await until(() => evaluate("!!document.querySelector('textarea[readonly]')"), "real one-time token missing")
    assert.equal(evaluate("(() => { window.__managementToken = document.querySelector('textarea[readonly]').value; return window.__managementToken.startsWith('smcp1_') })()"), true)
    assert.equal(evaluate("(async()=> (await fetch('/_fixture/authorize',{method:'POST',headers:{Authorization:'Bearer '+window.__managementToken,'X-Fixture-Scope':'catalog:read'}})).status===204)()"), true)
    assert.equal(evaluate("(async()=> (await fetch('/_fixture/authorize',{method:'POST',headers:{Authorization:'Bearer '+window.__managementToken,'X-Fixture-Scope':'catalog:write'}})).status===403)()"), true)
    evaluate("window.__managementClipboardWrite=navigator.clipboard.writeText;navigator.clipboard.writeText=async()=>{throw new DOMException('Synthetic denial','NotAllowedError')};true")
    click("Copy token")
    await until(() => textContains("Could not copy. Select the token"), "clipboard failure feedback missing")
    assert.equal(evaluate("!!document.querySelector('textarea[readonly]')?.value"), true)
    evaluate("navigator.clipboard.writeText=window.__managementClipboardWrite;delete window.__managementClipboardWrite;true")
    click("Saved; hide token")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "secret dialog did not close")
    await until(() => textContains("Synthetic desktop assistant"), "created metadata missing")
    await mode("ListCredentials", "error"); click("Reload metadata")
    await until(() => textContains("The service could not be reached."), "list query error missing")
    const blocked = evaluate("document.querySelector('button[aria-label^=\"Rotate token for\"]').disabled")
    assert.equal(blocked, true, "cached list actions must be blocked after metadata query failure")
    assert.equal(evaluate("document.activeElement !== document.body"), true, "list error lost focus")
    evidence.push({ check: "cached query failure blocks writes, retains metadata and stable focus" })
    if (values.red === "token-error") return
    await mode(); browser(["click", "section[aria-busy] > div > button"])
    await until(() => !textContains("The service could not be reached."), "list recovery failed")
    click("Rotate token for Synthetic desktop assistant")
    await until(() => textContains("Confirm rotation"), "rotation metadata unavailable")
    await tabTo("button[data-mcp-cancel]"); press("Escape")
    assert.equal(evaluate("document.activeElement?.getAttribute('aria-label')"), "Rotate token for Synthetic desktop assistant")
    click("Rotate token for Synthetic desktop assistant")
    await until(() => textContains("Confirm rotation"), "rotation metadata unavailable")
    click("Confirm rotation")
    await until(() => evaluate("!!document.querySelector('textarea[readonly]')"), "replacement missing")
    assert.equal(evaluate("(async()=> { const old=window.__managementToken; window.__managementToken=document.querySelector('textarea[readonly]').value; return old!==window.__managementToken && (await fetch('/_fixture/authorize',{method:'POST',headers:{Authorization:'Bearer '+old,'X-Fixture-Scope':'catalog:read'}})).status===403 })()"), true)
    click("Saved; hide token")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "rotation dialog stayed open")
    click("Revoke token for Synthetic desktop assistant")
    await until(() => textContains("Confirm revocation"), "revocation metadata unavailable")
    const beforeRevoke = (await control("state")).counts.RevokeCredential ?? 0
    press("Escape")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')") && evaluate("document.querySelector('.management-page section[aria-busy]')?.getAttribute('aria-busy') === 'false'"), "cancelled revocation did not settle")
    assert.equal(evaluate("document.activeElement?.getAttribute('aria-label')"), "Revoke token for Synthetic desktop assistant", "cancelled revocation lost its usable trigger")
    assert.equal((await control("state")).counts.RevokeCredential ?? 0, beforeRevoke, "cancelled revocation sent a mutation")
    await mode("ListCredentials", "slow")
    click("Revoke token for Synthetic desktop assistant")
    await until(() => textContains("Confirm revocation"), "second revocation review unavailable")
    click("Confirm revocation")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "revocation did not complete")
    assert.equal(evaluate("(async()=> { const status=(await fetch('/_fixture/authorize',{method:'POST',headers:{Authorization:'Bearer '+window.__managementToken,'X-Fixture-Scope':'catalog:read'}})).status; delete window.__managementToken; return status===403 })()"), true)
    await until(() => evaluate("document.querySelector('button[aria-label^=\"Revoke token for\"]').disabled"), "revoked actions not disabled")
    await until(() => evaluate("document.querySelector('.management-page section[aria-busy]')?.getAttribute('aria-busy') === 'false'"), "revocation metadata refresh did not settle")
    assert.equal(evaluate("document.activeElement?.matches('.management-page h1')"), true, "revocation did not restore focus to the stable heading")
    assert.equal((await control("state")).counts.RevokeCredential, beforeRevoke + 1, "revocation was replayed")
    evidence.push({ check: "revocation cancel restores its trigger without writing; slow successful refresh restores the stable heading after exactly one write" })
    await mode()
    await control("seed", {}); await delay(1200)
    await navigate("/server-logging"); await until(() => textContains("File retention"), "real log policy missing")
    browser(["fill", "input[id$=-maxBackups]", "9"])
    await mode("GetLogSettings", "error")
    click("Reload and discard draft")
    if (evaluate("!!document.querySelector('[role=dialog]')")) click("Discard and reload")
    await until(() => textContains("Logging settings could not be read"), "log read error missing")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "9", "failed query lost draft")
    assert.equal(textContains("No logging failures recorded in this process."), false, "failed query must not claim current healthy status")
    assert.equal(evaluate("document.activeElement !== document.body"), true, "log read error lost focus")
    if (values.red === "log-health") return
    await mode(); click("Reload and discard draft"); click("Discard and reload")
    await until(() => textContains("Current settings loaded."), "log recovery missing")
    browser(["fill", "input[id$=-maxBackups]", "8"])
    await mode("UpdateLogSettings", "conflict"); await saveLog()
    await until(() => textContains("Settings changed in another request."), "real CAS conflict missing")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "8")
    assert.equal(evaluate("document.querySelector('form button[type=submit]').disabled"), true)
    await mode(); click("Reload and discard draft"); click("Discard and reload")
    await until(() => textContains("Current settings loaded."), "CAS reconciliation failed")
    browser(["fill", "input[id$=-maxBackups]", "6"])
    await mode("UpdateLogSettings", "drop")
    const before = (await control("state")).counts.UpdateLogSettings ?? 0
    await saveLog()
    await until(() => textContains("The server did not confirm this change"), "lost receipt missing")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "6")
    assert.equal(evaluate("document.querySelector('form button[type=submit]').disabled"), true)
    press("Enter"); await delay(200)
    assert.equal((await control("state")).counts.UpdateLogSettings, before + 1, "unknown mutation replayed")
    await mode(); click("Reload and discard draft"); click("Discard and reload")
    await until(() => textContains("Current settings loaded."), "unknown reconciliation failed")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "6", "real committed write missing")
    browser(["fill", "input[id$=-retentionDays]", "21"]); await saveLog()
    await until(() => textContains("Logging settings saved."), "real settings save missing")
    evidence.push({ check: "real TLS/Connect/SQLite token lifecycle; log CAS, committed lost receipt and explicit reconciliation" })
    browser(["fill", "input[id$=-maxBackups]", "19"])
    await choose("Interface language", "简体中文")
    await choose("主题", "深色")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "19", "locale/theme change lost log draft")
    await choose("界面语言", "English")
    click("Reload and discard draft"); press("Escape")
    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "19", "cancel discarded draft")
    click("Reload and discard draft"); click("Discard and reload")
    await until(() => textContains("Current settings loaded."), "draft reconciliation failed")
    await control("health-degraded", {}); click("Reload settings")
    await until(() => textContains("Logging is degraded."), "real degraded health missing")
    assert.equal(evaluate("[...document.querySelectorAll('.management-health dd')].at(-1).textContent"), "1")
    capture("logging-degraded-dark-en")
    await navigate("/mcp-settings")
    await until(() => textContains("Fixture expired credential"), "seed metadata missing")
    assert.equal(textContains("Expired"), true)
    assert.equal(evaluate("document.querySelector('button[aria-label=\"Rotate token for Fixture expired credential\"]').disabled"), true)
    await choose("Theme", "System")
    click("Edit metadata for Fixture expired credential")
    await until(() => evaluate("!!document.querySelector('input[name=mcp-credential-name]')"), "editor missing")
    browser(["fill", "input[name=mcp-credential-name]", "Preserved draft — 中英"])
    browser(["set", "media", "light"])
    browser(["set", "viewport", "375", "900"])
    assert.equal(evaluate("document.querySelector('input[name=mcp-credential-name]').value"), "Preserved draft — 中英")
    await mode("GetCredential", "error")
    evaluate("window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));true")
    await until(() => textContains("The service could not be reached."), "background detail query did not fail")
    assert.equal(evaluate("document.querySelector('input[name=mcp-credential-name]').value"), "Preserved draft — 中英", "background query failure lost token draft")
    assert.equal(evaluate("document.querySelector('form button[type=submit]').disabled"), true)
    await mode(); click("Reload metadata")
    await until(() => !textContains("The service could not be reached."), "background query recovery failed")
    assert.equal(evaluate("document.querySelector('input[name=mcp-credential-name]').value"), "Preserved draft — 中英")
    press("Escape")
    browser(["set", "viewport", "1440", "960"])
    await mode("GetCredential", "error"); click("Edit metadata for Fixture expired credential")
    await until(() => textContains("The service could not be reached."), "detail failure missing")
    await mode(); click("Reload metadata")
    await until(() => evaluate("!!document.querySelector('input[name=mcp-credential-name]')"), "detail recovery missing")
    assert.equal(evaluate("document.activeElement !== document.body && !!document.activeElement.closest('[role=dialog]')"), true)
    press("Escape")
    await mode("CreateCredential", "drop"); click("Create token")
    browser(["fill", "input[name=mcp-credential-name]", "Synthetic uncertain create"])
    await tabTo("[role=checkbox]"); press("Space")
    const beforeCreate = (await control("state")).counts.CreateCredential ?? 0
    await tabTo("form button[type=submit]"); press("Enter")
    await until(() => textContains("The server did not confirm the operation."), "token unknown result missing")
    assert.equal(evaluate("document.querySelector('input[name=mcp-credential-name]').value"), "Synthetic uncertain create")
    press("Enter"); await delay(100)
    assert.equal((await control("state")).counts.CreateCredential, beforeCreate + 1, "token mutation replayed")
    await mode(); click("Close and reload metadata")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')") && textContains("Synthetic uncertain create"), "committed unknown credential missing")
    click("Edit metadata for Synthetic uncertain create")
    await until(() => evaluate("!!document.querySelector('input[name=mcp-credential-name]')"), "CAS editor missing")
    browser(["fill", "input[name=mcp-credential-name]", "CAS draft retained"])
    await control("bump-token", { Name: "Synthetic uncertain create" })
    browser(["click", "form button[type=submit]"])
    await until(() => textContains("The write was not confirmed and has not been retried."), "real token CAS error missing")
    assert.equal(evaluate("document.querySelector('input[name=mcp-credential-name]').value"), "CAS draft retained")
    assert.equal(evaluate("document.querySelector('form button[type=submit]').disabled"), true)
    click("Close and reload metadata")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "CAS close failed")
    click("View audit metadata for Synthetic uncertain create")
    await until(() => textContains("Create credential"), "real token audit missing")
    press("Escape")
    evidence.push({ check: "token background query preserves draft; actual CAS rejects stale write; committed Unknown never replays; audit contains metadata only" })
    if (!values["skip-matrix"]) {
      for (const locale of ["en", "zh-CN"]) {
        await choose(evaluate("document.documentElement.lang") === "en" ? "Interface language" : "界面语言", locale === "en" ? "English" : "简体中文")
        for (const preference of ["light", "dark", "system"]) {
          await choose(locale === "en" ? "Theme" : "主题", locale === "en" ? { light: "Light", dark: "Dark", system: "System" }[preference] : { light: "浅色", dark: "深色", system: "跟随系统" }[preference])
          for (const media of preference === "system" ? ["light", "dark"] : [preference]) {
            browser(["set", "media", media])
            assert.equal(evaluate("document.documentElement.dataset.theme"), media)
            for (const reduced of [false, true]) {
              browser(["set", "media", media, ...(reduced ? ["reduced-motion"] : [])])
              assert.equal(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), reduced, "reduced-motion emulation did not match the matrix")
              for (const [width, font] of [[1440, 100], [375, 100], [320, 100], [375, 200], [320, 200]]) {
                browser(["set", "viewport", String(width), "900"])
                evaluate(`document.documentElement.style.fontSize='${font}%'`)
                for (const path of ["/mcp-settings", "/server-logging"]) {
                  if (evaluate("location.pathname") !== path) await navigate(path)
                  await until(() => evaluate("!!document.querySelector('.management-page')"), "management page missing")
                  evaluate("document.querySelector('.management-page h1').focus();true")
                  assert.equal(noOverflow(".management-page"), true, `reflow ${path}/${locale}/${preference}/${media}/${reduced}/${width}/${font}`)
                  assert.equal(wholeWords(".management-page"), true, `split English word ${path}/${locale}/${width}/${font}`)
                  if (path === "/mcp-settings") {
                    await tabTo(".management-page .page-heading button"); press("Enter")
                    await until(() => evaluate("!!document.querySelector('input[name=mcp-credential-name]')"), "matrix dialog missing")
                    assert.equal(noOverflow(".management-dialog"), true, `dialog reflow ${locale}/${width}/${font}`)
                    assert.equal(wholeWords(".management-dialog"), true, `split dialog English word ${locale}/${width}/${font}`)
                    await tabTo("[role=checkbox]"); press("Space")
                    assert.equal(evaluate("document.activeElement.getAttribute('aria-checked')"), "true")
                    assert.equal(evaluate("getComputedStyle(document.activeElement).outlineStyle !== 'none'"), true)
                    assert.equal(evaluate("(() => {const rect=document.activeElement.getBoundingClientRect();return rect.top>=0&&rect.bottom<=innerHeight})()"), true)
                    if (width === 320 && font === 200 && !reduced && preference === "dark") capture(`permissions-${locale}-dark-320-200`)
                    if (reduced) assert.equal(evaluate("getComputedStyle(document.querySelector('[role=dialog]')).animationName"), "none")
                    press("Escape")
                    assert.equal(evaluate("document.activeElement !== document.body && !document.activeElement.disabled"), true)
                    await tabTo(".management-disclosure summary"); press("Enter")
                    assert.equal(evaluate("document.querySelector('.management-disclosure').open"), true)
                    assert.equal(wholeWords(".management-page"), true, `expanded permissions word reflow ${locale}/${width}/${font}`)
                    press("Enter")
                  } else {
                    browser(["fill", "input[id$=-maxBackups]", "19"])
                    await tabTo(".management-page .management-actions button:last-child"); press("Enter")
                    assert.equal(noOverflow(".management-dialog"), true, `confirmation reflow ${locale}/${width}/${font}`)
                    await tabTo("[data-management-cancel]"); press("Space")
                    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "Space did not cancel reload")
                    assert.equal(evaluate("document.querySelector('input[id$=-maxBackups]').value"), "19")
                    browser(["fill", "input[id$=-maxBackups]", "6"])
                    evaluate("document.querySelector('.management-page h1').focus();true")
                    await tabTo("input[id$=-maxBackups]")
                    assert.equal(evaluate("(() => {const rect=document.activeElement.getBoundingClientRect();return rect.top>=0&&rect.bottom<=innerHeight})()"), true)
                  }
                  evidence.push({ path, locale, preference, media, reduced, width, rootFontPercent: font, reflow: true, wholeWords: true, keyboard: true })
                  if (width === 375 && font === 200 && !reduced && preference !== "system") capture(`${path.slice(1)}-${locale}-${preference}-375-200`)
                }
              }
            }
          }
        }
      }
    }
    evaluate("document.documentElement.style.fontSize='100%'"); browser(["set", "viewport", "1440", "960"])
    await choose(evaluate("document.documentElement.lang") === "en" ? "Interface language" : "界面语言", "English")
    await navigate("/server-logging")
    await control("health-failure", {}); click("Reload settings")
    await until(() => textContains("Logging settings could not be read"), "real closed logger error missing")
    assert.equal(textContains("No logging failures recorded in this process."), false)
    capture("logging-health-unavailable")
    browser(["open", `${ready.url}/mcp-settings`]); await login("management-member")
    await navigate("/server-logging")
    await until(() => textContains("Administrator access required"), "member restriction missing")
    assert.equal(evaluate("!!document.querySelector('main form')"), false)
    capture("logging-member-restricted")
    assert.equal(evaluate("[...Object.keys(localStorage),...Object.keys(sessionStorage)].every(key=>!/(token|secret|password)/i.test(key)) && !/smcp1_/.test(JSON.stringify({...localStorage,...sessionStorage}))"), true)
    evidence.push({ check: "ordinary member denied by production authorization; no secrets in browser storage" })
  } catch (error) {
    failure = error
    if (browserStarted) {
      try {
        evidence.push({ failureState: evaluate("({path:location.pathname,width:innerWidth,rootFont:getComputedStyle(document.documentElement).fontSize,activeTag:document.activeElement?.tagName,main:!!document.querySelector('main'),dialog:!!document.querySelector('[role=dialog]')})") })
        if (!evaluate("!!document.querySelector('textarea[autocomplete=off]') || !!document.querySelector('input[type=password]')?.value")) capture("failure-no-secret")
      } catch {}
    }
  }
  finally {
    if (browserStarted) { try { browser(["close"]) } catch { failure ??= new Error("browser cleanup failed") } }
    try {
      if (ready && !exited) await control("stop", {})
      fixtureResult = await Promise.race([terminal, delay(10000).then(() => { throw new Error("fixture stop timeout") })])
      acceptFixture(fixtureResult, readFileSync(logPath, "utf8"))
    } catch (error) { failure ??= error; fixture.kill("SIGKILL") }
    closeSync(log)
    writeFileSync(join(output, "receipts.json"), JSON.stringify({ exit: failure ? 1 : 0, fixture: fixtureResult, evidence, failure: failure ? String(failure.message) : undefined }, null, 2), { mode: 0o600 })
    if (failure) throw failure
  }
  console.log(`management UI: ${evidence.length} evidence receipts; fixture cleanly stopped`)
}

try { if (values["self-test"]) await selfTest(); else await run() }
catch (error) { console.error(error.message); process.exitCode = 1 }
