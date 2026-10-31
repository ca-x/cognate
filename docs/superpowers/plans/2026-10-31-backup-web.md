# Administrator Backup Web Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Mainline implementation follows the user's existing authorization; an independent representative reviews the completed slice before commit.

**Goal:** Give administrators a real Web entry for the already accepted manual instance-backup RPCs, with honest uncertain-result handling and the same first-release UI quality bar as the public catalog.

**Architecture:** Add one authenticated TanStack route and one typed client on the existing Connect transport. Reuse React Query, the existing Button, and Radix-backed Dialog/Checkbox components; use a small pure boundary module for validated responses and a tab-local, owner-scoped nonsecret request identity. Do not change the controller, authorization, database, upload, or restore protocols.

**Tech Stack:** Existing React, TanStack Router/Query, Radix UI, Lucide, Tailwind/CSS tokens, i18next, Connect and Buf. Go 1.27.3 and `CGO_ENABLED=0` for the Linux test-only App fixture.

**Spec:** `docs/REQUIREMENTS.md` F4, `docs/SERVER-BACKUP.md`, and `docs/UI-DESIGN.md`. This plan delivers the manual management page, not automatic scheduling, retention, full restore or GA; those requirements remain open.

## Global Constraints

- User reiterated that UI must use mature frameworks and their components. No new dependencies, self-made dialog/checkbox/focus system, toast system, animation library or parallel design system.
- Route `/server-backup` uses the existing authenticated workspace. The server is authoritative for administrator access; ordinary users see an access explanation, never an enabled/disabled guess or fetched task history.
- Status, disabled, unsupported-server, loading, list-empty and read-error states stay distinct. The UI never treats an unknown or failed response as an empty instance or successful backup.
- A backup is sensitive and instance-wide, including other owners. Starting requires an explicit Radix confirmation with an unchecked scope acknowledgement. S3 credentials and the recovery KEK remain in server-private files, not browser forms or DTOs.
- A confirmed `remote_committed` means exact remote publication, not a verified restore. Cancel never claims rollback. Reconcile is exact read-only verification, not another upload or restore.
- Disable automatic mutation retries. Before Start, store only its canonical nonzero UUID in `sessionStorage` under the current origin and owner. No token, key, target, credential, job contents or configuration is persisted there. Storage failure blocks Start before network I/O.
- A lost/malformed Start response preserves that UUID across route changes/reload; continuing is an explicit same-request operation, never an automatic new request. A separate explicit forget confirmation affects only the browser record and makes no cancellation/rollback promise.
- Reads use abort signals; late responses from an old owner/client do not update the current UI or query cache. Query keys include owner; mutation callbacks check the current lifecycle before updating anything.
- Task lists request 20 items, follow only user-selected pages, and poll at 5 seconds only while a visible known task is active. No unbounded automatic paging, fake percent progress, invented counts or animation of polled data.
- Preserve Chinese/English, light/dark/system, keyboard/focus recovery, 320/375px and 200% text. Reuse shared press and dialog-entry motion; no extra ambient loops or motion on keyboard/ReduceMotion paths.
- All Go tests/builds/vet explicitly use `CGO_ENABLED=0`; no race/cgo. Test-only credentials stay synthetic and are never written to console, screenshots, receipts or error messages.

## Review Focus

- A response is lost after Start is durably registered; reloading must keep the original request identity, and continuing must return the same job without a second capture.
- The owner or client changes while a request is held; the new owner must not see the old response or inherit a pending operation.
- RPC returns a malformed job/status/page or an unknown phase; preserve uncertainty and disable unsafe actions instead of displaying success or empty history.
- A task is canceled while complete may already exist; describe the actual server phase and cancellation flag, never a rollback or restored-data promise.
- Long UUIDs, translated confirmations and 200% text must remain readable and keyboard-reachable without clipping or shrinking text.

## File Map

- Modify `web/src/api.tsx`: add the typed backup client to the existing authenticated transport.
- Modify `web/src/main.tsx`: lazy route, navigation label and namespace registration only.
- Create `web/src/server-backup-state.ts`: closed response validation, active-phase predicates, and owner-scoped request-intent storage.
- Create `web/src/server-backup-i18n.ts`: complete Chinese/English namespace and navigation label.
- Create `web/src/server-backup.tsx` and `web/src/server-backup.css`: page, existing-component confirmations, job read/detail/actions and responsive layout.
- Create `scripts/verify-backup-web-state.mjs`: executable pure boundary/storage regression tests using the existing bundler, no new test framework.
- Create `internal/server/backup_browser_fixture_linux_test.go` and `scripts/verify-backup-web.mjs`: real TLS/SQLite App and synthetic TLS S3 fixture, production Web build, browser interactions, fault injection and screenshots.
- Modify `web/embed.go` and `web/embed_test.go`: explicitly serve the new GET/HEAD deep link without widening the asset or RPC fallback.
- Update `docs/SERVER-BACKUP.md`, `docs/UI-DESIGN.md`, `docs/TODO.md`, `docs/REQUIREMENTS.md`, `docs/VERIFICATION.md` and `docs/FORMAL-RELEASE-AUDIT.md` after actual acceptance.
- Do not touch the parallel source-page worker's six-file write set or import its unfinished work.

## Task 1: Boundary and Persistent Request Identity

**Interfaces:** `acceptBackupStatus`, `acceptBackupJob`, `acceptBackupPage`, `isActiveBackup`, `readBackupIntent`, `writeBackupIntent`, `clearBackupIntent`. Validation throws a fixed private-data-free error. Intent state is `empty`, `pending` with only `requestId`, or `blocked`.

- [x] Add an executable boundary test covering valid and malformed status/job/page, unknown phases/codes, UUIDs and bigint/date limits, duplicate jobs and cursor safety. The initial RED was a missing-module baseline, not a behavioral counterexample.
- [x] Cover empty/pending/blocked storage, exact same-ID continuation, different-ID refusal, thrown storage operations, owner separation and compare-before-clear.
- [x] Implement the smallest pure functions and verify the same script passes (60 checks).

## Task 2: Mature-Component Product Entry

- [x] Register the typed client, namespace and `/server-backup` route; preserve unrelated routes and transport/session handling. A real browser 404 also drove a permanent embedded deep-link RED/GREEN test.
- [x] Implement distinct authorized/disabled/loading/error/empty/history states, with a concise primary task region and secondary deployment/restore boundaries.
- [x] Add explicit Start/same-request continuation, browser-record forget and Cancel confirmations using existing Radix components; Reconcile remains an explicit read-only action.
- [x] Add validated Get/detail, manual paging, bounded active polling and fixed error feedback, guarding late callbacks and preserving uncertain intent.
- [x] Run Web typecheck/build and existing Grok edit/import state regressions.

## Task 3: Real Browser and Negative Paths

- [x] Build the production Web assets, then compile the Linux Go App fixture with `CGO_ENABLED=0`; fixture is skipped unless explicitly selected and uses private isolated state.
- [x] Verify administrator and ordinary-user boundaries, enabled/disabled/error/empty/history, real manual publication and complete-last ciphertext, same-ID continuation after lost/malformed response, and browser-record forget semantics.
- [x] Verify held upload cancellation and exact read-only reconciliation, paging, theme/language, lifecycle cancellation and no secret-bearing diagnostics. Existing ApiProvider cache clearing remains part of same-owner client-renewal isolation, not a separately proved component guarantee.
- [x] Capture light/dark Chinese/English, 320/375px and 200% text geometry, keyboard dialog/focus paths and ReduceMotion using the real running production UI (v8: 88 receipts, 56 layout/focus checks).
- [x] Keep original failures and fixes; inspect real screenshots, not only DOM presence or screenshots of static mocks. v7 seed HTTP 500 cause remains UNKNOWN; added safe stage/code diagnostics, subsequent 63-job RPC-only probe passed without proving the old failure fixed.

## Task 4: Acceptance and Stage Delivery

- [x] Independent representative reviews the frozen write set and adverse paths; fix concrete findings with reproducible evidence. Dewey's final local ACCEPT closes I1/M1, preserves the existing M2 production cache-clear dependency and does not close v7's historical UNKNOWN.
- [x] Run fresh scoped Go tests/vet, Web build/state/browser gates, then full same-combination CGO0 tests/vet/modules/Buf parity and applicable cross-builds without source drift (7885 PASS / 162 SKIP / 0 FAIL, 18 CGO0 builds).
- [x] Update documentation only to the verified scope, including original failures, the local review boundary and open full-F4/UI requirements.

Delivery operations follow this accepted source freeze: commit and SSH push the slice, read back the remote SHA, and save one durable progress event with returned identities. Their actual results belong in the delivery receipt rather than a guessed self-referential commit hash. Do not publish GA or close the full goal for this slice.
