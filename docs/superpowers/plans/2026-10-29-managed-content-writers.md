# Managed Content Writers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox syntax. Implementation starts only after complete B1a acceptance and its mainline commit.

**Goal:** Keep one admitted operation across archive publication and its database commit, without holding maintenance leases during upstream HTTP.

**Architecture:** Managed Store and cache each cold-join the same physical data-directory gate and retain independent lifecycle references. The library takes an outer Store writer scope only after fetching/scanning, then performs cache Put and the final database operation under its returned context. Direct managed cache writers and startup also take independent leases; default unmanaged behavior stays unchanged.

**Tech Stack:** Go1.27.3, CGO_ENABLED=0, existing ent/entsqlite, Afero rooted filesystem and B1 maintenance primitives; no new dependency/schema/RPC.

**Spec:** `docs/superpowers/specs/2026-10-28-server-backup.md` §3.1; current `library/{service,refresh,automatic_refresh,cache}.go`. This is B1b, not an App switch, snapshot/export, restore, S3 or backup-availability claim.

**Execution checkpoint (2026-10-29):** complete B1a is independently accepted and committed at `0c542727`. Only Task1's five source paths are currently assigned; main owns this plan/spec. Task2's three library commit paths remain reserved for the complete private-source integration first, then receive the cross-resource writer scope. Parallel tasks must not overwrite these paths or claim the full barrier before Task2 is accepted.

## Global Constraints

- Complete B1a, including native logical retry/callback leases, is a prerequisite. Never implement against moving B1a files or silently broaden its scope.
- Cold explicit managed mode only; no arbitrary Instance injection, hot attach, fallback, or implicit default single-process restriction. Managed non-Unix remains unsupported; existing unmanaged behavior is preserved.
- Lease order is admission → cache lock/token gate → SQLite. HTTP/KDF/moejs preparation stays outside. Preserve SourceGuard, MCP guards, owner/CAS, final authorization and unknown-commit handling.
- Store/cache run the shared `maintenance.CheckPlatform(ctx)` after basic argument validation and before filesystem access, then join their validated canonical parent with inode recheck before creating cache directories, policy or stages. Close rejects new local writers, drains independently counted children, closes the filesystem root, then releases the cache's instance reference only after known successful physical closure. Failed or unknown root closure keeps closing state and the instance reference; cache the error and require process-exit recovery rather than pretending a second Close is a successful retry.
- No source fetch, database, AI configuration or credential from a real HOME. Go1.27.3/CGO0 only, no race/CGO1. Hand edits use apply_patch; no branch/stage/commit/push by workers.

## Review Focus

1. Independent `RefreshAutomatic` must receive the same cross-resource scope as Collect and owner Refresh.
2. A cache object published before a rejected/unknown database commit is an orphan within the existing budget, never a retry authorization or a successful collection.
3. Mixed managed/unmanaged components and two different physical data roots must fail construction, not silently provide a partial barrier.
4. Network stalls must not prevent quiescence; a request whose HTTP finishes during quiescence must fail before publishing any cache stage or database update.
5. Closing one component must not unlock another; callbacks/nested writes inherit live scoped context and preserve cancellation/Close drain.

### Task 1: Store Writer Scope And Cache Lifecycle

**Files:** Create `internal/server/state/maintenance_scope.go`, `maintenance_scope_test.go`; modify `internal/server/library/cache.go`; create `cache_maintenance.go`, `cache_maintenance_test.go` there. Do not modify the accepted B1a implementation files.

**Interfaces:** Add `(*state.Store).MaintenanceManaged() bool` and Go1.27 generic method `(*state.Store).WithMaintenanceWriter[Value any](context.Context, func(context.Context) (Value, error)) (Value, error)`, implemented through existing private `enterWriter`. Add zero-default `CacheOptions.MaintenanceManaged bool`; cache mode/lifecycle helpers remain package-private. A scope never exposes an Instance or grants backup-exclusive authority.

- [ ] Write `TestManagedWriterScope`, `TestManagedCacheStartupAndClose`, `TestManagedCacheDirectPut` using real SQLite, real file locks and `maintenance.Join/Quiesce` on isolated private directories. Assert nested context works while quiescence waits, stale/foreign context fails, startup and Put reject before filesystem mutations, and Close drains before releasing its root/reference. Inject a real root-close failure and prove that writers stay rejected and a cooperating subprocess cannot acquire the instance lock while the failed object still owns it. Verify the unsupported-platform check precedes filesystem probes. First establish compiling fixtures, then observe behavioral RED, not a missing-symbol failure.
- [ ] Implement only the two Store methods, explicit managed cache startup/lifecycle and outer Put admission. Existing Put validation, budget, immutability, cleanup, Afero middleware and filesystem verification stay intact; cleanup remains inside the lease.
- [ ] Run `CGO_ENABLED=0 go test -mod=readonly -count=1 -run 'TestManaged(WriterScope|Cache)' ./internal/server/state ./internal/server/library`; expected PASS. Run matching vet; expected exit0. A dropped startup/Put gate or early instance release must produce a real assertion failure.

### Task 2: Three Library Commit Paths

**Files:** Modify `internal/server/library/service.go`, `refresh.go`, `automatic_refresh.go`; create `maintenance_integration_test.go` there. Add only a scoped progress update to backup spec §3.1 and this plan. Total authorized write set: eleven paths including the five from Task1, these four library paths and two documents; implementation owner must report the exact counted manifest before touching any additional path.

**Interfaces:** Consume Task1's typed Store scope and cache-private mode/root checks. `NewService` keeps its existing signature; require matching modes and validate physical peer identity by nesting cache admission inside a Store scope, without adding another directory-equality API or trusting path strings. Existing default constructors remain unmanaged.

- [ ] Write `TestManagedLibraryContentCommitScope`: real Collect, owner Refresh and RefreshAutomatic, including plugin/selected-Lists callers through their existing library routes. Pause after cache publication and before DB commit; quiescence cannot become ready until the logical operation returns, and the resulting repository/Pack/archive closure must agree. Inject DB rejection/real unknown commit and preserve existing error/authorization behavior without replay.
- [ ] Write `TestManagedLibraryNetworkOutsideScope`, `TestManagedLibraryPeerMismatch`, `TestManagedLibraryComponentLifetime`: stalled real loopback Fetch allows quiescence; releasing the response while exclusive fails before cache/DB writes; mixed modes/different roots reject; two Stores/cache retain independent locks through nested children and Close. Unmanaged multiple Stores/processes retain the existing behavior.
- [ ] Wrap only cache Put → database collect/refresh/apply in `WithMaintenanceWriter`, propagating its callback context. Fetch/scan, prechecks and ordinary verification reads remain outside. Pairing checks perform no schema/policy mutation; failures leave cleanup with existing owners.
- [ ] Run `CGO_ENABLED=0 go test -mod=readonly -count=10 -run TestManaged ./internal/server/state ./internal/server/library`, then maintenance/state/library/scheduler/sourcerefresh/mcpserver package tests and vet; expected PASS. Remove each of the three outer commit scopes in separate temporary overlays; all must behavior-fail, with no compile/panic/whole-test-timeout pseudo RED.
- [ ] Freeze precise base/eleven-path manifest/tree and at most one-page handoff with actual logs. Main performs clean full tests/build integration and independent acceptance, then exact-path commit/SSH push. No backup endpoint or automatic cleanup is enabled by this task.

## Preflight And Rulings

Task2 consumes exactly Task1's mode/scope/cache gate interfaces. No App/proto/schema/module/native files overlap; B1a's native logical-lease repair must be committed before Task1 compiles. Eleven-path count is a cap to be reconciled before implementation, not permission to omit tests or a necessary safety boundary. User-authorized representative review replaces an additional human implementation checkpoint; no §3 technology decision changes.
