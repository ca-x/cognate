# Client subscription watch: CLI vertical slice

## Scope

Implement the persistent CLI sync entry required by F5/F7. Reuse `Manager.Sync` unchanged, including its deduplication, backup, local-edit and pending-operation gates. This slice does not implement server source refresh, desktop scheduling, background OS services or automatic recovery; those remain release requirements.

## Contract

- Add shared `subscription.Watch(ctx, WatchOptions, cycle, report)`. Run one cycle immediately, then serially wait after completion; never overlap cycles or accumulate timer ticks.
- Interval defaults to five minutes and is bounded to one second–24 hours. Per-attempt timeout defaults to two minutes and is bounded to one second–ten minutes. Invalid options or nil callbacks fail before any side effect.
- A failed cycle retries after `min(30 seconds, interval)`, doubling up to the interval; success resets backoff. Continue until cancellation or report failure. A canceled parent stops the attempt and suppresses success/failure reporting for it. Callbacks must honor their context, as the existing sync kernel does; do not abandon goroutines to pretend cancellation works.
- Reports contain only success/result, consecutive-failure count and next delay, never raw cycle errors or credential-bearing data. Failed attempts report a zero result, not partial counts labeled as completed.
- Report callbacks receive the parent context and must cooperate with cancellation. The CLI checks output capabilities before touching storage: native Linux pipes/terminals use an independently reopened nonblocking descriptor, a five-second write deadline and cancellation wakeup; known memory writers are synchronous; custom writers must implement cooperative `WriteContext`. Unsupported outputs fail closed. Regular files retain their caller offset/ownership and synchronous context checks, without a hard bound on uninterruptible kernel disk I/O. No abandoned writer goroutines or mutation of caller descriptor flags/deadlines.
- `pack watch --interval --attempt-timeout` opens and closes a fresh subscription manager inside each cycle. Close completes before reporting or sleeping, so the installation lock is not held while idle. Ctrl-C returns cleanly; report/output failure stops the command. It does not start an implicit system service or persist a new preference.

## Implementation And Verification

1. Add deterministic `testing/synctest` behavior tests for immediate execution, fixed delay after completion, exponential cap/reset, no overlap, parent cancellation during wait/attempt, attempt deadline, rejected options, report errors and partial-result suppression. First observe red, then implement `internal/subscription/watch.go`.
2. Add the CLI command in a separate file; register it in `internal/cli/pack.go`. Test invalid flags before HOME/filesystem access and real isolated-home Pack watch→install/update→cancel behavior with the existing synthetic server fixture. Check that another installation session can acquire the lock while watch is idle.
3. Update `docs/CLIENT-SUBSCRIPTIONS.md` and README with the foreground invocation, backoff, cancellation and remaining limits. No token arguments or raw errors in recurring diagnostics.
4. Run targeted tests/counts, vet, then clean-export Web build and full Go tests/vet with Go 1.27.3 / CGO0. An independent reviewer verifies the exact changed files and rejection paths before commit/SSH push. CGO0 cannot run the race detector; do not claim it did.

## Write Set

`internal/subscription/watch.go`, `internal/subscription/watch_test.go`, `internal/subscription/watch_integration_test.go`, `internal/cli/pack_watch.go`, `internal/cli/pack_watch_test.go`, `internal/cli/pack_watch_output.go`, `internal/cli/pack_watch_output_linux.go`, `internal/cli/pack_watch_output_other.go`, `internal/cli/pack_watch_output_linux_test.go`, the one command-registration line in `internal/cli/pack.go`, `docs/CLIENT-SUBSCRIPTIONS.md`, README, and this plan. No server/ent/proto/App/MyGo or parallel proof/snapshot files.
