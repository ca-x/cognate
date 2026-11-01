# Runtime Worker Lease

Status: independently accepted Linux synchronous snapshot-worker slice; main integration verified.
Historical implementation base: `6c22784433acbeb1161ada56ceeeb2568595ea2c`; do not copy an older
App over the accepted T3S lifecycle. No branch/worktree, cgo or kernel rewrite.
Approved handoff SHA256: `f6a264617c1e9204ad2bb098a4dce3fc9eadbf5c827dc6daabbb7571f4297ceb`.

## Accepted Integration

Non-author Spec/Quality scoped ACCEPT covers the original thirteen files plus one test-only
integration correction in `cmd/cognate-server/snapshot_worker_integration_test.go`: migrate its
hidden mode/magic/version to v2 and explicitly reject the old frame/mode. Original production and
kernel bytes remain unchanged. Exact14 patch SHA256:
`6b6032e2d6bed3066bfabdbfd26ea128f7e2e7ec05b52983255d684aab43caf2`.
Review report SHA256 `a181570cccbd97d10702bb0f93e4e6f8b841c8e4314cb4686369ff7dac160103`.
The clean current-source composition passes whole-repository Go tests, vet and eighteen CGO0 builds;
the first stale-v1 fixture failure and one independent 150ms positive-EX probe timeout remain recorded
in `VERIFICATION.md`. This does not accept restore-inspector EX participation or complete R2/R3.

## Contract

- Existing Pdeathsig/Kill/Wait does not by itself keep the runtime root locked until a surviving
  worker actually exits. Inherit the same flock open-file description, never reopen by pathname.
- Add synchronous Runtime/OfflineRoot `WithWorkerFiles` scopes, validating live ownership,
  root/lock/fence identity and cancellation. Scope covers process Start, cancellation callback
  and actual Wait. Never close borrowed descriptors, issue LOCK_UN or escape unjoined work.
- Store borrows a typed Runtime from App; bind the legacy DB parent before writes. Bare internal
  Stores remain compatible but do not qualify production runtime exclusion.
- Preserve ExtraFiles 3–9; append lock FD10 and stable-root FD11. Internal worker frame becomes
  v2 without increasing its 448-byte length: bit0 at byte11 signals the lease, bytes432–447 bind
  root dev/ino. Reject old/unknown/partial frames and wrong, swapped or missing descriptors before
  SQLite/staging access. Keep inherited descriptors alive until process exit.
- Preserve T3S partial ownership, cancellation and teardown. Unknown Wait/pipe Close must travel
  through the actual state/library/App error chain and retain SH, not merely rely on a callback
  promise. Avoid holding an unrelated mutex while joining a process or callback that needs it.
- Keep normal SQLite/WAL/journal crash recovery and two ordinary SH Apps. Do not add a global
  startup dirty marker or treat every historical Close error as permanent corruption.
- Non-SQLite layout/current/rollback changes need their own durable authenticated operation intent
  before mutation. That belongs to complete recovery, not a generic after-error poison mechanism.

## Exact Source Scope

Add runtimegen `worker_linux.go`, `worker_other.go`, `worker_linux_test.go`.
Modify App `app.go` only at Store option injection; automatic backup separately owns Serve hunks.
Modify state `store.go`, `local_snapshot.go`, `local_snapshot_worker.go`,
`local_snapshot_worker_linux.go`, `local_snapshot_worker_other.go`, `local_snapshot_linux_test.go`.
Add state `local_snapshot_runtime_linux_test.go`.
Modify server `app_runtime_linux_test.go` and servercli `runtime_linux_test.go`.
Thirteen source/test files total; original four lease files and `app_runtime.go` remain unchanged.

## Acceptance

- Real old-path RED/new-path GREEN: extend the parent-death scheduling window with test-only
  Pdeathsig=0, kill parent, prove EX remains denied while the inherited worker can execute, then
  succeeds after real exit/reap. Record default-Pdeathsig behavior separately.
- Response/EOF is not process termination. Exercise response-before-exit, cancel, unknown Wait,
  descriptor close failures, concurrent App.Close, repeated Close and GC with real EX probes.
- Reject forged/missing descriptor identity and frames; verify actual exec descriptors, not only
  the parent's ExtraFiles slice. No production test flag or global mutable test hook.
- Preserve dual SH, backup maintenance and real committed-WAL restart/key identity; existing
  fences, restore intents and invalid inputs still refuse startup.
- Go1.27.3/CGO0 directed tests, relevant regressions and vet; independent source/behavior review
  before integration. This plan is not R2, complete recovery, another OS or GA acceptance.
