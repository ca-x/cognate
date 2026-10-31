import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, phase: { type: "string", default: "after" }, "self-test": { type: "boolean" } } })

function terminalGate(exit, receipt) {
  assert(exit && exit.code === 0 && exit.signal === null && !exit.timeout, "fixture terminal did not complete cleanly")
  assert(receipt && receipt.ok === true && receipt.completed === receipt.expected && receipt.completed > 0 && receipt.failure === null, "missing or contradictory browser receipt")
}

if (values["self-test"]) {
  const good = { ok: true, expected: 1, completed: 1, failure: null }
  terminalGate({ code: 0, signal: null }, good)
  const cases = [
    [{ code: 3, signal: null }, good], [{ code: null, signal: "SIGTERM" }, good],
    [{ code: 0, signal: null, timeout: true }, good], [null, good],
    [{ code: 0, signal: null }, null], [{ code: 0, signal: null }, { ...good, ok: false }],
    [{ code: 0, signal: null }, { ...good, completed: 0 }], [{ code: 0, signal: null }, { ...good, failure: "late failure" }],
  ]
  for (const [exit, receipt] of cases) assert.throws(() => terminalGate(exit, receipt))
  const children = [
    spawnSync(process.execPath, ["-e", "process.exit(7)"]),
    spawnSync(process.execPath, ["-e", "process.kill(process.pid, 'SIGTERM')"]),
    spawnSync(process.execPath, ["-e", "setInterval(()=>{},1000)"], { timeout: 80 }),
    spawnSync(process.execPath, ["-e", "process.stdout.write('{}')"]),
    spawnSync(process.execPath, ["-e", "process.stdout.write(JSON.stringify({ok:true,expected:1,completed:0,failure:null}))"]),
  ]
  for (const child of children) assert.throws(() => terminalGate({ code: child.status, signal: child.signal, timeout: child.error?.code === "ETIMEDOUT" }, child.stdout?.length ? JSON.parse(child.stdout) : null))
  console.log(`terminal gate: ${cases.length} negative receipts and ${children.length} real abnormal children rejected`)
  process.exit(0)
}

for (const key of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[key], `missing ${key}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "private-fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestPackConfigurationUIBrowserFixture$", "-test.v", "-test.timeout=13m"], { env: { ...process.env, COGNATE_PACK_CONFIGURATION_UI_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise(done => fixture.once("exit", (code, signal) => done({ code, signal })))
const session = `pack-configuration-${process.pid}`
const evidence = []
const geometryEvidence = []
const fullExpected = ["owner-attachment-entry", "cancel-zero-writes", "ordered-replacement-dual-revisions", "remove-and-clear", "maximum-and-no-duplicates", "stale-explicit-revision", "missing-reference-retained", "asset-CAS-refusal", "pack-CAS-refusal", "configuration-CAS-refusal", "preflight-changed-no-write", "known-owner-refusal", "lost-committed-unknown", "malformed-committed-unknown", "malformed-error-unknown", "duplicate-error-unknown", "malformed-details-unknown", "cancel-after-commit-unknown", "cancel-before-dispatch", "read-errors-preserve-draft", "pack-switch-stale-review", "layout-keyboard-motion", "owner-isolation", "skills-shares-unchanged", "unsupported-retained", "delete-invalidates", "logout-invalidates", "metadata-only-storage"]
const focusExpected = ["owner-attachment-entry", "focus-refresh", "focus-background-refresh", "focus-add", "focus-move-up", "focus-move-down", "focus-remove", "focus-remove-last", "focus-clear", "focus-discard"]
const expected = values.phase === "focus" ? focusExpected : values.phase === "layout" ? ["owner-attachment-entry", "layout-keyboard-motion"] : fullExpected
const delay = duration => new Promise(done => setTimeout(done, duration))
let ready, failure
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
  assert.equal(result.status, 0, `browser ${["find", "click"].includes(args[0]) ? args.join(" ") : args[0]} failed`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, `browser ${args[0]} rejected`)
  return parsed.data
}
function evaluate(script) { const result = browser(["eval", "--stdin"], script); return result && Object.hasOwn(result, "result") ? result.result : result }
async function until(check, message) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
async function control(path, body) {
  const response = await fetch(ready.controlURL + "/" + path, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200, `fixture ${path} failed`)
  return path === "state" ? response.json() : undefined
}
function click(text) { browser(["find", "role", "button", "click", "--name", text, "--exact"]) }
function capture(label, full = true) {
  assert(!evaluate("document.body.textContent.includes('PRIVATE_ATTACHMENT_BODY_CANARY') || document.body.textContent.includes('PRIVATE_OTHER_BODY_CANARY') || !!document.querySelector('#created-share-token')"), "unsafe screenshot surface")
  browser(["screenshot", join(output, `${label}.png`), ...(full ? ["--full"] : [])])
}
function record(label) { assert(expected.includes(label) && !evidence.includes(label), `unexpected or duplicate receipt ${label}`); evidence.push(label) }
const mode = (Mode, Target = "ReplaceAttachments") => control("mode", { Mode, Target })
const count = async procedure => (await control("state")).counts[procedure] ?? 0
const text = () => evaluate("document.querySelector('.pack-configurations')?.textContent || ''")
const press = key => browser(["press", key])
async function newEdit() {
  browser(["click", ".pc-refresh"])
  await until(() => evaluate("!!document.querySelector('.pc-edit:not(:disabled)')"), "verified edit unavailable")
  browser(["click", ".pc-edit"])
  await until(() => evaluate("!!document.querySelector('.pc-editor')"), "editor unavailable")
}
async function add(index) {
  browser(["click", ".pc-candidate [role=combobox]"])
  const position = evaluate(`[...document.querySelectorAll('.pc-select [role=option]')].findIndex(option => option.textContent.includes(${JSON.stringify(ready.assetIds[index].name)}))`)
  assert(position >= 0, "eligible metadata choice absent")
  press("Home")
  for (let offset = 0; offset < position; offset++) press("ArrowDown")
  press("Enter")
  assert(evaluate(`document.querySelector('.pc-candidate [role=combobox]').textContent.includes(${JSON.stringify(ready.assetIds[index].name)})`), `selected asset differs from explicit choice ${index}`)
  browser(["click", ".pc-add"])
}
function review() {
  browser(["click", ".pc-review"])
  assert.equal(evaluate("document.querySelector('.pc-approval [role=checkbox]').getAttribute('data-state')"), "unchecked")
  assert.equal(evaluate("document.querySelector('.pc-confirm').disabled"), true)
  assert(evaluate("document.querySelector('.pc-context').textContent.includes(location.origin)"))
}
function confirm() { browser(["click", ".pc-approval [role=checkbox]"]); browser(["click", ".pc-confirm"]) }
async function success() { await until(() => text().includes("Attachment metadata replaced"), "replacement receipt missing") }
async function unknown() {
  await until(() => text().includes("Submission is Unknown"), "Unknown state absent")
  const writes = await count("ReplaceAttachments")
  assert.equal(evaluate("document.querySelector('.pc-edit').disabled"), true)
  assert.equal(evaluate("!!document.querySelector('.pc-confirm, .pc-review')"), false)
  browser(["click", ".pc-refresh"])
  await until(() => text().includes("old draft is for inspection only"), "explicit reconciliation absent")
  assert.equal(await count("ReplaceAttachments"), writes, "reconciliation replayed mutation")
  assert.equal(evaluate("!!document.querySelector('.pc-editor') && !document.querySelector('.pc-review')"), true)
}
async function keyboardTo(selector, key = "Enter") {
  for (let index = 0; index < 220; index++) {
    if (evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`)) {
      const metrics = evaluate("(() => { const element = document.activeElement; const rect = element.getBoundingClientRect(); const dialog = element.closest('[role=dialog]'); const frame = dialog?.getBoundingClientRect(); return {visible: rect.top >= Math.max(0,frame?.top ?? 0) && rect.bottom <= Math.min(innerHeight,frame?.bottom ?? innerHeight) && rect.left >= 0 && rect.right <= innerWidth, outline: getComputedStyle(element).outlineStyle, rect:{top:rect.top,bottom:rect.bottom,left:rect.left,right:rect.right}, viewport:{width:innerWidth,height:innerHeight}, frame:frame ? {top:frame.top,bottom:frame.bottom}:null} })()")
      assert(metrics.visible, `keyboard target is obscured: ${selector} ${JSON.stringify(metrics)}`)
      assert.notEqual(metrics.outline, "none", "keyboard target has no focus ring")
      if (key) press(key)
      return
    }
    press("Tab")
  }
  assert.fail(`keyboard target unavailable: ${selector}`)
}
function focusedEnter(selector) {
  assert(evaluate(`(() => { const target = document.querySelector(${JSON.stringify(selector)}); if (!target || target.disabled) return false; target.focus(); return document.activeElement === target })()`), `focus target unavailable: ${selector}`)
  press("Enter")
}
function activeWithin(selector) {
  return evaluate(`(() => { const active = document.activeElement; return active !== document.body && !active.disabled && !!active.closest(${JSON.stringify(selector)}) })()`)
}
async function focusRegressions() {
  const writes = await count("ReplaceAttachments")
  await mode("slow", "GetAttachments")
  focusedEnter(".pc-refresh")
  await until(() => evaluate("!!document.querySelector('.pc-edit:not(:disabled)')"), "reload did not finish")
  assert(activeWithin(".pc-refresh"), "reload lost focus after its read completed")
  record("focus-refresh")
  const external = evaluate("(() => { const link = [...document.querySelectorAll('a[href]')].find(element => !element.closest('.pack-configurations')); if (!link) return false; link.focus(); link.dataset.packFocusProbe = 'external'; return document.activeElement === link })()")
  assert(external, "external focus control unavailable")
  evaluate("document.querySelector('.pc-refresh').click()")
  await until(() => evaluate("!!document.querySelector('.pc-edit:not(:disabled)')"), "background reload did not finish")
  assert(evaluate("document.activeElement?.dataset.packFocusProbe === 'external'"), "background read stole external focus")
  record("focus-background-refresh")
  await mode("normal", "")
  await newEdit(); await add(0)
  assert(activeWithin(".pc-candidate [role=combobox]"), "adding a reference lost focus")
  record("focus-add")
  await add(1)
  focusedEnter('.pc-editor button[aria-label="Move item 2 up"]')
  assert(activeWithin(".pc-editor .pc-reference:first-child"), "moving to the first position lost focus")
  record("focus-move-up")
  focusedEnter('.pc-editor button[aria-label="Move item 1 down"]')
  assert(activeWithin(".pc-editor .pc-reference:last-child"), "moving to the last position lost focus")
  record("focus-move-down")
  focusedEnter('.pc-editor button[aria-label="Remove item 1"]')
  assert(activeWithin(".pc-editor .pc-reference"), "removing a reference lost its successor focus")
  record("focus-remove")
  focusedEnter('.pc-editor button[aria-label="Remove item 1"]')
  assert(activeWithin(".pc-candidate [role=combobox]"), "removing the last reference lost focus")
  record("focus-remove-last")
  await add(0)
  focusedEnter('.pc-editor > .pc-actions > button:first-child')
  assert(activeWithin(".pc-candidate [role=combobox]"), "clearing the draft lost focus")
  record("focus-clear")
  focusedEnter('.pc-editor > .pc-actions > button:last-child')
  assert(activeWithin(".pc-edit"), "discarding the draft lost focus")
  assert.equal(await count("ReplaceAttachments"), writes, "draft focus checks dispatched a replacement")
  record("focus-discard")
  capture("focus-stable")
}
function geometry(label) {
  const result = evaluate(`(() => {
    const root = document.querySelector('[role=dialog]') || document.querySelector('.pack-configurations');
    const outside = [...root.querySelectorAll('*')].filter(element => { const rect = element.getBoundingClientRect(); return rect.width && (rect.left < -1 || rect.right > innerWidth + 1) }).map(element => ({tag: element.tagName, class: element.className, text: element.textContent.slice(0,100)}));
    const splitWords = [];
    for (const element of root.querySelectorAll('p, h3, h4, label, button, strong')) {
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) {
        if (walker.currentNode.parentElement.closest('code') || walker.currentNode.parentElement.closest('[role=combobox]')) continue;
        for (const match of walker.currentNode.textContent.matchAll(/[A-Za-z]+/g)) {
          const range = document.createRange(); range.setStart(walker.currentNode,match.index); range.setEnd(walker.currentNode,match.index+match[0].length);
          if (range.getClientRects().length > 1) splitWords.push({word:match[0],tag:element.tagName,class:element.className,width:element.getBoundingClientRect().width,font:getComputedStyle(element).font,padding:getComputedStyle(element).padding});
        }
      }
    }
    return {label: ${JSON.stringify(label)}, width: innerWidth, scrollWidth: document.documentElement.scrollWidth, rootFont: getComputedStyle(document.documentElement).fontSize, outside, splitWords};
  })()`)
  geometryEvidence.push(result)
  assert.equal(result.scrollWidth <= result.width + 1, true, `${label}: page overflow`)
  assert.deepEqual(result.outside, [], `${label}: controls overflow`)
  assert.deepEqual(result.splitWords, [], `${label}: readable words split`)
}
async function locale(language) {
  browser(["click", ".preferences > :last-child"])
  browser(["find", "role", "option", "click", "--name", language === "en" ? "English" : "简体中文", "--exact"])
  assert.equal(evaluate("document.documentElement.lang"), language)
}
async function theme(appearance) {
  const chinese = evaluate("document.documentElement.lang === 'zh-CN'")
  browser(["click", ".preferences > :first-child"])
  const names = chinese ? { dark: "深色", light: "浅色", system: "跟随系统" } : { dark: "Dark", light: "Light", system: "System" }
  browser(["find", "role", "option", "click", "--name", names[appearance], "--exact"])
}

async function layoutMatrix() {
  await newEdit()
  for (const language of ["en", "zh-CN"]) for (const appearance of ["dark", "light"]) {
    await locale(language); await theme(appearance)
    for (const width of [1440, 375, 320]) {
      browser(["set", "viewport", String(width), "960"])
      evaluate("document.documentElement.style.fontSize = '200%'")
      const label = `${language}-${appearance}-${width}-200pct`
      assert.equal(evaluate("getComputedStyle(document.documentElement).fontSize"), "32px")
      geometry(label + "-editor")
      press("Tab")
      await keyboardTo(".pc-review")
      await until(() => evaluate("!!document.querySelector('.pc-dialog')"), "keyboard review missing")
      geometry(label + "-review")
      await keyboardTo(".pc-approval [role=checkbox]", "Space")
      assert.equal(evaluate("document.querySelector('.pc-approval [role=checkbox]').getAttribute('data-state')"), "checked")
      await keyboardTo(".pc-confirm", null)
      geometry(label + "-revealed")
      capture(label + "-keyboard-review", false)
      press("Escape")
      assert.equal(evaluate("document.activeElement.matches('.pc-review')"), true, "dialog did not restore trigger")
      evaluate("document.documentElement.style.fontSize = ''")
    }
  }
  browser(["set", "viewport", "1440", "960"])
  await locale("en"); await theme("system")
  browser(["set", "media", "light"]); assert.equal(evaluate("document.documentElement.dataset.theme"), "light")
  browser(["set", "media", "dark"]); assert.equal(evaluate("document.documentElement.dataset.theme"), "dark")
  browser(["set", "media", "reduced-motion", "reduce"])
  browser(["click", ".pc-review"])
  assert.equal(evaluate("getComputedStyle(document.querySelector('.pc-dialog')).animationName"), "none")
  press("Escape")
  browser(["set", "media", "reduced-motion", "no-preference"])
  await keyboardTo(".pc-review")
  assert.equal(evaluate("getComputedStyle(document.querySelector('.pc-dialog')).animationName"), "none", "keyboard open animated")
  press("Escape")
  record("layout-keyboard-motion")
}

try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture did not start")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  assert.equal((await fetch(ready.controlURL + "/state")).status, 403)
  browser(["open", ready.url + "/packs"])
  evaluate("localStorage.setItem('skillhub.locale','en'); localStorage.setItem('skillhub.theme','dark')")
  browser(["open", ready.url + "/packs"])
  await until(() => evaluate("!!document.querySelector('#username')"), "login unavailable")
  browser(["fill", "#username", ready.username])
  browser(["fill", "#password", ready.password])
  browser(["click", "form button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('.pack-sharing')"), "real Pack detail unavailable")
  assert(evaluate("!!document.querySelector('.pack-configurations')"), "RED: owner attachment management is absent on real Pack detail")
  browser(["find", "role", "button", "click", "--name", "Attachment review · 配置审阅"])
  await until(() => evaluate(`document.querySelector('.pack-configurations h3')?.id === ${JSON.stringify("pc-heading-" + ready.packId)} && !!document.querySelector('.pc-edit:not(:disabled)')`), "baseline owner metadata unavailable")
  record("owner-attachment-entry")
  capture("owner-attachments")

  if (values.phase === "focus") { await focusRegressions() } else if (values.phase === "layout") { await newEdit(); await add(1); review(); confirm(); await success(); await layoutMatrix() } else {
  click("Create share token")
  await until(() => evaluate("!!document.querySelector('#created-share-token')"), "real share creation absent")
  const distribution = evaluate("(async()=>{ const response = await fetch('/skillhub.pack.v1.DistributionService/GetManifest',{method:'POST',headers:{'Content-Type':'application/json','Connect-Protocol-Version':'1',Authorization:'Bearer '+document.querySelector('#created-share-token').value},body:'{}'}); const body=await response.json(); return {status:response.status,skills:body.skills?.length,hasConfiguration:Object.keys(body).some(key=>/configuration|secret/i.test(key))} })()")
  assert.deepEqual(distribution, { status: 200, skills: 1, hasConfiguration: false })
  click("Saved, hide token")
  const shareCount = evaluate("document.querySelectorAll('.share-record').length")
  assert.equal(shareCount, 1)

  await newEdit()
  assert.equal(evaluate("document.querySelector('.pc-add').disabled"), true, "asset was implicitly selected")
  await add(0); await add(1)
  const initialWrites = await count("ReplaceAttachments")
  review(); click("Cancel review")
  assert.equal(await count("ReplaceAttachments"), initialWrites)
  assert.equal(evaluate("document.activeElement.matches('.pc-review')"), true)
  record("cancel-zero-writes")

  browser(["find", "role", "button", "click", "--name", "Move item 2 up", "--exact"])
  const before = (await control("state")).snapshot
  review(); confirm(); await success()
  const ordered = (await control("state")).snapshot
  assert.equal(ordered.PackRevision, before.PackRevision + 1)
  assert.equal(ordered.ConfigurationRevision, before.ConfigurationRevision + 1)
  assert.deepEqual(ordered.Entries.map(entry => entry.AssetID), [ready.assetIds[1].id, ready.assetIds[0].id])
  assert.equal(await count("ReplaceAttachments"), initialWrites + 1)
  record("ordered-replacement-dual-revisions")
  capture("ordered-replacement")

  await newEdit()
  browser(["find", "role", "button", "click", "--name", "Remove item 1", "--exact"])
  review(); confirm(); await success()
  assert.equal((await control("state")).snapshot.Entries.length, 1)
  await newEdit(); click("Clear proposed references"); review(); confirm(); await success()
  assert.equal((await control("state")).snapshot.Entries.length, 0)
  record("remove-and-clear")

  await newEdit()
  for (let index = 0; index < 16; index++) await add(index)
  assert.equal(evaluate("document.querySelectorAll('.pc-editor .pc-reference').length"), 16)
  assert.equal(evaluate("document.querySelector('.pc-candidate [role=combobox]').disabled && document.querySelector('.pc-add').disabled"), true)
  review(); confirm(); await success()
  assert.equal(new Set((await control("state")).snapshot.Entries.map(entry => entry.AssetID + entry.TargetID)).size, 16)
  record("maximum-and-no-duplicates")
  await newEdit(); click("Clear proposed references"); await add(0); review(); confirm(); await success()

  await control("asset-conflict", {})
  await newEdit()
  assert(text().includes("Stale asset revision"))
  assert.equal(evaluate("document.querySelectorAll('.pc-editor .pc-reference').length"), 1)
  click("Use current asset revision"); review(); confirm(); await success()
  assert.equal((await control("state")).snapshot.Entries[0].AssetRevision, 2)
  record("stale-explicit-revision")

  await mode("missing-metadata", "ListAssets")
  browser(["click", ".pc-refresh"])
  await until(() => text().includes("Asset metadata missing; reference retained"), "missing metadata was silently removed")
  assert.equal(evaluate("document.querySelectorAll('.pack-configurations > .pc-references > li').length"), 1)
  record("missing-reference-retained")

  for (const [fault, receipt] of [["asset-race", "asset-CAS-refusal"], ["pack-race", "pack-CAS-refusal"], ["configuration-race", "configuration-CAS-refusal"]]) {
    await newEdit(); click("Clear proposed references"); await add(0)
    const writes = await count("ReplaceAttachments")
    await mode(fault); review(); confirm()
    await until(() => text().includes("revision conflict"), `${fault}: known CAS refusal absent`)
    assert.equal(await count("ReplaceAttachments"), writes + 1)
    assert.equal(evaluate("!!document.querySelector('.pc-editor') && !document.querySelector('.pc-review')"), true)
    record(receipt)
  }

  await newEdit(); await add(1); review()
  const preflightWrites = await count("ReplaceAttachments")
  await control("pack-conflict", {}); confirm()
  await until(() => text().includes("no replacement was sent"), "changed review was submitted")
  assert.equal(await count("ReplaceAttachments"), preflightWrites)
  record("preflight-changed-no-write")

  await newEdit(); await mode("denied"); review(); confirm()
  await until(() => text().includes("current owner cannot access"), "permission refusal mislabeled Unknown")
  record("known-owner-refusal")

  for (const [fault, receipt] of [["lost", "lost-committed-unknown"], ["malformed", "malformed-committed-unknown"], ["malformed-error", "malformed-error-unknown"], ["duplicate-error", "duplicate-error-unknown"], ["malformed-details", "malformed-details-unknown"], ["committed-slow", "cancel-after-commit-unknown"]]) {
    await newEdit(); click("Clear proposed references"); await add(1)
    const snapshot = (await control("state")).snapshot
    const writes = await count("ReplaceAttachments")
    await mode(fault); review(); confirm()
    await until(async () => (await control("state")).snapshot.ConfigurationRevision === snapshot.ConfigurationRevision + 1, `${fault}: real commit absent`)
    if (fault === "committed-slow") click("Stop waiting and close")
    await unknown()
    assert.equal(await count("ReplaceAttachments"), writes + 1)
    record(receipt)
    capture(receipt)
  }

  await newEdit(); review()
  const beforeCancel = await count("ReplaceAttachments")
  await mode("slow", "GetAttachments"); confirm(); click("Stop waiting and close")
  await delay(200)
  assert.equal(await count("ReplaceAttachments"), beforeCancel)
  record("cancel-before-dispatch")

  await newEdit()
  const draftCount = evaluate("document.querySelectorAll('.pc-editor .pc-reference').length")
  await mode("error", "GetAttachments"); browser(["click", ".pc-refresh"])
  await until(() => text().includes("Read failed or Pack revision is unverified"), "read error absent")
  assert.equal(evaluate("document.querySelector('.pc-review').disabled"), true)
  assert.equal(evaluate("document.querySelectorAll('.pc-editor .pc-reference').length"), draftCount)
  await mode("normal", ""); await newEdit()
  await mode("error", "ListAssets"); browser(["click", ".pc-refresh"])
  await until(() => text().includes("Read failed or Pack revision is unverified"), "asset metadata read error absent")
  assert.equal(evaluate("document.querySelector('.pc-review').disabled"), true)
  await mode("normal", ""); await newEdit()
  record("read-errors-preserve-draft")

  const beforeSwitch = await count("ReplaceAttachments")
  browser(["find", "role", "button", "click", "--name", "Alternate pack · 另一组合"])
  await until(() => evaluate(`document.querySelector('.pack-configurations h3')?.id === ${JSON.stringify("pc-heading-" + ready.alternateId)}`), "Pack switch failed")
  assert.equal(evaluate("!!document.querySelector('.pc-editor')"), false)
  browser(["find", "role", "button", "click", "--name", "Attachment review · 配置审阅"])
  await until(() => evaluate("!!document.querySelector('.pc-edit:not(:disabled)')"), "Pack return failed")
  assert.equal(await count("ReplaceAttachments"), beforeSwitch)
  record("pack-switch-stale-review")

  await layoutMatrix()

  const isolation = evaluate(`(async () => {
    const rpc = async (path, body, token) => { const response = await fetch(path, {method:'POST',headers:{'Content-Type':'application/json','Connect-Protocol-Version':'1',...(token ? {Authorization:'Bearer '+token} : {})},body:JSON.stringify(body)}); return {status:response.status, body:await response.json()} };
    const login = await rpc('/skillhub.auth.v1.AuthService/Login',{username:${JSON.stringify(ready.otherUsername)},password:${JSON.stringify(ready.password)}});
    const token=login.body.sessionToken;
    const get=await rpc('/skillhub.pack.configuration.v1.PackConfigurationService/GetAttachments',{packId:${JSON.stringify(ready.packId)}},token);
    const replace=await rpc('/skillhub.pack.configuration.v1.PackConfigurationService/ReplaceAttachments',{packId:${JSON.stringify(ready.packId)},expectedPackRevision:'1',expectedConfigurationRevision:'1',references:[]},token);
    const assets=await rpc('/skillhub.configuration.v1.ConfigurationService/ListAssets',{},token);
    await rpc('/skillhub.auth.v1.AuthService/Logout',{},token);
    return {get:get.status,replace:replace.status,onlyOwnAssets:assets.body.assets?.every(asset=>asset.id === ${JSON.stringify(ready.foreignAssetId)})};
  })()`)
  assert.equal(isolation.get, 404); assert.equal(isolation.replace, 404); assert.equal(isolation.onlyOwnAssets, true)
  record("owner-isolation")
  assert.equal((await control("state")).skillCount, 1)
  assert.equal(evaluate("document.querySelectorAll('.share-record').length"), shareCount)
  assert.equal(await count("CreateShare"), 1)
  assert.equal(await count("RevokeShare"), 0)
  assert.equal(await count("UpdatePack"), 0)
  record("skills-shares-unchanged")

  await newEdit(); click("Clear proposed references"); await add(0); review(); confirm(); await success()
  await control("unsupported", {}); await newEdit()
  assert(text().includes("Unsupported metadata"))
  assert.equal((await control("state")).snapshot.Entries[0].State, "unsupported")
  assert.equal(evaluate("document.querySelectorAll('.pc-editor .pc-reference').length"), 1)
  review(); confirm()
  await until(() => text().includes("server refused the references"), "unsupported reference silently rewritten")
  record("unsupported-retained")
  capture("unsupported-retained")

  const finalState = await control("state")
  for (const method of ["ReadDraft", "ReadAsset", "Reveal", "Preview", "CreateAsset", "UpdateAsset", "DeleteAsset", "CopyAsset"]) assert.equal(finalState.counts[method] ?? 0, 0, `metadata page called ${method}`)
  assert.deepEqual(evaluate("Object.keys(localStorage).filter(key=>!['skillhub.locale','skillhub.theme'].includes(key))"), [])
  assert.equal(evaluate("sessionStorage.length"), 0)
  assert(!text().includes("PRIVATE_ATTACHMENT_BODY_CANARY"))
  record("metadata-only-storage")

  await newEdit(); review(); await control("delete", {}); confirm()
  await until(() => text().includes("no replacement was sent"), "deleted Pack allowed stale review")
  record("delete-invalidates")
  click("Sign out")
  await until(() => evaluate("!!document.querySelector('#username')"), "logout did not invalidate workspace")
  assert.equal(evaluate("!!document.querySelector('.pc-editor, .pc-dialog')"), false)
  browser(["fill", "#username", ready.otherUsername]); browser(["fill", "#password", ready.password]); browser(["click", "form button[type=submit]"])
  await until(() => evaluate("!!document.querySelector('.packs-ui')"), "other owner login failed")
  assert.equal(evaluate("!!document.querySelector('.pack-configurations')"), false)
  record("logout-invalidates")
  }
} catch (error) { failure = error; try { capture("failure") } catch {} }
finally {
  try { browser(["close"]) } catch {}
  try { if (ready) await control("stop", {}) } catch {}
  let exit = await Promise.race([terminal, delay(5000).then(() => null)])
  if (!exit) { fixture.kill("SIGTERM"); exit = { ...await terminal, timeout: true } }
  closeSync(log)
  rmSync(directory, { recursive: true, force: true })
  const receipt = { ok: !failure, expected: expected.length, completed: evidence.length, failure: failure?.message ?? null, evidence, geometry: geometryEvidence }
  try { terminalGate(exit, receipt) } catch (error) { failure ??= error }
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ ...receipt, ok: !failure, failure: failure?.message ?? null, exit }, null, 2), { mode: 0o600 })
}
if (failure) { console.error(failure.message); process.exitCode = 1 } else console.log(`Pack configuration UI: ${evidence.length} receipts`)
