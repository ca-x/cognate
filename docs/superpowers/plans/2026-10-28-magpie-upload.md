# Magpie Authorized Upload Implementation Plan

> **For agentic workers:** Use subagent-driven-development and ub-mode. Implement behavior tests before production changes; independent spec and quality review precedes commit and SSH push.

**Goal:** Select a Magpie library JSON export, preview repository candidates on the server, then explicitly collect selected repositories.

**Architecture:** One stateless Connect preview RPC invokes the existing server-side moejs importer and owner-scoped library listing. The browser sequentially invokes existing CollectRepository for selected candidates. No upload session, preview token, new schema or batch transaction.

**Tech Stack:** Go 1.27.3, Echo, Connect, Buf, ent/entsqlite, Afero, moejs; existing React/TanStack/Radix/i18next UI. CGO_ENABLED=0.

**Spec:** docs/REQUIREMENTS.md F2/F6/F11; docs/PLUGIN-IMPORT.md; docs/MAGPIE-IMPORTER.md. This is a limited F6 slice, not M6 or first-release completion.

## Global Constraints

- Plugins run only on the server; no client dependency on moejs/importer.
- Uploads stay in request memory, never logged or persisted. No arbitrary server paths, JS, hooks, URLs or owner input.
- Only Magpie repository references are migrated, not local skill contents, private sources or client configuration.
- Existing repository collection scans the whole public archive and remains manual, private by default and idempotent by owner/repository/branch.
- Preserve all established stack decisions and modern Go guidance; do not add cgo or dependencies.

## Review Focus

- Authentication, quota admission and shutdown must precede body decoding, including rejected slow requests.
- Enforce 1 MiB decoded export and 2 MiB HTTP/message limits; reject compressed requests rather than hide expanded memory.
- A five-second upload/preview deadline must interrupt a real network body read; Close must cancel and drain before storage closes.
- Preserve lowercase head as a real branch, while HEAD and empty mean default branch; existing IDs are owner-scoped observations only.
- Account changes/unmount invalidate requests and erase drafts; locale/theme changes preserve drafts. Cancellation is not rollback.

### Task 1: Server preview boundary

**Files:** proto/skillhub/import/v1/import.proto, its generated Go/TS files, internal/server/httpapi/imports*.go, auth.go, guard.go.

**Interfaces:** ImportService.PreviewLibraryExport; request format=1/export_data=2; response repositories=1/referenced_skill_count=2. RepositoryCandidate has repository=1/branch=2/existing_repository_id=3. ExportFormat has unspecified=0/magpie_library_json=1.

- [ ] Generate contracts using both existing Buf templates; lint.
- [ ] Observe behavior red with a compiling stub, then implement authenticated preview, owner lookup, safe error mapping and zero collection/network writes.
- [ ] Verify empty/invalid/oversized/unsupported formats, source filtering, HEAD semantics, redaction, owner isolation, revoked sessions, compressed/framed requests and unchanged ordinary API limits.
- [ ] Implement decode-before-admission protection: global two, per-owner one, no queue; release on all exits. Five-second deadline and idempotent Close cancel/drain.

### Task 2: App lifecycle and transport integration

**Files:** internal/server/app.go, internal/server/app_import_test.go.

**Interfaces:** NewImportHandler(authentication *auth.Service, collection *library.Service) (*ImportHandler,error), Guard(http.Handler) http.Handler, Close() error; httpapi.Options.Imports and App.imports share this instance.

- [ ] Test the App route with genuine Connect calls, no state changes and real TCP slow bodies.
- [ ] Wire constructor, route and early Close before library/store teardown; verify cancellation/drain and rejection after Close.
- [ ] Prove repeated and partially failed Collect calls preserve per-repository semantics without pretending batch rollback.

### Task 3: Repository-page workflow

**Files:** web/src/import-repositories.tsx, api.tsx, repositories.tsx, i18n.ts, locales/en.ts.

- [ ] Add accessible, translated upload/preview/explicit selection UI using existing components; no default selection or new navigation.
- [ ] Bound file size before reading, keep raw bytes outside queries/storage, discard after preview; bind callbacks to the current account generation.
- [ ] Collect sequentially; explicit per-item errors may continue, cancellation/auth/transport/capacity/unknown errors stop the batch with current result uncertain.
- [ ] Preserve completed results, expose not-attempted/uncertain states and safe retry guidance; do not skip existing entries' cache validation.
- [ ] Build and exercise real browser flow, owner cleanup, locale/theme draft preservation, empty/error/keyboard/narrow-screen paths.

### Task 4: Independent acceptance and delivery

- [x] Independent user representative reviews spec and quality, returning findings to original implementers for fixes and re-review.
- [x] Run focused and all Go tests/vet, Buf lint/regeneration parity, npm build/audit and CGO-disabled cross-builds.
- [ ] Update controlled docs with actual evidence and remaining limits; commit and SSH push only the verified slice, then check exact-SHA CI.
- [ ] Save precise cross-machine progress to Nowledge without calling this full M6 or GUI completion.

## Action Log

- 2026-10-28: Recovered SSH-pushed baseline dda1dd9 and targeted Nowledge context; locked this smallest next F6 slice. Backend and Web implementation use disjoint write sets; controller owns App integration and final verification.
- 2026-10-28: Observed compiling behavior failures before wiring; implemented server and Web slices, passed real TCP deadlines/drain, all Go tests/vet/module checks, clean index export build/tests/generation parity, production audit 0 and 12 CGO-disabled cross-builds.
- 2026-10-28: Repeated browser acceptance on the final embedded production bundle, including real public GitHub partial failure/retry and cancellation. Independent representative completed Spec PASS / Quality PASS with zero confirmed P1/P2; no production fixes requested. Commit/push and exact-SHA CI are delivery follow-through, not implied by these local gates.
