import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "backup-web-state-test"
const bundle = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/server-backup-state.ts"))}; export * from ${JSON.stringify(resolve("web/src/gen/skillhub/management/v1/backup_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
const destination = join(mkdtempSync(join(tmpdir(), "backup-web-state-")), "state.mjs")
await bundle.write({ file: destination, format: "esm" })
await bundle.close()
const state = await import(pathToFileURL(destination))
let checks = 0
function check(name, assertion) { assertion(); checks++ }
const identifier = (ordinal) => `00000000-0000-4000-8000-${String(ordinal).padStart(12, "0")}`
const owner = identifier(1)
const request = identifier(2)
const status = () => state.create(state.GetBackupStatusResponseSchema, { enabled: true, scope: "instance" })
const job = (ordinal = 3) => state.create(state.BackupJobSchema, { id: identifier(ordinal), phase: "pending", createdUnixMillis: 1793448000000n, updatedUnixMillis: 1793448000000n })
const page = () => state.create(state.ListBackupJobsResponseSchema, { jobs: [job()] })

check("enabled and disabled status remain distinct", () => {
  assert.equal(state.acceptBackupStatus(status()).enabled, true)
  const disabled = status(); disabled.enabled = false
  assert.equal(state.acceptBackupStatus(disabled).enabled, false)
})
for (const field of ["scope", "automaticEnabled", "restoreAvailable"]) check(`closed status field ${field}`, () => {
  const response = status(); response[field] = field === "scope" ? "owner" : true
  assert.throws(() => state.acceptBackupStatus(response))
})
check("missing status does not become disabled", () => assert.throws(() => state.acceptBackupStatus(undefined)))
for (const phase of ["pending", "capturing", "uploading", "committing", "remote_committed", "commit_unknown", "interrupted", "failed", "canceled"]) check(`known phase ${phase}`, () => {
  const candidate = job(); candidate.phase = phase
  if (["uploading", "committing", "remote_committed", "commit_unknown"].includes(phase)) { candidate.objects = 5; candidate.ciphertextBytes = 1024n }
  assert.equal(state.acceptBackupJob(candidate).phase, phase)
  assert.equal(state.isActiveBackup(candidate), ["pending", "capturing", "uploading", "committing"].includes(phase))
})
for (const code of ["", "invalid", "unavailable", "budget", "conflict", "uncertain", "canceled", "interrupted", "put_unknown", "verify_unknown", "missing"]) check(`known error ${code || "none"}`, () => {
  const candidate = job(); candidate.errorCode = code
  assert.equal(state.acceptBackupJob(candidate).errorCode, code)
})
for (const [field, value] of [
  ["id", "00000000-0000-0000-0000-000000000000"], ["id", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"], ["id", "opaque"],
  ["phase", "restore_verified"], ["errorCode", "upstream-private-error"], ["createdUnixMillis", 0n], ["createdUnixMillis", -1n],
  ["updatedUnixMillis", 1n], ["updatedUnixMillis", 8640000000000001n], ["createdUnixMillis", 1793448000000],
  ["objects", -1], ["objects", 0.5], ["objects", 4294967296], ["ciphertextBytes", -1n], ["ciphertextBytes", 9223372036854775808n],
  ["cancelRequested", "true"],
]) check(`reject invalid ${field} ${String(value).slice(0, 24)}`, () => {
  const candidate = job(); candidate[field] = value
  assert.throws(() => state.acceptBackupJob(candidate))
})
check("a response cannot replace a different requested job", () => assert.throws(() => state.acceptBackupJob(job(), identifier(99))))
check("missing mutation job is unknown, not success", () => assert.throws(() => state.acceptBackupJob(undefined)))
check("publication requires catalog accounting", () => { const candidate = job(); candidate.phase = "remote_committed"; assert.throws(() => state.acceptBackupJob(candidate)) })
check("catalog accounting is internally consistent", () => {
  for (const [objects, bytes] of [[0, 1n], [4, 1n], [5, 0n]]) { const candidate = job(); candidate.objects = objects; candidate.ciphertextBytes = bytes; assert.throws(() => state.acceptBackupJob(candidate)) }
})
check("exact bigint accounting stays bigint", () => {
  const candidate = job(); candidate.objects = 5; candidate.ciphertextBytes = 9007199254740993n
  assert.equal(state.acceptBackupJob(candidate).ciphertextBytes, 9007199254740993n)
})
check("valid first page", () => assert.equal(state.acceptBackupPage(page()).jobs.length, 1))
check("empty history is explicit", () => assert.equal(state.acceptBackupPage(state.create(state.ListBackupJobsResponseSchema)).jobs.length, 0))
check("missing page cannot become empty history", () => assert.throws(() => state.acceptBackupPage(undefined)))
check("bounded twenty-job page", () => { const response = page(); response.jobs = Array.from({ length: 21 }, (_, index) => job(index + 10)); assert.throws(() => state.acceptBackupPage(response)) })
check("duplicate job rows reject", () => { const response = page(); response.jobs.push(job()); assert.throws(() => state.acceptBackupPage(response)) })
check("cursor must be last row of a full page", () => {
  const response = page(); response.nextCursor = response.jobs[0].id
  assert.throws(() => state.acceptBackupPage(response))
  response.jobs = Array.from({ length: 20 }, (_, index) => job(index + 10)); response.nextCursor = response.jobs.at(-1).id
  assert.equal(state.acceptBackupPage(response).nextCursor, response.nextCursor)
  response.nextCursor = identifier(80); assert.throws(() => state.acceptBackupPage(response))
})
check("cursor cannot reappear or loop to visited history", () => {
  const response = page(); assert.throws(() => state.acceptBackupPage(response, response.jobs[0].id))
  response.jobs = Array.from({ length: 20 }, (_, index) => job(index + 10)); response.nextCursor = response.jobs.at(-1).id
  assert.throws(() => state.acceptBackupPage(response, identifier(90), [response.nextCursor]))
})

function storage() {
  const entries = new Map()
  return { entries, getItem: (key) => entries.get(key) ?? null, setItem: (key, value) => entries.set(key, value), removeItem: (key) => entries.delete(key) }
}
check("request identity survives a new read", () => {
  const store = storage()
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "empty" })
  assert.equal(state.writeBackupIntent(store, owner, request), true)
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "pending", requestId: request })
  assert.deepEqual([...store.entries.values()], [request])
  assert.equal(state.writeBackupIntent(store, owner, request), true)
  assert.equal(state.writeBackupIntent(store, owner, identifier(4)), false)
  assert.equal(state.clearBackupIntent(store, owner, identifier(4)), false)
  assert.equal(state.clearBackupIntent(store, owner, request), true)
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "empty" })
})
check("identities are separated by owner", () => {
  const store = storage(); state.writeBackupIntent(store, owner, request)
  assert.deepEqual(state.readBackupIntent(store, identifier(8)), { kind: "empty" })
  assert.equal(state.clearBackupIntent(store, identifier(8), request), false)
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "pending", requestId: request })
})
check("corrupt browser state never becomes a new request", () => {
  const store = storage(); state.writeBackupIntent(store, owner, request)
  store.entries.set([...store.entries.keys()][0], "unknown-state")
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "blocked" })
  assert.equal(state.writeBackupIntent(store, owner, request), false)
  assert.equal(state.clearBackupIntent(store, owner, request), false)
})
check("missing and denied storage fail before Start", () => {
  const denied = { getItem() { throw new Error("denied") }, setItem() { throw new Error("denied") }, removeItem() { throw new Error("denied") } }
  for (const store of [undefined, denied]) {
    assert.deepEqual(state.readBackupIntent(store, owner), { kind: "blocked" })
    assert.equal(state.writeBackupIntent(store, owner, request), false)
    assert.equal(state.clearBackupIntent(store, owner, request), false)
  }
})
check("failed or silently ignored publication cannot permit Start", () => {
  const store = storage(); store.setItem = () => {}
  assert.equal(state.writeBackupIntent(store, owner, request), false)
  const throwing = storage(); throwing.setItem = () => { throw new Error("full") }
  assert.equal(state.writeBackupIntent(throwing, owner, request), false)
})
check("failed removal retains same request", () => {
  const store = storage(); state.writeBackupIntent(store, owner, request); store.removeItem = () => {}
  assert.equal(state.clearBackupIntent(store, owner, request), false)
  assert.deepEqual(state.readBackupIntent(store, owner), { kind: "pending", requestId: request })
})
check("invalid owner or request never touches storage", () => {
  const denied = { getItem() { assert.fail("invalid identity touched storage") }, setItem() { assert.fail("invalid identity touched storage") }, removeItem() { assert.fail("invalid identity touched storage") } }
  assert.deepEqual(state.readBackupIntent(denied, "invalid"), { kind: "blocked" })
  assert.equal(state.writeBackupIntent(denied, "invalid", request), false)
  assert.equal(state.writeBackupIntent(denied, owner, "invalid"), false)
  assert.equal(state.clearBackupIntent(denied, owner, "invalid"), false)
})
console.log(`Backup Web boundary and intent: ${checks} checks passed`)
