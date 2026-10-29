# C0-K capability map — proposal, no source authority

2026-10-29; fixed source base de2886e. User delegates planning/acceptance to an independent representative. This map needs representative approval before a new spec/plan or Go grant; existing C0-S/B1c grants remain separate.

| Module | Responsibility | Depends on |
|---|---|---|
| client-key-core | Owned existing-key load and explicit default-root initialization; trusted roots, global key lock → existing installation lock, no database creation; persistent possible-use marker | Existing filelock / bounded rooted file primitives |
| client-key-entrypoints | CLI and native service use the shared core before opening state; explicit CLI init and native initialization action; remove zero-subscription creation shortcuts | client-key-core |

Build/review order: core → entrypoints (CLI/native integration may be independently checked in the same module). Future native configuration orchestration consumes these modules; no C1 or other agent implementation is implied.

## Minimal policy proposed for approval

- Only `home/.config/skillhub/client.key` and `home/.local/share/skillhub` are managed. Lower-level APIs accepting explicit roots/raw keys remain caller-managed; no claim to find arbitrary disk copies. Do not reject unrelated server `master.key` beside the client key or scan actual user files. No path rename, rotation or general key registry.
- `LoadExisting(ctx,home)` never creates/replaces a key or opens SQLite. `Initialize(ctx,home)` is explicit and returns no key. Missing key plus existing possible-use marker, any DB/WAL/native vault/backup/unknown state residue, or invalid key is recovery-required, regardless of subscription count. A valid existing key is never replaced; missing marker may be enrolled without parsing/re-writing old state or changing its cryptographic authority. Existing domain authentication remains mandatory.
- Under both locks, first durable-create a new key only after the pristine-state check; then durable-create the fixed bounded versioned possible-use marker **before delivering a loaded key or permitting a product state writer**. This deliberately reverses the preliminary note's marker-first order: failure after durable key but before marker is retryable using the same valid key, rather than leaving a fresh user with a marker and a key that never existed. Partial/invalid key or unknown/corrupt marker is preserved and reported, never silently repaired/deleted. Failed release returns no key; an error does not claim to undo durable writes.
- Marker grants nothing and contains no secret/hash/absolute path: only version and fixed default-root identifier. Its presence blocks regeneration even when the state directory later disappears. It is not a schema migration or proof that an arbitrary supplied key decrypts every domain. Stop old binaries during upgrade; deleting key+marker+state together or malicious same-UID writers is outside historical-proof guarantees.
- Prefer existing `filelock.Acquire` plus strict default-root/no-DB-link validation and `state.ResolveDatabasePath` (no Open) to acquire the **same physical** installation lock without adding an unused public `install.Admission` layer. A real child holding `install.OpenSession` must block this admission and vice versa; if current primitives cannot establish safe correspondence, request a narrower shared seam before implementation rather than copying a new lock protocol.
- All normal CLI/native opens load/enroll the existing key **before** Store/Session/manager Open; no `len(subscriptions)==0` authorization. No key API is called while holding an installation session; locks are released before later Open, and initialization can never rotate that key. Shared package may create only required trusted private directories/lock/marker metadata, not DB, canonical skills or agent settings.
- Native UI gets an explicit asynchronous initialization action on shared service, with no token use. This is an entrypoint task, not permission to redesign native configuration UI. No silent initialization on login, browse, install or theme/locale change.

## Evidence required by the eventual spec

Real TempDir/SQLite/child-process positive first use and preserved legacy key/data; DB never created during core init/load; zero-subscription/native-only/marker-only/unknown residue missing-key refusal; init/init and init/real Session contention/cancellation; secure regular-file identity/permissions/links; durable-boundary failures and honest unknown outcomes; existing CLI/native shortcuts cannot regenerate a key. Compiling scope/shortcut/release negative controls, not timeout/panic/build-error pseudo RED. Go1.27.3, CGO0, synthetic HOME/key only; no Go or tracked spec was written for this map.
