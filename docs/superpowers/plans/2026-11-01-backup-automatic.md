# Automatic Backup Production Slice

Status: R1–R5 and sixteen-file implementation scope technically approved by main, not implemented.
Start from committed `6c22784433acbeb1161ada56ceeeb2568595ea2c` in a private archive.
Approved handoff SHA256: `9d4b6aaf6adc81d35e7790ae6a93ef54837ccb23aad9e12df0886b8f5425aa52`.
These are implementation rulings, not quotations of additional user requirements.

## Decisions

1. Default off. Private JSON `automatic_interval_seconds`: absent/0 off, integer60–86400 on;
   reject invalid, duplicate, unknown and overflowing input. Require backup opt-in and a nonempty
   effective administrator set. No additional flag/env, Web toggle, dependency or protobuf.
2. Monotonic fixed delay: wait a complete interval before the first check and after each check,
   no catch-up, jitter0, fixed-interval failure delay, no persisted UTC slot or cross-restart
   exactly-once promise. Explicitly replace the older derived spec's UTC-slot clause when
   implementing; the authoritative F4 automatic/manual requirement remains intact.
3. Use ControlStore.InstanceID as the system actor, not a login account. Existing authenticated
   administrators manage all instance tasks; test a different administrator's cancel/reconcile.
4. Interval is scheduling policy, not content/target identity. Exclude it from generation and
   options fingerprint, preserving original absent/zero HMAC bytes. Target/keys/limits/version
   changes still prevent cross-generation reconciliation.
5. Any retained interrupted/commit_unknown blocks automatic creation until explicit resolution;
   no automatic reconciliation, old PUT replay or new request ID to hide an uncertain attempt.
   Unattempted interrupted work can use existing Cancel; complete-unknown stays unknown until
   precise reconciliation. Keep the 1000-job/16MiB limits and preserve all retained history.
6. Reuse automatic_enabled for configured policy, not a health/success indicator. Existing Web
   must accept true only with enabled=true and display accurate bilingual limitations.
   restore_available remains false. Existing manual confirmation and Unknown semantics remain.

## Lifecycle And Atomicity

- NewController constructs but does not start a timer. App.Serve starts one owned loop with its
  runtime-scoped context; Stop is idempotent and does not restart the same controller implicitly.
- Under the existing controller/store admission mutex, atomically check closing, active work,
  unresolved history and budget, then share the existing startLocked registration path. Do not
  perform a List/Start TOCTOU or create a second capture/upload implementation.
- Close forbids new admission, cancels/joins the timer without holding its required mutex, then
  drains the original controller job before target/engine/key/control resources. Concurrent Serve
  deferred Stop and App.Close share one completion signal. Unknown cleanup retains ownership/SH.
- A registered job outlives its triggering request/scheduler context; only explicit Cancel or
  normal controller shutdown interrupts it. Skip a busy/blocked tick without adding a queue.

## Exact Scope And Verification

Production: backup `controller.go`, new `automatic.go`; server `backup_config.go`,
`backup_runtime.go`, `app.go` Serve hunks only; httpapi `backup.go`; Web
`server-backup-state.ts`, `server-backup.tsx`, `server-backup-i18n.ts`.
Tests: new backup `automatic_test.go`, server `backup_automatic_linux_test.go`; existing server
`backup_config_test.go`, `backup_browser_fixture_linux_test.go`.
Docs: `SERVER-BACKUP.md`, `REQUIREMENTS.md`, `superpowers/specs/2026-10-28-server-backup.md`.
No production changes to app_runtime, ControlStore, old lease kernel, protocol or generated code.

Require behavior RED/GREEN, deterministic timer tests plus a real App.Serve minimum60-second
Capture→TLS S3 publication (not StartBackup RPC), restart/unknown/zero-replay/real capacity bounds,
generation controls, administrator/ordinary/anonymous/share/MCP authorization, Stop/Close/SH
barriers and real current Web mode/details/cancel/reconcile. Use existing mature components and
actual built embed assets; do not substitute empty embed or status mocks for browser verification.
Run directed Go1.27.3/CGO0 tests/vet and Web checks, freeze the complete slice for non-author review.
Retention/orphan cleanup, full restore, external S3 and GA remain separate unfinished obligations.
