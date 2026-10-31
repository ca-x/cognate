import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, "browser-bin": { type: "string" }, "chrome-bin": { type: "string" }, output: { type: "string" }, theme: { type: "string", default: "dark" } } })
assert(["dark", "light"].includes(values.theme))
for (const name of ["fixture-bin", "browser-bin", "chrome-bin", "output"]) assert(values[name], `missing ${name}`)
const output = resolve(values.output)
mkdirSync(output, { recursive: true, mode: 0o700 })
const directory = mkdtempSync(join(output, "fixture-"))
const log = openSync(join(output, "fixture.log"), "w", 0o600)
const fixture = spawn(resolve(values["fixture-bin"]), ["-test.run=^TestOpenCodeAssetsBrowserFixture$", "-test.v", "-test.timeout=270s"], { env: { ...process.env, COGNATE_OPENCODE_BROWSER_DIR: directory }, stdio: ["ignore", log, log] })
const terminal = new Promise((done) => fixture.once("exit", (code, signal) => done({ code, signal })))
const delay = (duration) => new Promise((done) => setTimeout(done, duration))
const session = `grok-web-${process.pid}-${Date.now()}`
const evidence = []
let ready, failure, browserStarted = false
function browser(args, input) {
  const result = spawnSync(resolve(values["browser-bin"]), ["--session", session, "--executable-path", resolve(values["chrome-bin"]), "--allowed-domains", "127.0.0.1", "--ignore-https-errors", "--json", ...args], { env: process.env, input, encoding: "utf8", timeout: 30000, maxBuffer: 2 << 20 })
  assert.equal(result.status, 0, `browser command failed: ${args[0]}`)
  const parsed = JSON.parse(result.stdout)
  assert.notEqual(parsed.success, false, "browser operation rejected")
  return parsed.data
}
function evaluate(script) { const data = browser(["eval", "--stdin"], script); return data && Object.hasOwn(data, "result") ? data.result : data }
async function until(check, message) { const deadline = Date.now() + 15000; while (Date.now() < deadline) { if (await check()) return; await delay(100) }; assert.fail(message) }
function mark(expression) {
  assert(evaluate(`(() => { const node=(${expression}); if(!node)return false; document.querySelectorAll('[data-grok-test]').forEach(item=>item.removeAttribute('data-grok-test')); node.setAttribute('data-grok-test','true'); node.scrollIntoView({block:'center'}); return true })()`), "UI control missing")
  browser(["click", "[data-grok-test]"])
}
function click(name) { mark(`Array.from((Array.from(document.querySelectorAll('[role=dialog]')).at(-1) ?? document).querySelectorAll('button')).find(node=>node.textContent===${JSON.stringify(name)} || node.getAttribute('aria-label')===${JSON.stringify(name)})`) }
function fill(label, value) { browser(["find", "label", label, "fill", value, "--exact"]) }
function select(label, value) { browser(["find", "label", label, "click", "--exact"]); browser(["find", "role", "option", "click", "--name", value, "--exact"]) }
function secret() { evaluate(`(() => { const input=Array.from(document.querySelectorAll('[role=dialog] input[type=password]')).at(-1); if(!input)return false; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(ready.replacement)}); input.dispatchEvent(new Event('input',{bubbles:true})); return true })()`) }
function row(name, action) { mark(`Array.from(document.querySelectorAll('li')).find(node=>node.querySelector('h3')?.textContent===${JSON.stringify(name)})?.querySelectorAll('button') && Array.from(Array.from(document.querySelectorAll('li')).find(node=>node.querySelector('h3')?.textContent===${JSON.stringify(name)}).querySelectorAll('button')).find(node=>node.textContent===${JSON.stringify(action)})`) }
async function state() { const response = await fetch(`${ready.controlURL}/state`, { headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, signal: AbortSignal.timeout(5000) }); assert.equal(response.status, 200); return response.json() }
async function control(action, assetID) { const response = await fetch(`${ready.controlURL}/command`, { method: "POST", headers: { "X-Fixture-Capability": ready.capability, "Content-Type": "application/json", Connection: "close" }, body: JSON.stringify({ action, ...(assetID ? { assetID } : {}) }), signal: AbortSignal.timeout(5000) }); assert.equal(response.status, 200) }
async function saved(name, revision = 1) { await until(async () => (await state()).assets.some(asset => asset.name === name && asset.revision === revision), "durable asset missing"); await until(() => evaluate("!document.querySelector('[role=dialog]')"), "saved dialog did not close"); return (await state()).assets.find(asset => asset.name === name) }
async function create(name, custom) {
  click("新建配置资产"); select("创建目标", "Grok Build"); fill("配置资产名称", name)
  if (!custom) return
  select("Grok 认证模式", "Grok 自定义模型（custom-model）"); click("确认更改认证模式")
  for (const [label, value] of [["本地 profile（最多 128 UTF-8 字节）", "profile-one"], ["上游 model（最多 256 UTF-8 字节）", "x-ai/model"], ["Grok HTTPS 端点", "https://ExAmPle.invalid:443/a%2Fb"], ["模型显示名称（最多 128 UTF-8 字节）", "Model title"], ["Context window（精确整数）", "9007199254740991"]]) fill(label, value)
  select("Grok API backend", "responses")
  browser(["find", "label", "声明环境变量名（不读取变量值）", "click", "--exact"]); fill("Grok API key 环境变量名", "GROK_REF")
  select("grokbuild.api-key 操作", "输入新凭据（replace）"); secret()
}
try {
  await until(() => existsSync(join(directory, "ready.json")), "fixture readiness missing")
  ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8"))
  browserStarted = true
  browser(["open", ready.url])
  evaluate(`localStorage.setItem('skillhub.locale','zh-CN'); localStorage.setItem('skillhub.theme',${JSON.stringify(values.theme)}); location.reload(); true`)
  await until(() => evaluate("!!document.querySelector('#username')"), "login missing")
  fill("用户名", ready.username)
  evaluate(`(() => { const input=document.querySelector('#password'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(ready.password)}); input.dispatchEvent(new Event('input',{bubbles:true})); return true })()`)
  click("登录")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('a')).find(node=>node.textContent==='AI 配置')"), "login did not complete")
  browser(["find", "role", "link", "click", "--name", "AI 配置", "--exact"])
  await until(() => evaluate("!!Array.from(document.querySelectorAll('button')).find(node=>node.textContent==='新建配置资产')"), "configuration page missing")
  await create("Grok native", false); click("仅保存配置资产")
  const native = await saved("Grok native")
  assert.equal(native.grokProvider.mode, "native-login"); assert.equal(native.grokProvider.model, undefined); assert.equal(native.slots[0].present, false)
  evidence.push("native-declaration-saved-without-model-or-key")
  await create("Grok custom", true)
  assert(evaluate(`!document.body.textContent.includes(${JSON.stringify(ready.replacement)})`), "secret appeared in review text")
  click("仅保存配置资产")
  const custom = await saved("Grok custom")
  assert.equal(custom.expectedGrokSecretEqual, true)
  assert.deepEqual(custom.grokProvider.model, { profile: "profile-one", upstreamModel: "x-ai/model", endpoint: "https://ExAmPle.invalid:443/a%2Fb", name: "Model title", apiBackend: "responses", contextWindow: 9007199254740991, envKey: "GROK_REF" })
  assert.equal((await state()).plaintextAbsent, true)
  evidence.push("full-model-max-integer-and-key-durable-encrypted")
  row("Grok custom", "复制（不带凭据）")
  await until(() => evaluate("!!document.querySelector('[role=dialog] input') && !document.querySelector('[role=dialog] button[type=submit]')?.disabled"), "copy source not validated")
  fill("新副本名称", "Grok copy"); click("复制为无凭据资产")
  const copied = await saved("Grok copy")
  assert.deepEqual(copied.grokProvider, custom.grokProvider); assert.equal(copied.slots[0].present, false)
  evidence.push("copy-keeps-all-model-fields-without-key")
  row("Grok copy", "编辑配置")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('label')).find(node=>node.textContent==='声明环境变量名（不读取变量值）')"), "copy editor missing")
  browser(["find", "label", "声明环境变量名（不读取变量值）", "click", "--exact"])
  click("仅保存配置资产")
  const absent = await saved("Grok copy", 2)
  assert.equal(absent.grokProvider.model.envKey, undefined); assert.equal(absent.readiness, "needs-credential")
  row("Grok copy", "编辑配置")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('label')).find(node=>node.textContent==='grokbuild.api-key 操作')"), "copy credential editor missing")
  select("grokbuild.api-key 操作", "输入新凭据（replace）"); secret(); click("仅保存配置资产")
  assert.equal((await saved("Grok copy", 3)).expectedGrokSecretEqual, true)
  row("Grok copy", "编辑配置")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('label')).find(node=>node.textContent==='grokbuild.api-key 操作')"), "clear editor missing")
  select("grokbuild.api-key 操作", "清除凭据（clear）"); click("确认清除 grokbuild.api-key"); click("仅保存配置资产")
  assert.equal((await saved("Grok copy", 4)).slots[0].present, false)
  evidence.push("environment-absence-and-explicit-replace-clear-roundtrip")
  row("Grok custom", "编辑配置")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('label')).find(node=>node.textContent==='grokbuild.api-key 操作')"), "editor missing")
  fill("本地 profile（最多 128 UTF-8 字节）", "review-draft")
  select("grokbuild.api-key 操作", "输入新凭据（replace）"); secret()
  select("Grok 认证模式", "Grok 原生登录（native-login）"); click("继续编辑")
  assert(evaluate("document.querySelector('[role=dialog] input[type=password]').value===''"), "mode cancel retained new key")
  secret(); select("界面语言", "English")
  assert(evaluate("document.querySelector('[role=dialog] input[type=password]').value==='' && document.documentElement.lang==='en'"), "language did not clear new key")
  assert(evaluate("Array.from(document.querySelectorAll('[role=dialog] input')).some(node=>node.value==='review-draft')"), "language discarded non-secret draft")
  browser(["set", "viewport", "375", "812"])
  assert(evaluate("Array.from(document.querySelectorAll('[role=dialog]')).every(node=>node.scrollWidth<=node.clientWidth+1)"), "narrow form has horizontal overflow")
  browser(["press", "Tab"])
  assert(evaluate("!!document.activeElement?.closest('[role=dialog]')"), "keyboard focus escaped dialog")
  assert.equal(evaluate("document.documentElement.classList.contains('dark')"), values.theme === "dark")
  browser(["screenshot", join(output, `grok-${values.theme}-en-375.png`)])
  select("Interface language", "简体中文"); select("grokbuild.api-key 操作", "保留原凭据（keep）")
  await control("external-update", custom.id)
  click("仅保存配置资产")
  await until(() => evaluate("Array.from(document.querySelectorAll('[role=alert]')).some(node=>node.textContent.includes('配置版本已改变'))"), "CAS conflict not visible")
  assert.equal((await state()).assets.find(asset => asset.id === custom.id).revision, 2)
  assert(evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"), "stale form can replay")
  evidence.push("mode-cancel-language-key-clear-mobile-keyboard-and-stale-CAS")
  click("关闭对话框"); click("放弃更改")
  row("Grok custom external", "编辑配置")
  await until(() => evaluate("!!Array.from(document.querySelectorAll('label')).find(node=>node.textContent==='Grok 认证模式')"), "latest draft missing")
  select("Grok 认证模式", "Grok 原生登录（native-login）"); click("确认更改认证模式"); click("仅保存配置资产")
  const transitioned = await saved("Grok custom external", 3)
  assert.equal(transitioned.grokProvider.mode, "native-login"); assert.equal(transitioned.grokProvider.model, undefined); assert.equal(transitioned.slots[0].present, false)
  evidence.push("native-transition-explicitly-clears-model-and-key")
  await control("seed-preserved-extras", copied.id)
  click("刷新配置元数据")
  row("Grok copy", "编辑配置")
  await until(() => evaluate("Array.from(document.querySelectorAll('[role=alert]')).some(node=>node.textContent.includes('未知字段'))"), "unknown fields not readonly")
  assert(evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"), "unknown fields can be overwritten")
  click("关闭对话框")
  evidence.push("preserved-extras-remain-readonly")
  row("Grok native", "复制（不带凭据）")
  await until(() => evaluate("!!document.querySelector('[role=dialog] button[type=submit]') && !document.querySelector('[role=dialog] button[type=submit]').disabled"), "native copy not ready")
  fill("新副本名称", "Grok corrupt reply")
  await control("corrupt-copy")
  click("复制为无凭据资产")
  await until(() => evaluate("Array.from(document.querySelectorAll('[role=alert]')).some(node=>node.textContent.includes('配置写入结果待确认'))"), "corrupt successful receipt not Unknown")
  assert(evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"), "corrupt receipt permits automatic replay")
  assert.equal((await state()).assets.find(asset => asset.name === "Grok corrupt reply").slots[0].present, false)
  evidence.push("corrupt-copy-receipt-unknown-without-replay")
  click("关闭对话框"); click("放弃更改")
  await create("Grok lost reply", false)
  const beforeLoss = await state()
  await control("drop-after-commit")
  click("仅保存配置资产")
  await until(() => evaluate("Array.from(document.querySelectorAll('[role=alert]')).some(node=>node.textContent.includes('配置写入结果待确认'))"), "committed response loss not Unknown")
  const afterLoss = await state()
  assert.equal(afterLoss.assets.filter(asset => asset.name === "Grok lost reply").length, 1)
  assert.equal(afterLoss.counters.writes, beforeLoss.counters.writes + 1)
  assert.equal(afterLoss.counters.commits, beforeLoss.counters.commits + 1)
  assert(evaluate("document.querySelector('[role=dialog] button[type=submit]').disabled"), "lost receipt can be replayed")
  evidence.push("postcommit-response-loss-one-write-one-commit-no-replay")
  const final = await state()
  assert(final.sideEffectsZero && final.plaintextAbsent && final.otherWrites === 0)
} catch (error) { failure = error }
finally {
  if (browserStarted) { try { browser(["close"]) } catch (error) { failure ??= error } }
  if (ready) { try { await control("release"); await control("stop") } catch (error) { failure ??= error } }
  const joined = await Promise.race([terminal, delay(10000).then(() => null)])
  if (!joined) { fixture.kill("SIGKILL"); await terminal; failure ??= new Error("fixture failed to drain") }
  else if (joined.code !== 0 || joined.signal) failure ??= new Error("fixture terminal failed")
  closeSync(log)
  const stopped = existsSync(join(directory, "stopped.json")) && JSON.parse(readFileSync(join(directory, "stopped.json"), "utf8"))
  if (!stopped?.closed || stopped.active !== 0 || !stopped.sideEffectsZero) failure ??= new Error("fixture cleanup receipt failed")
  writeFileSync(join(output, "receipts.json"), JSON.stringify({ evidence, exit: failure ? 1 : 0 }, null, 2), { mode: 0o600 })
}
if (failure) { console.error(failure.message); process.exitCode = 1 }
else console.log("Grok Web browser PASS")
