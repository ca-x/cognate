import assert from "node:assert/strict"
import { createCipheriv, pbkdf2Sync } from "node:crypto"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const build = await rolldown({ input: resolve("web/src/magpie-export.ts") })
const destination = join(mkdtempSync(join(tmpdir(), "magpie-export-")), "export.mjs")
await build.write({ file: destination, format: "esm" })
await build.close()
const { prepareMagpieExport } = await import(pathToFileURL(destination))
const password = "synthetic-export-password"
const bundle = { version: 2, providers: [{ key: "SYNTHETIC-PRIVATE-KEY" }], library: { skills: [{ name: "review", source: { kind: "github", repo: "example/skills", path: "skills/review" }, agents: [], files: { "SKILL.md": "SYNTHETIC-FILE-CONTENT" } }], mcp: [{ env: { TOKEN: "SYNTHETIC-MCP-KEY" } }] } }
function seal(value) {
  const salt = Buffer.alloc(16, 7), nonce = Buffer.alloc(12, 9)
  const iterations = 600000
  const cipher = createCipheriv("aes-256-gcm", pbkdf2Sync(password, salt, iterations, 32, "sha256"), nonce)
  cipher.setAAD(Buffer.from(`magpie-backup/1/pbkdf2-sha256/${iterations}/${salt.toString("hex")}`))
  const data = Buffer.concat([cipher.update(JSON.stringify(value)), cipher.final(), cipher.getAuthTag()])
  return { format: "magpie-backup", version: 1, kdf: "pbkdf2-sha256", iterations, salt: salt.toString("base64"), nonce: nonce.toString("base64"), data: data.toString("base64") }
}
const encode = (value) => new TextEncoder().encode(JSON.stringify(value))
const envelope = seal(bundle)
const normalized = await prepareMagpieExport(encode(envelope), password, new AbortController().signal)
assert.equal(JSON.parse(new TextDecoder().decode(normalized)).skills[0].source.repo, "example/skills")
assert(!new TextDecoder().decode(normalized).includes("SYNTHETIC-"))
for (const changed of [{ ...envelope, version: 2 }, { ...envelope, iterations: 99999 }, { ...envelope, iterations: 10000001 }, { ...envelope, salt: "invalid" }, { ...envelope, data: envelope.data.slice(4) }]) {
  await assert.rejects(prepareMagpieExport(encode(changed), password, new AbortController().signal))
}
await assert.rejects(prepareMagpieExport(encode(envelope), "wrong-password", new AbortController().signal))
await assert.rejects(prepareMagpieExport(encode(envelope), "", new AbortController().signal))
await assert.rejects(prepareMagpieExport(encode(envelope), password, AbortSignal.abort()))
assert.deepEqual(await prepareMagpieExport(encode({ skills: [] }), "", new AbortController().signal), encode({ skills: [] }))
await prepareMagpieExport(new Uint8Array(Buffer.concat([Buffer.from([239,187,191]), encode(envelope)])), password, new AbortController().signal)
await assert.rejects(prepareMagpieExport(encode(seal({ ...bundle, version: 3 })), password, new AbortController().signal))
console.log("PASS: Magpie GUI encrypted backup, password/tamper/bounds/BOM/cancellation, legacy JSON and secret exclusion")
