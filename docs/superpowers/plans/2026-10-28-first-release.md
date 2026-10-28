# SkillHub First Formal Release Implementation Plan

> **For agentic workers:** Use subagent-driven-development, ub-mode and version-specific modern Go guidance. The user explicitly authorizes continuous execution through first formal release; stage checkpoints use an independent user representative, not repeated human approval. Never redefine release completion around the already implemented subset.

**Goal:** Implement and verify every confirmed requirement in REQUIREMENTS.md, then publish the first formal version with server, CLI, native MyGo client and automated distributables.

**Architecture:** Preserve Go/ent+entsqlite/Afero/Echo/Connect+Buf/moejs and the established Web stack. Plugins execute only on the server. Clients consume authenticated or explicitly public server resources and project canonical skills locally. Build and release are separate gates.

**Spec:** docs/REQUIREMENTS.md, especially sections 2–8. Prior progress and CI results are evidence pointers, not substitutes for current-state acceptance.

## Global Constraints

- Go 1.27.3, CGO_ENABLED=0, no replacement of MyGo native UI with a Web container.
- No credentials in source/logs/command arguments/URLs/browser persistence; no sensitive screenshot, accessibility snapshot or HAR.
- Natural keys, owner boundaries, revisions, encrypted credentials, conservative uninstall and filesystem trust remain intact.
- Independent spec/quality review, fresh tests and stage commit plus SSH push precede milestone completion.
- Only a full completion audit and verified published artifacts can complete the active first-release goal.

## Full Release Gates

- [ ] F1: real GitHub device authorization, selected Lists sync, persistent watermarks and safe periodic incremental refresh; private source handling where authorized.
- [ ] F2: manual collection validation, deduplication, branch/default handling and bounded archive storage.
- [ ] F3: local accounts, signed sessions and real OIDC login/binding acceptance, isolation and secret protection.
- [ ] F4: optional S3-compatible Afero backup, consistent SQLite/content snapshot, automatic/manual jobs and verified restore.
- [ ] F5: Pack/share/distribution, cross-Pack deduplication, actual upstream snapshot replacement and continuously scheduled client updates.
- [ ] F6: server plugin manifest/controlled host, cc-switch/Magpie/npx/general adapters, separately selectable repositories/skills/configuration, protected persistence and conflict previews.
- [ ] F7: common CLI/native-client application kernel, all specified canonical projections, Windows fallback/WSL copy, ownership-safe uninstall/backup/restore, selected configuration automatic application with rollback.
- [ ] F8/F9: opt-in anonymous catalog/install and owner-scoped multi-category management, including revocation during downloads.
- [ ] F10: scoped user-managed MCP tokens and real MCP tools for all promised maintained resource kinds, including categories/publication.
- [ ] F11: complete theme/locale, draft preservation, keyboard/narrow-screen/reduced-motion/CSP acceptance.
- [ ] M7: usable MyGo native desktop workflows and automated native-client packaging, not just CLI binaries.
- [ ] Delivery: fresh whole-project and cross-platform verification, artifact inspection/runtime smoke tests, release documentation, signed-off independent acceptance, actual formal tag/Release and published binaries/container/client packages.

## Review Focus

- Uploading a live SQLite main file is not a consistent export; never silently omit WAL or accept arbitrary server paths.
- Client preview/apply binds to the reviewed bytes; delayed callbacks must not mutate a new account or silently apply changed data.
- Repository refresh must preserve references and expose a real next client sync, not mutate old snapshots under unchanged hashes.
- Backup restore must reconstruct database plus referenced content and fail closed on corrupt/mismatched state without losing the current installation.
- Native platform claims require platform-appropriate evidence; cross-compilation does not establish UI/runtime/installer correctness.

## Batch 1: Import Consumers and Safe SQLite Upload

**Write ownership:** controller owns importer snapshot, ImportService protocol/generation, HTTP/App integration and Web source selection. CLI worker owns internal/importclient and import command/root registration. MyGo worker owns scratch spike only; independent representative owns audit/review reports only.

- [ ] CLI: add an authenticated Magpie preview/application consumer, explicit ordered selection and SHA-256 preview/apply pinning; bounded file/stdin/network input, strict origin/redirect rules, no plugin dependencies. Prove changed files and unselected candidates cause zero Collect calls; partial failure/unknown cancellation and redaction have real RPC tests.
- [ ] cc-switch snapshot: accept a separately bounded, self-contained rollback-mode SQLite snapshot, never a server path. Deserialize only into an isolated in-memory connection, restrict SQLite resource limits and read-only settings, validate schema/table/rows and reuse the server moejs transformation. Keep external administrator-path trust checks unchanged.
- [ ] Protocol/transport: add a separate cc-switch preview RPC so the Magpie 2 MiB wire/1 MiB raw/5 second limits stay unchanged. Preserve decode-before-auth rejection, shared per-owner/global admission, compression refusal, real read deadlines and Close drain.
- [ ] Web: explicit source choice and upload, no automatic selection/import; retain enabled metadata for cc-switch and preserve current locale/account/cancellation boundaries. Explain snapshot creation and whole-database secret exposure before upload.
- [ ] Verify: SQLite malformed/WAL/schema/size/rows/cancellation/no-disk and secret exclusion tests, cross-owner/transport/App tests; existing Magpie/CLI/browser regressions; Buf parity, all Go tests/vet, Web build/audit and no-cgo cross-builds. Independent user representative signs the actual scope; commit and SSH push verified stage.

## Subsequent Dependency Order

1. Establish MyGo native viability from a real minimal build; then implement native session/catalog/Pack/import/sync workflows on the shared client kernel and package them in CI.
2. Implement verified configuration formats and encrypted import resources, Pack configuration attachments, client conflict/apply/restore records and selectable migration of actual skill contents.
3. Complete source refresh, continuous client synchronization and Windows/WSL/recovery acceptance.
4. Implement S3 backup/restore and remaining plugin host/adapters/MCP tools, then full requirement-by-requirement acceptance and real external authorization tests.
5. Publish only after the complete requirements audit proves all release gates, and inspect actual released payloads. External credentials/platform evidence may require user input, but do not declare blocked while other meaningful development is available.

## Action Log

- 2026-10-28: Verified clean SSH-synced HEAD 7451251 and active unbounded first-release goal. Prior engineering turn changed authoritative source and verified CI, hence progress rather than a no-progress restart. Targeted Nowledge search returned the latest Magpie delivery context; cross-tool history does not replace source inspection.
- Retrieval: nmem CLI query `SkillHub F6切片 Magpie cc-switch 配置 MyGo 首发`, mode fast_bm25_vector, scope default, no filters. Ranked memories: 1) 4f704cd5-9cda-4427-b535-6d997e2075ac, Magpie upload delivery, score 0.99; 2) 00ddbdf3-0fc6-4318-bad6-fc4377f62d80, earlier M6 preview, 0.8559501243600083; 3) e81a87b9-e075-4340-8b7a-ccce8339ad65, first source SSH delivery, 0.7303877479604441. No graph MCP exists and CLI cannot establish matching authenticated browser/Space enforcement, so graph visualization is skipped rather than guessing a session.
