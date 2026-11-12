import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" } } })
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
assert(["before", "after"].includes(values.phase))
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestRepositoriesUIBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_REPOSITORIES_UI_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `cognate-navigation-${process.pid}-${Date.now()}`
const evidence = [], layouts = []
let ready, failure
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  let detail = ""
  if (result.status !== 0) { try { detail = String(JSON.parse(result.stdout).error ?? "").slice(0, 600) } catch {} }
  assert.equal(result.status, 0, `browser ${args[0]} failed: ${detail}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) {
  const deadline = Date.now() + 15000
  while (Date.now() < deadline) { if (await check()) return; await new Promise((done) => setTimeout(done, 100)) }
  assert.fail(message)
}
function dialogReady() {
  browser(["wait", "--fn", "[...document.querySelectorAll('[data-slot=dialog-content][data-state=open]')].every(node => getComputedStyle(node).pointerEvents === 'auto')"])
}
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]); dialogReady() }
function openNavigation(label) {
  const point = evaluate(`(() => {const node=document.querySelector('.workspace-menu-button');if(node?.getAttribute('aria-label') !== ${JSON.stringify(label)}) throw new Error('navigation trigger is not ready');const box=node.getBoundingClientRect();const positionX=box.x+box.width/2,positionY=box.y+box.height/2;if(document.elementFromPoint(positionX,positionY)?.closest('button') !== node) throw new Error('navigation trigger is covered');return {positionX,positionY}})()`)
  browser(["mouse", "move", String(Math.round(point.positionX)), String(Math.round(point.positionY))])
  browser(["mouse", "down"]); browser(["mouse", "up"])
  browser(["wait", "--fn", "!!document.querySelector('.workspace-navigation-dialog') && getComputedStyle(document.querySelector('.workspace-navigation-dialog')).pointerEvents === 'auto'"])
}
function capture(name) {
  assert.equal(evaluate("[...document.querySelectorAll('input[type=password]')].some(node => node.value.length > 0)"), false)
  browser(["screenshot", join(output, `${name}.png`)])
  evidence.push(name)
}
async function control(path, body) {
  const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body } : {}), signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200)
  return body === undefined ? response.json() : undefined
}
async function preferences(theme, locale, width) {
  if (evaluate("location.pathname") !== "/repositories") {
    browser(["click", '.sidebar a[href="/repositories"]'])
    await until(() => evaluate("!!document.querySelector('.repositories-ui')"), "client-side return to library failed")
  }
  browser(["set", "viewport", String(width), width < 800 ? "844" : "1000"])
  browser(["click", ".preferences [role=combobox]:last-child"])
  browser(["find", "role", "option", "click", "--name", locale === "en" ? "English" : "简体中文", "--exact"])
  browser(["click", ".preferences [role=combobox]:first-child"])
  browser(["find", "role", "option", "click", "--name", locale === "en" ? theme === "dark" ? "Dark" : "Light" : theme === "dark" ? "深色" : "浅色", "--exact"])
  await until(() => evaluate("!!document.querySelector('.repositories-ui') && document.body.textContent.includes(document.documentElement.lang === 'en' ? 'Your library is empty' : '资料库还是空的')"), "library not ready")
  assert.equal(evaluate("document.documentElement.dataset.theme"), theme)
  assert.equal(evaluate("document.documentElement.lang"), locale)
}
function rpcCounts(counts) {
  assert(counts && typeof counts === "object" && Object.keys(counts).some((path) => path.startsWith("/skillhub.")), "fixture RPC counters absent")
  return Object.fromEntries(Object.entries(counts).filter(([path]) => path.startsWith("/skillhub.")))
}
function layout(context) {
  const current = { ...context, ...evaluate("({width: innerWidth, scrollWidth: document.documentElement.scrollWidth, headerHeight: document.querySelector('.topbar').getBoundingClientRect().height, clipped: [...document.querySelectorAll('.topbar button, .topbar a, [role=dialog] button, [role=dialog] nav a')].filter(node => {const box=node.getBoundingClientRect();return box.width > 0 && (box.left < -1 || box.right > innerWidth + 1)}).length})") }
  layouts.push(current)
  assert(current.scrollWidth <= current.width + 1 && current.clipped === 0, `navigation overflow: ${JSON.stringify(current)}`)
  if (context.state === "workspace" && context.width <= 800) {
    assert.equal(evaluate("[...document.querySelectorAll('.workspace-menu-button, .workspace-mobile-brand, .workspace-quick-navigation, .topbar .preferences button')].every(node => {const box=node.getBoundingClientRect(),hit=document.elementFromPoint(box.x+box.width/2,box.y+box.height/2);return node === hit || node.contains(hit)})"), true, "mobile header controls must not cover one another")
  }
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture not ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["open", ready.url + "/repositories"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login absent")
  browser(["fill", "#username", "task2-owner"])
  browser(["fill", "#password", ready.password])
  browser(["click", "form button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('.repositories-ui')"), "login failed")
  if (values.phase === "before") {
    for (const width of [1440, 375]) {
      await preferences("dark", "zh-CN", width)
      capture(`workspace-before-${width}`)
    }
  } else {
    await preferences("light", "en", 1440)
    const beforeNavigation = rpcCounts(await control("state"))
    click("Quick navigation")
    await until(() => evaluate("document.activeElement?.id === 'workspace-navigation-query'"), "page finder did not focus its search")
    browser(["fill", "#workspace-navigation-query", "backup"])
    assert.equal(evaluate("document.querySelectorAll('.workspace-navigation-results a').length"), 1)
    assert.equal(evaluate("document.querySelector('.workspace-navigation-results a').getAttribute('href')"), "/server-backup")
    evaluate("document.querySelector('#workspace-navigation-query').dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',isComposing:true,bubbles:true,cancelable:true}))")
    assert.equal(evaluate("document.querySelector('#workspace-navigation-query').value"), "backup", "IME Escape must not clear or dismiss navigation")
    browser(["fill", "#workspace-navigation-query", "no-such-page"])
    assert.equal(evaluate("document.querySelectorAll('.workspace-navigation-results a').length"), 0)
    capture("quick-navigation-empty")
    browser(["press", "Escape"])
    assert.equal(evaluate("document.querySelector('#workspace-navigation-query').value"), "")
    assert.equal(evaluate("!!document.querySelector('[role=dialog]')"), true)
    browser(["press", "Escape"])
    assert.equal(evaluate("!!document.querySelector('[role=dialog]')"), false)
    await until(() => evaluate("document.activeElement.getAttribute('aria-label') === 'Quick navigation'"), "page finder did not restore its trigger focus")
    assert.deepEqual(rpcCounts(await control("state")), beforeNavigation, "opening and filtering page navigation must not read private data")
    evidence.push("page-finder-filter-empty-clear-dismiss-focus-and-no-private-reads")

    browser(["press", "Control+k"])
    assert.equal(evaluate("getComputedStyle(document.querySelector('.workspace-quick-dialog')).animationName"), "none")
    browser(["fill", "#workspace-navigation-query", "categories"])
    browser(["press", "Tab"])
    browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement.getAttribute('href')"), "/categories")
    browser(["press", "Enter"])
    await until(() => evaluate("location.pathname === '/categories' && !!document.querySelector('.categories-ui')"), "keyboard page selection failed")
    assert.equal(evaluate("!!document.querySelector('[role=dialog]')"), false)
    click("Create category")
    browser(["press", "Control+k"])
    assert.equal(evaluate("!!document.querySelector('#category-name') && !document.querySelector('.workspace-quick-dialog')"), true, "shortcut hijacked another dialog")
    browser(["press", "Escape"])
    evidence.push("keyboard-shortcut-no-motion-selects-page-and-respects-other-modal")

    for (const theme of ["light", "dark"]) for (const locale of ["zh-CN", "en"]) for (const width of [320, 375, 768, 1440]) {
      await preferences(theme, locale, width)
      layout({ theme, locale, width, state: "workspace" })
      capture(`workspace-${theme}-${locale}-${width}`)
      if (width <= 800) {
        openNavigation(locale === "en" ? "Open navigation" : "打开导航")
        await until(() => evaluate("!!document.querySelector('.workspace-navigation-dialog')"), "mobile navigation did not open")
        browser(["wait", "--fn", "document.querySelector('.workspace-navigation-dialog').getAnimations().every(animation => animation.playState === 'finished' || animation.playState === 'idle')"])
        assert.equal(evaluate("document.querySelectorAll('.workspace-navigation-dialog nav a').length"), 10)
        assert.equal(evaluate("document.activeElement.matches('.workspace-navigation-dialog nav a[data-status=active]')"), true, "opening navigation must focus its current page, not sign out")
        layout({ theme, locale, width, state: "navigation" })
        capture(`navigation-${theme}-${locale}-${width}`)
        browser(["press", "Escape"])
        await until(() => evaluate("document.activeElement.classList.contains('workspace-menu-button')"), "mobile dismissal did not restore the menu trigger")
        openNavigation(locale === "en" ? "Open navigation" : "打开导航")
        browser(["click", '.workspace-navigation-dialog a[href="/repositories"]'])
        await until(() => evaluate("!document.querySelector('[role=dialog]') && document.body.style.pointerEvents !== 'none'"), "same-page selection retained a blocking modal")
        openNavigation(locale === "en" ? "Open navigation" : "打开导航")
        browser(["set", "viewport", "1100", "900"])
        await until(() => evaluate("!document.querySelector('[role=dialog]')"), "desktop resize retained the mobile modal")
        await until(() => evaluate("document.activeElement.matches('.sidebar nav a[data-status=active]')"), "desktop resize must restore visible navigation focus")
      }
    }
    await preferences("dark", "en", 375)
    browser(["press", "Control+k"])
    await until(() => evaluate("document.activeElement?.id === 'workspace-navigation-query'"), "page finder was not ready for account navigation")
    browser(["fill", "#workspace-navigation-query", "account"])
    browser(["press", "Tab"]); browser(["press", "Tab"]); browser(["press", "Enter"])
    await until(() => evaluate("location.pathname === '/account' && !!document.querySelector('main h1')"), "account page did not open")
    browser(["set", "media", "dark", "--reduced-motion", "reduce"])
    evaluate("document.documentElement.style.fontSize='200%'")
    browser(["focus", ".workspace-menu-button"]); browser(["press", "Enter"])
    await until(() => evaluate("!!document.querySelector('.workspace-navigation-dialog') && document.activeElement.getAttribute('href') === '/account'"), "large-text navigation did not focus its current page")
    assert.equal(evaluate("getComputedStyle(document.querySelector('.workspace-navigation-dialog')).animationName"), "none")
    assert.equal(evaluate("(() => {const node=document.activeElement.getBoundingClientRect(),nav=document.querySelector('.workspace-navigation-dialog nav').getBoundingClientRect();return node.top >= nav.top-1 && node.bottom <= nav.bottom+1})()"), true, "current page focus must scroll into the navigation viewport")
    layout({ theme: "dark", locale: "en", width: 375, state: "large-text-reduced-motion" })
    capture("navigation-reduced-motion-large-text")
    evidence.push("mobile-navigation-themes-locales-small-screens-focus-resize-and-reduced-motion")
    evaluate("document.documentElement.style.fontSize='100%'")
    browser(["press", "Escape"])
    openNavigation("Open navigation")
    click("Sign out")
    await until(() => evaluate("!!document.querySelector('#username') && !document.querySelector('[role=dialog]')"), "mobile sign-out did not return to the existing login screen")
    evidence.push("mobile-account-retains-explicit-sign-out")
  }
} catch (error) {
  failure = error
  try {
    const header = evaluate("({scrollX,scrollY,elements:[...document.querySelectorAll('.topbar, .workspace-topbar-leading, .topbar button, .topbar .brand, .topbar .preferences')].map(node=>{const box=node.getBoundingClientRect(),style=getComputedStyle(node),hit=document.elementFromPoint(box.x+box.width/2,box.y+box.height/2);return {name:node.getAttribute('aria-label')??node.className,box:{x:box.x,y:box.y,width:box.width,height:box.height},display:style.display,flex:style.flex,hit:hit?.outerHTML.slice(0,300)}})})")
    writeFileSync(join(output, "header-failure.json"), JSON.stringify(header, null, 2), { mode: 0o600 })
    const diagnostic = evaluate("({layers:[...document.querySelectorAll('[data-slot=dialog-content], [data-slot=dialog-overlay]')].map(node=>{const style=getComputedStyle(node),box=node.getBoundingClientRect();return {slot:node.dataset.slot,state:node.dataset.state,motion:node.dataset.motion,z:style.zIndex,pointerEvents:style.pointerEvents,opacity:style.opacity,translate:style.translate,transform:style.transform,animation:style.animationName,box:{x:box.x,y:box.y,width:box.width,height:box.height}}}),navigation:[...document.querySelectorAll('.workspace-navigation-dialog nav a')].map(node=>{const box=node.getBoundingClientRect(),hit=document.elementFromPoint(box.x+box.width/2,box.y+box.height/2);return {path:node.getAttribute('href'),hit:hit?.tagName,slot:hit?.closest('[data-slot]')?.getAttribute('data-slot')}})})")
    writeFileSync(join(output, "modal-failure.json"), JSON.stringify(diagnostic, null, 2), { mode: 0o600 })
  } catch {}
  try { capture("failure") } catch {}
} finally {
  try { browser(["close"]) } catch {}
  try { if (ready) await control("stop", "") } catch (error) { failure ??= error }
  const exited = await Promise.race([terminal, new Promise((done) => setTimeout(() => done({ timeout: true }), 8000))])
  if (exited.timeout) fixture.kill("SIGKILL")
  if (!failure) assert.equal(exited.code, 0)
  closeSync(log)
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ pass: !failure, phase: values.phase, evidence, layouts, fixtureExit: exited, ...(failure ? { error: failure.stack } : {}) }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
console.log(JSON.stringify({ pass: true, evidence: evidence.length, layouts: layouts.length, output }))
