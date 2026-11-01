# Restore Candidate Sanitization

Status: main-thread technical approval; private implementation authorized, independent review pending.
Approved design: `/tmp/cognate-t4-coordinator-design-6f1e-amxELLrD/T4-COORDINATOR.md`, SHA256
`578bce2cff29315d523782aef74d7156fa08304bd48d829af8168f2ed25e34f4`.
This records implementation rulings, not additional requirements attributed to the user.

## Complete Slice

Implement one complete authenticated-candidate sanitization coordinator, not another SQL-only helper.
Consume the original one-shot verified Candidate, retain immutable authenticated source facts,
run EX-qualified inspection and an isolated writable revocation worker, commit and physically drain,
sync, independently inspect the resulting database, then transfer ownership to a one-shot Sanitized
owner. The original inventory/archives/source digest remain unchanged; the sanitized DB has its own
digest. Passive RestoreDatabaseEvidence is comparison data, never a mutation or activation capability.

Use the in-place private candidate DB; do not duplicate archives. Admit only the bound operation's
`.restore-<operation-id>/candidate/server.db` under the pinned OfflineRoot, with nofollow identities,
the same EX flock OFD, private files and no pre-existing SQLite sidecars. Do not make the old T1
worker writable. The new worker uses fixed SQL, a real transaction, DELETE journal, FULL sync,
foreign keys, bounded memory/CPU/file/record/staging budgets, and no network. Install seccomp TSYNC
and no_new_privs before SQLite; unsupported controls fail closed. First runtime qualification is
Linux amd64; other targets remain explicitly Unsupported and must compile without cgo.

The existing eight-DML helper runs once only. Preserve its admission and error rules when extracting
its read-only postcondition helper. Prove all other stored data unchanged through a bounded canonical
protected projection, including ciphertext, AAD identity, key/policy proofs, configuration references,
history and sequence counters; post-normalize MCP revision by exactly one. Re-run T1 and revocation
postconditions after actual commit, database/connection/pool/FD close, process Wait, callback join and
file/directory Sync. Wait for the root-loan post-admission result before exposing a capability.

## Ownership And Uncertainty

Candidate.Close must not race mutation or delete the transferred tree. Move its physical files and
private key ownership only after successful verification; old views expire and old Close loses unlink
authority. Sanitized.WithCandidate rechecks the bound root and result and lends one scoped view.
A private publication handoff must transfer unique deletion ownership before the first future rename;
test that renaming into a simulated generation and closing both prior owners does not delete it.

Any commit, physical close, Wait or Sync uncertainty yields no Sanitized capability and never retries
the non-idempotent helper on the same copy. Preserve the actual guard/fence/resources when terminal
ownership cannot be proved. Do not refresh all directory entries blindly after mutation, overwrite the
source metadata with the new DB digest, call owner.Close from its own scope, or recursively borrow the
same root mutex. Ordinary dual SH and SQLite crash recovery remain unchanged. R3's authenticated
operation intent/current/activation/rollback protocol is still separate and unimplemented.

## Approved Write Set

- Modify restore `candidate.go`, `candidate_files_linux.go`, `candidate_files_other.go`.
- Add restore `sanitize.go`, `sanitize_linux.go`, `sanitize_other.go`, `sanitize_linux_test.go`.
- Modify state `restore_credentials.go` only to extract its existing read-only postcondition check.
- Add state `restore_revoke.go`, `restore_revoke_linux.go`, `restore_revoke_other.go`,
  `restore_revoke_worker.go`, `restore_revoke_worker_linux.go`, `restore_revoke_worker_other.go`,
  `restore_revoke_checks_linux.go`, `restore_revoke_sandbox_linux_amd64.go`,
  `restore_revoke_sandbox_other.go`, `restore_revoke_linux_test.go`.
- Modify `cmd/cognate-server/main.go` only for the new hidden worker dispatch; add
  `cmd/cognate-server/restore_revoke_worker_integration_test.go`.
- Modify restore `inventory_integration_linux_test.go` and state `local_snapshot_linux_test.go`
  only at their TestMain dispatch registration, retaining existing assertions.

No runtimegen/kernel, old T1 worker, schema/ent, dependency, wire, App, live startup, recovery CLI or
publication implementation changes. R2 remains a pending dependency until independently accepted;
record any private dependency overlay separately from this slice's patch and full-source manifest.

## Acceptance

Require a real Capture/Seal/Open/Source-to-Candidate-to-transaction-to-postinspection chain; both owners,
all credential revocations, original proofs and all retained business data are checked. Cover forged or
expired capabilities, wrong root/FD/name/key, sidecars, schema/revision exhaustion and pre-SQL refusal.
Inject failures at each real transaction/Commit/Rollback/Close/Wait/Sync boundary and inspect actual
stored bytes without replay. Exercise parent death, same-OFD exclusion, cancellation/drain, physical
unknown retention, actual socket/connect rejection, each budget and deletion-ownership negative control.
Use Go1.27.3 with CGO_ENABLED=0, directed state/restore/actual-server tests and vet, then a fresh
non-author Spec/Quality review before integration. This is not complete restore, external S3 or GA.
