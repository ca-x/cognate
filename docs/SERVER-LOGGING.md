# Server Logging

## Objective And Authority

F13 adds private operational file logs and administrator-only Web configuration. This document specifies the initial implementation; it is not a completion receipt. The user delegates planning and acceptance to an independent representative, so no additional human implementation checkpoint is required. Existing requirements, CGO-free builds and secret-handling rules remain binding.

## Capability Map

1. L1: a process-safe private file logger, rotation, retention, settings persistence and safe health counters.
2. L2: administrator-authenticated Connect settings and application lifecycle/request instrumentation, depending on L1.
3. L3: an accessible, translated Web settings page using the existing React/TanStack/Radix components, depending on L2.

No database migration, backup interface, client logger, browser log viewer, arbitrary log download or third-party plugin logging is included.

## Configuration Contract

- Directory is fixed at `<server-data-dir>/logs`, never accepted from an RPC. Directory mode is 0700 and files are 0600 on systems enforcing POSIX modes; validate ownership, regular files and trusted directory identities using existing private-file primitives.
- Structured newline-delimited JSON, default level `info`. Supported levels: `debug`, `info`, `warn`, `error`.
- Defaults: 10 MiB maximum file size, seven rotated backup files, 14 retention days. Bounds: size 1–100 MiB, backups 1–100, retention 1–365 days. Zero never means unlimited.
- The active file and retained backups together are bounded by `(max_backups + 1) * max_file_mib` apart from fixed bounded settings/lock metadata. A record is either written whole or not accepted; oversized arbitrary input is not a logging API.
- Enforce age/count policy on startup, writes, successful settings updates and at least every minute while the App is open. Also rotate an aged active file, so an idle server does not retain expired records indefinitely. Age uses file age, not last appended request time.
- Settings have a positive monotonic revision; updates require the current revision (CAS). Malformed, out-of-range or stale updates do not mutate files or the active policy. Persist successful changes before acknowledging; report unknown publication failure as unavailable, not success. Read back before retrying an uncertain update.
- Use a private versioned JSON settings file, not SQLite: these are deployment-wide logging controls, unrelated to user data transactions. Shared filesystem locking serializes settings, append, rotation and pruning across cooperating processes. A process must not keep appending to a renamed stale descriptor or overwrite another process's settings.
- Every process reads the latest persisted policy while holding the shared lock for each operation, not merely at startup or on its own updates. Segment creation time is durably encoded and is never reset by append, rotation or restart.
- Under the shared lock, update ordering is: read current policy → validate revision/bounds → enforce proposed smaller retention/budget → atomically publish and sync new settings → acknowledge. Failed cleanup prevents publication. Cleanup may have removed eligible old logs before a later publication failure; that does not imply settings success. A published rename followed by failed sync is unknown and returns unavailable; a later read determines the visible revision, and callers never automatically replay it.
- Reducing a size/count/age limit applies the new bounds before success; do not recursively delete unrelated files. Recognize only exact owned log names and validate file type/identity before mutation. Refuse symlinks or unsafe permission/identity changes rather than following them.
- Short writes fail the record and attempt rollback to the pre-write size. If rollback cannot be completed, never append to the damaged tail; quarantine or reject that segment on subsequent writes and restart. Damaged segments and recognized temporary remnants count toward the same capacity budget. If cleanup cannot recover enough capacity, reject the new record rather than exceed the configured bound.

## Authorization And Protocol

- Deployment supplies repeatable `--admin-user-id` values, matching existing authenticated stable user IDs. Default empty disables settings administration. No first-registration privilege, username/email inference, browser assertion or mutable Web admin enrollment.
- Every settings read/update authenticates the existing Web session, checks explicit administrator membership, and retains the existing same-origin request guard. Anonymous, ordinary owner, Pack share and MCP credentials cannot administer logs.
- A dedicated Buf-managed additive Connect service provides Get and Update. Return only configuration, revision and bounded health counters/status; no absolute path, log contents, exception text or credentials.
- Suggested wire shape: `LogSettings { level, max_file_mib, max_backups, retention_days, revision }`; Get returns settings plus health; Update receives settings and expected revision and returns the persisted settings. Stale revision maps to `aborted`, invalid bounds to `invalid_argument`, non-admin to `permission_denied`, unavailable storage to a fixed `unavailable` error.

## Event And Failure Boundaries

- Log only host-defined event names and allowlisted scalar fields: lifecycle outcome, request route category, numeric HTTP status and duration. Derive severity from status/outcome; debug changes detail/threshold, not access to payloads.
- Do not accept arbitrary messages, user-provided names, raw paths/URLs/query strings, headers, IP addresses, bodies, errors, panic values/stacks, plugin output or configuration values. Do not install a process-global logger or HTTP body dumper. Server HTTP error logging emits only a fixed sanitized transport event, not net/http's original text.
- Request instrumentation must preserve response-writer capabilities (streaming/flushing/Hijacker/Pusher/ResponseController through Unwrap), cancellation, Connect trailers and native RPC transport lifecycle. Do not force buffering, retries or changes to 20-second native budgets.
- Log I/O has bounded lock admission and no unbounded queue/goroutine-per-request. A failed log write increments a safe counter and emits only a rate-limited fixed stderr warning; ordinary request responses still have their original authorization, body and status. Counters are process-local and reset on restart; expose that limitation.
- Close stops and joins the retention loop and drains admitted logger work before releasing filesystem resources. Concurrent/repeated Close and reconfiguration cannot append after successful closure. Disk-full, short-write, rename, sync and cleanup errors must not recurse into logging.
- A failed or unknown physical close keeps the manager in closing state and rejects new work; cache its failure rather than treating a repeated Close as successful recovery. Retain ownership of any resources not known to be closed. Context cancellation bounds cooperative lock admission/loops, not a promise to forcibly interrupt arbitrary kernel filesystem I/O.
- Logging is operational, not a guaranteed transactional audit trail; dropped/failed records are visible through health. No claim of lossless crash delivery or protection against a malicious process with the same OS identity.

## Web Interaction

- Dedicated server-log settings page, session-scoped TanStack state, existing Input/Select/Button, visible labels, range help, in-flight state, refresh and save result feedback.
- Unconfigured/non-admin accounts see a clear restricted-state explanation without configuration disclosure. Deployment instructions explain how to discover one's stable user ID without handling tokens.
- Do not overwrite dirty form fields on background refetch or theme/locale change. On conflict preserve draft and require explicit reload; on uncertain update do not auto-replay. Never persist settings drafts or credentials in browser storage.
- Chinese and English, existing theme tokens, keyboard operation and narrow screens; no new visual framework or decorative animation. Existing component motion/reduced-motion conventions suffice.

## Structure And Verification

L1 lives in `internal/server/logging/`; L2 in `internal/server/httpapi/logging.go`, App/CLI wiring, additive `proto/skillhub/management/v1/` and generated artifacts; L3 in `web/src/server-logging.tsx` with its own i18n resource and existing router/API context integration. Tests stay adjacent to each Go package. New Go uses Go1.27.3 modern guidelines and `encoding/json/v2`.

Use isolated TempDir/HOME, synthetic secrets and loopback servers only. Never open actual user configuration or credentials. Start with behavior-failing tests, then smallest implementation, independent exact-candidate review and exact-path commit/SSH push.

Commands: `CGO_ENABLED=0 go test ./internal/server/logging ./internal/server/httpapi ./internal/server ./internal/servercli`; `CGO_ENABLED=0 go vet ./...`; `npm run build --prefix web`; `buf lint`; `buf generate`; `buf generate --template buf.gen.web.yaml`; `CGO_ENABLED=0 go test ./...`. Build server/CLI/desktop for Linux/amd64, Windows/amd64 and Darwin/arm64 with CGO disabled; cross-build is not runtime support evidence.

Required behavior: initial file creation, all four levels, exact-size rotation and complete JSON lines, backup-count/age/idle cleanup, smaller policy on save, restart/CAS, cooperating process writes/reconfiguration, unsafe file/path rejection, injected storage errors, health/drop warning, close/drain, full admin authorization matrix, malicious synthetic inputs absent from disk/stderr and real request/lifecycle events. Browser acceptance covers translated admin save/reload, restricted state, dirty/conflict handling, keyboard, themes and narrow layout.

## Deployment

Start the server normally, create the intended local or OIDC account and open **Server logs** in the signed-in workspace. Without administrator enrollment the restricted page shows only your stable account UUID. The deployment operator then restarts the server with `--admin-user-id <account-uuid>`; repeat the flag to authorize additional accounts (at most 32). Empty, noncanonical or duplicate IDs are rejected before server data-directory access. Only the deployment operator can change this list; it is not an editable Web setting.

Open **Server logs** again to change level, size, count and age. Save applies the policy to cooperating processes sharing the data directory. An unavailable/timed-out save may have committed; use the explicit reload control before trying another write. Reload discards the draft only when a fresh read succeeds. Health counters describe only the responding process; refresh explicitly for a current snapshot.

Keep the server data directory on a persistent private volume when using Docker; its `logs` subdirectory contains settings and JSONL segments. Logs are never served by the Web UI. Check the container/service's stderr for fixed degradation notices if the filesystem becomes unwritable. Do not mount log files publicly or configure a proxy/body dumper to bypass the confidentiality policy. Removing an administrator ID from the startup configuration and restarting revokes that account's management access without changing its ordinary account data.
