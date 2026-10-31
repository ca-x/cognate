import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync, rmSync, closeSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" }, "focus-regression": { type: "boolean" } } })
for (const key of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[key], `missing ${key}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestPacksUIBrowserFixture$", "-test.v", "-test.timeout=16m"], { env: { ...process.env, COGNATE_PACKS_UI_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise(done => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `packs-ui-${process.pid}-${Date.now()}`
const evidence = []
const focusObservations = []
const delay = duration => new Promise(done => setTimeout(done, duration))
let ready, failure
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${args[0]} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} rejected`)
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
async function control(path, body) {
  const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200, `control ${path}`)
  return body === undefined ? response.json() : undefined
}
const mode = (Mode, Target) => control("mode", { Mode, Target })
const count = async procedure => (await control("state")).counts[procedure] ?? 0
function click(name) { browser(["find", "role", "button", "click", "--name", name, "--exact"]) }
function fill(selector, value) { browser(["fill", selector, value]) }
function has(text) { return evaluate(`document.querySelector('main').textContent.includes(${JSON.stringify(text)})`) }
function capture(name, full = true) {
  assert.equal(evaluate("!!document.querySelector('#created-share-token')"), false, "screenshots must not contain credentials")
  assert.equal(evaluate("!!document.querySelector('.pack-sharing[aria-busy=true]')"), false, "never capture an in-flight token issuance")
  browser(["screenshot", join(output, `${name}.png`), ...(full ? ["--full"] : [])])
  evidence.push(name)
}
async function detail() { await until(() => evaluate("!!document.querySelector('.pack-detail-heading')"), "real Pack detail did not load") }
function geometry(label) {
  const result = evaluate(`(() => {
    const root = document.querySelector('.packs-ui') || document.querySelector('main')
    const dialog = document.querySelector('[role=dialog]')
    const nodes = [...root.querySelectorAll('*'), ...(dialog ? [dialog, ...dialog.querySelectorAll('*')] : [])]
    const elements = nodes.filter(node => getComputedStyle(node).visibility !== 'hidden').flatMap(node => {
      const rect = node.getBoundingClientRect()
      return rect.width && (rect.left < -1 || rect.right > innerWidth + 1) ? [{tag: node.tagName, class: node.className}] : []
    })
    const splitWords = [], wordMetrics = [], checked = new Set()
    for (const element of nodes.filter(node => node.matches('button, p, h1, h2, h3, h4, label'))) {
      if (element.closest('.font-mono, code') || element.textContent.includes('/')) continue
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
      while (walker.nextNode()) {
        if (checked.has(walker.currentNode)) continue
        checked.add(walker.currentNode)
        for (const match of walker.currentNode.textContent.matchAll(/[A-Za-z]+/g)) {
        const range = document.createRange(); range.setStart(walker.currentNode, match.index); range.setEnd(walker.currentNode, match.index + match[0].length)
        if (range.getClientRects().length > 1) { splitWords.push(match[0]); wordMetrics.push({word: match[0], element: element.outerHTML.slice(0, 250), width: element.getBoundingClientRect().width, font: getComputedStyle(element).font, rects: [...range.getClientRects()].map(rect => ({width: rect.width, top: rect.top}))}) }
        }
      }
    }
    return { width: innerWidth, scrollWidth: document.documentElement.scrollWidth, elements, splitWords, wordMetrics }
  })()`)
  evidence.push({ label, ...result })
  assert.deepEqual(result.elements, [], `${label}: out-of-bounds elements`)
  assert.deepEqual(result.splitWords, [], `${label}: split readable words`)
  assert(result.scrollWidth <= result.width + 1, `${label}: overflow`)
}
async function keyboardTo(selector, key = "Enter") {
  for (let index = 0; index < 180; index++) {
    if (evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`)) {
      const visible = evaluate("(() => { const rect = document.activeElement.getBoundingClientRect(); return rect.top >= 0 && rect.bottom <= innerHeight && rect.left >= 0 && rect.right <= innerWidth })()")
      assert(visible, "keyboard target must be fully revealed")
      if (key) browser(["press", key]); return
    }
    browser(["press", "Tab"])
  }
  assert.fail("keyboard target not reachable")
}
async function locale(value) {
  browser(["click", ".preferences > :last-child"])
  browser(["find", "role", "option", "click", "--name", value === "en" ? "English" : "简体中文", "--exact"])
  assert.equal(evaluate("document.documentElement.lang"), value)
}
async function theme(value) {
  browser(["click", ".preferences > :first-child"])
  const chinese = evaluate("document.documentElement.lang === 'zh-CN'")
  browser(["find", "role", "option", "click", "--name", value === "system" ? chinese ? "跟随系统" : "System" : value === "dark" ? chinese ? "深色" : "Dark" : chinese ? "浅色" : "Light", "--exact"])
}
async function focusRegression() {
  click("创建分享令牌")
  await until(() => evaluate("!!document.querySelector('#created-share-token')"), "real share issuance unavailable")
  click("已保存，隐藏令牌")
  await until(() => evaluate("!!document.querySelector('.share-record button')"), "real share action unavailable")
  const procedures = ["CreateShare", "RevokeShare", "UpdatePack", "DeletePack"]
  const writes = async () => Object.fromEntries(await Promise.all(procedures.map(async procedure => [procedure, await count(procedure)])))
  const baseline = await writes()
  const action = ".share-record button"
  const refresh = ".pack-sharing .packs-section-heading button"
  const heading = ".pack-sharing h4"
  const preference = ".preferences > :last-child"
  async function background(label, target, error, expected) {
    await keyboardTo(target, "")
    await delay(31000)
    assert.equal(evaluate(`document.activeElement.matches(${JSON.stringify(target)})`), true, "ordinary renders stole focus before background query")
    await mode(error ? "error" : "normal", error ? "ListShares" : "")
    const reads = await count("ListShares")
    evaluate("window.dispatchEvent(new Event('visibilitychange'))")
    await until(async () => await count("ListShares") === reads + 1, "stale background ListShares did not execute")
    await until(() => evaluate(`!document.querySelector(${JSON.stringify(refresh)}).disabled`), "background query did not settle")
    const observation = evaluate(`(() => ({ label: ${JSON.stringify(label)}, focused: document.activeElement.matches(${JSON.stringify(expected)}), activeTag: document.activeElement.tagName, actions: document.querySelectorAll('.share-record button').length, unknown: !!document.querySelector('.pack-sharing .packs-scope[role=status]'), createDisabled: document.querySelector('.pack-share-controls > button').disabled }))()`)
    observation.listReads = await count("ListShares") - reads
    observation.writesUnchanged = JSON.stringify(await writes()) === JSON.stringify(baseline)
    focusObservations.push(observation)
    capture(label)
    assert.equal(observation.listReads, 1)
    assert.equal(observation.writesUnchanged, true, "background query replayed a mutation")
    assert.equal(observation.unknown, error)
    assert.equal(observation.actions, error ? 0 : 1, "unknown state must hide stale revoke actions")
    if (error) assert.equal(observation.createDisabled, true)
    assert.equal(observation.focused, true, "background ListShares must hand off removed-action focus only, without stealing other focus")
  }
  async function recover(label) {
    await mode("normal", "")
    const reads = await count("ListShares")
    await keyboardTo(refresh, "Enter")
    await until(() => evaluate("!!document.querySelector('.share-record button') && !document.querySelector('.pack-sharing .packs-scope[role=status]')"), "explicit keyboard read did not recover share state")
    assert.equal(await count("ListShares"), reads + 1)
    assert.deepEqual(await writes(), baseline, "explicit recovery replayed a mutation")
    focusObservations.push({ label, listReads: 1, actions: 1, unknown: false, writesUnchanged: true, keyboard: "Tab/Enter" })
    capture(label)
  }
  await background("focused-action-background-error", action, true, heading)
  await recover("focused-action-explicit-recovery")
  await locale("en"); await theme("light")
  await background("other-focus-background-error", preference, true, preference)
  await recover("other-focus-explicit-recovery")
  await background("focused-action-background-success", action, false, action)
  assert.deepEqual(await writes(), baseline)
  evidence.push("stale-background-focus-handoff-no-steal-explicit-recovery-no-mutation")
}
async function matrix(stage) {
  for (const language of ["zh-CN", "en"]) {
    await locale(language)
    for (const appearance of ["light", "dark"]) {
      await theme(appearance)
      for (const width of [1440, 375, 320]) for (const scale of width === 1440 ? [100] : [100, 200]) {
        browser(["set", "viewport", String(width), "960"])
        evaluate(`document.documentElement.style.fontSize = '${scale}%'`)
        const label = `${stage}-${language}-${appearance}-${width}-${scale}pct`
        geometry(label)
        if (scale === 200 || width === 1440) capture(label)
      }
      evaluate("document.documentElement.style.fontSize = ''")
    }
  }
  browser(["set", "viewport", "1440", "960"])
  await locale("zh-CN"); await theme("dark")
}
async function confirmationMatrix() {
  for (const language of ["zh-CN", "en"]) {
    await locale(language)
    for (const appearance of ["light", "dark"]) {
      await theme(appearance)
      for (const width of [375, 320]) {
        browser(["set", "viewport", String(width), "960"])
        evaluate("document.documentElement.style.fontSize = '200%'")
        browser(["click", ".share-record button"])
        await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "confirmation missing")
        const motion = evaluate("(() => { const dialog = document.querySelector('[role=dialog]'); return {name: getComputedStyle(dialog).animationName, duration: getComputedStyle(dialog).animationDuration, mode: dialog.dataset.motion} })()")
        assert.equal(motion.mode, "on")
        assert.equal(motion.name, "panel-enter")
        await keyboardTo("[role=dialog] .danger-button", null)
        const label = `confirm-${language}-${appearance}-${width}-200pct`
        geometry(label); capture(label, false)
        browser(["press", "Escape"])
        assert.equal(evaluate("document.activeElement.matches('.share-record button')"), true)
        evaluate("document.documentElement.style.fontSize = ''")
      }
    }
  }
  browser(["set", "viewport", "1440", "960"])
  await locale("zh-CN"); await theme("dark")
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture did not start")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  assert.equal((await fetch(ready.controlURL + "/state")).status, 403)
  browser(["open", ready.url + "/packs"])
  evaluate("localStorage.setItem('skillhub.locale','zh-CN'); localStorage.setItem('skillhub.theme','dark')")
  browser(["open", ready.url + "/packs"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login unavailable")
  fill("#username", ready.username); fill("#password", ready.password); click("登录")
  await detail()
  if (values["focus-regression"]) {
    await focusRegression()
  } else {
  capture(`${values.phase}-detail-dark-zh`)
  click("编辑"); fill("#pack-name", "Draft survives refresh")
  await mode("error", "ListPacks")
  click("刷新")
  await until(() => evaluate("!!document.querySelector('main [role=alert]')"), "read failure not surfaced")
  assert.equal(evaluate("document.querySelector('#pack-name')?.value"), "Draft survives refresh", "background refresh discarded the draft")
  assert.equal(has("配置附件自动应用尚未提供"), true, "missing explicit configuration boundary")
  evidence.push("draft-and-configuration-boundary")
  capture("cached-list-error-draft-preserved")
  await mode("normal", ""); click("刷新")
  await until(() => evaluate("!document.querySelector('main [role=alert]')"), "list read did not recover")
  await mode("error", "GetPack"); click("刷新")
  await until(() => evaluate("!!document.querySelector('main [role=alert]')"), "detail read error missing")
  assert.equal(evaluate("document.querySelector('#pack-name')?.value"), "Draft survives refresh")
  assert.equal(evaluate("document.querySelector('button[type=submit]').disabled"), true)
  capture("cached-detail-error-draft-preserved")
  await mode("normal", ""); click("刷新")
  await until(() => evaluate("!document.querySelector('main [role=alert]')"), "detail read did not recover")
  await matrix("editor")
  assert.equal(evaluate("document.querySelector('#pack-name')?.value"), "Draft survives refresh", "preferences/reflow discarded draft")
  assert.equal(evaluate("document.querySelectorAll('.pack-picked').length"), 2)
  await control("conflict", {}); click("刷新")
  await until(() => evaluate("!document.querySelector('main [role=alert]')"), "refresh before CAS failed")
  click("保存组合")
  await until(() => has("组合已在其他会话中更改"), "real CAS conflict not surfaced")
  assert.equal(evaluate("document.querySelector('#pack-name').value"), "Draft survives refresh")
  assert.equal(evaluate("document.querySelector('button[type=submit]').disabled"), true)
  capture("cas-conflict-draft-preserved")
  browser(["click", ".pack-editor > .pack-detail-heading button"])
  await detail(); click("刷新"); click("编辑")
  fill("#pack-name", "Engineering review updated")
  click("保存组合"); await detail()
  await until(() => has("Engineering review updated"), "real Pack update not visible")
  assert((await control("state")).packs.some(entry => entry.Name === "Engineering review updated" && entry.Revision === 3))
  evidence.push("real-update-and-revision-CAS")
  await matrix("detail")
  await theme("system")
  browser(["set", "media", "light"]); assert.equal(evaluate("document.documentElement.dataset.theme"), "light")
  browser(["set", "media", "dark"]); assert.equal(evaluate("document.documentElement.dataset.theme"), "dark")
  await theme("dark")
  await mode("slow", "CreateShare")
  click("创建分享令牌")
  assert.equal(has("正在创建…"), true)
  await until(() => evaluate("!!document.querySelector('#created-share-token')"), "real share token not returned")
  assert.equal(evaluate("document.querySelector('#created-share-token').value.startsWith('shp1.') && document.querySelector('#created-share-token').readOnly"), true)
  const manifest = evaluate("(async () => { const response = await fetch('/skillhub.pack.v1.DistributionService/GetManifest', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1', Authorization: 'Bearer ' + document.querySelector('#created-share-token').value }, body: '{}' }); const data = await response.json(); return {status: response.status, skills: data.skills?.length, revision: data.packRevision} })()")
  assert.equal(manifest.status, 200)
  assert.equal(manifest.skills, 1, "overlapping selections must be deduplicated by real distribution")
  evidence.push({ label: "real-TLS-Connect-manifest-dedup", ...manifest })
  assert.equal(evaluate("[...document.querySelectorAll('.pack-share-controls button')].every(button => button.disabled)"), true)
  click("已保存，隐藏令牌")
  assert.equal(evaluate("!!document.querySelector('#created-share-token')"), false)
  assert.equal(evaluate("JSON.stringify({...localStorage, ...sessionStorage}).includes('shp1.') || location.href.includes('shp1.')"), false)
  capture("share-created-secret-hidden")
  await matrix("shares")
  await confirmationMatrix()
  await mode("error", "ListShares"); click("重新读取分享状态")
  await until(() => has("分享状态未知"), "cached share read failure not explicit")
  assert.equal(evaluate("document.querySelector('.pack-share-controls > button').disabled"), true)
  capture("cached-share-status-unknown")
  await mode("normal", ""); click("重新读取分享状态")
  await until(() => evaluate("!!document.querySelector('.share-record')"), "shares did not recover")
  browser(["set", "viewport", "320", "960"])
  evaluate("document.documentElement.style.fontSize = '200%'")
  await keyboardTo(".share-record button")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "keyboard revoke confirmation missing")
  assert.equal(evaluate("getComputedStyle(document.querySelector('[role=dialog]')).animationName"), "none")
  browser(["press", "Escape"])
  assert.equal(evaluate("document.activeElement.matches('.share-record button')"), true)
  await keyboardTo(".share-record button", "Space")
  await keyboardTo("[role=dialog] .danger-button")
  await until(() => has("已撤销"), "real revocation did not persist")
  assert.equal(evaluate("document.activeElement.textContent"), "已有分享")
  capture("keyboard-revoke-320-200pct")
  evidence.push("real-keyboard-Tab-Space-Enter-Escape-focus-restoration")
  evaluate("document.documentElement.style.fontSize = ''"); browser(["set", "viewport", "1440", "960"])
  await control("expire", {}); click("重新读取分享状态")
  await until(() => has("已过期"), "expired share not shown")
  capture("share-expired")
  const shareWrites = await count("CreateShare")
  await mode("lost", "CreateShare"); click("创建分享令牌")
  await until(() => has("创建结果未确认"), "lost share receipt not surfaced")
  assert.equal(evaluate("document.querySelector('.pack-share-controls > button').disabled"), true)
  await delay(350); assert.equal(await count("CreateShare"), shareWrites + 1, "share write replayed")
  assert.equal(evaluate("!!document.querySelector('#created-share-token')"), false)
  capture("lost-share-receipt-no-replay")
  click("重新读取分享状态")
  await until(() => evaluate("!document.querySelector('.pack-share-controls > button').disabled"), "explicit share reconciliation failed")
  click("撤销")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "revoke dialog missing")
  await mode("lost", "RevokeShare")
  const revokeWrites = await count("RevokeShare")
  browser(["click", "[role=dialog] .danger-button"])
  await until(() => has("撤销结果未确认"), "lost revocation receipt missing")
  browser(["press", "Escape"])
  assert.equal(await count("RevokeShare"), revokeWrites + 1)
  click("重新读取分享状态")
  await until(() => evaluate("!document.querySelector('.share-record button')"), "reconciled revocation not visible")
  evidence.push("lost-share-and-revocation-real-commits-no-replay")
  click("新建组合")
  browser(["click", ".pack-editor > .pack-detail-heading button"])
  assert.equal(evaluate("document.activeElement.textContent"), "新建组合")
  click("新建组合")
  fill("#pack-name", "Browser created pack")
  await until(() => evaluate("!!document.querySelector('.pack-choices [role=checkbox]')"), "real collected skills missing")
  await keyboardTo(".pack-choices [role=checkbox]", "Space")
  assert.equal(evaluate("document.querySelectorAll('.pack-picked').length"), 1)
  browser(["click", "#pack-repository"])
  browser(["press", "ArrowDown"]); browser(["press", "Enter"])
  await until(() => evaluate("document.querySelector('.pack-choices [role=checkbox]')?.getAttribute('aria-checked') === 'false'"), "repository switch failed")
  await keyboardTo(".pack-choices [role=checkbox]", "Space")
  assert.equal(evaluate("document.querySelectorAll('.pack-picked').length"), 2)
  click("保存组合"); await detail()
  assert.equal(evaluate("document.activeElement.id"), "packs-title")
  assert((await control("state")).packs.some(entry => entry.Name === "Browser created pack" && entry.Revision === 1 && entry.ItemCount === 2))
  evidence.push("real-cross-repository-create-overlap-selection")
  click("编辑"); fill("#pack-name", "Committed without receipt")
  await mode("lost", "UpdatePack")
  const updateWrites = await count("UpdatePack")
  click("保存组合")
  await until(() => has("本次保存未确认"), "lost update receipt not surfaced")
  assert.equal(evaluate("document.querySelector('button[type=submit]').disabled"), true)
  assert.equal(evaluate("document.querySelector('#pack-name').value"), "Committed without receipt")
  assert((await control("state")).packs.some(entry => entry.Name === "Committed without receipt" && entry.Revision === 2))
  await delay(350); assert.equal(await count("UpdatePack"), updateWrites + 1)
  capture("lost-update-receipt-preserved-no-replay")
  browser(["click", ".pack-editor > .pack-detail-heading button"])
  await detail(); click("刷新")
  await until(() => has("Committed without receipt"), "lost update did not reconcile")
  browser(["set", "media", "dark", "reduced-motion"])
  assert.equal(evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true)
  click("删除组合")
  await until(() => evaluate("!!document.querySelector('[role=dialog]')"), "delete confirmation missing")
  assert.equal(evaluate("getComputedStyle(document.querySelector('[role=dialog]')).animationName"), "none")
  capture("delete-confirmation-reduced-motion")
  browser(["press", "Escape"])
  assert.equal(evaluate("document.activeElement.closest('.pack-danger') !== null"), true)
  click("删除组合")
  browser(["click", "[role=dialog] .danger-button"])
  await until(() => (count("DeletePack")).then(value => value === 1), "real delete did not execute")
  await until(() => evaluate("document.querySelectorAll('.collected-repository-select').length === 1"), "real delete not reconciled")
  assert.equal((await control("state")).packs.length, 1)
  assert.equal(evaluate("document.activeElement.id"), "packs-title")
  evidence.push("real-delete-confirmation-reduced-motion-focus")
  click("删除组合")
  await mode("lost", "DeletePack")
  browser(["click", "[role=dialog] .danger-button"])
  await until(() => has("删除未确认"), "lost delete receipt not surfaced")
  await delay(350); assert.equal(await count("DeletePack"), 2)
  assert.equal((await control("state")).packs.length, 0)
  browser(["press", "Escape"]); click("重新读取组合状态")
  await until(() => has("把需要的技能放在一起"), "empty state after lost delete not reconciled")
  capture("empty-after-lost-delete-reconciled")
  assert.equal(evaluate("JSON.stringify({...localStorage, ...sessionStorage}).includes('shp1.')"), false)
  evidence.push("real-delete-receipt-loss-no-replay-and-empty")
  }
} catch (error) {
  failure = error
  try { if (!evaluate("!!document.querySelector('#created-share-token')")) capture("failure") } catch {}
} finally {
  try { browser(["close"]) } catch {}
  try { if (ready) await control("stop", {}) } catch {}
  const exit = await Promise.race([terminal, delay(5000).then(() => null)])
  if (!exit) fixture.kill("SIGTERM")
  if (!failure && exit?.code !== 0) failure = new Error("fixture shutdown failed")
  closeSync(log)
  rmSync(join(directory, "ready.json"), { force: true })
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ phase: values.phase, focusedRegression: !!values["focus-regression"], focusObservations, evidence, failure: failure?.message ?? null, exit }, null, 2), { mode: 0o600 })
}
if (failure) { console.error(failure.message); process.exitCode = 1 } else console.log(`Pack UI: ${evidence.length} evidence entries`)
