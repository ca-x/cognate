import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { closeSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync, writeSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { fileURLToPath } from "node:url"

const { values } = parseArgs({ options: { "fixture-bin": { type: "string" }, output: { type: "string" }, fixtures: { type: "string", default: "3" }, rounds: { type: "string", default: "5" }, "audit-state": { type: "string" }, "self-test": { type: "boolean" } } })

function auditState(state, expected) {
  assert.equal(state.violation, false, "fixture contract violated")
  assert(Object.hasOwn(state, "seedFailure"), "seed outcome missing")
  assert(state.seedFailure === null || typeof state.seedFailure === "object" && !Array.isArray(state.seedFailure) && Object.keys(state.seedFailure).length === 0, "seed failed")
  assert(Object.hasOwn(state, "rpcFailures"), "RPC ledger missing")
  assert(state.rpcFailures === null || Array.isArray(state.rpcFailures), "RPC ledger invalid")
  assert.equal((state.rpcFailures ?? []).length, 0, "unexpected RPC failure")
  if (expected !== undefined) assert.equal(state.completes, expected, "complete-last count mismatch")
  assert(Number.isSafeInteger(state.completes) && state.completes >= 0, "publication count invalid")
}

if (values["self-test"]) {
  const green = { violation: false, seedFailure: null, rpcFailures: null, completes: 21 }
  auditState(green, 21)
  auditState({ ...green, seedFailure: {} }, 21)
  const { rpcFailures, ...missing } = green
  const { seedFailure, ...missingOutcome } = green
  for (const red of [{ ...green, violation: true }, { ...green, seedFailure: { code: "internal" } }, { ...green, rpcFailures: [{ method: "GetSession", code: "internal", status: 500 }] }, { ...green, rpcFailures: {} }, { ...green, completes: 20 }, missing, missingOutcome, { ...green, seedFailure: [] }]) assert.throws(() => auditState(red, 21))
  console.log(JSON.stringify({ gateSelfTest: "pass", checks: 10 }))
  process.exit(0)
}
if (values["audit-state"]) {
  try {
    auditState(JSON.parse(readFileSync(values["audit-state"], "utf8")))
    console.log(JSON.stringify({ ledgerAuditExit: 0 }))
  } catch {
    console.log(JSON.stringify({ ledgerAuditExit: 1 }))
    process.exitCode = 1
  }
} else {
  assert(values["fixture-bin"] && values.output, "fixture-bin and output are required")
  const fixtureCount = Number(values.fixtures), rounds = Number(values.rounds)
  assert(Number.isInteger(fixtureCount) && fixtureCount > 0 && fixtureCount <= 20, "fixture count invalid")
  assert(Number.isInteger(rounds) && rounds > 0 && rounds <= 30, "round count invalid")
  const fixtureBinary = resolve(values["fixture-bin"]), output = resolve(values.output)
  const hash = (file) => createHash("sha256").update(readFileSync(file)).digest("hex")
  const receipt = { identity: { scriptSHA256: hash(fileURLToPath(import.meta.url)), fixtureSHA256: hash(fixtureBinary), node: process.version }, uiLoaded: false, backendRecoveryRetries: 0, exit: 0, rounds: [] }
  mkdirSync(output, { recursive: true, mode: 0o700 })
  for (let fixtureIndex = 1; fixtureIndex <= fixtureCount && receipt.exit === 0; fixtureIndex++) {
    const run = join(output, `run-${fixtureIndex}`)
    mkdirSync(run, { mode: 0o700 })
    const directory = mkdtempSync(join(run, "private-")), home = join(directory, "home")
    mkdirSync(home, { mode: 0o700 })
    const log = openSync(join(run, "fixture.log"), "w", 0o600)
    let child, terminal, ready, readyTimeout, stage = "readiness"
    let readySignal
    const readiness = new Promise((done, reject) => {
      readySignal = () => {
        try { ready = JSON.parse(readFileSync(join(directory, "ready.json"), "utf8")); done() } catch { reject(new Error("private readiness invalid")) }
      }
      readyTimeout = setTimeout(() => reject(new Error("fixture readiness deadline")), 15000)
    })
    child = spawn(fixtureBinary, ["-test.run=^TestBackupBrowserFixture$", "-test.v", "-test.timeout=600s"], { env: { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, "config"), XDG_DATA_HOME: join(home, "data"), COGNATE_BACKUP_BROWSER_DIR: directory }, stdio: ["ignore", "pipe", log] })
    let startup = ""
    child.stdout.on("data", (chunk) => {
      writeSync(log, chunk)
      if (!ready) {
        startup += chunk.toString()
        if (startup.includes("cognate_probe backup_ready\n")) readySignal()
      }
    })
    terminal = new Promise((done) => { child.once("exit", (code, signal) => done({ code, signal })); child.once("error", () => done({ code: -1, signal: null })) })
    const control = async (name, body) => {
      assert(ready && new URL(ready.controlURL).hostname === "127.0.0.1", "non-loopback fixture refused")
      const response = await fetch(`${ready.controlURL}/${name}`, { method: body === undefined ? "GET" : "POST", headers: { "X-Fixture-Capability": ready.capability, Connection: "close" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(90000) })
      if (response.status !== (body === undefined ? 200 : 204)) receipt.controlFailure = { operation: name, status: response.status }
      assert.equal(response.status, body === undefined ? 200 : 204, "control request failed")
      return body === undefined ? response.json() : undefined
    }
    try {
      await Promise.race([readiness, terminal.then(() => { throw new Error("fixture exited before readiness") })])
      clearTimeout(readyTimeout)
      for (let round = 1; round <= rounds; round++) {
        stage = "seed"
        await control("seed", {})
        stage = "state"
        const state = await control("state")
        writeFileSync(join(run, "safe-state.json"), JSON.stringify(state, null, 2), { mode: 0o600 })
        stage = "audit"
        auditState(state, round * 21)
        receipt.rounds.push({ fixture: fixtureIndex, round, completes: state.completes, objects: state.objects, unexpectedRPCs: 0 })
      }
    } catch {
      receipt.exit = 1
      receipt.failureStage = stage
      if (ready) { try { writeFileSync(join(run, "failure-state.json"), JSON.stringify(await control("state"), null, 2), { mode: 0o600 }) } catch {} }
    } finally {
      clearTimeout(readyTimeout)
      if (ready) { try { await control("stop", {}) } catch { receipt.exit = 1 } }
      else child.kill("SIGTERM")
      const stopTimeout = setTimeout(() => { receipt.exit = 1; child.kill("SIGKILL") }, 10000)
      const finished = await terminal
      clearTimeout(stopTimeout); closeSync(log)
      if (finished.code !== 0 || finished.signal) receipt.exit = 1
      receipt.fixtureTerminal = finished
    }
  }
  writeFileSync(join(output, "receipts.json"), JSON.stringify(receipt, null, 2), { mode: 0o600 })
  console.log(JSON.stringify({ exit: receipt.exit, fixtures: fixtureCount, completedSeedRounds: receipt.rounds.length, backupsPerSeed: 21, backendRecoveryRetries: 0 }))
  process.exitCode = receipt.exit
}
