import { Code, ConnectError } from "@connectrpc/connect"
import type { BackupJob, GetBackupStatusResponse, ListBackupJobsResponse } from "./gen/skillhub/management/v1/backup_pb"

const phases = ["pending", "capturing", "uploading", "committing", "remote_committed", "commit_unknown", "interrupted", "failed", "canceled"] as const
const codes = ["", "invalid", "unavailable", "budget", "conflict", "uncertain", "canceled", "interrupted", "put_unknown", "verify_unknown", "missing"] as const
export const backupPageSize = 20
export const backupMaxPages = 50
export type BackupJobView = Omit<BackupJob, "$typeName" | "$unknown" | "phase" | "errorCode"> & { phase: typeof phases[number]; errorCode: typeof codes[number] }
export type BackupIntent = { kind: "empty" } | { kind: "pending"; requestId: string } | { kind: "blocked" }
type IntentStorage = Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined

function validIdentifier(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) && value !== "00000000-0000-0000-0000-000000000000"
}

function invalidResponse(): never {
  throw new ConnectError("unsupported backup response", Code.DataLoss)
}

export function acceptBackupStatus(value?: GetBackupStatusResponse) {
  if (!value || typeof value.enabled !== "boolean" || value.scope !== "instance" || value.automaticEnabled !== false || value.restoreAvailable !== false) return invalidResponse()
  return { enabled: value.enabled, scope: "instance" as const, automaticEnabled: false, restoreAvailable: false }
}

export function acceptBackupJob(value?: BackupJob, expectedId?: string): BackupJobView {
  if (!value || !validIdentifier(value.id) || expectedId !== undefined && value.id !== expectedId || !phases.some((phase) => phase === value.phase) || !codes.some((code) => code === value.errorCode)) return invalidResponse()
  if (typeof value.createdUnixMillis !== "bigint" || typeof value.updatedUnixMillis !== "bigint" || value.createdUnixMillis <= 0n || value.updatedUnixMillis < value.createdUnixMillis || value.updatedUnixMillis > 8640000000000000n) return invalidResponse()
  if (!Number.isInteger(value.objects) || value.objects < 0 || value.objects > 4294967295 || typeof value.ciphertextBytes !== "bigint" || value.ciphertextBytes < 0n || value.ciphertextBytes > 9223372036854775807n || typeof value.cancelRequested !== "boolean") return invalidResponse()
  if (value.objects === 0 ? value.ciphertextBytes !== 0n || ["uploading", "committing", "remote_committed", "commit_unknown"].includes(value.phase) : value.objects < 5 || value.ciphertextBytes === 0n) return invalidResponse()
  return { id: value.id, phase: value.phase as BackupJobView["phase"], errorCode: value.errorCode as BackupJobView["errorCode"], createdUnixMillis: value.createdUnixMillis, updatedUnixMillis: value.updatedUnixMillis, objects: value.objects, ciphertextBytes: value.ciphertextBytes, cancelRequested: value.cancelRequested }
}

export function acceptBackupPage(value?: ListBackupJobsResponse, cursor = "", visited: readonly string[] = []) {
  if (!value || !Array.isArray(value.jobs) || value.jobs.length > backupPageSize || cursor !== "" && !validIdentifier(cursor)) return invalidResponse()
  const jobs = value.jobs.map((job) => acceptBackupJob(job))
  if (new Set(jobs.map((job) => job.id)).size !== jobs.length || jobs.some((job) => job.id === cursor)) return invalidResponse()
  if (typeof value.nextCursor !== "string" || value.nextCursor !== "" && (!validIdentifier(value.nextCursor) || jobs.length !== backupPageSize || jobs.at(-1)?.id !== value.nextCursor || value.nextCursor === cursor || visited.includes(value.nextCursor))) return invalidResponse()
  return { jobs, nextCursor: value.nextCursor }
}

export function isActiveBackup(job: Pick<BackupJobView, "phase">) {
  return ["pending", "capturing", "uploading", "committing"].includes(job.phase)
}

function intentKey(owner: string) {
  return `cognate.backup.intent.v1.${owner}`
}

export function readBackupIntent(storage: IntentStorage, owner: string): BackupIntent {
  if (!storage || !validIdentifier(owner)) return { kind: "blocked" }
  try {
    const value = storage.getItem(intentKey(owner))
    if (value === null) return { kind: "empty" }
    return validIdentifier(value) ? { kind: "pending", requestId: value } : { kind: "blocked" }
  } catch { return { kind: "blocked" } }
}

export function writeBackupIntent(storage: IntentStorage, owner: string, requestId: string) {
  if (!storage || !validIdentifier(owner) || !validIdentifier(requestId)) return false
  const current = readBackupIntent(storage, owner)
  if (current.kind === "blocked") return false
  if (current.kind === "pending") return current.requestId === requestId
  try {
    storage.setItem(intentKey(owner), requestId)
    const recorded = readBackupIntent(storage, owner)
    return recorded.kind === "pending" && recorded.requestId === requestId
  } catch { return false }
}

export function clearBackupIntent(storage: IntentStorage, owner: string, requestId: string) {
  if (!storage || !validIdentifier(owner) || !validIdentifier(requestId)) return false
  const current = readBackupIntent(storage, owner)
  if (current.kind !== "pending" || current.requestId !== requestId) return false
  try {
    storage.removeItem(intentKey(owner))
    return storage.getItem(intentKey(owner)) === null
  } catch { return false }
}
