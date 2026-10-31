# Server Logging

首次安装可使用 [首次管理员初始化](BOOTSTRAP-ADMIN.md) 创建本地管理员并接入此处的 UUID allowlist。已有实例继续显式授权；初始化不覆盖密码，也不提升已有普通账号。

## Objective And Authority

F13 provides private operational file logs and administrator-only Web configuration. L1–L3 have been implemented, independently accepted and delivered; the named Delivery Checkpoint below records the exact identity and evidence. This document preserves the implementation contract and is not a whole-release completion receipt. The user delegates planning and acceptance to an independent representative, so no additional human implementation checkpoint is required. Existing requirements, CGO-free builds and secret-handling rules remain binding.

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
- Warning emission itself is nonblocking best effort: a full pipe, closed descriptor or unsupported sink must not trap business work, retention or Close. Drop the diagnostic when it cannot be attempted safely; counters and fixed storage-unavailable responses remain observable. Do not mutate caller-owned descriptor flags/deadlines, assume a duplicated descriptor has independent flags, or abandon a goroutine blocked on the sink. An available supported sink must still receive the fixed privacy-safe notice.
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

## Delivery Checkpoint (2026-10-30)

**F13 is delivered, not spec-only.** The delivery commit is `52a235c9d3a4293040a396a4dbd1ef81aeb4a84c`, tree `feba337c15cf34154b2c99f3554288dae3bb0284`. It contains the accepted private file manager, administrator-only Connect settings, App/request instrumentation, translated Web editor and deployment flag. The historical delivery ledger records SSH push/readback and successful Build `40815459306`; the status audit did not re-query remote Git or CI.

At the frozen, pre-edit observation base `66d27e7f220bd2beb68f16a959f8148a64967583` (tree `be4d6550d1a6d555c3823327c94bb8b8fb6dbf92`), **all 32 original acceptance-inventory paths match the delivery commit byte-for-byte and mode-for-mode**. This checkpoint update changes only this document and the logging implementation plan; it does not change the implementation or transfer unrelated work into F13.

Named local acceptance receipts under `.superpowers/sdd/2026-10-29-server-logging/` identify the historical evidence; these are review artifacts, not runtime files:

| Receipt | Accepted scope and SHA256 |
|---|---|
| `task-1-fix1-review.md` | L1 Spec/Quality PASS; `LOG-L1-01` closed. `ff9a757a4ff2f3491c6cffb79536e6d9edcb4c7af4ba249c1e9b46dc3a917e03` |
| `task-2-3-final-review.md` | L2/L3 main19 conditional Spec/Quality PASS, including the reviewer's rebuilt server and real browser checks. `c87b4f8dfe7bd0f2bac480eddd4b5f818dd49ada639a6cfdde985a5bd1d101e8` |
| `combined-final-review.md` | Exact `feba337c15cf34154b2c99f3554288dae3bb0284` combination Spec/Quality PASS, zero open P1/P2 in this slice; discharges the L1 dependency condition. `d33b3012e17527c5787599f7544298c347b6a3ba3f4f712f3f231afc932707ca` |

The historical independent browser evidence covers administrator save/reload and restart persistence, stale-CAS draft preservation, uncertain-save/no-replay recovery, ordinary-account restriction, Chinese/English and theme changes, keyboard operation, narrow layout and safe browser storage. The combination review additionally verifies actual full-stderr failure/shutdown and actual-logger native EOF, streaming, drain and the unshortened 20-second budget. Original D98 rejection/RED and old a445 conditional acceptance remain historical failures/conditions, not retroactive successes.

The separate 2026-10-30 status audit at the observation base freshly ran Go1.27.3/CGO0 with synthetic HOME/SQLite/keys: the logging package count3, logging-focused HTTP/App/server-CLI/embed tests, and vet for those five packages all exited0. The focused integration run executed 14 top-level tests with no skips; the logging package's subprocess-helper entry skip is not counted as a behavioral test, while its real cooperating-process parent tests passed. Audit report SHA256: `f49b6eed546090d379e5cbe269751fdfef0e8aea70899c0e8b5da345edab6287` (`f13-status-audit` report, `/tmp/cognate-f13-status-audit.IYBP8Qvo/REPORT.md`).

This documentation update does not rerun that audit or the historical browser, full-repository, generation, cross-build or native-budget gates. It does not establish non-Linux runtime/installation, lossless audit delivery, new F12/14-agent capabilities or whole-release completion. The operational and platform boundaries above remain binding; no new logging source implementation is pending solely because this contract was originally written as a specification.
