import assert from "node:assert/strict"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "backup-settings-state-test"
const bundle = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/server-backup-settings-state.ts"))}; export { BackupSettingsSchema } from ${JSON.stringify(resolve("web/src/gen/skillhub/management/v1/backup_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
const destination = join(mkdtempSync(join(tmpdir(), "backup-settings-state-")), "state.mjs")
await bundle.write({ file: destination, format: "esm" })
await bundle.close()
const { backupIntervalSeconds, backupSettingsChanged, create, BackupSettingsSchema } = await import(pathToFileURL(destination))
let checks = 0
for (const [value, seconds] of [["60", 60n], ["86400", 86400n], ["1e2", 100n], ["1.2e2", 120n], ["120.0", 120n]]) {
  assert.equal(backupIntervalSeconds(value), seconds, value); checks++
}
for (const value of ["", " ", "59", "86401", "120.5", "NaN", "Infinity", "1e100", "0x78", "-120", "1e", "3,600"]) {
  assert.equal(backupIntervalSeconds(value), undefined, value); checks++
}
const initial = create(BackupSettingsSchema, { revision: 1n })
const changed = (draft = initial, interval = "3600", action = "replace", hasCredentialInput = false) => backupSettingsChanged(initial, draft, interval, action, hasCredentialInput)
assert.equal(changed(), false); checks++
assert.equal(changed(initial, "invalid"), false); checks++
assert.equal(changed(initial, "3600", "replace", true), true); checks++
for (const field of ["enabled", "endpoint", "region", "bucket", "prefix", "recoveryKeyId", "recoveryKeyFile", "maxDatabaseBytes", "maxContentBytes", "jobTimeoutSeconds"]) {
  const value = typeof initial[field] === "bigint" ? 1n : typeof initial[field] === "boolean" ? true : "changed"
  assert.equal(changed({ ...initial, [field]: value }), true, field); checks++
}
const configured = create(BackupSettingsSchema, { revision: 2n, automaticIntervalSeconds: 120n, credentialsConfigured: true })
assert.equal(backupSettingsChanged(configured, configured, "1.2e2", "keep", false), false); checks++
assert.equal(backupSettingsChanged(configured, configured, "1e2", "keep", false), true); checks++
assert.equal(backupSettingsChanged(configured, configured, "bad", "keep", false), true); checks++
assert.equal(backupSettingsChanged(configured, configured, "120", "replace", false), true); checks++
assert.equal(backupSettingsChanged(configured, { ...configured, automaticIntervalSeconds: 0n }, "120", "keep", false), true); checks++
console.log(`Backup settings interval and dirty state: ${checks} checks passed`)
