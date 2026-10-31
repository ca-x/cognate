# Configuration Workspace UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve the production configuration overview and shared editor shell without changing provider schemas, secret transport, or native application eligibility.

**Architecture:** Keep React, TanStack Query/Router and existing Radix components. Add page-scoped styling and local translations; exercise the actual built Web against TLS Connect handlers and SQLite, not mocked successful RPCs. Provider-specific forms remain read-only dependencies of this slice; defects requiring their changes are reported before expanding scope.

**Tech Stack:** Existing React/Radix/Connect/Buf, Go 1.27.3 with `CGO_ENABLED=0`, Node 26.12.0. No added runtime dependencies.

**Spec:** `docs/REQUIREMENTS.md`, `docs/UI-DESIGN.md`, `docs/CONFIGURATION.md`, `docs/CCSWITCH-CONFIGURATION-MATRIX.md`.

## Global Constraints

- Preserve Go/ent+entsqlite/Afero/Echo/moejs/MyGo and canonical skill installation decisions. This slice changes neither production Go nor protobuf.
- Keep the distinction between saved assets, credentials, supported provider forms, native eligibility and applied client configuration. Unsupported types stay explicitly read-only, never fake completed targets.
- Never place secrets in Query caches, browser persistence, logs, screenshots, reports or committed fixtures. Tests use only synthetic identities.
- Preserve Keep/Replace/Clear, copy without secrets, owner isolation, revision/CAS and Unknown semantics. Mutations must not replay automatically.
- Use mature framework components; only purposeful transform/opacity motion, reduced-motion and visible keyboard focus. Retain drafts across permitted theme/language changes and failed queries.
- Match Chinese/English, light/dark/system, 320/375/1440 CSS px, actual 200% root font size and ReduceMotion. Check complete words and reachable actions, not merely horizontal overflow.
- Work in a private archive of `e2003855c69e2e72e05962d011faa8f788abd733`; no new branch/worktree, no main edits, commits or child agents. Use `apply_patch` for source edits.

## Task 1: Overview And Shared Editor Shell

**Files:** Modify `web/src/configuration.tsx`, `web/src/configuration-order.tsx`, `web/src/configuration-order-i18n.ts`; create `web/src/configuration-ui.css`, `web/src/configuration-ui-i18n.ts`, `internal/server/httpapi/configuration_ui_browser_fixture_test.go`, `scripts/verify-configuration-ui.mjs`, `docs/CONFIGURATION-UI.md`. Exactly eight files; no shared CSS, locale, state/codec, protocol or provider form edits without main-thread agreement.

**Interfaces:** Keep `ConfigurationPage`, existing form exports and configuration/order RPCs. Add no new application or import claims.

- [ ] Read specifications, existing page/forms/order and source/auth/management browser contracts. Record meaningful baseline defects and a targeted failing test before changing UI.
- [ ] Improve asset grouping, true metadata and credential readiness, empty/loading/error states, ordering, clear primary actions and dangerous areas. Stale data after failed refresh must not be presented as current state or authorize stale writes.
- [ ] Preserve and test create/edit/copy/delete, ordering, cancellation, unsupported read-only assets, CAS and dropped successful replies. Keep drafts visible and explicitly reconcile Unknown without replay.
- [ ] Check dialog initial focus, Escape/confirm/cancel recovery, background failures and disappearing/disabled triggers. Only restore focus when the affected subtree actually held it; never steal external focus.
- [ ] Build production Web and run actual TLS/Connect/SQLite fixture. Cover each currently supported provider branch at least at list/open/edit validation level; distinguish unsupported native consumption from editable server assets.
- [ ] Run theme/language/width/font/motion and genuine keyboard checks with safe screenshots. Use a strict fixture terminal gate rejecting nonzero exit, signal, timeout and missing/contradictory receipts, with self-tests.
- [ ] Freeze exact eight-file patch/tar/hash plus commands, original RED, final evidence and untested boundaries; submit for non-author Spec/Quality review. Main integrates and commits only after appropriate checks.

## Delivery Ledger

- Base `e200385` is the accepted four-page/server-fix stage, SSH-pushed on 2026-11-01; remote CI was still running when this plan was written.
- Task 4 MCP/logging is on a disjoint eight-file slice under independent review; it does not supply evidence for this page.
- Full provider coverage, client application, imports, Pack attachments, restore and platform release gates remain open. This plan cannot authorize a GA label by itself.
