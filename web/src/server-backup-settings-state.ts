import type { BackupSettings } from "./gen/skillhub/management/v1/backup_pb"

export function backupIntervalSeconds(value: string): bigint | undefined {
  if (!/^[+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?$/i.test(value)) return undefined
  const seconds = Number(value)
  if (!Number.isSafeInteger(seconds) || seconds < 60 || seconds > 86400) return undefined
  return BigInt(seconds)
}

export function backupSettingsChanged(initial: BackupSettings, draft: BackupSettings, interval: string, credentialsAction: string, hasCredentialInput: boolean) {
  const fields = ["enabled", "endpoint", "region", "bucket", "prefix", "recoveryKeyId", "recoveryKeyFile", "maxDatabaseBytes", "maxContentBytes", "jobTimeoutSeconds"] as const
  return fields.some((field) => initial[field] !== draft[field])
    || (draft.automaticIntervalSeconds > 0n ? backupIntervalSeconds(interval) : 0n) !== initial.automaticIntervalSeconds
    || credentialsAction === "replace" && (initial.credentialsConfigured || hasCredentialInput)
}
