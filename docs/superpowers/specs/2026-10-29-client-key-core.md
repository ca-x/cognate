# C0-K Client Key Core

## Objective And Module

Implement `client-key-core` from the representative-approved C0-K capability map, before `client-key-entrypoints`. CLI and native configuration/subscription flows need one default-root key admission rule: first-time initialization must be explicit, while missing keys for possibly used state must never be silently regenerated. The user delegates design and acceptance checkpoints to an independent representative. This specification does not itself authorize Go changes.

The approved map is `docs/superpowers/specs/2026-10-29-client-key-capability-map.md` SHA256 `a54a9543e920bce1da15627a0635eb2994cf4cad7e87760fdae86f2e426881fd` (byte-identical to the original ignored map); approval is recorded in `current-selection-final-review.md`. This replaces the preliminary note's marker-first/legacy-data refusal proposals, not existing ciphertext formats or domain authentication.

## Contract

New package `internal/clientkey` exposes:

```go
func LoadExisting(ctx context.Context, home string) ([]byte, error)
func Initialize(ctx context.Context, home string) error
```

- `home` is an explicit clean absolute trusted home, never ambient process HOME; no custom root or key argument. Only `home/.config/skillhub/client.key` and the default `home/.local/share/skillhub` state root are managed. Keep historical paths for compatibility. Existing raw-key/custom-root lower-level APIs remain caller-managed; arbitrary disk copies are not discoverable.
- Linux is the first runtime implementation. Other platforms return a fixed unsupported error without filesystem changes; CGO0 cross-compilation remains required. This is not completion of cross-platform install or recovery.
- `LoadExisting` never generates, replaces, deletes or chmods a key, and never opens/migrates SQLite. It may durably enroll a valid existing key using the marker below. `Initialize` is explicit and idempotent, returns no key, and never changes an existing valid key. The later entrypoint module must call `LoadExisting` before Store/Session/manager Open, remove zero-subscription shortcuts, and expose explicit CLI and native initialization actions; no ordinary action implicitly calls `Initialize`.
- A successful loaded key is an owned fresh 32-byte slice. Caller clears it after use. On any error, including late cancellation, root/file close or either lock release failure, clear any tentative key and return nil. `Initialize` also propagates release/close/cancellation failures; failure never promises rollback of durable writes. Fixed errors classify initialization-required, recovery-required, unsafe/unavailable and unsupported without embedding path names, file content, key bytes, raw OS errors or sensitive diagnostics.

## Files, Trust And Locking

- Only required private directories and fixed metadata may be created. Never create `client.db`, its schema/WAL/SHM, a native vault, canonical skills, an agent directory or target settings. Root validation rejects symlinks, unsafe owners/permissions or component replacement; intermediates must be trusted and final config/state roots must be owned and private (0700 on creation). Root-owned sticky ancestors such as the test fixture's `/tmp` are allowed under the existing trusted-path policy; no symlink normalization silently changes the managed root.
- Validate key, marker and both locks as regular, owned, private, singly linked files before/after opening and at locked use. Use rooted no-follow/nonblocking opens, bounded reads and same-identity checks. Existing unsafe files are preserved and refused, not repaired. No raw filesystem path supplied by RPC or plugin enters this API.
- Lock order is fixed: config-root `client-key.init.lock` then default physical `client.db.install.lock`; release in reverse order, then close roots. Reuse `internal/filelock.Acquire` and `state.ResolveDatabasePath` without calling OpenSession or state.Open. Validate that the database path has no symlink/hardlink/unsafe alias before resolving, and that resulting root/lock identities still match after acquisition. The physical lock must be the same one used by `install.OpenSession`; a copied independent lock protocol is forbidden. If this cannot be established with existing primitives, stop for a narrow shared-seam design amendment.
- Waits honor caller context. No goroutine outlives a returned operation, no network or UI confirmation occurs under these locks, and no caller may invoke either API while holding an installation session. Normal later state open happens after key admission releases locks; initialization never rotates keys, so that gap cannot silently substitute a new key.
- Safe failures may leave only required directories/lock metadata or a partial newly created key/marker. Preserve these for explicit recovery; do not pretend an interrupted write never happened. Rooted handle and resource close errors must remain observable through fixed errors.

## Initialization, Enrollment And Durability

Marker is a private bounded strict JSON file `home/.config/skillhub/client-key-use.v1`, outside the state root, at most 256 bytes, with exactly `version: 1` and `root: "default-client-state"`. Reject duplicates, unknown fields, trailing input and any other value. It has no key/hash, absolute path, user id or authorization. Presence records possible use and blocks regeneration, even if state is subsequently removed. It does not prove that a supplied key decrypts every domain and does not replace existing AEAD/key-id/CAS checks.

Under both locks:

1. Inspect and authenticate file identities/shape. A malformed/partial key, unknown/corrupt marker, unsafe metadata or unreadable state is refused and preserved. Missing key plus any marker is recovery-required. Key absence with any state residue is also recovery-required, even a zero-subscription database or an empty native directory; never query the DB to infer pristineness.
2. For a missing key, inspect the default root with bounded directory reads. The sole admissible existing entry is the verified `client.db.install.lock`. Any other entry, including DB/WAL/SHM, backup/native/temporary directory or unknown filename, forbids generation. No recursive scan or unbounded allocation is needed: after accounting for the single lock, one other entry proves residue. Do not reject unrelated server `master.key` or other unrelated siblings in the config root.
3. If missing and verified pristine, `LoadExisting` returns initialization-required without key/marker creation. Only `Initialize` may create a random 32-byte key with O_EXCL/0600, complete-write checks, file sync and required parent-directory sync. A race finding an unexpected existing key is revalidated under the locks, never overwritten. Partial files remain recovery-required.
4. For a complete valid key, preserve the identical bytes regardless of legacy data or marker absence. Establish its durability again before use: sync the existing key and required trusted parent directories, including newly created directory ancestors. This covers retries after uncertain earlier sync and avoids treating visible bytes as proven durable. Do not parse/rewrite existing DB/native records to enroll the key.
5. Only after key durability is established, durably create the fixed marker with O_EXCL, full-write checking, file sync and directory sync, or authenticate and re-sync an existing valid marker. Only after both are durable and all close/releases succeed may `LoadExisting` deliver the key or `Initialize` acknowledge. A complete key left by a failed marker write/sync is reused on a later explicit retry, never regenerated. Partial/corrupt marker is preserved and requires recovery; no delete/rewrite shortcut.

Key-first ordering deliberately allows retry with the same complete key after a pre-marker crash. Marker-first would strand a new installation with a marker but no key ever created. Uncertain persistence returns an error; reread/revalidate/re-sync on retry, never report false rollback or success. Absence after someone deletes key, marker and every state artifact cannot prove historical non-use. Malicious same-UID mutation, arbitrary copied roots and old binaries bypassing this contract are outside the guarantee; stop old writers during upgrade.

## Structure, Style And Boundaries

Candidate source paths are `internal/clientkey/key_linux.go`, `key_other.go`, `key_linux_test.go`, `key_other_test.go`. Tests use real files/SQLite/child processes in synthetic TempDir homes, not actual HOME. No new dependency, ent migration, protocol or install/state implementation change is authorized by this specification. If a cohesive implementation needs a separate private filesystem file, request the exact additional path before writing it.

Go 1.27.3, `CGO_ENABLED=0`, modern Go CLI guidance before editing, standard errors/context and `encoding/json/v2` for the new marker; no one-letter names or unsolicited inline comments. Keep business policy in the shared core, not two CLI/GUI copies. Use Afero only where its filesystem semantics fit; this security boundary must retain actual rooted-file identity/fsync/no-follow and shared OS locking rather than replacing them with a permissive memory filesystem.

Always preserve existing bytes and domain authority; ask before adding dependencies, shared seams, extra paths or changing the contract; never read actual user credentials, rotate/force/delete a key, log raw errors, invoke moejs on clients or use subscription counts as permission.

## Acceptance And Commands

Use the installed toolchain, not a downloaded replacement:

```sh
export PATH="$(mise where go@1.27.3)/bin:$PATH"
export CGO_ENABLED=0 GOTOOLCHAIN=local
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go test -mod=readonly -count=1 ./internal/clientkey
go test -mod=readonly -shuffle=on -count=10 ./internal/clientkey
go vet -mod=readonly ./internal/clientkey
```

The plan must pin actual test names and real behavioral controls for:

1. Pristine load refuses without DB/key/marker; explicit initialize then load works, repeated initialize returns the same bytes; legacy valid key with nonempty DB/native state enrolls unchanged, and sibling server key remains untouched. No core call creates DB/agent/skill files.
2. Missing key with zero-subscription DB, WAL-only, native-only, backup, marker-only or unknown residue is recovery-required with original bytes unchanged. Initialize, then remove the key and default state root while preserving the config-root marker: reinitialization must still refuse. Partial/invalid key or marker, symlink/hardlink/FIFO, unsafe mode/owner and replaced roots fail safely; no raw values enter errors.
3. Real child-process init/init contention yields one stable key; both directions of init versus actual OpenSession contention/cancellation prove the same physical lock. A real writer's residual DB blocks a later missing-key initialization. Use event barriers, not sleep to infer admission; join/reap children.
4. Real write/sync/parent-sync/close/release faults at durable boundaries and restart/retry preserve keys, refuse partial state, withhold tentative keys and never acknowledge unknown durability. Test-only overlays may inject actual resource faults; no unused production fault API. Retain both invalid setup attempts and valid behavioral RED/GREEN.
5. Independently compiling mutations of residual-state refusal, explicit-only generation, marker/key durability ordering, installation lock and release-error handling must cause bounded assertion RED, not build error/panic/whole-test timeout; restore exact source GREEN. Other-platform tests explicitly require unsupported with no filesystem mutation, not Linux runtime claims.

Independent Spec and Quality acceptance is required before commit. Core acceptance is not entrypoint coverage, a CLI/MyGo configuration flow, arbitrary-key recovery, full F12/14-agent support, backup completion or a release.
