# Client Key Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans for this plan, plus ub-mode and use-modern-go. No source grant is implied; main separately assigns the exact paths after independent design acceptance. Do not spawn, stage, commit or push.

**Goal:** Provide one explicit, durable default-root key initialization and existing-key admission API without opening SQLite or risking old encrypted state.

**Architecture:** A Linux-only shared core validates rooted private files and acquires the existing physical installation lock after a global key lock. Under both locks it refuses missing-key residue, preserves valid legacy keys, establishes key durability, then records possible use outside the state root before returning any key. Other platforms are explicit non-mutating unsupported implementations.

**Tech Stack:** Go 1.27.3; existing filelock/state path resolution; real rooted filesystem calls, encoding/json/v2; CGO0; no new dependency.

**Spec:** `docs/superpowers/specs/2026-10-29-client-key-core.md` (approved core module of the C0-K capability map; exact SHA recorded in the grant).

## Global Constraints

- Go 1.27.3, `CGO_ENABLED=0`, modern Go CLI guidance before editing, standard errors/context and `encoding/json/v2` for the new marker; no one-letter names or unsolicited inline comments.
- Only `home/.config/skillhub/client.key` and the default `home/.local/share/skillhub` state root are managed. Keep historical paths for compatibility.
- Marker is `home/.config/skillhub/client-key-use.v1`, outside the state root, at most 256 bytes, with exactly `version: 1` and `root: "default-client-state"`.
- Lock order is fixed: config-root `client-key.init.lock` then default physical `client.db.install.lock`; release in reverse order, then close roots.
- `LoadExisting` never generates, replaces, deletes or chmods a key, and never opens/migrates SQLite. `Initialize` is explicit and idempotent, returns no key, and never changes an existing valid key.
- Only after both key and marker are durable and all close/releases succeed may `LoadExisting` deliver the key or `Initialize` acknowledge. Error returns withhold and clear any tentative key; failure never promises rollback of durable writes.
- No new dependency, ent migration, protocol or install/state implementation change is authorized. Other platforms return unsupported without filesystem changes.

## Review Focus

- An apparently complete key left after a failed sync is not durable until a retry re-syncs key and required parents before delivering it (Task 1).
- Valid old keys must remain usable with real legacy state; registration is not a new authentication proof or permission to rewrite that state (Task 1).
- A removed default state root must not erase the possible-use history kept beside the key (Task 1).
- The initializer and actual installation session must contend on the same physical lock, including a DB created while the initializer waits (Task 2).
- Late resource-release/cancellation failures must withhold tentative bytes, and unsupported platforms must not create even lock directories (Tasks 1–2).

## Exact File Structure And Interfaces

Create only:

1. `internal/clientkey/key_linux.go`: public API, fixed policy/errors, trusted filesystem/lock helpers; Linux filename constraint.
2. `internal/clientkey/key_other.go`: `//go:build !linux`, same exported API and error identities, no filesystem operations.
3. `internal/clientkey/key_linux_test.go`: synthetic-home real filesystem, real SQLite and cooperating OS-process tests.
4. `internal/clientkey/key_other_test.go`: `//go:build !linux`, unsupported/no-mutation tests and ordinary cross-compilation.

Public API:

```go
var ErrInitializationRequired error
var ErrRecoveryRequired error
var ErrUnavailable error
var ErrUnsupported error

func LoadExisting(ctx context.Context, home string) ([]byte, error)
func Initialize(ctx context.Context, home string) error
```

The error values are fixed sentinels, not unwrapped OS errors. `errors.Is` also observes context cancellation/deadline when applicable without exposing any path or value. Do not add an exported Key struct, raw key/root override, force/delete API, callback hook or global mutable filesystem. Consumers own and clear returned slices. If the production file needs a cohesive extra private filesystem path, request that exact file before writing it; do not silently expand the four-file grant.

## Task 1: Explicit Initialization And Existing-Key Admission

**Consumes:** `filelock.Acquire(ctx,path)` and `state.ResolveDatabasePath(path)`, never Store/OpenSession open. `os.Root`/file identity and Sync primitives establish real ownership and durability.

**Produces:** The two exported APIs/errors above, guarded by both physical locks; Linux behavior and unsupported stubs are one reviewable deliverable. Task 2 verifies the cross-process/durable-boundary behavior of the same implementation, not an independent writer protocol.

- [ ] Read the approved map/spec/review; run the complete modern-Go `list --go-version 1.27` before any Go edit. Record baseline commit, exact paths, synthetic HOME/toolchain and unavailable UB tools honestly. Preserve all tooling/setup failures separately from valid behavioral RED.
- [ ] Write `TestClientKeyInitializeAndLoad`: pristine LoadExisting returns nil key/ErrInitializationRequired and creates no DB/key/marker/agent files; explicit Initialize creates a private complete key and valid config-root marker; repeated init/load returns independent equal 32-byte slices without replacing identities. No state entry other than the verified install lock exists. Initial missing-symbol compile errors are setup, not behavioral refusal proof.
- [ ] Write `TestClientKeyLegacyEnrollment`: with an existing valid 32-byte key plus a real zero-subscription SQLite DB and opaque synthetic native state, LoadExisting enrolls the exact old key without opening/modifying DB/native bytes. Preserve sibling `master.key`. Test an existing valid marker and absent marker; registration is not decrypting data. Record file bytes/identities and no unknown new paths.
- [ ] Write `TestClientKeyMissingKeyResidue`: real empty DB, WAL-only, native-only, backup, unknown file/directory, valid marker only, corrupt/unknown marker; both APIs refuse without generating a key and preserve all original bytes. After valid init, remove key and default state root but retain config-root marker: Initialize still returns recovery-required. Do not use subscription queries or pass merely because an error occurred for the wrong reason.
- [ ] Write `TestClientKeyTrustAndBounds`: explicit invalid/relative/unclean home, component symlink, replaced private root, key/marker/DB/lock symlink or hardlink, FIFO, unsafe permissions/owner when a real fixture is possible, overlong/short key and marker duplicate/unknown/trailing/version/root errors. Refusal is bounded, mutation-free for pre-existing unsafe files, no raw path/content/secret in returned errors. Use no unrelated real HOME files.
- [ ] Implement the minimum shared Linux policy from the spec: trusted roots/durable directory creation; global key lock then no-alias DB resolution and physical install lock; validate identities after admission; bounded residue check; explicit O_EXCL full 32-byte write; sync key and all required parent directories; strict bounded marker create/validate and sync; late cleanup and final context check before returning. Reuse the same admission path for both APIs and never create a Store, native vault or target directory.
- [ ] Implement the non-Linux stub and `TestClientKeyUnsupported` requiring ErrUnsupported/nil key with an untouched synthetic home. No platform is advertised as supported by compilation alone.
- [ ] Run focused Task 1 tests to GREEN, inspect genuine run/pass counts, and test separate returned slices by clearing one and loading again. Tests assert the public observed contract, not counts of private helper calls.

## Task 2: Physical-Lock, Crash And Failure Proof

**Files:** The same exact four paths. Additional test-only overlays/scripts and raw receipts live under a unique `/tmp/cognate-client-key.*` evidence root, never in production/package APIs.

- [ ] Write `TestClientKeyProcess` as a child fixture and `TestClientKeyConcurrentInitialization`: two real children coordinate at a barrier, both explicit init succeed or honestly report a real failure, and all successful loads observe exactly one stable complete key/marker. Child failures include bounded fixed diagnostics only; parent always cancels/joins/reaps. An empty parent helper invocation is not counted as behavioral proof.
- [ ] Write `TestClientKeyInstallationLock`: actual `install.OpenSession` in a child first holds the lock; initializer waits then is canceled without key creation, and after that child has created DB/releases, init refuses residue. Reverse direction pauses real initializer while both locks are held; an actual child OpenSession must block/cancel until release. Use a test overlay/event pipe if a precise internal barrier is needed; never sleep to infer that a lock was reached. Check exactly the physical default DB lock, not a separately invented file.
- [ ] Write `TestClientKeyDurableFailures` with isolated test overlays and real invalidated resource/write/sync/close faults at key write/key sync/parent sync, marker write/sync/parent sync, and release. A known-complete key is preserved and reused after retry; a partial key/marker is preserved and refused; no failed call returns a key or success. Close/release failure after a nonzero candidate must produce nil plus error, not zeroing only a local copy. Crash a real subprocess after durable key but before marker, restart and prove same-key enrollment succeeds; crash after marker and then remove key/state, regeneration refuses.
- [ ] For uncertain key/directory durability, retry must actually re-sync the existing key and parents before marker or delivery. Prove this with faults at the retry boundary rather than merely counting Sync calls. Fault probes never rewrite old DB or call B.Open as a pristine check. Record what persisted, what is unknown and what needs explicit recovery, without claiming rollback.
- [ ] Independently compile then run five negative controls: allow residue/new-key generation; let LoadExisting generate; marker-first ordering; bypass installation lock; drop late release failure. Each control must fail its corresponding public/child behavior by bounded assertion, not a compiler error, panic, skip or overall timeout. Preserve RED outputs and restore exact GREEN source. A separate retry-durability control must expose returning a key without re-sync if not already killed by the five controls.
- [ ] Run package focused count1, then shuffle count10, package vet and relevant filelock/install/state regressions. Review JSON events for actual required tests/subtests and no new skip/fail; parent process helper is recorded separately. Cross-compile the package tests for Windows amd64/Darwin arm64 with CGO0, explicitly static only.
- [ ] Freeze the exact four-file candidate onto the assigned baseline with full diff, manifest, tree and reproducible archive; seal raw evidence/commands/exit codes and author report under ignored `.superpowers/sdd/2026-10-28-release/client-key-core-report.md`. Stop all source writes and return to main for independent Spec/Quality review.

## Verification Commands

```sh
export PATH="$(mise where go@1.27.3)/bin:$PATH"
export CGO_ENABLED=0 GOTOOLCHAIN=local
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go test -mod=readonly -count=1 ./internal/clientkey
go test -mod=readonly -shuffle=on -count=10 ./internal/clientkey
go vet -mod=readonly ./internal/clientkey
go test -mod=readonly -count=1 ./internal/filelock ./internal/install ./internal/state
GOOS=windows GOARCH=amd64 go test -mod=readonly -c -o "$EVIDENCE/key-windows.test.exe" ./internal/clientkey
GOOS=darwin GOARCH=arm64 go test -mod=readonly -c -o "$EVIDENCE/key-darwin.test" ./internal/clientkey
```

Main owns integration full tests, exact-path staging, commit/SSH push/readback, CI and durable memory. Core completion alone does not close old CLI/GUI shortcuts: the separate entrypoint module must wire both normal opens before state creation and expose explicit CLI/native initialization. C1 remains existing-key-only until that module is accepted; no silent init, custom root or full-release claim.
