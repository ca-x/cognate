# T3S Legacy Startup Participation

**Status:** Main-thread implementation approval, not product acceptance. Implement privately from
`2efa940f3569015cc5f060627334fb19963a0fdc`; independently review the resulting source before integration.
No branch/worktree, cgo, automatic restore activation or permission relaxation.

**Goal:** Make Linux ordinary and backup-enabled startup hold SH before business/key writes and until
all App dependencies have definitely closed. Preserve fresh-instance program-generated master keys.
Reject recovery-layout traces instead of silently falling back to the legacy database.

**Architecture:** Reuse the accepted runtimegen lease kernel unchanged. Add a narrow live-Runtime
fresh-root proof, a readonly legacy-layout gate and a single partial-App lifecycle owner.
This is legacy-only preparation, not Resolve, offline restore, generation migration or publication.

## Ordering And Ownership

1. Validate pure configuration; existing readonly TLS parsing may remain before root acquisition.
2. Prepare only the existing minimal private data root; acquire Linux SH before key parent/init lock,
   database, cache, backup-control or logging writes. No unlocked Linux fallback.
3. Check the legacy-layout gate before reading/initializing the master key.
4. Strictly read an existing key or use the new typed runtime-bound initialization path.
5. Record each resource in a partial App as soon as acquired. Preserve ordinary-mode SH coexistence
   and existing MaintenanceManaged semantics; do not make all Stores managed.
6. Close stops admission and cancels dependencies before waiting on blocked handlers. Join HTTP,
   handlers and all Serve defers, including final logging; close library/cache/Store/logging before SH.
7. Concurrent/repeated Close returns one result without retrying an uncertain physical Close.
   Owning constructors with unproven cleanup, unjoined work or resource-close uncertainty retain the
   App and Runtime by strong reference, without releasing SH. This is process-lifetime retention,
   not a durable business-failure fence after process exit.

## Narrow Interfaces

- `runtimegen.CheckLegacyLayout(ctx, root)` is readonly and does not prove a lease.
- `(*Runtime).CheckLegacy(ctx, root)` binds the named root and coordinated objects to the live lease.
- `(*Runtime).WithFreshRoot(ctx, root, initialize)` borrows one synchronous scope protected against
  Close. Prove that the complete root entry set consists only of the specific pinned `.runtime.lock`
  and empty `.runtime-fences`; verify ownership, modes, type, inode and fresh directory-read offsets.
  Do not use a name-only hidden-file allowlist, caller boolean or persistent "was empty" claim.
- `state.InitializeMasterKeyWithRuntime(ctx, path, dataDirectory, runtime)` retains the existing
  key-path lock, strict existing-key read and no-overwrite publication. Missing keys require that
  live fresh-root proof. The original public initializer retains its actual zero-entry contract.
- Non-Linux normal legacy startup keeps its existing platform boundary, with the readonly trace gate;
  it does not gain SH/EX runtime qualification or an offline recovery path.

## Reserved Recovery Names

The main thread approves this minimum naming constraint, not a generation-publication format:

- Reject any existing `runtime.current`, `generations` or `restore`, regardless of type/content.
- Reject `runtime.*`, `.runtime.*`, `.runtime-*` except the two exact coordinated objects.
- Reject `restore.*`, `.restore*`, and root-level `layout.intent`, `switch.intent`, `rollback.intent`.
- Never parse partial/unknown records as absence, delete them, or select newest/previous automatically.
- Future R3 writers must first establish an intent in the reserved restore namespace before changing
  legacy DB/archives. New names require updating and independently reviewing this gate first.
- Normal legacy objects are not globally whitelisted. Fresh-key proof is deliberately stricter and
  rejects any business/history/unknown entry, even an empty business directory.

## Exact Implementation Write Set

Modify `internal/server/app.go`, `internal/server/state/masterkey.go`,
`internal/server/state/masterkey_init_test.go`. Add `internal/server/app_runtime.go`,
`internal/server/runtimegen/legacy.go`, `internal/server/runtimegen/legacy_linux.go`,
`internal/server/runtimegen/legacy_other.go`, `internal/server/runtimegen/legacy_linux_test.go`,
`internal/server/app_runtime_linux_test.go`, `internal/servercli/runtime_linux_test.go`.

No changes to the four accepted lease files, backup runtime, workers, CLI production entry, Candidate,
directory security, LazyCat wrapper, schemas or modules. Report a concrete gap before expanding scope.
Main maintains shared evidence documents; the private implementation does not edit them.

## Acceptance And Remaining Gates

- Keep behavior RED for old startup writing under EX, and for SH-only wiring breaking fresh keys.
- Real processes must cover startup-before-writes, live/in-progress App versus EX, two ordinary SH
  Apps, final resource/handler/log barriers, failed-startup/Close uncertainty and GC without release.
- Test exact fresh-root identity, wrong/closed Runtime, EOF directory offsets, foreign/recovery traces,
  existing invalid keys, lost-key refusal, cancellation and concurrent initialization.
- Build a real temporary server for first initialization, restart preserving key bytes/inode, lost key
  refusal and EX-blocked startup. Use synthetic secrets and join every child; no helper SKIP as proof.
- Run bounded CGO0 targeted tests/vet and compile-successful negative controls; freeze patch/source/SHA
  and obtain non-author Spec/Quality review before main integration.
- Stable naming is the main thread's technical ruling along the original stable-root plan, not a user
  quotation. Old nonparticipating supervisors must be disabled. Cross-inode global authority is not
  promised. Worker parent-death/lease handoff, durable business-failure fencing, Resolve, authenticated
  current/intent, migration, revoke/publish/rollback and real restore CLI remain separate R2/R3 gates.
