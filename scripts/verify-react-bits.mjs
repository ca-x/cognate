import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"

const expected = "4bdd1fa59a6b7d5954bc3b213dbc34e455c635a94bbcecf9293553797d0fad02"
for (const path of ["web/public/assets/react-bits-LICENSE.txt", "web/dist/assets/react-bits-LICENSE.txt"]) {
  assert.equal(createHash("sha256").update(readFileSync(path)).digest("hex"), expected, `${path}: complete pinned license required`)
}
console.log("React Bits: source and built license bytes verified")
