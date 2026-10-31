import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync, closeSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" }, "checks-only": { type: "boolean", default: false }, "geometry-only": { type: "boolean", default: false } } })
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
assert(["before", "after"].includes(values.phase))
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const home = join(directory, "home")
mkdirSync(home, { mode: 0o700 })
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestPublicCatalogBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_PUBLIC_CATALOG_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `public-catalog-${process.pid}-${Date.now()}`
const evidence = []
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
let ready, failure, started = false
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed: ${result.stderr || result.stdout}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, JSON.stringify(parsed))
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
function capture(name, full = true) { browser(["screenshot", join(output, `${name}.png`), ...(full ? ["--full"] : [])]); evidence.push(name) }
async function control(path, body) { const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body } : {}), signal: AbortSignal.timeout(5000) }); assert.equal(response.status, 200); return body === undefined ? response.json() : undefined }
async function load(path = "/explore") { browser(["open", ready.url + path]); await until(() => evaluate("!!document.querySelector('main h1')"), "page did not mount") }
async function results(count = 24) { await until(() => evaluate(`document.querySelectorAll('.catalog-results > li').length === ${count}`), `expected ${count} real catalog results`) }
function click(text) { browser(["find", "role", "button", "click", "--name", text, "--exact"]) }
function overflow() { return evaluate("document.documentElement.scrollWidth > innerWidth + 1") }
async function preferences(theme, locale, width) {
  browser(["set", "viewport", String(width), width <= 375 ? "812" : "960"])
  browser(["set", "media", theme])
  evaluate(`localStorage.setItem('skillhub.theme', ${JSON.stringify(theme)}); localStorage.setItem('skillhub.locale', ${JSON.stringify(locale)})`)
  await load()
  await results()
  assert.equal(evaluate("document.documentElement.dataset.theme"), theme)
  assert.equal(evaluate("document.documentElement.lang"), locale)
  assert.equal(overflow(), false, `catalog overflow at ${width}/${locale}/${theme}`)
}
function reflowLayout(context) {
  return { ...context, ...evaluate(`(() => {
    const root = document.querySelector('.public-workspace')
    const width = innerWidth
    const outside = (rectangle, bounds = {left: 0, right: width}) => rectangle.width > 0 && rectangle.height > 0 && (rectangle.left < bounds.left - 1 || rectangle.right > bounds.right + 1)
    const describe = (node, rectangle) => ({tag: node.tagName, class: node.className, text: node.textContent.trim().slice(0, 100), left: rectangle.left, right: rectangle.right, width: rectangle.width})
    const elements = Array.from(root.querySelectorAll('*')).filter(node => getComputedStyle(node).visibility !== 'hidden').flatMap(node => {
      const rectangle = node.getBoundingClientRect()
      return outside(rectangle) ? [describe(node, rectangle)] : []
    })
    const text = []
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
    while (walker.nextNode()) {
      const node = walker.currentNode
      if (!node.textContent.trim() || getComputedStyle(node.parentElement).visibility === 'hidden') continue
      const range = document.createRange()
      range.selectNodeContents(node)
      for (const rectangle of range.getClientRects()) {
        if (outside(rectangle)) text.push(describe(node.parentElement, rectangle))
      }
    }
    const controls = []
    for (const panel of root.querySelectorAll('.catalog-category-pages, .catalog-pagination, .install-command')) {
      const bounds = panel.getBoundingClientRect()
      for (const node of panel.querySelectorAll('button, a')) {
        const rectangle = node.getBoundingClientRect()
        if (outside(rectangle, bounds)) controls.push({...describe(node, rectangle), container: panel.className, containerLeft: bounds.left, containerRight: bounds.right})
      }
    }
    return {width, scrollWidth: document.documentElement.scrollWidth, fontSize: getComputedStyle(document.documentElement).fontSize, overflowX: getComputedStyle(document.body).overflowX, elements, text, controls}
  })()` ) }
}
function copyWordLayout(context) {
  const layout = { ...context, ...evaluate(`(() => {
    const button = document.querySelector('.install-command .button')
    const words = []
    const walker = document.createTreeWalker(button, NodeFilter.SHOW_TEXT)
    while (walker.nextNode()) {
      const node = walker.currentNode
      for (const match of node.textContent.matchAll(/[A-Za-z]+/g)) {
        const range = document.createRange()
        range.setStart(node, match.index)
        range.setEnd(node, match.index + match[0].length)
        words.push({word: match[0], rectangles: Array.from(range.getClientRects(), rectangle => ({left: rectangle.left, top: rectangle.top, width: rectangle.width, height: rectangle.height}))})
      }
    }
    const style = getComputedStyle(button)
    return {label: button.textContent, fontSize: style.fontSize, paddingInline: style.paddingInline, words}
  })()`) }
  assert.deepEqual(layout.words.map(entry => entry.word), context.state === "copy" ? ["Copy", "command"] : ["Command", "copied"])
  return layout
}
async function reflowMatrix() {
  const layouts = []
  const copyWords = []
  for (const theme of ["light", "dark"]) for (const locale of ["en", "zh-CN"]) for (const width of [375, 320]) {
    const context = { theme, locale, width, textScale: 2 }
    const name = `reflow-${theme}-${locale}-${width}-200pct`
    await preferences(theme, locale, width)
    evaluate("document.documentElement.style.fontSize='200%'")
    await until(() => evaluate("document.querySelector('#catalog-category').options.length===65"), "real first category page missing")
    layouts.push(reflowLayout({ ...context, page: "catalog" }))
    if (!values["geometry-only"]) {
      capture(`${name}-catalog`, false)
      evaluate("document.querySelector('.catalog-category-pages').scrollIntoView({block:'center'})")
      capture(`${name}-categories`, false)
      evaluate("document.querySelector('.workspace-footer').scrollIntoView({block:'center'})")
      capture(`${name}-footer`, false)
    }
    evaluate("document.querySelector('.catalog-category-pages button').focus()")
    browser(["press", "Enter"])
    await until(() => evaluate("document.querySelector('#catalog-category').options.length===3"), "200% category pagination did not activate with Enter")
    layouts.push(reflowLayout({ ...context, page: "catalog-next-categories" }))
    if (!values["geometry-only"]) capture(`${name}-next-categories`, false)
    evaluate("document.querySelector('.catalog-category-pages button').focus()")
    browser(["press", "Enter"])
    await until(() => evaluate("document.querySelector('#catalog-category').options.length===65"), "200% previous category page missing")
    evaluate("document.querySelector('.catalog-pagination button:last-of-type').focus()")
    browser(["press", "Enter"])
    await results(3)
    evaluate("document.querySelector('.catalog-pagination button:first-of-type').focus()")
    browser(["press", "Enter"])
    await results()
    browser(["fill", "#catalog-query", "Change review"])
    browser(["press", "Enter"])
    await results(3)
    const path = evaluate("Array.from(document.querySelectorAll('.catalog-skill-link')).find(node=>node.textContent==='Change review').getAttribute('href')")
    await load(path)
    evaluate("document.documentElement.style.fontSize='200%'")
    await until(() => evaluate("!!document.querySelector('pre code')"), "200% real public detail missing")
    layouts.push(reflowLayout({ ...context, page: "detail" }))
    assert.equal(evaluate("document.querySelector('pre code').textContent"), `cognate install-public --server '${ready.url}' --skill '${path.split('/').at(-1)}'`)
    evaluate("document.querySelector('.install-command .button').focus()")
    if (locale === "en") copyWords.push(copyWordLayout({ ...context, state: "copy" }))
    if (!values["geometry-only"] || theme === "light" && locale === "en") capture(`${name}-detail-command`, false)
    browser(["press", "Enter"])
    const copied = locale === "en" ? "The command was copied. No installation has been performed." : "仅复制了命令，尚未执行安装。"
    await until(() => evaluate(`document.querySelector('.catalog-copy-status').textContent.includes(${JSON.stringify(copied)})`), "200% copy-only feedback missing after Enter")
    if (locale === "en") copyWords.push(copyWordLayout({ ...context, state: "copied" }))
    if (!values["geometry-only"] || theme === "light" && locale === "en") capture(`${name}-detail-copied`, false)
    if (!values["geometry-only"]) {
      evaluate("document.querySelector('.workspace-footer').scrollIntoView({block:'center'})")
      capture(`${name}-detail-footer`, false)
    }
  }
  writeFileSync(join(output, "reflow-layouts.json"), JSON.stringify(layouts, null, 2), { mode: 0o600 })
  writeFileSync(join(output, "copy-word-layouts.json"), JSON.stringify(copyWords, null, 2), { mode: 0o600 })
  const failed = layouts.filter(layout => layout.scrollWidth > layout.width + 1 || layout.elements.length || layout.text.length || layout.controls.length || layout.fontSize !== "32px" || ["hidden", "clip"].includes(layout.overflowX))
  assert.deepEqual(failed, [], "200% text reflow must keep page, rendered text and controls within their containers without clipping")
  evidence.push("200-percent-320-375-themes-locales-catalog-detail-geometry-and-keyboard-actions")
  assert.equal(copyWords.length, 8)
  assert(copyWords.every(layout => layout.fontSize === "28px"), "200% copy button font must not shrink")
  const splitWords = copyWords.flatMap(layout => layout.words.filter(entry => entry.rectangles.length !== 1).map(entry => ({theme: layout.theme, width: layout.width, state: layout.state, ...entry})))
  assert.deepEqual(splitWords, [], "English copy button words must each occupy one Range rectangle, before and after copying")
  evidence.push("200-percent-english-copy-command-and-copied-label-whole-words")
}

try {
  await until(() => existsSync(join(directory, "ready.json")), "real TLS fixture was not ready")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browser(["open", ready.url + "/explore"]); started = true
  await results()
  if (!values["checks-only"] && !values["geometry-only"]) for (const theme of ["light", "dark"]) for (const locale of ["zh-CN", "en"]) for (const width of [1440, 375]) {
    await preferences(theme, locale, width)
    capture(`explore-${theme}-${locale}-${width}`, false)
    capture(`explore-full-${theme}-${locale}-${width}`)
    await load("/explore?q=Change%20review"); await results(3)
    const detailPath = evaluate("Array.from(document.querySelectorAll('.catalog-skill-link')).find(node=>node.textContent==='Change review').getAttribute('href')")
    await load(detailPath)
    await until(() => evaluate("!!document.querySelector('pre code')"), "real public detail missing")
    assert.equal(overflow(), false, `detail overflow at ${width}/${locale}/${theme}`)
    assert.match(evaluate("document.querySelector('pre code').textContent"), /^cognate install-public --server '/)
    capture(`detail-${theme}-${locale}-${width}`)
  }
  if (values.phase === "after" && !values["geometry-only"]) {
    await preferences("light", "zh-CN", 1440)
    assert.equal(evaluate("!!document.querySelector('.public-catalog [aria-label=\"当前筛选\"]')"), true, "applied-filter context missing")
    assert.equal(evaluate("document.querySelector('#public-skills-title').textContent"), "公开技能")
    const firstIDs = evaluate("Array.from(document.querySelectorAll('.catalog-skill-link'), node => node.getAttribute('href'))")
    click("下一页"); await results(3)
    const lastIDs = evaluate("Array.from(document.querySelectorAll('.catalog-skill-link'), node => node.getAttribute('href'))")
    assert(lastIDs.every((id) => !firstIDs.includes(id)))
    assert.equal(evaluate("Array.from(document.querySelectorAll('button')).find(node=>node.textContent==='下一页').disabled"), true)
    click("上一页"); await results()
    evidence.push("real-cursor-pagination-24-plus-3-no-duplicates")

    const category = evaluate("document.querySelector('#catalog-category option[value]:not([value=\"\"])').value")
    browser(["select", "#catalog-category", category])
    click("更多分类")
    await until(() => evaluate("document.querySelector('#catalog-category').options.length===4 && document.querySelector('#catalog-category').selectedOptions[0].textContent.includes('不在本页')"), "selected category not preserved across category pages")
    click("查找技能")
    await until(() => evaluate("new URL(location.href).searchParams.has('category') && document.querySelectorAll('.catalog-results > li').length > 0 && document.querySelectorAll('.catalog-results > li').length < 24"), "category filter not applied")
    assert.equal(new URL(evaluate("location.href")).searchParams.get("category"), category)
    click("清除筛选"); await results()
    evidence.push("real-category-pages-preserve-selection-and-filter")

    browser(["fill", "#catalog-query", "Change review"]); browser(["press", "Enter"])
    await until(() => evaluate("new URL(location.href).searchParams.get('q') === 'Change review' && document.querySelectorAll('.catalog-results > li').length===3"), "search did not use real query")
    capture("search-results")
    click("清除筛选"); await results()
    const beforeInvalid = (await control("state")).requests.filter((request) => request.path.endsWith("/ListSkills")).length
    browser(["fill", "#catalog-query", "界".repeat(43)]); browser(["press", "Enter"])
    await until(() => evaluate("!!document.querySelector('[aria-invalid=true]')"), "UTF-8 validation missing")
    assert.equal((await control("state")).requests.filter((request) => request.path.endsWith("/ListSkills")).length, beforeInvalid)
    capture("utf8-validation")
    await load(`/explore?q=${encodeURIComponent("界".repeat(43))}`)
    await until(() => evaluate("document.querySelector('main').textContent.includes('当前未请求技能列表')"), "deep-link byte guard missing")
    assert.equal((await control("state")).requests.filter((request) => request.path.endsWith("/ListSkills")).length, beforeInvalid)
    evidence.push("utf8-form-and-deep-link-guard-no-list-request")

    await load("/explore?q=no-public-fixture-match")
    await until(() => evaluate("document.querySelector('main').textContent.includes('没有找到公开技能')"), "empty state missing")
    capture("empty-results")
    await control("mode", "slow")
    await load("/explore?q=Change%20review")
    assert.equal(evaluate("document.querySelector('main').textContent.includes('正在读取公开技能…')"), true)
    capture("loading-results")
    await results(3)
    await control("mode", "error")
    await load()
    await until(() => evaluate("!!document.querySelector('main [role=alert]')"), "error state missing")
    capture("error-results")
    await control("mode", "normal")
    browser(["click", ".public-catalog .list-section .error-notice button"])
    await results()
    browser(["click", ".public-catalog .catalog-search .error-notice button"])
    await until(() => evaluate("!document.querySelector('main [role=alert]')"), "category error did not recover independently")
    capture("recovered-results")
    evidence.push("real-empty-loading-fault-and-retry-states")

    await control("mode", "repeat")
    await load(); await results()
    click("下一页"); await results(3)
    await until(() => evaluate("document.querySelector('main').textContent.includes('已达到本次分页浏览边界')"), "repeated skill cursor guard missing")
    assert.equal(evaluate("Array.from(document.querySelectorAll('button')).find(node=>node.textContent==='下一页').disabled"), true)
    click("更多分类")
    await until(() => evaluate("document.querySelector('main').textContent.includes('已达到本次分类浏览边界')"), "repeated category cursor guard missing")
    capture("repeated-cursor-guards")
    await control("mode", "normal")
    evidence.push("repeated-skill-and-category-cursors-stop-without-auto-fetch")

    await load(); await results()
    browser(["press", "Tab"])
    await until(() => evaluate("document.activeElement?.classList.contains('skip-link')"), "keyboard skip link missing")
    browser(["press", "Enter"]); browser(["press", "Tab"])
    assert.equal(evaluate("document.activeElement?.id"), "catalog-query")
    browser(["fill", "#catalog-query", "Change review"])
    browser(["press", "Tab"]); assert.equal(evaluate("document.activeElement?.id"), "catalog-category")
    browser(["press", "Tab"]); browser(["press", "Enter"])
    await results(3)
    capture("keyboard-search")
    evidence.push("real-tab-skip-search-category-submit-order")

    const path = evaluate("document.querySelector('.catalog-skill-link').getAttribute('href')")
    await load(path)
    await until(() => evaluate("!!document.querySelector('pre code')"), "detail did not load")
    const command = evaluate("document.querySelector('pre code').textContent")
    assert.equal(command, `cognate install-public --server '${ready.url}' --skill '${path.split('/').at(-1)}'`)
    assert.equal(evaluate("!!document.querySelector('a[href=\"https://github.com/catalog-fixture/review-tools\"][rel=noreferrer]')"), true)
    click("复制命令")
    await until(() => evaluate("document.querySelector('main').textContent.includes('仅复制了命令，尚未执行安装。')"), "copy did not acknowledge copy-only state")
    capture("copy-command-only")
    evaluate("Object.defineProperty(navigator.clipboard, 'writeText', {configurable:true, value: async () => {throw new Error('synthetic clipboard denial')}})")
    click("已复制命令")
    await until(() => evaluate("document.querySelector('main').textContent.includes('浏览器无法复制，请手动选择命令。')"), "clipboard failure state missing")
    capture("copy-failed-manual-command")
    await load("/explore/missing-public-fixture")
    await until(() => evaluate("!!document.querySelector('main [role=alert]')"), "unavailable detail not surfaced")
    assert.equal(evaluate("!!document.querySelector('pre code')"), false)
    capture("unavailable-detail")
    evidence.push("exact-anonymous-command-source-copy-only-denial-and-not-found")

    browser(["set", "media", "dark", "reduced-motion"])
    await preferences("dark", "en", 375)
    browser(["set", "media", "dark", "reduced-motion"])
    assert.equal(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true)
    assert.equal(evaluate("document.querySelector('.public-catalog').getAnimations({subtree:true}).some(animation=>animation.playState==='running')"), false)
    assert.equal(evaluate("getComputedStyle(document.querySelector('.catalog-skill-link svg')).transitionDuration"), "0s")
    capture("reduced-motion-en-375")
    evaluate("document.documentElement.style.fontSize='125%'")
    assert.equal(overflow(), false)
    capture("large-text-en-375")
    const largeTextPath = evaluate("document.querySelector('.catalog-skill-link').getAttribute('href')")
    await load(largeTextPath)
    evaluate("document.documentElement.style.fontSize='125%'")
    await until(() => evaluate("!!document.querySelector('pre code')"), "large-text detail missing")
    assert.equal(overflow(), false)
    capture("large-text-detail-en-375")
    browser(["set", "viewport", "812", "375"])
    assert.equal(overflow(), false)
    capture("landscape-detail-en-812x375")
    evidence.push("reduced-motion-125-percent-text-and-landscape-no-overflow")
  }
  if (values.phase === "after" || values["geometry-only"]) {
    await reflowMatrix()
    const requests = (await control("state")).requests
    assert(requests.every((request) => request.authorization === false))
    assert(requests.every((request) => !request.path.endsWith("/DownloadSkill")))
    assert(requests.every((request) => !JSON.stringify(request).includes("PRIVATE-FIXTURE-CATALOG")))
    writeFileSync(join(output, "public-rpc-trace.json"), JSON.stringify(requests, null, 2), { mode: 0o600 })
    evidence.push("anonymous-only-no-download-no-private-fixture-data")
  }
} catch (error) { failure = error }
finally {
  if (ready) { try { writeFileSync(join(output, "public-rpc-trace.json"), JSON.stringify((await control("state")).requests, null, 2), { mode: 0o600 }) } catch (error) { failure ??= error } }
  if (started) { try { browser(["close"]) } catch (error) { failure ??= error } }
  if (ready) { try { await control("stop", "") } catch (error) { failure ??= error } }
  else fixture.kill("SIGTERM")
  const exited = await Promise.race([terminal, delay(10000).then(() => ({ code: null, signal: "timeout" }))])
  if (exited.signal === "timeout") fixture.kill("SIGTERM")
  closeSync(log)
  if (!failure) { try { assert.equal(exited.code, 0, "fixture did not join cleanly") } catch (error) { failure = error } }
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ phase: values.phase, evidence, fixtureExit: exited, pass: !failure, ...(failure ? { error: failure.stack } : {}) }, null, 2), { mode: 0o600 })
}
if (failure) throw failure
console.log(JSON.stringify({ pass: true, phase: values.phase, evidence: evidence.length, output }))
