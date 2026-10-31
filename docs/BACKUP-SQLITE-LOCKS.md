# Backup SQLite lock investigation

## Scope and evidence plan (2026-10-31)

Private archive: `/tmp/cognate-backup-locks.KplS5a6u/work`; baseline:
`59da539509f6d7aa10c28f652b72c879157be63c`. Main and the frozen Sources v2
artifacts are not modified. This work does not retrospectively explain historical
backup v7, nor conflate SQLite 8714 with the Sources transaction error 517.

Supplied evidence: `backup-browser-correct-package/failure-state.json` records
seed `wait/internal` after 25 complete objects; `backup-diagnostic-4` adds the
allowlisted `authentication` boundary and numeric `session_read sqlite_code=8714
canceled=false deadline=false`. The preceding successful diagnostic rounds and
all failed rounds remain historical evidence, not proof of a fix.

Hypothesis, not yet a conclusion: ordinary DB/SHM descriptor closes in parent
source admission release process-associated SQLite record locks. A different
process can then obtain the WAL DMS exclusive lock and truncate a live mapping.
Check independently using real SQLite connections, child-process F_GETLK/F_SETLK,
and an explicitly labelled controlled DMS-guarded truncate reproducer. Never use
SehInject or manufacture a SQLite return code. Establish RED before changing
production code. Diagnostics contain fixed stages, booleans and numeric codes,
not raw errors, SQL, tokens, account data or headers.

Proposed write boundary: relevant `local_snapshot_source*` and
`local_snapshot_worker*`, their durable tests, the backup browser test fixture,
a dedicated probe script, and this document. No auth/store/App/protocol expansion
without first reporting the exact need and plan. Preserve inode pins, path
checks, byte/resource budgets, uncertainty retention, worker terminal-state
fencing and cleanup. No retry, sleep, global mutex, driver upgrade, cgo or
journal-mode workaround. Verification is targeted state/maintenance tests and
real fixture/browser exercises, not full-repository tests or repeated release
build matrices. All Go commands use Go 1.27.3 and explicit `CGO_ENABLED=0`.

## Deterministic RED and minimal repair plan

`artifacts/locks-red.log` records real child-process observations: admission
changes both main and SHM locks from present to absent; closing the retained
ordinary DB descriptor alone drops the main lock. A child can then obtain the
actual DMS exclusive record lock and perform a controlled truncate; the parent's
next real SQLite query returns numeric 8714. This is a guarded-truncate model of
the SQLite DMS path, not a claim to have stopped an unmodified SQLite worker
inside its truncate syscall. No simulated error or SehInject is used.

Minimal production repair: retain the parent DB inode using Linux O_PATH, whose
close does not remove the process's POSIX record locks; make parent admission
metadata-only (identity, private-file/path/journal checks, sidecar size caps).
The existing resource-limited worker already runs the complete bounded
100/32/136-byte inspector before opening SQLite. Keep that ordering and preserve
budget/cleanup classification in its existing response statuses. No additional
worker operation or wire-format change is needed. The source descriptor is
still inherited, identity-checked and pinned throughout the same lifecycle;
parent/source/stage cleanup and worker fencing stay unchanged.

## Dependency evidence and causal boundary

The pinned `modernc.org/sqlite@v1.60.1/lib/sqlite.go` defines
`SQLITE_IOERR_IN_PAGE = 8714`. Its `lib/seh.go` converts real faults inside the
WAL-index mapping to that code. The active Linux/amd64 source is
`lib/sqlite_g_0000000000003f80.go`: `_unixLockSharedMemory` checks the DMS byte
128 with F_GETLK, takes F_WRLCK if no other process holds it, truncates SHM to
**3 bytes**, and downgrades to a shared lock. These are observations of this
fixed dependency, not a driver version upgrade or a new SQLite implementation.

The first controlled reproducer used a zero-byte truncate and already produced
8714. After reading the precise VFS branch, the final durable test uses exactly
3 bytes: the old-production archive fails all 10 repetitions, with 10 real
8714 query errors. The candidate passes the same 10 repetitions: both database
and SHM process locks remain present, the child cannot acquire the DMS exclusive
lock, it never truncates, and the parent query succeeds with code 0. The original
zero-byte RED/GREEN logs are retained as earlier investigation, not relabelled
as the final test identity. The controlled child is not an unmodified SQLite
worker; the real supplied authentication 8714, lock-loss regression, exact VFS
branch and real mapping-fault reproducer establish the mechanism without
claiming every historical 500 has this cause. Historical v7 stays unattributed.

## Final implementation write set

1. `internal/server/state/local_snapshot_source_linux.go`: O_PATH inode pin;
   parent metadata-only admission, including journal absence and private
   sidecar/path/size checks. It opens/closes no ordinary DB/WAL/SHM descriptor.
2. `internal/server/state/local_snapshot_worker_linux.go`: retain the existing
   full fixed-prefix inspection before SQLite open, preserving budget refusal
   and cleanup-failure classification using existing status 6/7. Known-terminal
   process checks, unknown-terminal resource retention and cleanup gating are
   unchanged. No new operation or wire-format field.
3. `internal/server/state/local_snapshot_source_locks_linux_test.go`: real
   SQLite, independent child-process lock observations, ordinary-source-close
   regression, and DMS-guarded 3-byte mapping fault regression.
4. `internal/server/backup_browser_fixture_linux_test.go`: test-only allowlisted
   boundary diagnostics; actual Auth/Backup failure ledger, conservative
   Internal/unknown violation gate, safe readiness marker and final state.
   No account/token/header/raw error logging or production auth change.
5. `scripts/verify-backup-sqlite-locks.mjs`: no-UI real TLS/SQLite/S3 seed probe,
   exact complete-last assertions, missing/error-ledger rejection and self-test.
6. `docs/BACKUP-SQLITE-LOCKS.md`: this bounded report and reproduction commands.

The original 268-byte prefix bound, geometry checks, necessary-refusal arithmetic,
inode pins, safe paths, resource/heap/staging limits, reference validation,
worker fence, cancellation/terminal joins and uncertainty retention remain.
The full byte inspector now executes only in the already-isolated worker,
under its existing limits and before its first SQLite connection. Parent
metadata admission reads zero source content bytes. No store, maintenance,
App, auth, source transaction, UI or other worker code changes; no global mutex,
retry/sleep, journal mode change, new dependency or driver replacement.

## Author verification results

- `locks-red-exact-repeat10.log`: old production plus final test, exit 1;
  admission loses main+SHM locks, retained-source close loses main lock;
  controlled truncate produces 8714 in 10/10 repetitions.
- `locks-green-exact-repeat10.log`: same tests, candidate production, exit 0;
  locks retained, 10/10 guarded truncates refused, query code 0.
- `snapshot-final.log`: all `TestLocalSnapshot*` and `TestSnapshot*` targeted
  regressions pass, including budgets, physical replacement, independent
  restore, cancellation, worker controls and cleanup. State vet also passes.
- `maintenance-targeted.log`: real snapshot-worker fence process test passes.
- `fixture-ledger.log`: success/Internal/malformed/permission-denied ledger
  cases pass; raw private message is not persisted.
- `gate-self-test.log`: 10 gate checks pass; `gate-historical-red.log` rejects
  the supplied diagnostic failure state with actual exit 1.
- `rpc-seed/receipts.json`: three fresh independent fixtures, five successful
  seed rounds each, **315** real complete-last backups, no UI, zero RPC errors,
  zero recovery retries. All three final states contain 105 complete and 630
  objects. Each seed still uses the existing job-state polling; it is not a
  recovery retry. No polling cadence or sleeps were added or changed.
- `browser/receipts.json`: final real browser exit 0, **88** receipts, **56**
  geometry/focus checks, **43** original screenshots, 26 complete-last backups
  and 157 objects. The final actual-RPC ledger has exactly the intentional
  ordinary-user GetBackupStatus permission_denied/403, no Internal/unknown;
  the fixture violation flag is false. UI forced-unavailable/malformed branches,
  lost response/same ID, cancel/reconcile, pagination, keyboard, Chinese/English,
  light/dark, 320/375 at 200% root font and ReduceMotion remain exercised by
  the unchanged browser script. This is not real OS/browser zoom qualification.

`artifacts/VERIFICATION.json` binds the final probe, fixture, unchanged browser
script and copied production Web dist identities. The Web input is a private
copy of main's existing `web/dist`, explicitly allowed for this task; all 48
files are identified in `artifacts/WEB-DIST-SHA256SUMS`. No Web source or
node_modules was edited and no new Web production build is claimed here.

Preserved harness failures: `locks-green-build-red.log` is an intermediate
missing-import build failure, then corrected; `rpc-seed-harness-red` is a
successful 21-backup backend round rejected by an overly strict null-only
seedFailure assertion. Go json/v2 encodes the empty map as `{}`. The final gate
accepts only an explicitly present null/empty object, rejects nonempty/missing/
wrong-shaped outcomes, and has negative tests. These are harness failures, not
backend failures or hidden product recovery. Initial green runs using the
pre-hardening fixture remain under `browser-initial`/`rpc-seed-initial`; only
the final identities in VERIFICATION are the delivery evidence.

## Exact reproduction commands

```bash
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
ROOT=/tmp/cognate-backup-locks.KplS5a6u
cd "$ROOT/work"
CGO_ENABLED=0 go test ./internal/server/state -run '^TestLocalSnapshot(SourcePreservesSQLiteLocks|AdmissionProtectsLiveMapping)$' -count=10 -v
CGO_ENABLED=0 go test ./internal/server/state -run '^(TestLocalSnapshot|TestSnapshot)' -count=1 -v
CGO_ENABLED=0 go test ./internal/server/maintenance -run '^Test.*(Snapshot|Guard|Fence)' -count=1 -v
CGO_ENABLED=0 go test ./internal/server -run '^TestBackupSQLiteFailureLedger$' -count=1 -v
CGO_ENABLED=0 go vet ./internal/server/state
CGO_ENABLED=0 go test -c ./internal/server -o "$ROOT/artifacts/backup-locks-reproduce.test"
node scripts/verify-backup-sqlite-locks.mjs --self-test
node scripts/verify-backup-sqlite-locks.mjs --fixture-bin "$ROOT/artifacts/backup-locks-reproduce.test" \
  --fixtures 3 --rounds 5 --output "$ROOT/artifacts/rpc-reproduce"
node scripts/verify-backup-web.mjs --fixture-bin "$ROOT/artifacts/backup-locks-reproduce.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output "$ROOT/artifacts/browser-reproduce"
```

The old-production RED control is not created by reverting the candidate:

```bash
ROOT=/tmp/cognate-backup-locks.KplS5a6u
CONTROL=$(mktemp -d /tmp/cognate-backup-locks-red.XXXXXXXX)
git -C /home/czyt/code/go/skillhub archive 59da539509f6d7aa10c28f652b72c879157be63c | tar -xf - -C "$CONTROL"
cp "$ROOT/work/internal/server/state/local_snapshot_source_locks_linux_test.go" "$CONTROL/internal/server/state/"
cd "$CONTROL"
CGO_ENABLED=0 go test ./internal/server/state -run '^TestLocalSnapshot(SourcePreservesSQLiteLocks|AdmissionProtectsLiveMapping)$' -count=10 -v
# Expected exit 1; execute separately from commands using set -e.
```

For an initial candidate archive, copy the allowed production Web input before
building the fixture, without modifying main:

```bash
cp -a /home/czyt/code/go/skillhub/web/dist "$ROOT/work/web/dist"
ln -s /home/czyt/code/go/skillhub/web/node_modules "$ROOT/work/web/node_modules"
```

The existing private copy already has both; do not overlay a frozen copy or
repeat these commands there. The probe keeps readiness/session/database files
under private directories, never in its public receipts or evidence archive.

## Delivery and unverified boundaries

`delivery/backup-sqlite-locks.patch` is the six-file cumulative patch against
59da539. Its source tar contains exactly those six files; SHA256SUMS enumerates
every path/hash and ARTIFACT-SHA256SUMS identifies patch/tars/provenance. A fresh
private baseline passes patch --check, actual apply and all six hashes. The
evidence tar preserves supplied initial failure and all four diagnostic rounds,
our RED/build/harness failures, final targeted logs, final 315-backup RPC ledger,
and final browser screenshots/ledger; intermediate green PNG duplication,
readiness/session data, databases, profiles, node_modules and binaries are
excluded. Original Sources v2 artifact hashes remain unchanged.

This is author evidence pending independent review, not full GA acceptance.
Not verified: an unmodified worker deliberately stopped inside ftruncate;
causality for historical v7 or every old seed500; other SQLite error causes;
all concurrent deployments/filesystems/kernel versions; non-Linux runtime;
external S3/GitHub, Safari/Firefox, real assistive technology or physical-device
zoom. Full-repository Go tests, production server/release builds and the
18-target build matrix were intentionally not repeated. There is no commit,
push, main-source write or modification to the frozen Sources v2 delivery.

## Subsequent independent limited review

The six-file candidate has since received **Spec ACCEPT / Quality ACCEPT /
Overall ACCEPT**, with zero new blocking findings. The non-author reviewer
verified O_PATH inheritance and close-lock preservation, the ordinary-descriptor
negative control, inode/link replacement refusals, actual worker format/budget
rejections, cleanup precedence, and 18 verification-gate probes. The reviewer
did not repeat or take credit for the main thread's combined suites.

Frozen review: `/tmp/skillhub-backup-independent.3XqsMFhR/delivery/REVIEW.md`,
SHA256 `a66f668b7a4504f915f7b91af9c88539244e689811d44ec7aaf3672749649636`.
Independent evidence SHA256:
`2e7ac2fe8dbfc8fbc756341835ac6bcf3b757c26e0a7f8e75a1064690cdc1a3e`.
The original author evidence and qualifications remain unchanged above.

Main integration at `59da539` plus the Sources/N1 and three UI candidates has
also completed 30 seed rounds across three fresh fixtures, **630 real
TLS/SQLite/S3-like backups**, with zero unexpected RPC failures and no recovery
retry. The actual backup management page completed 88 evidence entries,
56 layout/focus checks and 43 safe screenshots; fixture code 0, signal null.
These receipts are in `/tmp/cognate-integrated-release.JWDjDE0I`. They do not
close full F4/recovery, external-provider or GA acceptance, and do not attribute
historical v7 failures.
