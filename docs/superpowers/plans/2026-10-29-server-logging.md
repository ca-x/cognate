# Server File Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Follow the user's UB-mode test-first rhythm; only main stages, commits and SSH-pushes accepted exact candidates.

**Goal:** Deliver private rotating file logs with bounded automatic retention and administrator Web settings.

**Architecture:** One logging manager owns local lifecycle and uses the existing cross-process file lock for short shared filesystem operations. Fixed typed events prevent arbitrary secrets from entering a general formatting API. Connect exposes only validated deployment-wide settings and safe process-local health, then the existing Web framework supplies the editor.

**Tech Stack:** Go1.27.3/CGO_ENABLED=0, encoding/json/v2, ent/entsqlite unchanged, Afero, existing filelock, Echo, Connect/Buf, React19/TanStack/Radix/i18next. No process-global logger, new database table or client/plugin execution.

**Spec:** `docs/SERVER-LOGGING.md`, `docs/REQUIREMENTS.md` F13, `docs/SECURITY-LOGGING.md`.

## Global Constraints

- Private fixed data-directory path, explicit stable-user-ID administrator allowlist, no auto-enrollment or MCP management.
- Levels debug/info/warn/error; defaults 10 MiB/seven backups/14 days; ranges 1–100 MiB/1–100 backups/1–365 days, no unlimited zero.
- Revision CAS, cross-process append/rotate/settings serialization, no stale open descriptor, total budget and idle-age cleanup.
- Only fixed event and route enums, bounded numeric outcome/status/duration fields; no arbitrary string/error/request/configuration log surface.
- Existing native transport timing, slots, EOF cleanup, ResponseController and streaming semantics must remain intact.
- Go hand edits use apply_patch; modern Go CLI before Go changes. Tests use TempDir/HOME, synthetic secrets and loopback only; never actual user AI configurations, DB or credentials.
- S1 fourteen-path private-source repair, native client A/B and B1b future library work are independently owned and excluded. No whole-worktree tests/staging as candidate evidence.

## Review Focus

1. Ordinary signed-in users must not become server administrators; test anonymous, non-admin, Web admin, revoked session, MCP and share credentials.
2. Two cooperating processes must see CAS/settings changes and retain complete log records while rotating; test genuine subprocesses, not just goroutines.
3. Idle logs must expire by creation age rather than append mtime; test an old still-active file and a lower policy that requires immediate pruning.
4. Disk-full, short-write, rename/sync/cleanup failure must be observable without changing the business response or leaking an underlying error.
5. Logging outside the native handler must not recreate canceled keepalive contexts, lose flush/deadline capabilities or weaken body/slot cleanup; test healthy repeated TLS RPC and malicious synthetic data.

### Task 1: L1 Private File Manager

**Files:** Create only `internal/server/logging/{manager,settings,files,events}.go` and adjacent `_test.go` files, plus approved private `files_unix.go` and `files_other.go` helpers (ten paths). They validate regular-file ownership/link-count/mode without extending state or shared RootedFS; non-Unix preserves the existing unsupported server capability. Do not edit public RootedFS, state/ent, library, app, HTTP or Web.

**Interfaces:** Export `Settings` with Level string, MaxFileMiB/MaxBackups/RetentionDays int and Revision int64; `Health` with monotonic process-local write/cleanup/drop counters and fixed degraded indication; `Snapshot{Settings,Health}`. Export `Open(ctx context.Context, dataDirectory string) (*Manager,error)`, `(*Manager).Snapshot(ctx context.Context) (Snapshot,error)`, `Update(ctx context.Context, expectedRevision int64, proposed Settings) (Snapshot,error)`, `Record(Event) error`, `HTTPErrorLog() *log.Logger`, `Close() error`. Define sentinel errors for invalid input, conflict, unavailable and closed; public errors have fixed messages. `Event` accepts only declared event/route enum values, HTTP status and duration; enum validation rejects unknown underlying values. Communicate final exact types before L2 starts.

- [ ] Write compiling behavior tests first, establish initial RED for defaults/persistence/rotation/age/count/CAS/concurrent lifecycle and privacy. Missing symbols alone are setup failure, not behavior RED.
- [ ] Implement the smallest manager and a local restricted Afero adapter if needed; keep existing shared filesystem abstractions unchanged. Use constant bounded lock admission, one retention loop, no unbounded queue or per-record goroutine. Prove restart and cooperating-process rotation/reconfiguration, unsafe symlink/permission/identity rejection and injected I/O failure behavior.
- [ ] Run `CGO_ENABLED=0 go test -mod=readonly -count=5 ./internal/server/logging` and matching vet. Separate temporary behavioral controls must fail on bypassed CAS, retention and failure accounting. Report exact commands/counts and actual exit codes, not zero-match green.
- [ ] Freeze exact source manifest/base/tree and implementation report; independent Spec and Quality acceptance precedes delivery.

### Task 2: L2 Connect And Runtime Integration

**Files:** Create `proto/skillhub/management/v1/logging.proto` and its generated Go/Connect/Web artifacts. Create `internal/server/httpapi/logging.go`, `logging_test.go`, optional separate instrumentation file/test; modify `httpapi/auth.go` only for Options/registration/wrapping. Modify `internal/server/app.go`, create `app_logging_test.go`; modify `internal/servercli/root.go` and adjacent tests. Do not change native production files or source/library paths. Main owns these paths.

**Interfaces:** Consume final L1 types. Add Config/Options administrator user IDs and manager. Dedicated `LogManagementService.GetLogSettings` and `UpdateLogSettings`, all through existing authenticate and Origin guard; 4KiB requests and small bounded responses. Serialize int64 revision as standard Protobuf; no raw JSON or paths in RPC. Stable user ID remains discoverable through existing session API and operator documentation.

- [ ] Add true HTTP/Connect authorization and CAS tests before wiring; validate no settings disclosure for non-admin, no arbitrary path fields, safe validation/error mapping, repeated headers and expired/revoked credentials.
- [ ] Add manager startup/cleanup/Close wiring with safe lifecycle and transport events; request logging is fixed category/status/duration and preserves streaming/native behavior. Keep all startup-failure cleanup paths covered and default multi-process Store mode unchanged.
- [ ] Verify actual TempDir App produces only safe JSON log lines on success/failure/shutdown, survives logging failure without altered responses and closes all retention work. Re-run configuration/native privacy tests plus actual keepalive and streaming cases.
- [ ] Run Buf lint/generation parity, focused tests/vet and clean full Go tests. Freeze exact candidate for independent acceptance, preserving original RED evidence.

### Task 3: L3 Administrator Web Editor And Delivery

**Files:** Create `web/src/server-logging.tsx`, `server-logging-i18n.ts`; modify `web/src/{api,main}.tsx` and `web/embed.go`, `embed_test.go` for the exact SPA deep link; update the three owned documentation files with verified outcomes. No new component framework, database or client code. The embed pair is a recorded necessary scope correction after a real browser 404; keep `/logs/*` and unrecognized descendants inaccessible.

**Interfaces:** Consume L2 generated Connect client. Scope cache to active session; preserve dirty draft on theme/locale change, stale revision or uncertain save. Use server-provided current revision, explicit reload after conflict/unknown, no automatic mutation retry or sensitive browser persistence.

- [ ] Implement form with visible labels, native integer constraints/range help, existing Radix Select/Input/Button, restricted/loading/error states, safe health counters and save/reload feedback in Chinese/English.
- [ ] Run `npm run build --prefix web`; browser-check real administrator save/reload, normal-user restriction, dirty conflict, language/theme, keyboard and narrow screens against a synthetic isolated server. Existing components handle motion; add none without purpose.
- [ ] Run clean integrated fullGo/vet, module/Buf generation parity and nine CGO0 binary cross-builds. Obtain exact-candidate independent Spec/Quality and user-representative acceptance; stage only the accepted manifest, commit/SSH push/readback, then record Nowledge progress with remaining release limitations.

## Execution Checkpoint

Independent representative review conditionally accepts the spec/plan with four required contracts now written in the spec: lock-time latest-policy reads and durable segment age; short-write/damaged-tail/remnant accounting; CAS→cleanup→atomic persistence→ACK ordering and unknown outcomes; failed-close ownership and cooperative I/O cancellation limits. L1 implementation may proceed under that exact contract; L2/L3 implementation waits for L1's final types. Existing S1 and native-client work continues separately.
