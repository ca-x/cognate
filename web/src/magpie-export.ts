import { Code, ConnectError } from "@connectrpc/connect"

const maximumBackupBytes = 64 * 1024 * 1024
const maximumLibraryBytes = 1024 * 1024

function invalid(): never { throw new ConnectError("Magpie export could not be opened. Check the file and password.", Code.InvalidArgument) }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid()
  return value as Record<string, unknown>
}
function base64(value: unknown, maximum: number): Uint8Array<ArrayBuffer> {
  if (typeof value !== "string" || value.length > maximum * 4 / 3 + 8 || value.length % 4 !== 0 || /[^A-Za-z0-9+/=]/.test(value)) return invalid()
  try {
    const decoded = atob(value)
    const bytes = new Uint8Array(decoded.length)
    for (let index = 0; index < decoded.length; index++) bytes[index] = decoded.charCodeAt(index)
    return bytes
  } catch { return invalid() }
}

export async function prepareMagpieExport(raw: Uint8Array, password: string, signal: AbortSignal): Promise<Uint8Array> {
  signal.throwIfAborted()
  if (raw.length > maximumBackupBytes) throw new ConnectError("Magpie backup exceeds 64 MiB.", Code.ResourceExhausted)
  let envelope: Record<string, unknown>
  try { envelope = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))) } catch { return invalid() }
  if (envelope.format !== "magpie-backup") {
    if (raw.length > maximumLibraryBytes) throw new ConnectError("Magpie library exceeds 1 MiB.", Code.ResourceExhausted)
    return raw
  }
  if (!password || new TextEncoder().encode(password).length > 4096 || !globalThis.crypto?.subtle || envelope.version !== 1 || envelope.kdf !== "pbkdf2-sha256" || !Number.isSafeInteger(envelope.iterations) || Number(envelope.iterations) < 100000 || Number(envelope.iterations) > 10000000) return invalid()
  const salt = base64(envelope.salt, 64)
  const nonce = base64(envelope.nonce, 12)
  const data = base64(envelope.data, maximumBackupBytes)
  if (salt.length < 16 || nonce.length !== 12 || data.length < 16) return invalid()
  const encodedPassword = new TextEncoder().encode(password)
  let plain: Uint8Array | undefined
  try {
    const material = await crypto.subtle.importKey("raw", encodedPassword, "PBKDF2", false, ["deriveKey"])
    const key = await crypto.subtle.deriveKey({ name: "PBKDF2", hash: "SHA-256", salt, iterations: Number(envelope.iterations) }, material, { name: "AES-GCM", length: 256 }, false, ["decrypt"])
    signal.throwIfAborted()
    const header = `magpie-backup/1/pbkdf2-sha256/${envelope.iterations}/${Array.from(salt, (byte) => byte.toString(16).padStart(2, "0")).join("")}`
    plain = new Uint8Array(await crypto.subtle.decrypt({ name: "AES-GCM", iv: nonce, additionalData: new TextEncoder().encode(header), tagLength: 128 }, key, data))
    signal.throwIfAborted()
    const bundle = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(plain)))
    if (bundle.version !== 1 && bundle.version !== 2) return invalid()
    const library = object(bundle.library)
    if (!Array.isArray(library.skills) || library.skills.length > 1000) return invalid()
    const skills = library.skills.map((value) => {
      const skill = object(value)
      const original = skill.source == null ? undefined : object(skill.source)
      const source = original === undefined ? undefined : original.kind === "github" ? { kind: original.kind, repo: original.repo, ref: original.ref, path: original.path } : { kind: original.kind }
      return { name: skill.name, source, agents: Array.isArray(skill.agents) ? [] : undefined }
    })
    const instructions = library.instructions ? object(library.instructions) : {}
    const placeholders = (value: unknown) => Array.isArray(value) ? value.map(() => ({})) : []
    const normalized = new TextEncoder().encode(JSON.stringify({ skills, mcp: placeholders(library.mcp), instructions: { sets: placeholders(instructions.sets), agents: placeholders(instructions.agents) }, providers: placeholders(bundle.providers) }))
    if (normalized.length > maximumLibraryBytes) { normalized.fill(0); throw new ConnectError("Magpie library exceeds 1 MiB.", Code.ResourceExhausted) }
    return normalized
  } catch (failure) {
    signal.throwIfAborted()
    if (failure instanceof ConnectError) throw failure
    return invalid()
  } finally { encodedPassword.fill(0); plain?.fill(0); data.fill(0); salt.fill(0); nonce.fill(0) }
}
