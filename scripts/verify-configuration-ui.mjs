import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: {
  "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" },
  "self-test": { type: "boolean" }, red: { type: "boolean" }, "skip-matrix": { type: "boolean" }, "matrix-smoke": { type: "boolean" },
  "focus-regression": { type: "boolean" },
  "ordering-copy": { type: "boolean" },
} })
const delay = (duration) => new Promise((done) => setTimeout(done, duration))

async function terminalResult(child, timeout) {
  let timer
  try {
    return await Promise.race([
      new Promise((done, reject) => { child.once("error", () => reject(new Error("fixture spawn failed"))); child.once("close", (code, signal) => done({ code, signal })) }),
      new Promise((done, reject) => { timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error("fixture terminal timeout")) }, timeout) }),
    ])
  } finally { clearTimeout(timer) }
}
function acceptFixture(result, output) {
  assert.equal(result.signal, null, "fixture terminated by signal")
  assert.equal(result.code, 0, "fixture exited nonzero")
  assert.match(output, /CONFIGURATION_UI_STOP_RECEIPT/, "stop receipt missing")
  assert.match(output, /--- PASS: TestConfigurationUIBrowserFixture /, "test PASS missing")
  assert.doesNotMatch(output, /--- FAIL:|panic:|DATA RACE/, "contradictory fixture failure")
}
async function selfTest() {
  const good = "CONFIGURATION_UI_STOP_RECEIPT\n--- PASS: TestConfigurationUIBrowserFixture (0.01s)"
  const cases = [
    ["normal", "console.log(process.env.RECEIPT)", good, true],
    ["nonzero", "console.log(process.env.RECEIPT);process.exit(7)", good, false],
    ["signal", "console.log(process.env.RECEIPT);process.kill(process.pid,'SIGTERM')", good, false],
    ["missing-stop", "console.log('--- PASS: TestConfigurationUIBrowserFixture (0.01s)')", "", false],
    ["missing-pass", "console.log('CONFIGURATION_UI_STOP_RECEIPT')", "", false],
    ["contradictory", "console.log(process.env.RECEIPT);console.log('--- FAIL: fixture')", good, false],
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
  console.log(`terminal self-tests: ${receipts.length}/${cases.length}`)
}

async function run() {
  assert.equal(process.versions.node, "26.12.0")
  for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
  const output = resolve(values.output)
  mkdirSync(output, { recursive: true, mode: 0o700 })
  const directory = mkdtempSync(join(output, "fixture-"))
  const logPath = join(output, "fixture.log"), log = openSync(logPath, "w", 0o600)
  const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestConfigurationUIBrowserFixture$", "-test.v", "-test.timeout=26m"], {
    env: { ...process.env, COGNATE_CONFIGURATION_UI_DIR: directory }, stdio: ["ignore", log, log],
  })
  const terminal = terminalResult(fixture, 26*60_000)
  terminal.catch(() => {})
  let exited = false
  fixture.once("exit", () => { exited = true })
  const session = `configuration-${process.pid}-${Date.now()}`, evidence = [], layouts = [], screenshots = []
  let ready, failure, fixtureResult, browserVersion, started = false, language = "en"
  function browser(args, input) {
    const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { input, encoding: "utf8", timeout: 30000, maxBuffer: 4 << 20 })
    assert.equal(result.status, 0, `browser command failed: ${args[0]}`)
    const parsed = JSON.parse(result.stdout)
    assert.notEqual(parsed.success, false, `browser operation failed: ${args[0]}`)
    return parsed.data
  }
  function evaluate(source) { const data = browser(["eval", "--stdin"], source); return data && Object.hasOwn(data,"result") ? data.result : data }
  async function until(check, message) { const deadline = Date.now()+15000; while(Date.now()<deadline) { if(await check()) return; await delay(100) }; assert.fail(message) }
  async function control(path, body) {
    const response = await fetch(`${ready.controlURL}/${path}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection:"close" }, ...(body === undefined ? {} : {body:JSON.stringify(body)}), signal:AbortSignal.timeout(5000) })
    assert.equal(response.status,200,`fixture control: ${path}`)
    return body === undefined ? response.json() : undefined
  }
  const mode = (Mode="normal", Procedure="") => control("mode",{Mode,Procedure})
  const text = () => evaluate("document.body.innerText")
  const click = (name) => browser(["find","role","button","click","--name",name,"--exact"])
  function fill(label,value) {
    const id = evaluate(`Array.from(document.querySelectorAll('[role=dialog] label')).find(node=>node.textContent===${JSON.stringify(label)})?.htmlFor`)
    assert(id, `field label missing: ${label}`)
    const selector=`[id=${JSON.stringify(id)}]`
    if(value === "") { browser(["focus",selector]);assert(evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`),"empty-field input did not acquire focus");browser(["press","Control+a"]);browser(["press","Backspace"]) }
    else browser(["fill",selector,value])
    assert.equal(evaluate(`document.querySelector(${JSON.stringify(selector)}).value`),value,`field value: ${label}`)
  }
  async function choose(label,option) { browser(["find","role","combobox","click","--name",label,"--exact"]); browser(["find","role","option","click","--name",option,"--exact"]) }
  async function row(name, action) {
    const selector = evaluate(`(() => {const row=[...document.querySelectorAll('.configuration-asset')].find(row=>row.querySelector('h3')?.textContent===${JSON.stringify(name)}); const button=[...row?.querySelectorAll('button')||[]].find(button=>button.textContent===${JSON.stringify(action)}); if(!button)return null; button.dataset.browserAction='current'; return '[data-browser-action=current]'})()`)
    assert(selector,`row action unavailable: ${name}`)
    browser(["focus",selector]);browser(["click",selector])
    evaluate("document.querySelector('[data-browser-action]')?.removeAttribute('data-browser-action')")
    await until(()=>evaluate("!!document.querySelector('[role=dialog]')"),"dialog missing")
  }
  async function closeDirty() {
    click(language==="en" ? "Close dialog":"关闭对话框")
    if(evaluate("document.querySelectorAll('[role=dialog]').length>1")) click(language==="en" ? "Discard changes":"放弃更改")
    await until(()=>!evaluate("!!document.querySelector('[role=dialog]')"),"dialog not closed")
  }
  function capture(name) {
    assert(evaluate("[...document.querySelectorAll('input[type=password]')].every(node=>node.value==='')"),"refuse secret screenshot")
    browser(["screenshot",join(output,`${name}.png`)]); screenshots.push(name)
  }
  function geometry(selector, context) {
    const result = evaluate(`(() => {
      const root=document.querySelector(${JSON.stringify(selector)}); if(!root)return {missing:true}
      const outside=[...root.querySelectorAll('h1,h2,h3,p,button,input,dt,dd,label,summary,[role=combobox]')].filter(node=>{if(node.closest('.sr-only,[hidden],[aria-hidden=true]'))return false;const box=node.getBoundingClientRect();return box.width&&box.height&&(box.left < -1||box.right > innerWidth+1)}).map(node=>node.tagName+':'+node.textContent.trim().slice(0,60))
      const words=[], clippedWords=[], bounds=root.getBoundingClientRect(), walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT)
      while(walker.nextNode()) {const node=walker.currentNode,parent=node.parentElement;if(!parent||parent.closest('code,textarea,[class*=font-mono],.sr-only,[hidden],[aria-hidden=true]')||!parent.getBoundingClientRect().width||parent.closest('details:not([open])')&&!parent.closest('summary'))continue; for(const word of node.textContent.matchAll(/[A-Za-z]{4,}/g)){const range=document.createRange();range.setStart(node,word.index);range.setEnd(node,word.index+word[0].length);const boxes=[...range.getClientRects()].filter(box=>box.width>0);if(boxes.length>1)words.push(word[0]);if(boxes.some(box=>box.left<Math.max(0,bounds.left)-1||box.right>Math.min(innerWidth,bounds.right)+1))clippedWords.push(word[0])}}
      const overflowNodes=root.scrollWidth>root.clientWidth+1?[...root.querySelectorAll('*')].filter(node=>node.getBoundingClientRect().right>bounds.right+1).map(node=>({tag:node.tagName,class:node.className,text:node.textContent.slice(0,60),width:node.getBoundingClientRect().width,clip:getComputedStyle(node).clip})):[]
      return {width:innerWidth,scrollWidth:document.documentElement.scrollWidth,rootScrollWidth:root.scrollWidth,rootClientWidth:root.clientWidth,rootFont:getComputedStyle(document.documentElement).fontSize,outside,words,clippedWords,overflowNodes,scheme:getComputedStyle(document.documentElement).colorScheme,reduce:matchMedia('(prefers-reduced-motion: reduce)').matches,language:document.documentElement.lang}
    })()`)
    layouts.push({...context,...result})
    assert(!result.missing,"layout root missing")
    assert(result.scrollWidth<=result.width+1,`overflow: ${JSON.stringify(context)}`)
    assert(result.rootScrollWidth<=result.rootClientWidth+1,`surface overflow: ${JSON.stringify(context)}`)
    assert.deepEqual(result.outside,[],`clipped content: ${JSON.stringify(context)}`)
    assert.deepEqual(result.words,[],`broken English word: ${JSON.stringify(context)}`)
    assert.deepEqual(result.clippedWords,[],`clipped English word: ${JSON.stringify(context)}`)
    assert.equal(result.rootFont, context.font===200 ? "32px":"16px")
    assert.equal(result.reduce,context.motion==="reduce","actual reduced-motion state mismatch")
    assert.equal(result.language,context.locale,"actual language mismatch")
    assert(evaluate(`[...document.querySelector(${JSON.stringify(selector)}).querySelectorAll('[role=checkbox]')].every(node=>{const box=node.getBoundingClientRect();return Math.abs(box.width-box.height)<=1})`),"checkbox stretched by action styles")
  }
  async function tabTo(selector) {
    for(let index=0;index<150;index++) { if(evaluate(`document.activeElement?.matches(${JSON.stringify(selector)})`))return;browser(["press","Tab"]) }
    assert.fail(`unreachable keyboard target: ${selector}`)
  }
  async function saved(name) { await until(async()=> (await control("state")).assets.some(asset=>asset.Name===name),"real SQLite save missing"); await until(()=>!evaluate("!!document.querySelector('[role=dialog]')"),"save did not close dialog") }
  async function focusRegressions() {
    const observations = []
    const observe = (provider, phase, result) => {
      observations.push({provider,phase,...result})
      evidence.push({provider,phase,pass:result.pass})
      writeFileSync(join(output,"focus-regressions.json"),JSON.stringify({browserVersion,observations},null,2),{mode:0o600})
    }
    const focus = () => evaluate("({tag:document.activeElement?.tagName,inside:!!document.activeElement?.closest('[role=dialog]'),title:document.activeElement?.matches('[data-configuration-editor-title]'),disabled:document.activeElement?.matches(':disabled'),pending:document.querySelector('[role=dialog] form')?.getAttribute('aria-busy')==='true'})")
    browser(["press","Escape"])
    browser(["set","viewport","320","900"]);browser(["set","media","dark","reduced-motion"])
    evaluate("document.documentElement.style.fontSize='32px'")
    for(const provider of ["Claude","Codex","Gemini","OpenCode","Grok"]) {
      await row(`${provider} synthetic`,"Edit configuration")
      await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"focus editor missing")
      fill(provider==="OpenCode"?"Configuration name":"Configuration asset name",`Focus ${provider} synthetic`)
      const before=(await control("state")).requests["/skillhub.configuration.v1.ConfigurationService/UpdateAsset"]??0
      await mode("slow","UpdateAsset");await tabTo('[role=dialog] button[type=submit]');browser(["press","Enter"])
      const pending=focus()
      observe(provider,"F1-pending-save",{...pending,pass:pending.pending&&pending.inside&&pending.tag!=="BODY"&&!pending.disabled})
      evaluate("document.querySelector('[role=dialog] form')?.requestSubmit();document.querySelector('[role=dialog] form')?.requestSubmit()")
      if(provider==="Claude")capture("focus-pending-save")
      await saved(`Focus ${provider} synthetic`)
      const after=(await control("state")).requests["/skillhub.configuration.v1.ConfigurationService/UpdateAsset"]
      observe(provider,"single-flight",{requests:after-before,pass:after===before+1})
      await mode()
    }
    for(const provider of ["Claude","Codex","Gemini","OpenCode","Grok"]) {
      await row(`Focus ${provider} synthetic`,"Edit configuration")
      await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"CAS editor missing")
      const draft=`Focus ${provider} retained draft`
      fill(provider==="OpenCode"?"Configuration name":"Configuration asset name",draft)
      const asset=(await control("state")).assets.find(asset=>asset.Name===`Focus ${provider} synthetic`)
      await control("external",{ID:asset.ID});click(provider==="OpenCode"?"Save configuration asset":"Save asset only")
      await until(()=>text().includes("Discard draft and read again"),"real CAS reconciliation missing")
      evaluate("[...document.querySelectorAll('[role=dialog] button')].find(node=>node.textContent==='Discard draft and read again').dataset.focusReload='current'")
      for(const key of ["Escape","Enter"]) {
        await tabTo('[data-focus-reload=current]');browser(["press","Space"])
        await until(()=>evaluate("document.querySelectorAll('[role=dialog]').length===2"),"reconcile confirmation missing")
        assert(evaluate("document.activeElement?.textContent==='Continue editing'"),"reconcile cancel initial focus missing")
        browser(["press",key])
        await until(()=>evaluate("document.querySelectorAll('[role=dialog]').length===1"),"reconcile cancellation missing")
        await delay(250)
        const canceled={...focus(),trigger:evaluate("document.activeElement?.matches('[data-focus-reload=current]')"),draft:evaluate(`[...document.querySelectorAll('[role=dialog] input')].some(node=>node.value===${JSON.stringify(draft)})`)}
        observe(provider,`F2-cancel-${key}`,{...canceled,pass:canceled.trigger&&canceled.inside&&canceled.draft})
        if(provider==="Claude"&&key==="Escape")capture("focus-reconcile-cancel")
      }
      await tabTo('[data-focus-reload=current]');browser(["press","Space"]);browser(["press","Escape"])
      browser(["focus","[data-configuration-editor-title]"]);await delay(250)
      observe(provider,"cancel-no-external-focus-steal",{pass:evaluate("document.activeElement?.matches('[data-configuration-editor-title]')")})
      browser(["click","[data-focus-reload=current]"]);click("Discard draft and read again")
      await until(()=>evaluate(`[...document.querySelectorAll('[role=dialog] input')].some(node=>node.value===${JSON.stringify(`Focus ${provider} synthetic external`)})`),"explicit reconciliation did not read real latest draft")
      await delay(250)
      const reconciled=focus()
      observe(provider,"explicit-reconcile",{...reconciled,pass:reconciled.inside&&reconciled.tag!=="BODY"&&!reconciled.disabled})
      await closeDirty()
    }
    await row("Focus Claude synthetic external","Edit configuration")
    await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"external-focus editor missing")
    fill("Configuration asset name","Focus transport failure draft")
    await mode("slow-error","UpdateAsset");await tabTo('[role=dialog] button[type=submit]');browser(["press","Enter"])
    browser(["focus",'[role=dialog] button[aria-label="Close dialog"]'])
    await until(()=>evaluate("document.querySelector('[role=dialog] form')?.getAttribute('aria-busy')==='false'"),"pending failure did not settle")
    observe("Claude","pending-no-external-focus-steal",{pass:evaluate("document.activeElement?.getAttribute('aria-label')==='Close dialog'")})
    await mode();await closeDirty()
    assert.deepEqual(observations.filter(row=>!row.pass).map(row=>`${row.provider}:${row.phase}`),[],"F1/F2 focus regression")
  }
  try {
    await until(()=>existsSync(join(directory,"ready.json"))||exited,"fixture readiness timeout")
    assert(!exited,"fixture exited before ready")
    ready=JSON.parse(readFileSync(join(directory,"ready.json"),"utf8"))
    const credentials=await control("login")
    browser(["open",ready.url+"/configuration"]);started=true
    browserVersion=evaluate("navigator.userAgent")
    await until(()=>evaluate("!!document.querySelector('#username')"),"login missing")
    if(evaluate("document.documentElement.lang!=='en'")) await choose("界面语言","English")
    evaluate(`(() => {const input=document.querySelector('#password');const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;setter.call(input,${JSON.stringify(credentials.password)});input.dispatchEvent(new Event('input',{bubbles:true}))})()`)
    browser(["fill","#username",credentials.username]);click("Sign in")
    credentials.password=""
    await until(()=>evaluate("!!document.querySelector('.configuration-assets')"),"configuration missing")
    await row("Claude synthetic","Edit configuration")
    await until(()=>evaluate("!!document.querySelector('[role=dialog] form')"),"Claude editor missing")
    assert(evaluate("document.activeElement?.matches('[data-configuration-editor-title]')"),"RED: editor initial focus must land on stable title, not language selector")
    evidence.push("stable-editor-initial-focus")
    if(values["ordering-copy"]) {
      browser(["press","Escape"])
      browser(["set","viewport","320","900"])
      browser(["set","media","dark","reduced-motion"])
      evaluate("document.documentElement.style.fontSize='32px'")
      const before=(await control("state")).requests
      for(const locale of ["en","zh-CN"]) {
        if(locale==="zh-CN") { await choose("Interface language","简体中文");language=locale }
        await row("OpenCode synthetic",locale==="en"?"Edit configuration":"编辑配置")
        await until(()=>evaluate("!!document.querySelector('[role=dialog] form')"),"OpenCode editor missing")
        const content=evaluate("document.querySelector('[role=dialog] form').textContent")
        assert(content.includes(locale==="en"?"Close this editor to adjust provider-list order in the configuration workspace's ordering section.":"关闭此编辑器后，可在配置工作空间的排序区域单独调整提供方列表。"),"OpenCode ordering guidance does not match the available workspace")
        assert(!content.includes("requires a separate backend slice")&&!content.includes("仍待独立后端片"),"OpenCode ordering guidance still claims the feature is unavailable")
        geometry('[role=dialog]',{surface:"OpenCode ordering guidance",locale,font:200,motion:"reduce"})
        capture(`OpenCode-ordering-copy-${locale}`)
        evidence.push(`OpenCode-ordering-guidance-${locale}`)
        browser(["press","Escape"])
        const orderingLabel=locale==="en"?"Arrange configurations":"排列配置"
        assert(evaluate(`[...document.querySelectorAll('button')].some(button=>button.textContent.trim()===${JSON.stringify(orderingLabel)}&&!button.disabled)`),"workspace ordering entry described by the guidance is unavailable")
      }
      const after=(await control("state")).requests
      for(const method of ["CreateAsset","UpdateAsset","CopyAsset","DeleteAsset","ReplaceAssetOrder"]) {
        const route=`/skillhub.configuration.v1.ConfigurationService/${method}`
        assert.equal(after[route]??0,before[route]??0,"reading ordering guidance dispatched a business mutation")
      }
      evidence.push("ordering-guidance-read-only")
      return
    }
    if(values["focus-regression"]) { await focusRegressions();return }
    if(values.red) return
    fill("Configuration asset name","non-secret preserved draft")
    browser(["press","Escape"])
    assert(evaluate("document.activeElement?.textContent==='Continue editing'"),"discard cancel is not initial focus")
    browser(["press","Enter"])
    assert(evaluate("document.activeElement?.value==='non-secret preserved draft'"),"cancel did not restore draft field focus")
    browser(["set","media","dark"])
    assert(evaluate("[...document.querySelectorAll('[role=dialog] input')].some(input=>input.value==='non-secret preserved draft')"),"system theme lost draft")
    browser(["set","media","light"])
    evidence.push("Escape-Enter-cancel-focus-and-system-theme-preserves-draft")
    await mode("error","ListAssets")
    await choose("Interface language","简体中文");language="zh-CN"
    await until(()=>text().includes("配置请求失败" )||evaluate("!!document.querySelector('.configuration-ui [role=alert]')"),"failed list query missing")
    assert(evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"),"failed list read permits stale write")
    assert(evaluate("[...document.querySelectorAll('[role=dialog] input')].some(node=>node.value==='non-secret preserved draft')"),"failed query lost draft")
    evidence.push("failed-query-disables-write-preserves-draft")
    await mode();await choose("界面语言","English");language="en"
    await until(()=>!evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"),"fresh metadata did not restore editing")
    await closeDirty()
    for(const provider of ["Claude","Codex","Gemini","OpenCode","Grok"]) {
      await row(`${provider} synthetic`,"Edit configuration")
      await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),`${provider} editor missing`)
      fill(provider === "OpenCode" ? "Configuration name" : "Configuration asset name","");click(provider === "OpenCode" ? "Save configuration asset" : "Save asset only")
      assert(evaluate("!!document.querySelector('[role=dialog] [role=alert], [role=dialog] [aria-invalid=true]')"),`${provider} validation missing`)
      await closeDirty(); evidence.push(`${provider}-real-open-edit-validation`)
    }
    const unknown=evaluate("[...document.querySelectorAll('.configuration-asset')].find(row=>row.querySelector('h3')?.textContent==='Unknown format synthetic')?.textContent")
    assert(unknown.includes("read-only")||unknown.includes("metadata"),"unknown format not explicit")
    assert(evaluate("![...document.querySelectorAll('.configuration-asset')].find(row=>row.querySelector('h3')?.textContent==='Unknown format synthetic')?.textContent.includes('Edit configuration')"),"unknown editable")
    evidence.push("unknown-schema-metadata-only")
    click("Create configuration asset")
    await choose("Creation target","Grok Build")
    fill("Configuration asset name","Created synthetic");click("Save asset only");await saved("Created synthetic")
    evidence.push("real-create-server-asset-not-applied")
    click("Arrange configurations")
    await choose("Agent and configuration type","claude · Provider")
    await until(()=>evaluate("!!document.querySelector('.configuration-order ol li')"),"complete order group missing")
    const firstOrder=evaluate("[...document.querySelectorAll('.configuration-order ol li')].map(node=>node.querySelector('p').textContent)")
    await tabTo('.configuration-order ol li:first-child button:last-child');browser(["press","Enter"])
    await until(()=>evaluate(`document.querySelector('.configuration-order ol li:nth-child(2) p')?.textContent===${JSON.stringify(firstOrder[0])}`),"real order not saved")
    assert(evaluate("document.activeElement?.matches('.configuration-order ol li')"),"order focus not stable")
    await mode("slow","ReplaceAssetOrder")
    browser(["click",".configuration-order ol li:nth-child(2) button:first-child"])
    browser(["press","Tab"])
    const outsideOrderFocus=evaluate("document.activeElement?.outerHTML")
    await until(()=>evaluate(`document.querySelector('.configuration-order ol li:first-child p')?.textContent===${JSON.stringify(firstOrder[0])}`),"second order did not save")
    assert.equal(evaluate("document.activeElement?.outerHTML"),outsideOrderFocus,"async reorder stole external focus")
    await mode()
    click("Read current order")
    assert(evaluate("document.activeElement?.matches('.configuration-order h2')"),"order reload lost focus")
    await until(()=>evaluate("!!document.querySelector('.configuration-order ol li')"),"order reload missing")
    browser(["press","Escape"]);evidence.push("real-order-Enter-stable-row-focus")
    await row("Grok synthetic","Copy without credentials");await until(()=>evaluate("!document.querySelector('[role=dialog] button[type=submit]')?.disabled"),"copy preflight missing")
    fill("New copy name","Copy synthetic");click("Copy without credentials");await saved("Copy synthetic")
    evidence.push("real-copy-no-credentials")
    await row("Grok synthetic","Edit configuration");await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"edit missing")
    fill("Configuration asset name","CAS draft")
    const source=(await control("state")).assets.find(asset=>asset.Name==="Grok synthetic")
    await control("external",{ID:source.ID});click("Save asset only")
    await until(()=>evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"),"CAS did not block stale draft")
    assert((await control("state")).assets.some(asset=>asset.Name==="Grok synthetic external"&&asset.Revision===2))
    evidence.push("CAS-conflict-no-overwrite")
    await closeDirty()
    await row("Grok synthetic external","Edit configuration");await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"latest edit missing")
    fill("Configuration asset name","Unknown committed synthetic");await mode("lost","UpdateAsset");click("Save asset only")
    await until(()=>evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"),"Unknown not blocked")
    await until(async()=> (await control("state")).assets.some(asset=>asset.Name==="Unknown committed synthetic"&&asset.Revision===3),"dropped reply failed to commit")
    const count=(await control("state")).requests["/skillhub.configuration.v1.ConfigurationService/UpdateAsset"]
    await delay(1200)
    assert.equal((await control("state")).requests["/skillhub.configuration.v1.ConfigurationService/UpdateAsset"],count,"automatic mutation replay")
    await mode();click("Discard draft and read again");click("Discard draft and read again")
    await until(()=>evaluate("[...document.querySelectorAll('[role=dialog] input')].some(input=>input.value==='Unknown committed synthetic')"),"explicit Unknown reconcile missing")
    await closeDirty();evidence.push("dropped-success-reply-no-replay-explicit-reconcile")
    await row("Copy synthetic","Delete configuration asset")
    await tabTo('[role=checkbox]');browser(["press","Space"])
    await tabTo('[role=dialog] button[type=submit]');browser(["press","Enter"])
    await until(async()=>!(await control("state")).assets.some(asset=>asset.Name==="Copy synthetic"),"delete did not commit")
    await until(()=>evaluate("document.activeElement?.matches('[data-configuration-heading]')"),"delete focus unstable")
    evidence.push("real-delete-Space-Enter-stable-focus")
    await mode("slow-error","ListAssets")
    click("Refresh configuration metadata")
    assert(evaluate("document.querySelector('.configuration-assets').getAttribute('aria-busy')==='true'"),"real loading state missing")
    browser(["focus",".topbar [role=combobox]"])
    await until(()=>evaluate("!!document.querySelector('.configuration-ui [role=alert]')"),"background error missing")
    assert(evaluate("document.activeElement?.matches('.topbar [role=combobox]')"),"background failure stole external focus")
    assert(evaluate("document.querySelector('.configuration-ui .page-heading button').disabled"),"failed metadata enabled stale actions")
    await mode();click("Refresh configuration metadata")
    await until(()=>evaluate("!document.querySelector('.configuration-ui .page-heading button').disabled"),"metadata recovery missing")
    evidence.push("real-loading-background-failure-no-external-focus-steal")
    if(!values["skip-matrix"]) {
      for(const locale of ["en","zh-CN"]) for(const theme of values["matrix-smoke"]?["system"]:["light","dark","system"]) {
        await choose(language==="en"?"Theme":"主题", language==="en"?{light:"Light",dark:"Dark",system:"System"}[theme]:{light:"浅色",dark:"深色",system:"跟随系统"}[theme])
        await choose(language==="en"?"Interface language":"界面语言",locale==="en"?"English":"简体中文");language=locale
        for(const scheme of theme==="system"?["light","dark"]:[theme]) {
          browser(["set","media",scheme])
          for(const width of values["matrix-smoke"]?[320]:[320,375,1440]) for(const font of values["matrix-smoke"]?[200]:[100,200]) for(const motion of ["no-preference","reduce"]) {
            browser(["set","viewport",String(width),"900"]);browser(["set","media",scheme,...(motion==="reduce"?["reduced-motion"]:[])])
            evaluate(`document.documentElement.style.fontSize='${font===200?32:16}px'`)
            await until(()=>evaluate(`getComputedStyle(document.documentElement).colorScheme===${JSON.stringify(scheme)}`),"theme/system mismatch")
            const context={locale,theme,scheme,width,font,motion}
            geometry(".configuration-ui",{...context,page:"overview"})
            await tabTo('.configuration-scope summary');browser(["press","Space"])
            geometry(".configuration-ui",{...context,page:"scope"});browser(["press","Enter"])
            for(const provider of ["Claude","Codex","Gemini","OpenCode","Unknown committed"]) {
              await row(`${provider} synthetic`,language==="en"?"Edit configuration":"编辑配置")
              await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"matrix editor missing")
              geometry('[role=dialog]',{...context,page:"editor",provider})
              if(values["matrix-smoke"]&&provider==="OpenCode"&&scheme==="dark"&&motion==="reduce") { evaluate("document.querySelector('[data-field-path=\"v2.settings.chunkTimeout\"]')?.scrollIntoView({block:'center'})");capture(`${locale}-OpenCode-field-path-320-200`) }
              browser(["press","Tab"])
              assert(evaluate("!!document.activeElement?.closest('[role=dialog]')"),"focus escaped editor")
              if(width<400&&font===200&&motion==="reduce") {
                await tabTo('[role=dialog] button[type=submit]')
                assert(evaluate("(() => {const box=document.activeElement.getBoundingClientRect();return box.top>=0&&box.bottom<=innerHeight})()"),"keyboard did not reveal full save action")
                evidence.push({...context,provider,keyboard:"Tab-to-save-Escape"})
              }
              if(width===320&&font===200&&motion==="reduce"&&(theme==="dark"||values["matrix-smoke"]&&scheme==="dark"))capture(`${locale}-${provider}-editor-320-200`)
              browser(["press","Escape"])
              await until(()=>!evaluate("!!document.querySelector('[role=dialog]')"),"Escape failed")
            }
            click(language==="en"?"Arrange configurations":"排列配置")
            await choose(language==="en"?"Agent and configuration type":"目标 agent 与配置类型",language==="en"?"claude · Provider":"claude · Provider 配置")
            await until(()=>evaluate("!!document.querySelector('.configuration-order ol li')"),"matrix order missing")
            geometry('.configuration-order',{...context,page:"order"})
            browser(["press","Escape"])
            for(const operation of ["copy","delete"]) {
              await row("Unknown committed synthetic",language==="en"?(operation==="copy"?"Copy without credentials":"Delete configuration asset"):(operation==="copy"?"复制（不带凭据）":"删除配置资产"))
              await until(()=>evaluate("!!document.querySelector('[role=dialog] button[type=submit]')"),"operation shell missing")
              geometry('[role=dialog]',{...context,page:operation})
              browser(["press","Escape"])
            }
            if(width===320&&font===200&&motion==="reduce")capture(`${locale}-${theme}-${scheme}-320-200`)
            writeFileSync(join(output,"progress.json"),JSON.stringify({completed:layouts.length,context}),{mode:0o600})
          }
        }
      }
    }
    evaluate("document.documentElement.style.fontSize='16px'")
    browser(["set","viewport","1440","1000"])
    capture("configuration-final")
    await control("empty",{})
    click(language==="en"?"Refresh configuration metadata":"刷新配置元数据")
    await until(()=>text().includes(language==="en"?"No configuration assets yet":"还没有配置资产"),"real empty state missing")
    assert.equal((await control("state")).assets.length,0)
    capture("configuration-empty")
    evidence.push("real-SQLite-empty-state")
    assert(evaluate("sessionStorage.length===0 && Object.keys(localStorage).every(key=>['skillhub.locale','skillhub.theme'].includes(key))"),"unexpected browser persistence")
    evidence.push("browser-persistence-preferences-only")
  } catch(error) {
    failure=error
    if(started) { try { capture("failure-no-secret") } catch {} }
  } finally {
    if(started) { try { browser(["close"]) } catch { failure ??= new Error("browser cleanup failed") } }
    try { if(ready&&!exited)await control("stop",{});fixtureResult=await Promise.race([terminal,delay(10000).then(()=>{throw new Error("fixture stop timeout")})]);acceptFixture(fixtureResult,readFileSync(logPath,"utf8")) }
    catch(error) {failure ??=error;fixture.kill("SIGKILL")}
    closeSync(log)
    writeFileSync(join(output,"receipts.json"),JSON.stringify({exit:failure?1:0,browserVersion,fixture:fixtureResult,evidence,layouts,screenshots,failure:failure?.message},null,2),{mode:0o600})
    if(failure)throw failure
  }
  console.log(`configuration UI: ${evidence.length} business checks, ${layouts.length} layout checks; fixture clean`)
}
try { if(values["self-test"])await selfTest();else await run() }
catch(error) { console.error(error.message);process.exitCode=1 }
