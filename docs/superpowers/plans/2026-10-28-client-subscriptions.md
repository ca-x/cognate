# Client subscriptions and parallel server follow-through

**Goal:** connect Pack distribution to actual client installation and persistent subscription ownership; independently advance Lists scheduling and server-only import adapters. This is not a declaration that M2–M7 or first release are complete.

**Spec:** `docs/REQUIREMENTS.md` F1, F5–F7 and §5–7; `docs/PACK-DISTRIBUTION.md`.

## Global Constraints
- Work in the user-selected current repository, preserving all existing uncommitted files. User update on 2026-10-28 authorizes and requires committing and pushing each accepted stage; main serializes Git operations after independent review and verification. Do not create branches, force-push, publish releases, or clean the shared workspace without separate authorization.
- Go 1.27.3, no cgo, ent/entsqlite, Afero, Connect/Buf and existing pinned dependencies remain binding. Run the modern-Go guidance before Go edits and follow ub-mode red→green tests.
- Plugins execute only on the server. Client imports may not depend on `internal/server`, moejs or third-party importers. No RPC accepts arbitrary server paths.
- Share credentials must not appear in URLs, logs, command arguments, persisted plaintext, formatted diagnostics or test output. Network clients use only the configured SkillHub origin and reject redirects.
- Preserve binary data, hidden files, empty directories and executable bits; verify transfer framing/SHA256 and logical subtree hash before installation. Bound network, archive, memory and concurrency work.
- Latest UI instruction: use mature frameworks/components rather than home-grown base controls. Preserve the locked React/shadcn/TanStack Web stack and MyGo native desktop components; custom product orchestration is not a substitute for accessible component primitives.
- No silent overwrite of unmanaged local skills or conflicting slug/hash selections. Multiple subscriptions to identical slug/hash share one canonical installation. Failed work must remain recoverable and not claim successful application.

## Parallel Ownership
1. Downloader implementer: `internal/packclient/**` and its task report only. Public contract: `New(serverURL, shareToken string, client *http.Client) (*Client, error)`, `Manifest(context.Context) (*packv1.GetManifestResponse, error)`, `Download(context.Context, *packv1.GetManifestResponse, *packv1.PackSkill) (*Snapshot, error)`, `Snapshot.Path() string`, `Snapshot.Close() error`. Download returns a private verified temporary skill directory, never writes canonical state. Constructor rejects unsafe origins and credential syntax without echoing input; the supplied transport can be used for test servers but redirects are always rejected.
2. Scheduler implementer: `internal/github/lists*.go`, `internal/server/scheduler/**`, server state/schema/generated ent, `internal/server/app*.go`, `internal/servercli/**` and a task report. Do not modify Pack state/signing/distribution/HTTP APIs. Implement configurable periodic selected-list sync with durable watermarks, unchanged-list item skips, bounded lifecycle/cancellation and public repository auto-collection; preserve credential-generation fencing. Private repository fetch and source-content replacement require separate follow-through, not unsafe approximations.
3. Importer implementer: `internal/server/importer/**`, `internal/server/plugin/**`, dedicated importer documentation and task report. Retrieve prior magpie/cc-switch cases first; implement a bounded server-only magpie read-only preview for repositories and installed skill references through moejs using verified source formats. Missing export is a normal skip. No arbitrary process execution, local path import RPC, client integration, global dependency edits or unverified config formats.
4. Main thread: `internal/state/**` client ent/schema, `internal/subscription/**`, `internal/install/**`, `internal/cli/**` and shared integration docs. Define transactional subscription/source membership, encrypted local share storage and cross-pack planning; connect fetch/install/status/sync through the existing installation journal. Do not modify delegated write sets while agents run.
5. Independent representative: scoped review packages after implementation, separate from all implementers. Report both spec compliance and code quality; reproduce concrete edge cases rather than accepting implementation self-review.
6. Uninstall implementer: `internal/install/uninstall_execute*.go` and its report only. Reuse `PrepareUninstallLocal` and existing state transition APIs; provide `Session.UninstallLocal(context.Context, string) (state.Backup, error)` that safely executes the prepared operation and completes the journal. Preserve backups and refuse changed/replaced/unmanaged targets. Ask for additional file scope instead of touching the main thread's state or installer code.

## Tasks
- [x] Downloader: red tests for valid binary transfer/extraction and malformed framing/hash/archive/redirect/credential failures; implement verified bounded downloads and cleanup.
- [x] Scheduler: red tests for unchanged-watermark skips, credential races, bounded periodic work/Close and public auto-collection; implement and wire configurable lifecycle.
- [x] Importer: retrieve verified cases, add fixture-backed malformed/missing/valid preview tests and server-only adapter.
- [x] Subscription state: persist encrypted credentials and canonical memberships with CAS; test overlapping packs, slug conflicts, failed attempts and removal ownership without deleting unrelated files.
- [x] Client orchestration/CLI: fetch all desired manifests before mutation, deduplicate downloads/installations, retain unresolved changes on failure, expose safe stdin credential subscription, status and explicit sync. Reuse existing journal and backup/uninstall semantics.
- [x] Scoped verification: retained focused regressions and independent spec+quality review/fixes; clean staged export passed full no-cgo tests/vet, module checks, Web build/production audit, Buf generation comparison, client dependency isolation and 12 CLI/server cross builds. Independent client/server and browser acceptance applies only to the exercised scopes recorded in `docs/VERIFICATION.md`; final stage integration spec/quality review passed on 2026-10-28.
- [ ] Remaining verification/delivery: remote commit/CI confirmation, broader end-to-end and first-release acceptance, non-Linux native runtime and all unverified product scopes remain separate gates. This stage does not complete M2–M7.

## Appended F8–F11 Follow-through

- [x] Scoped F8–F9 backend: category/publication ent+state, anonymous bounded service/streaming and owner/anonymous Connect endpoints; independent backend representative review passed.
- [x] Scoped F8–F9 consumer: anonymous consumer+CLI and shared App lifecycle integration; independent consumer representative review passed. Anonymous installation does not create a subscription.
- [x] Scoped F10: official SDK MCP credentials/management, implemented typed tools and owner-only token Web page; independent backend/UI review passed. Category/publication MCP tools and scopes remain unimplemented.
- [x] Scoped F11: mature Web public/owner screens, light/dark/system themes and zh-CN/en with preference persistence and strict CSP; independent real-browser/keyboard/320px/390px review passed for exercised cases, including the locale/MCP-draft fix and session-boundary replay.

These F8–F11 checkboxes record independently accepted subscopes, not completion of every F8–F11 requirement or M2–M7. Exact acceptance and unverified boundaries are summarized in `docs/VERIFICATION.md`.

The completed checkboxes apply only to the bounded task scopes, not complete F1/F5/F6, source-content replacement, automatic client scheduling, non-Linux runtime or release. Independent retained regressions closed PAX-tail acceptance, deadline cursor starvation and FIFO-key bootstrap in round1; detailed verdicts/evidence remain in the task ledger.

## Review Focus
- A redirect, error string, formatter, test failure or subscription listing must not disclose the share credential.
- Extra frames after final, wrong offset/hash/revision, truncated gzip and unsafe archive paths must never produce a published local skill.
- Two packs sharing identical slug/hash must share one installation; distinct hashes under a slug must fail before filesystem mutation.
- A failed download/application must not advance the successfully applied revision or strand ownership that permits deleting unmanaged files.
- Scheduler shutdown must interrupt in-flight work and wait; an old token/selection generation must not resurrect revoked catalog data.

## Execution Ledger
- 2026-10-28: resumed after the server/Web Pack sub-plan passed independent replay. Final evidence was recorded and Nowledge context `85e1dbfd-8d38-4de3-9e43-38babd1887ca` saved. First release remains unpublished.
- Ruling: use a disjoint-write-set shared checkout rather than a fresh worktree because nearly all current source is untracked and the user explicitly selected this repository; a fresh checkout would omit that work. No commits or cleanup are authorized by this plan.
- Ruling: parallel implementation is authorized by the latest user message. The main thread owns subscription state now, not the downloader or scheduler critical paths, and can work while those sidecars run. Review occurs independently after each report.
- Ruling: native MyGo GUI remains required and incomplete. This round prioritizes the shared client core it will consume; the existing Web Pack screen is not represented as complete frontend delivery.
- User update (2026-10-28): “代码完成一个阶段就要提交和推送下”. This supersedes the earlier no-commit/no-push ruling, not the review, secret, shared-worktree preservation, or no-release boundaries. Main will commit the accepted development-preview stage and push the current branch, then verify remote SHA/CI; workers still do not independently commit.
