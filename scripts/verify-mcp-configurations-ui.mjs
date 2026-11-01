import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, "self-test": { type: "boolean" }, "zoom-only": { type: "boolean" } } })
const delay = duration => new Promise(done => setTimeout(done, duration))
function terminalResult(child, timeout) {
  return new Promise(resolve => {
    let expired = false, spawnError = false
    const timer = setTimeout(() => { expired = true; child.kill("SIGKILL") }, timeout)
    child.once("error", () => { spawnError = true })
    child.once("close", (code, signal) => { clearTimeout(timer); resolve({ code, signal, expired, spawnError, waited: true }) })
  })
}
function acceptFixture(result, output) {
  assert(result.waited && !result.expired && !result.spawnError, "fixture did not complete within its owned lifetime")
  assert.equal(result.signal, null, "fixture terminated by signal")
  assert.equal(result.code, 0, "fixture exited nonzero")
  assert.match(output, /MCP_CONFIGURATIONS_STOP_RECEIVED APP_CLOSE_OK SERVE_WORKERS_JOINED/, "explicit stop/real App join receipt missing")
  assert.match(output, /--- PASS: TestMCPConfigurationsBrowserFixture /, "test PASS absent")
  assert.doesNotMatch(output, /--- FAIL:|panic:|DATA RACE/, "contradictory fixture failure")
}
async function selfTest() {
  const good = "MCP_CONFIGURATIONS_STOP_RECEIVED APP_CLOSE_OK SERVE_WORKERS_JOINED\n--- PASS: TestMCPConfigurationsBrowserFixture (0.01s)"
  const receipts = []
  for (const [name, source, receipt, expected] of [
    ["normal", "console.log(process.env.RECEIPT)", good, true],
    ["nonzero", "console.log(process.env.RECEIPT);process.exit(7)", good, false],
    ["signal", "console.log(process.env.RECEIPT);process.kill(process.pid,'SIGTERM')", good, false],
    ["missing-stop", "console.log('--- PASS: TestMCPConfigurationsBrowserFixture (0.01s)')", "", false],
    ["missing-pass", "console.log('MCP_CONFIGURATIONS_STOP_RECEIVED APP_CLOSE_OK SERVE_WORKERS_JOINED')", "", false],
    ["contradictory", "console.log(process.env.RECEIPT);console.log('--- FAIL: fixture')", good, false],
    ["timeout", "setInterval(()=>{},1000)", "", false],
  ]) {
    const child = spawn(process.execPath, ["-e", source], { env: { RECEIPT: receipt }, stdio: ["ignore", "pipe", "ignore"] })
    const terminal = terminalResult(child, 700)
    let output = "", accepted = false
    child.stdout.on("data", chunk => { output += chunk })
    const result = await terminal
    try { acceptFixture(result, output); accepted = true } catch {}
    assert.equal(accepted, expected, `terminal self-test: ${name}`)
    receipts.push({ name, result, accepted, expected })
  }
  if (values.output) { mkdirSync(resolve(values.output), { recursive: true, mode: 0o700 }); writeFileSync(join(resolve(values.output), "terminal-self-tests.json"), JSON.stringify(receipts, null, 2), { mode: 0o600 }) }
  console.log("MCP browser terminal gates PASS 7/7")
}

async function run() {
  for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
  const output = resolve(values.output)
  mkdirSync(output, { recursive: true, mode: 0o700 })
  const directory = mkdtempSync(join(output, "fixture-"))
  const home = mkdtempSync(join(output, "browser-home-"))
  const socketDirectory = mkdtempSync("/tmp/cwm-")
  const browserEnvironment = { PATH: process.env.PATH, HOME: home, LANG: "C.UTF-8", TMPDIR: process.env.TMPDIR ?? directory, XDG_CONFIG_HOME: join(home, "config"), XDG_CACHE_HOME: join(home, "cache"), AGENT_BROWSER_SOCKET_DIR: socketDirectory }
  const logPath = join(output, "fixture.log"), log = openSync(logPath, "wx", 0o600)
  const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestMCPConfigurationsBrowserFixture$", "-test.v", "-test.timeout=13m"], { env: { PATH: process.env.PATH, HOME: home, LANG: "C.UTF-8", TMPDIR: process.env.TMPDIR ?? directory, COGNATE_MCP_CONFIGURATIONS_UI_DIR: directory }, stdio: ["ignore", log, log] })
  const terminal = terminalResult(fixture, 13 * 60_000)
  const session = `mcp-config-${process.pid}-${Date.now()}`, events = [], layouts = []
  let ready, failure, fixtureResult, started = false, language = "en", stopped = false, zoomConnection
  const record = (name, data = {}) => { events.push({ name, pass: true, ...data }); writeFileSync(join(output, "events.json"), JSON.stringify(events, null, 2), { mode: 0o600 }) }
  function browser(args, input) {
    const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", values["zoom-only"] ? "127.0.0.1,settings" : "127.0.0.1", "--ignore-https-errors", "--json", ...args], { env: browserEnvironment, input, encoding: "utf8", timeout: 30_000, maxBuffer: 4 << 20 })
    assert.equal(result.status, 0, `browser command failed: ${args[0]} ${result.stderr.slice(0, 300)}`)
    const parsed = JSON.parse(result.stdout)
    assert.notEqual(parsed.success, false, `browser operation failed: ${args[0]} ${JSON.stringify(parsed.error)}`)
    return parsed.data
  }
  function evaluate(source) { const data = browser(["eval", "--stdin"], source); return data && Object.hasOwn(data, "result") ? data.result : data }
  async function until(check, message) { const deadline = Date.now() + 15_000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
  async function control(path, body) {
    const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(8000) })
    assert.equal(response.status, 200, `fixture control: ${path}`)
    return body === undefined ? response.json() : response.text()
  }
  const mode = (Mode = "normal", Procedure = "") => control("mode", { Mode, Procedure })
  const text = () => evaluate("document.body.innerText")
  const click = name => browser(["find", "role", "button", "click", "--name", name, "--exact"])
  function field(label, scope = "[role=dialog]") {
    const id = evaluate(`[...document.querySelector(${JSON.stringify(scope)}).querySelectorAll('label')].find(node=>node.textContent===${JSON.stringify(label)})?.htmlFor`)
    assert(id, `field label missing: ${label}`)
    return `[id=${JSON.stringify(id)}]`
  }
  function fill(label, value, scope) {
    const selector = field(label, scope)
    if (value === "") { browser(["focus", selector]); browser(["press", "Control+a"]); browser(["press", "Backspace"]) }
    else browser(["fill", selector, value])
    assert.equal(evaluate(`document.querySelector(${JSON.stringify(selector)}).value`), value, `input did not update: ${label}`)
  }
  function toggle(label, scope) { browser(["click", field(label, scope)]) }
  function choose(label, option, scope) {
    browser(["click", field(label, scope)])
    browser(["find", "role", "option", "click", "--name", option, "--exact"])
  }
  function scopedClick(scope, name, last = false) {
    const selector = evaluate(`(() => {const matches=[...document.querySelector(${JSON.stringify(scope)}).querySelectorAll('button')].filter(node=>node.textContent===${JSON.stringify(name)});const node=matches[${last ? "matches.length-1" : "0"}];if(!node)return null;node.dataset.probeAction='current';return '[data-probe-action=current]'})()`)
    assert(selector, `button missing: ${name}`)
    browser(["click", selector]); evaluate("document.querySelector('[data-probe-action]')?.removeAttribute('data-probe-action')")
  }
  function markServers() { evaluate("[...document.querySelectorAll('[data-mcp-server]')].forEach((node,index)=>node.dataset.probeServer=index)") }
  const serverScope = index => `[data-probe-server="${index}"]`
  const slot = ref => `[data-mcp-slot=${JSON.stringify(ref)}]`
  async function row(name, action) {
    const id = evaluate(`[...document.querySelectorAll('[data-mcp-asset]')].find(node=>node.querySelector('h3').textContent===${JSON.stringify(name)})?.dataset.mcpAsset`)
    assert(id, `asset row missing: ${name}`)
    scopedClick(`[data-mcp-asset="${id}"]`, action)
    await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "dialog missing")
    if (action === "Edit MCP asset") await until(() => evaluate("!!document.querySelector('.mcp-form')"), "sanitized editor missing")
    return id
  }
  async function review() {
    click("Logical preview")
    await until(() => evaluate("!!document.querySelector('.mcp-review')") && text().includes("Logical preview reviewed"), "logical preview missing")
    toggle("I reviewed this exact draft and credential actions")
  }
  async function saved(name) {
    await until(async () => (await control("state")).assets.some(asset => asset.name === name), "real SQLite save missing")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "save did not close editor")
    await until(() => text().includes(name), "saved list not refreshed")
  }
  async function closeDraft() {
    click(language === "en" ? "Close dialog" : "关闭对话框")
    await delay(150)
    if (evaluate("document.querySelectorAll('[role=dialog]').length>1")) click(language === "en" ? "Discard draft" : "放弃草稿")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "draft did not close")
  }
  function capture(name) {
    assert(evaluate("[...document.querySelectorAll('input[type=password]')].every(node=>node.value==='')"), "refuse secret screenshot")
    browser(["screenshot", join(output, `${name}.png`)])
  }
  async function readback() {
    click("Discard draft and read current state")
    await until(() => evaluate("document.querySelectorAll('[role=dialog]').length===2"), "readback confirmation absent")
    assert.equal(evaluate("document.activeElement.textContent"), "Continue editing")
    browser(["press", "Escape"])
    assert.equal(evaluate("document.activeElement.textContent"), "Discard draft and read current state", "readback cancel lost focus")
    click("Discard draft and read current state")
    scopedClick('[role=dialog][data-state=open]:last-of-type', "Discard draft and read current state")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')") && text().includes("Current metadata read"), "explicit readback did not reconcile")
  }
  async function prepareRealZoom() {
    const { cdpUrl } = browser(["get", "cdp-url"])
    assert.match(cdpUrl, /^ws:\/\/127\.0\.0\.1:\d+\/devtools\/browser\//, "CDP is not the owned local browser")
    const socket = new WebSocket(cdpUrl)
    await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }) })
    let sequence = 0
    const pending = new Map()
    socket.addEventListener("message", event => {
      const message = JSON.parse(event.data), current = pending.get(message.id)
      if (!current) return
      pending.delete(message.id); clearTimeout(current.timer)
      if (message.error) current.reject(new Error(`CDP method rejected: ${current.method}`))
      else current.resolve(message.result)
    })
    const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
      const id = ++sequence, timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP method timed out: ${method}`)) }, 5000)
      pending.set(id, { resolve, reject, timer, method }); socket.send(JSON.stringify({ id, method, params, sessionId }))
    })
    const close = async () => {
      for (const current of pending.values()) { clearTimeout(current.timer); current.reject(new Error("owned CDP closed")) }
      pending.clear()
      if (socket.readyState !== WebSocket.CLOSED) await new Promise(resolve => { socket.addEventListener("close", resolve, { once: true }); socket.close() })
    }
    zoomConnection = { close }
    const targets = await send("Target.getTargets")
    const target = targets.targetInfos.find(target => target.type === "page" && target.url.startsWith(ready.url))
    assert(target, "owned App CDP page missing")
    const { sessionId } = await send("Target.attachToTarget", { targetId: target.targetId, flatten: true })
    const { windowId } = await send("Browser.getWindowForTarget", { targetId: target.targetId })
    const setWindow = async (width, height) => {
      await send("Browser.setWindowBounds", { windowId, bounds: { windowState: "normal", width, height } })
      await send("Emulation.clearDeviceMetricsOverride", {}, sessionId)
    }
    const metrics = () => evaluate("({outerWidth,outerHeight,innerWidth,innerHeight,dpr:devicePixelRatio,pinch:visualViewport.scale,rootFont:getComputedStyle(document.documentElement).fontSize,cssZoom:getComputedStyle(document.documentElement).zoom})")
    await setWindow(1280, 1000)
    const before = metrics()
    assert.equal(before.rootFont, "16px"); assert.equal(before.dpr, 1); assert.equal(before.pinch, 1)
    browser(["open", "chrome://settings/appearance"])
    assert.equal(evaluate("new Promise(resolve=>chrome.settingsPrivate.getDefaultZoom(resolve))"), 1)
    evaluate("chrome.settingsPrivate.setDefaultZoom(2)")
    await until(() => evaluate("new Promise(resolve=>chrome.settingsPrivate.getDefaultZoom(resolve))") === 2, "Chrome native default zoom was not set to 200%")
    browser(["open", `${ready.url}/configuration?section=mcp`])
    const after = metrics()
    assert.equal(after.rootFont, "16px"); assert.equal(after.cssZoom, "1"); assert.equal(after.pinch, 1); assert.equal(after.dpr, 2)
    assert(Math.abs(after.innerWidth * 2 - before.innerWidth) <= 2, "native layout zoom did not halve CSS viewport")
    record("native Chrome zoom 100% -> 200%, verified independently of CSS fonts, CSS zoom, pinch and device emulation", { before, after, browser: await send("Browser.getVersion") })
    return { setWindow, metrics }
  }
  try {
    await until(() => existsSync(join(directory, "ready.json")), "real App readiness missing")
    ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
    const login = await control("login")
    browser(["open", `${ready.url}/configuration?section=mcp`]); started = true
    const realZoom = values["zoom-only"] ? await prepareRealZoom() : undefined
    browser(["snapshot", "-i"])
    evaluate("localStorage.setItem('skillhub.locale','en')")
    browser(["reload"])
    await until(() => evaluate("!!document.querySelector('#username')"), "login form missing")
    browser(["fill", "#username", login.username]); browser(["fill", "#password", login.password]); click("Sign in")
    await until(() => text().includes("Seed private MCP"), "owner MCP list missing")
    if (realZoom) {
      await row("Seed private MCP", "Edit MCP asset"); fill("Asset name", "Native zoom retained MCP")
      for (const width of [1280, 750]) {
        await realZoom.setWindow(width, 1000)
        const metrics = realZoom.metrics()
        const layout = evaluate("(() => {const editor=document.querySelector('.mcp-editor'),box=editor.getBoundingClientRect();return {document:document.documentElement.scrollWidth,client:editor.clientWidth,scroll:editor.scrollWidth,left:box.left,right:box.right,draft:[...editor.querySelectorAll('input')].some(node=>node.value==='Native zoom retained MCP')}})()")
        assert.equal(metrics.dpr, 2); assert.equal(metrics.pinch, 1); assert.equal(metrics.rootFont, "16px"); assert.equal(metrics.cssZoom, "1")
        assert(Math.abs(metrics.innerWidth * 2 - width) <= 2, "real window/CSS viewport ratio is not 200%")
        assert(layout.document <= metrics.innerWidth + 1 && layout.scroll <= layout.client + 1 && layout.left >= 0 && layout.right <= metrics.innerWidth + 1 && layout.draft, "native 200% zoom clips or loses draft")
        layouts.push({ type: "native Chrome default zoom 2; CDP real window; no emulated viewport/font mutation", windowWidth: width, metrics, layout })
        capture(`native-zoom-200-window-${width}`)
      }
      await review()
      browser(["focus", '.mcp-form button[type=submit]'])
      assert(evaluate("(() => {const node=document.activeElement,box=node.getBoundingClientRect();return node.matches('.mcp-form button[type=submit]')&&box.top>=0&&box.bottom<=innerHeight})()"), "native zoom save control unreachable")
      capture("native-zoom-200-save-controls")
      click("Save private asset"); await saved("Native zoom retained MCP")
      assert.equal((await control("state")).assets.find(asset => asset.name === "Native zoom retained MCP").revision, 2)
      assert(evaluate("document.activeElement!==document.body && !document.activeElement.matches(':disabled')"), "native zoom lost completion focus")
      record("native 200% zoom, physical windows 1280/750 -> CSS 640/375, retained draft, reachable preview/save, real SQLite CAS and restored focus")
      writeFileSync(join(output, "layouts.json"), JSON.stringify(layouts, null, 2), { mode: 0o600 })
      click("Sign out")
      assert.deepEqual(browser(["errors"]).errors, [], "native zoom browser runtime errors")
    } else {
    assert(!text().includes("Foreign private MCP"), "foreign asset leaked")
    const listState = await control("state")
    assert.equal(listState.requests["/skillhub.configuration.mcp.v1.MCPConfigurationService/ReadDraft"] ?? 0, 0, "listing read secret drafts eagerly")
    record("owner-only metadata; direct existing-route reload; no eager secret draft")
    await row("Unknown private MCP", "Read metadata")
    assert(!evaluate("!!document.querySelector('.mcp-form')"))
    await until(() => text().includes("preserved read-only"), "Unknown schema not readonly")
    await closeDraft()
    assert.equal((await control("state")).requests["/skillhub.configuration.mcp.v1.MCPConfigurationService/ReadDraft"] ?? 0, 0)
    record("Unknown schema metadata-only, no ReadDraft/conversion")

    browser(["find", "role", "link", "click", "--name", "AI configurations", "--exact"])
    await until(() => text().includes("Manage private MCP connection assets"), "provider back-link missing")
    browser(["find", "role", "link", "click", "--name", "Manage private MCP connection assets", "--exact"])
    await until(() => text().includes("Seed private MCP"), "MCP route return missing")
    await row("Seed private MCP", "Edit MCP asset"); fill("Asset name", "Navigation retained draft")
    browser(["back"])
    await until(() => evaluate("document.querySelectorAll('[role=dialog]').length===2"), "dirty history navigation was not blocked")
    assert.equal(evaluate("document.activeElement.textContent"), "Continue editing")
    browser(["press", "Escape"])
    assert(evaluate("[...document.querySelectorAll('.mcp-form input')].some(node=>node.value==='Navigation retained draft')"))
    browser(["back"])
    await until(() => evaluate("document.querySelectorAll('[role=dialog]').length===2"), "second dirty navigation not blocked")
    click("Discard draft")
    await until(() => evaluate("!!document.querySelector('.configuration-ui')"), "explicit navigation discard did not proceed")
    browser(["find", "role", "link", "click", "--name", "Manage private MCP connection assets", "--exact"])
    await until(() => text().includes("Seed private MCP"), "MCP return after discard missing")
    record("dirty history navigation blocks; Continue keeps draft; explicit Discard proceeds")

    click("New MCP asset"); fill("Asset name", "Browser ordered MCP")
    click("Add server"); markServers()
    fill("Server ID", "local", serverScope(0)); fill("Native server name", "browser-local", serverScope(0)); fill("Command declaration", "synthetic-not-executed", serverScope(0))
    toggle("Declare argument collection (including empty)", serverScope(0)); scopedClick(serverScope(0), "Add argument")
    fill("Argument ID", "first", '[data-mcp-row="0-args-0"]')
    scopedClick(serverScope(0), "Add argument"); fill("Argument ID", "second", '[data-mcp-row="0-args-1"]')
    scopedClick('[data-mcp-row="0-args-1"]', "Move up")
    assert(evaluate("document.activeElement.matches('[data-mcp-row=\"0-args-0\"]')"), "argument reorder focus lost")
    toggle("Declare environment collection (including empty)", serverScope(0)); scopedClick(serverScope(0), "Add environment reference"); fill("Environment name", "TOKEN", '[data-mcp-row="0-env-0"]')
    click("Add server"); markServers()
    fill("Server ID", "remote", serverScope(1)); fill("Native server name", "browser-http", serverScope(1)); choose("Transport", "HTTP", serverScope(1)); fill("Remote URL", "https://mcp.invalid/rpc", serverScope(1))
    toggle("Declare header collection (including empty)", serverScope(1)); scopedClick(serverScope(1), "Add header reference"); fill("Header name", "X-Token", '[data-mcp-row="1-headers-0"]')
    click("Add server"); markServers()
    fill("Server ID", "events", serverScope(2)); fill("Native server name", "browser-sse", serverScope(2)); choose("Transport", "SSE", serverScope(2)); fill("Remote URL", "http://events.invalid/sse", serverScope(2)); toggle("Enabled", serverScope(2)); toggle("Declare header collection (including empty)", serverScope(2))
    scopedClick(serverScope(2), "Move up", true); markServers(); scopedClick(serverScope(1), "Move down", true); markServers()
    choose("Operation", "Replace", slot("arg/local/first"))
    choose("Operation", "Replace", slot("env/local/TOKEN")); fill("New value (empty is a valid explicit replacement)", "WEB-MCP-NEW-CANARY", slot("env/local/TOKEN"))
    choose("Operation", "Replace", slot("header/remote/X-Token")); fill("New value (empty is a valid explicit replacement)", "synthetic-header", slot("header/remote/X-Token"))
    const beforePreview = (await control("state")).assets.length
    await review()
    assert.equal((await control("state")).assets.length, beforePreview, "preview wrote SQLite")
    assert(text().includes("Disable managed server") && text().includes("insecure HTTP"))
    click("Save private asset"); await saved("Browser ordered MCP")
    let asset = (await control("state")).assets.find(asset => asset.name === "Browser ordered MCP")
    assert.deepEqual(asset.declaration.servers.map(server => server.id), ["local", "remote", "events"])
    assert.deepEqual(asset.declaration.servers[0].stdio.args.values.map(arg => arg.id), ["second", "first"])
    assert.deepEqual(asset.declaration.servers[2].sse.headers.values, [])
    assert.equal(asset.declaration.servers[2].enabled, false)
    assert.deepEqual(asset.slots.map(slot => slot.present), [false, true, true, true])
    record("real create: stdio/http/sse, ordered members, disabled, explicit-empty collections, empty Replace; logical-only preview")

    await row("Browser ordered MCP", "Edit MCP asset")
    assert(evaluate("[...document.querySelectorAll('input[type=password]')].every(node=>node.value==='')"))
    assert(!text().includes("WEB-MCP-NEW-CANARY"))
    markServers(); toggle("Declare environment collection (including empty)", serverScope(0))
    click("Logical preview")
    assert(text().includes("Every removed reference requires Clear"), "last secret reference removal not guarded")
    choose("Operation", "Clear", slot("env/local/TOKEN")); toggle("Confirm Clear for this reference", slot("env/local/TOKEN"))
    fill("Asset name", "Browser cleared MCP")
    await review(); click("Save private asset"); await saved("Browser cleared MCP")
    asset = (await control("state")).assets.find(asset => asset.name === "Browser cleared MCP")
    assert.equal(asset.revision, 2); assert(!asset.slots.some(slot => slot.ref === "env/local/TOKEN")); assert(!Object.hasOwn(asset.declaration.servers[0].stdio, "env"))
    record("sanitized read, Keep, explicit Clear removes final secret reference in full CAS")

    await row("Browser cleared MCP", "Copy without credentials")
    await until(() => text().includes("copies no saved secrets"), "copy source not loaded")
    fill("Asset name", "Browser credential-free copy"); toggle("Confirm this operation"); click("Copy without credentials"); await saved("Browser credential-free copy")
    const copied = (await control("state")).assets.find(asset => asset.name === "Browser credential-free copy")
    assert(copied.slots.length > 0 && copied.slots.every(slot => !slot.present))
    record("copy is exact non-secret declaration; all copied slots absent")

    await row("Browser cleared MCP", "Edit MCP asset"); fill("Asset name", "CAS retained draft"); await review()
    await control("external", { ID: asset.id }); click("Save private asset")
    await until(() => text().includes("Revision changed"), "late CAS conflict was not surfaced")
    assert(evaluate("[...document.querySelectorAll('.mcp-form input')].some(node=>node.value==='CAS retained draft')"))
    assert(evaluate("document.activeElement.closest('[role=dialog]')!==null && !document.activeElement.matches(':disabled')"), "CAS focus lost")
    await readback()
    assert.equal((await control("state")).assets.find(row => row.id === asset.id).name, "Browser cleared MCP external")
    record("real late conflict after Preview; draft retained; explicit readback; focus cancel/restore")

    await row("Browser cleared MCP external", "Edit MCP asset"); fill("Asset name", "Unknown committed MCP"); await review()
    const updatePath = "/skillhub.configuration.mcp.v1.MCPConfigurationService/UpdateAsset"
    const beforeUnknown = (await control("state")).requests[updatePath] ?? 0
    await mode("lost", "UpdateAsset"); click("Save private asset")
    await until(() => text().includes("Write outcome is unknown"), "lost successful commit not Unknown")
    assert.equal((await control("state")).assets.find(row => row.id === asset.id).name, "Unknown committed MCP")
    evaluate("document.querySelector('.mcp-form').requestSubmit();document.querySelector('.mcp-form').requestSubmit()")
    await delay(350)
    assert.equal((await control("state")).requests[updatePath], beforeUnknown + 1, "Unknown automatically replayed")
    await mode(); await closeDraft()
    assert(text().includes("Write outcome is unknown"), "modal close lost Unknown barrier")
    click("Discard draft and read current state")
    await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "workspace readback missing")
    scopedClick('[role=dialog]', "Discard draft and read current state")
    await until(() => text().includes("Current metadata read"), "workspace readback did not finish")
    assert.equal((await control("state")).requests[updatePath], beforeUnknown + 1)
    record("lost-after-real-commit Unknown; no replay, barrier survives close; explicit readback only")

    await row("Unknown committed MCP", "Copy without credentials")
    await until(() => text().includes("copies no saved secrets"), "copy CAS source missing")
    fill("Asset name", "Copy conflict not committed"); toggle("Confirm this operation")
    await control("external", { ID: asset.id }); click("Copy without credentials")
    await until(() => text().includes("Revision changed"), "copy source CAS conflict absent")
    assert(!(await control("state")).assets.some(asset => asset.name === "Copy conflict not committed"))
    await readback()
    await row("Unknown committed MCP external", "Delete MCP asset")
    toggle("I confirm deletion at the displayed asset revision. Device files are not removed.")
    await control("external", { ID: asset.id }); click("Delete MCP asset")
    await until(() => text().includes("Revision changed"), "delete CAS conflict absent")
    assert((await control("state")).assets.some(row => row.id === asset.id))
    await readback()
    record("copy and delete use source/asset full CAS; real concurrent revisions reject without copying/deleting")

    await row("Seed private MCP", "Edit MCP asset")
    choose("Operation", "Replace", slot("env/local/TOKEN")); fill("New value (empty is a valid explicit replacement)", "WEB-MCP-NEW-CANARY", slot("env/local/TOKEN"))
    fill("Asset name", "Unsubmitted retained draft")
    await mode("slow", "Preview"); click("Logical preview")
    assert(evaluate("document.activeElement.closest('[role=dialog]')!==null && !document.activeElement.matches(':disabled')"), "pending preview focus lost")
    click("Cancel waiting and clear new credentials")
    await until(() => !text().includes("Operation in progress"), "read cancellation did not settle")
    assert(evaluate("[...document.querySelectorAll('input[type=password]')].every(node=>node.value==='')"), "cancel did not wipe replacement")
    assert(evaluate("[...document.querySelectorAll('.mcp-form input')].some(node=>node.value==='Unsubmitted retained draft')"), "cancel lost nonsecret draft")
    await mode(); await closeDraft()
    record("read cancellation aborts, clears new secrets, preserves non-secret draft; pending focus")

    await row("Seed private MCP", "Edit MCP asset"); fill("Asset name", "Locale-theme retained draft")
    for (const context of [{ locale: "en", theme: "light", width: 1280, font: 100, motion: false }, { locale: "zh-CN", theme: "dark", width: 375, font: 100, motion: true }, { locale: "en", theme: "dark", width: 375, font: 200, motion: true }, { locale: "zh-CN", theme: "light", width: 1280, font: 200, motion: false }]) {
      browser(["set", "viewport", String(context.width), "900"])
      browser(["set", "media", context.theme, ...(context.motion ? ["reduced-motion"] : [])])
      const themeLabel = language === "en" ? "Theme" : "主题", languageLabel = language === "en" ? "Interface language" : "界面语言"
      browser(["click", `[role=dialog] [role=combobox][aria-label=${JSON.stringify(themeLabel)}]`]); browser(["find", "role", "option", "click", "--name", language === "en" ? context.theme === "dark" ? "Dark" : "Light" : context.theme === "dark" ? "深色" : "浅色", "--exact"])
      browser(["click", `[role=dialog] [role=combobox][aria-label=${JSON.stringify(languageLabel)}]`]); browser(["find", "role", "option", "click", "--name", context.locale === "en" ? "English" : "简体中文", "--exact"])
      language = context.locale
      evaluate(`document.documentElement.style.fontSize='${context.font === 200 ? 32 : 16}px'`)
      const layout = evaluate("(() => {const editor=document.querySelector('.mcp-editor'),box=editor.getBoundingClientRect();return {width:innerWidth,documentWidth:document.documentElement.scrollWidth,editorWidth:editor.clientWidth,scrollWidth:editor.scrollWidth,left:box.left,right:box.right,font:getComputedStyle(document.documentElement).fontSize,theme:document.documentElement.dataset.theme,language:document.documentElement.lang,reduce:matchMedia('(prefers-reduced-motion: reduce)').matches,draft:[...document.querySelectorAll('.mcp-form input')].some(node=>node.value==='Locale-theme retained draft'),checkboxes:[...editor.querySelectorAll('[role=checkbox]')].every(node=>{const box=node.getBoundingClientRect();return Math.abs(box.width-box.height)<1})}})()")
      layouts.push({ context, actual: layout, zoom: "200% root text enlargement; not device-scale or browser-chrome zoom" })
      writeFileSync(join(output, "overflow-diagnostics.json"), JSON.stringify(evaluate("[...document.querySelectorAll('body *')].filter(node=>node.clientWidth&&node.scrollWidth>node.clientWidth+1).map(node=>({tag:node.tagName,class:node.className,client:node.clientWidth,scroll:node.scrollWidth})).slice(0,40)"), null, 2), { mode: 0o600 })
      assert(layout.documentWidth <= layout.width + 1 && layout.scrollWidth <= layout.editorWidth + 1 && layout.left >= 0 && layout.right <= layout.width + 1, "horizontal clipping/overflow")
      assert(layout.draft && layout.checkboxes)
      assert.equal(layout.font, context.font === 200 ? "32px" : "16px"); assert.equal(layout.theme, context.theme); assert.equal(layout.language, context.locale); assert.equal(layout.reduce, context.motion)
      capture(`editor-${context.locale}-${context.theme}-${context.width}-${context.font}`)
      browser(["press", "Tab"]); assert(evaluate("document.activeElement.closest('[role=dialog]')!==null"), "keyboard escaped modal")
    }
    writeFileSync(join(output, "layouts.json"), JSON.stringify(layouts, null, 2), { mode: 0o600 })
    browser(["click", `[role=dialog] [role=combobox][aria-label=${JSON.stringify(language === "en" ? "Interface language" : "界面语言")}]`]); browser(["find", "role", "option", "click", "--name", "English", "--exact"]); language = "en"
    await review()
    browser(["focus", '.mcp-form button[type=submit]'])
    assert(evaluate("(() => {const node=document.activeElement,box=node.getBoundingClientRect();return node.matches('.mcp-form button[type=submit]')&&box.top>=0&&box.bottom<=innerHeight})()"), "200% bottom save control unreachable")
    capture("editor-200-bottom-controls")
    await closeDraft()
    assert(evaluate("document.activeElement!==document.body && !document.activeElement.matches(':disabled')"), "close did not restore focus")
    record("English/Chinese light/dark 375px 200% text enlargement reduced-motion; theme/locale preserve draft; keyboard modal/focus")
    if (language !== "en") {
      browser(["click", '[role=combobox][aria-label="界面语言"]']); browser(["find", "role", "option", "click", "--name", "English", "--exact"]); language = "en"
    }
    evaluate("document.documentElement.style.fontSize='16px'"); browser(["set", "viewport", "1280", "900"])

    await row("Browser credential-free copy", "Delete MCP asset")
    toggle("I confirm deletion at the displayed asset revision. Device files are not removed."); click("Delete MCP asset")
    await until(async () => !(await control("state")).assets.some(row => row.id === copied.id), "real SQLite delete missing")
    await until(() => !evaluate("!!document.querySelector('[role=dialog]')"), "delete dialog did not close")
    record("real CAS deletion; focus fallback after removed row")

    click("Arrange configurations")
    await until(() => text().includes("Choose a complete group"), "ordering dialog missing")
    browser(["find", "role", "combobox", "click", "--name", "Agent and configuration type", "--exact"])
    browser(["find", "role", "option", "click", "--name", "claude · MCP connection", "--exact"])
    await until(() => evaluate("!!document.querySelector('[data-configuration-order-group] li')"), "complete MCP partition not loaded")
    const revisions = Object.fromEntries((await control("state")).assets.map(asset => [asset.id, asset.revision]))
    const orderedBefore = evaluate("[...document.querySelectorAll('[data-configuration-order-group] li')].map(row=>row.getAttribute('aria-label'))")
    browser(["find", "role", "button", "click", "--name", `Move ${orderedBefore[1]} up`, "--exact"])
    await until(() => text().includes("Order saved on the server"), "real partition order not saved")
    assert.deepEqual(Object.fromEntries((await control("state")).assets.map(asset => [asset.id, asset.revision])), revisions)
    assert.equal(evaluate("document.querySelector('[data-configuration-order-group] li').getAttribute('aria-label')"), orderedBefore[1])
    click("Close dialog")
    record("complete owner MCP partition CAS order including Unknown without payload conversion or asset revision mutation")

    await row("Seed private MCP", "Edit MCP asset"); fill("Asset name", "Identity discarded draft")
    choose("Operation", "Replace", slot("env/local/TOKEN")); fill("New value (empty is a valid explicit replacement)", "WEB-MCP-NEW-CANARY", slot("env/local/TOKEN"))
    await mode("slow", "Preview"); click("Logical preview")
    evaluate("[...document.querySelectorAll('.topbar button')].find(node=>node.textContent==='Sign out').click()")
    await until(() => evaluate("!!document.querySelector('#username')"), "logout did not clear identity")
    assert(!evaluate("!!document.querySelector('.mcp-form')"), "old operation survived identity change")
    await mode()
    browser(["fill", "#username", login.foreign]); browser(["fill", "#password", login.password]); click("Sign in")
    await until(() => text().includes("Foreign private MCP"), "foreign owner list missing")
    assert(!text().includes("Seed private MCP") && !text().includes("Identity discarded draft"), "stale identity contaminated new owner")
    const privacy = evaluate("({local:Object.keys(localStorage),session:Object.keys(sessionStorage),body:document.body.innerHTML.includes('WEB-MCP-STORED-CANARY')||document.body.innerHTML.includes('WEB-MCP-NEW-CANARY')})")
    assert(!privacy.body && privacy.session.length === 0 && privacy.local.every(key => ['skillhub.theme','skillhub.locale'].includes(key)), "secret/token persistence or saved secret disclosure")
    const serverPrivacy = await control("privacy")
    assert(serverPrivacy.checkedPrivateFiles >= 4 && !serverPrivacy.plaintextCanaryFound, "synthetic secret persisted as plaintext")
    record("identity change clears async/draft; new owner isolated; local/session storage contains only existing preferences", { storageKeys: privacy.local })
    click("Sign out")
    assert.deepEqual(browser(["errors"]).errors, [], "browser runtime errors")
    capture("final-logged-out")
    }
  } catch (error) { failure = error; if (started) { try { writeFileSync(join(output, "failure-snapshot.txt"), JSON.stringify(browser(["snapshot", "-i"])), { mode: 0o600 }) } catch {} } }
  finally {
    if (zoomConnection) { try { await zoomConnection.close() } catch (error) { failure ??= error } }
    if (started) { try { browser(["close"]) } catch (error) { failure ??= error } }
    rmSync(socketDirectory, { recursive: true })
    if (ready) { try { stopped = await control("stop", {}) === "stop received"; assert(stopped, "explicit stop not acknowledged") } catch (error) { failure ??= error } }
    else fixture.kill("SIGTERM")
    fixtureResult = await terminal
    closeSync(log)
    try { acceptFixture(fixtureResult, readFileSync(logPath, "utf8")) } catch (error) { failure ??= error }
    const report = { pass: !failure, events, layouts, explicitStop: stopped, fixture: fixtureResult, scope: "Local Linux built Web + real App/TLS/SQLite; no MCP connection, process launch, device apply, full F12 or GA", failure: failure?.message }
    writeFileSync(join(output, "report.json"), JSON.stringify(report, null, 2), { mode: 0o600 })
  }
  if (failure) throw failure
  console.log(`MCP configurations built browser PASS ${events.length} groups; explicit stop/App.Close/Serve/worker joins and terminal Wait verified`)
}

if (values["self-test"]) await selfTest()
else await run()
