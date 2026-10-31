# Full Restore Candidate Implementation Plan

> **For agentic workers:** Main executes the Candidate slice; the independent runtimegen lease task has a disjoint write set. Review the frozen candidate before integration. No branch/worktree, cgo, live Store, migration or startup changes.

**Goal:** Materialize an authenticated bundle into one private database/inventory/archive candidate, bind it to real T1 evidence, and admit a scoped callback only after source cleanup and complete file/skill closure verification.

**Architecture:** Reuse T1, readInventory and verifyArchive. A fixed private candidate slot and a durable admission fence prevent concurrent staging or blind reuse after uncertain cleanup. PrepareCandidate owns source-callback sequencing, not the borrowed bundle; Candidate owns only its copied files, private key copy and descriptors. Verification is one-shot and has no activation/rename side effects.

**Tech Stack:** Go 1.27.3, CGO_ENABLED=0, existing backup container/ent SQLite worker/Afero archive inspector, Linux descriptor-relative filesystem primitives. Other platforms fail closed without preventing cross compilation.

**Spec:** docs/REQUIREMENTS.md F4; docs/superpowers/plans/2026-11-01-offline-server-restore.md sections 3, 6 and 7.

## Execution Status

Core7 fix1 is independently accepted after the original staging-panic self-wait P2 was closed.
The original BLOCK and behavior RED remain preserved. Review:
`/tmp/cognate-candidate-fix1-review-f6744b3-zoG2YE/REPORT.md`, SHA256
`29160f0c70199a44f3b7ddb9894345552c0fd29be81c5715eb2079b51e4af602`.
The auxiliary state gate and CopyFile lookup have separate independent acceptance and are now combined
for main-thread validation. This is not a remote Source, activation, startup, CLI or GA acceptance.
Precise final validation and push receipts belong in `docs/VERIFICATION.md`.

## Global Constraints

- Preserve the original database, canonical references, archive hashes, cache-budget bytes, AEAD/AAD and backup wire format. No second persistent import envelope.
- Only callers controlling the supplied private parent may use this API; it is not an RPC path input. They keep borrowed arguments stable during each call.
- The parent namespace must remain stable for the candidate lifetime. Identity checks reject observed replacements; they do not claim global path authority against nonparticipating same-UID/root actors replacing the entire root and lock namespace. The T3 deployment contract similarly requires a stable root and stopped old supervisors, rather than an implicit HOME/global coordination directory.
- T2 is not the live shutdown lock, credential sanitization, generation publishing or restore CLI. Those remain required T3–T5 work.
- No original key, plaintext configuration, raw parser/SQL/filesystem error or private path in diagnostics. Caller key/data are never cleared by this API.

## Review Focus

1. Authenticated metadata must match the real database bytes/hash/schema/key, not merely a self-consistent inventory declaration.
2. Every repository sharing an archive must retain its complete skill-directory paths and content hashes, including hidden/binary/mode semantics.
3. A source callback that reports a late close failure, ignores a stage failure, runs twice or escapes its scope must never yield a verification-ready candidate.
4. All short-lived file/worker/directory readers must close before the verified callback. Close uncertainty leaves a fence and cannot be erased by cancellation.
5. Callback capability expires on return/cancel; Close waits for admitted work. Cleanup never follows links, recursively deletes or removes foreign replacements.

## Interfaces And Ownership

```go
type CandidateSource func(context.Context, func(context.Context, backup.CryptoVerifiedBundle) error) error
func StageCandidate(context.Context, backup.CryptoVerifiedBundle, *os.File, []byte, CandidateLimits) (*Candidate, error)
func PrepareCandidate(context.Context, CandidateSource, *os.File, []byte, CandidateLimits) (*Candidate, error)
func (*Candidate) WithVerified(context.Context, func(context.Context, VerifiedCandidate) error) error
func (*Candidate) Close() error
```

StageCandidate only stages and returns an unready candidate; it never closes the borrowed bundle. PrepareCandidate calls StageCandidate inside exactly one source callback and marks readiness only after that callback and the outer source return successfully. The trusted source implementation must include all its bundle/session/engine/recovery-key/head-scope cleanup in that result. This centralizes sequencing without pretending the API can verify arbitrary Go code that lies about its own cleanup. Source errors and ignored callback failures discard the candidate; late callbacks are rejected without touching it.

WithVerified is one-shot. It re-runs T1, rechecks inventory and canonical expectations, verifies all actual files and every archive, closes all short-lived handles, then lends opaque VerifiedCandidate only during the callback. It exposes safe summary accessors, no public path/FD or mutation API. This version cannot publish; T4 must separately define and review ownership transfer before any runtime generation move. Callback failure does not claim rollback or non-commit.

CandidateLimits contains Database state.LocalSnapshotLimits, MaxArchives, MaxInventoryBytes, MaxContentBytes, MaxStagingBytes, ArchiveCacheBytes and OperationTimeout. No defaults. Inventory <=64MiB, archives <=100000, canonical records <=1000000, content/cache <=1TiB; compressed archives retain the existing 32MiB inspector bound. Staging accounts for database, inventory, archives and small fence/cache-policy records separately from the source engine's budget. Expose LocalSnapshotLimits.Valid as a wrapper around the existing private predicate, rather than copying that policy.

## Filesystem And Lifecycle

- The supplied existing parent must be absolute/canonical, current-UID private directory with pinned FD/path identity. Duplicate its directory capability; never close the borrowed FD.
- Exclusively create `.restore-candidate-fence` (0600, single-link, fixed nonsecret version marker), Sync/Close it, then exclusively create `candidate/` (0700). A preexisting fence/slot is not deleted or adopted. This is local admission control, not a portable backup format.
- Candidate contains only `server.db`, `inventory.jsonl` and `archives/`; archives contains `.cache-budget` plus the hash-derived `<hash>.tar.gz` files. Use the existing `skillhub-archive-budget-v1:<bytes>\n` policy and an explicit cache cap covering the entire archive closure.
- Sequential O_EXCL/NOFOLLOW writes are bounded, synced and closed before readonly re-open. Each regular file is pinned by owner/mode/link count/inode/size/mtime; parent/root/archive directories retain identity. Actual directory entries must equal the expected sets in both directions. No upstream fetch repairs missing files.
- Candidate has its own cancellation context and key copy, independent of the source engine after preparation. Close cancels and joins the single admitted verifier/callback before clearing key and cleaning owned resources. A callback must cooperate and must not call Close synchronously on itself.
- Use instance-local physical operations for faults. Swap owned FD pointers before close, never retry an uncertain Close; retain unresolved handles so GC cannot silently change the outcome.
- Keep the durable fence until all owned files/directories and the duplicate parent have been cleaned/synced/closed successfully. Remove the final fence by its previously pinned named identity only after those handles close. No new FD is opened after that deletion. Its deletion is not a power-loss durable completion certificate: a fence may reappear after a crash and must then block admission pending explicit reconciliation. Resource-close uncertainty leaves the fence; no automatic cleanup/retry is promised. A final fence-unlink failure after all resources already closed is a distinct late cleanup outcome: do not claim a marker still exists if that unlink actually occurred before an error was observed.

## Exact Write Set

- New restore/candidate.go: limits, opaque types, Stage/WithVerified/Close, scoped readiness and lifecycle.
- New restore/candidate_source.go: PrepareCandidate callback sequencing and source-error barrier.
- New restore/candidate_files_linux.go / candidate_files_other.go: descriptor-relative ownership and exact cleanup; unsupported fallback.
- New restore/candidate_inventory.go: project already-proved canonical references into per-archive repository/skill expectations; no independent SQL or new canonical encoder.
- New restore/candidate_linux_test.go: real capture/open fixture, source/mutation/closure/lifecycle tests, per-instance faults.
- state/local_snapshot.go: only exported Valid wrapper, private validation unchanged.
- The missing configuration-order/Pack-attachment isolation gate is implemented separately in state/restore_assets_linux.go and its test, with one call from the existing constrained restore worker. It reuses that same readonly connection/resource cap; stale/unsupported but valid references remain legal. This is the genuinely needed auxiliary check, not a duplicate SQL canonical-reference scanner.
- This plan and, after validation, existing release/TODO/backup verification ledgers. No RPC/proto/generated/App/startup or runtimegen changes.

## Execution Checks

- [x] RED: retain real behavioral rejection controls, including metadata binding, shared-repository closure, source cleanup and the staged panic self-wait/FD failures; do not call compile failure behavior proof.
- [x] GREEN: empty business database and two owners with shared hidden/binary archives, packs/categories/configuration survive source-directory removal; real T1 metadata/closure match and archive cache opens under the declared policy.
- [x] Refuse wrong original key; wrong DB/schema/key metadata, inventory cut/count/owner/hash/closure, missing/extra/swapped/truncated archive or altered last shared repository skill. Preserve borrowed parent/key and zero valid callback count.
- [x] Refuse foreign/nonprivate/symlink/hardlink identities and existing fence/slot; two stage calls cannot both own one parent. Test exact file sets and foreign replacement preservation.
- [x] Source late error/ignored stage error/repeated or late callback never admits WithVerified; direct Stage remains unready even if a caller closes its borrowed bundle.
- [x] Cancel/Close joins worker and callback; retained capability becomes invalid; concurrent Close is idempotent. Physical Sync/Close/unlink fault probes and eight staging Sync panic paths preserve uncertain cleanup fences.
- [ ] Run CGO0 restore package plus affected state/backup regressions and vet; inspect independent review and staged-tree composition. Commit and SSH push only after those gates; read back remote SHA and CI state without claiming queued/running is green.
