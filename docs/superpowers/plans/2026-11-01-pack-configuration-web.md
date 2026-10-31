# Pack Configuration Web Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let owners review and maintain ordered configuration attachments on a Pack through the production Web, using the existing dual-CAS metadata protocol.

**Architecture:** Add a dedicated component to Pack detail and wire `PackConfigurationService` through the existing authenticated API context. Read only asset metadata and attachment snapshots, never `ReadDraft` or credentials. Reuse React/TanStack and Radix Dialog/Checkbox/Select with explicit review and single submission; no backend or protobuf changes.

**Tech Stack:** Existing React/Radix/Connect/Buf; Go 1.27.3, `CGO_ENABLED=0` fixture; Node 26.12.0. No added dependencies.

**Spec:** `docs/REQUIREMENTS.md` F12, `docs/PACK-CONFIGURATION-CLI.md`, `docs/UI-DESIGN.md`, `proto/skillhub/pack/configuration/v1/configuration.proto`, `internal/server/state/pack_configuration.go`.

## Global Constraints

- Fixed Go/ent+entsqlite/Afero/Echo/moejs/MyGo stack and no cgo. This is server metadata management, not a client runtime plugin or automatic configuration application.
- All targets remain NativeUnavailable in this feature. Current/stale/unsupported describes attachment metadata, not installed configuration or device grants. No secret sharing, anonymous configuration consumption or hidden auto-apply.
- Only production `GetAttachments`, `ReplaceAttachments` and configuration `ListAssets` metadata. No draft/body/reveal RPC; no secret state in caches, browser persistence, logs, screenshots or reports.
- Preserve owner isolation, exact order, maximum 16 references, no duplicate asset/target pairs, positive asset revisions and both CAS revisions. An empty list explicitly clears metadata only, without deleting assets or skills.
- Candidate provider targets are the existing server allowlist: claude, claude-desktop, codex, gemini, grokbuild, opencode, openclaw, hermes, pi, mcode, kimi, dsh, agy, omp; schema 1 provider assets map to `<agentId>.provider.user`. These are metadata choices, not assertions about native support. Server validation remains authoritative.
- Use existing mature components, visible focus and purposeful motion with ReduceMotion. Chinese/English, light/dark/system, 320/375/1440 CSS px, 200% actual root text and genuine keyboard activation must remain usable.
- Private archive base `e2003855c69e2e72e05962d011faa8f788abd733`; no main edits, new branch/worktree, commits or child agents. Use `apply_patch`.

## Task 1: Owner Attachment Review And Replace

**Files:** Modify `web/src/api.tsx`, `web/src/packs.tsx`; create `web/src/pack-configurations.tsx`, `web/src/pack-configurations-i18n.ts`, `web/src/pack-configurations.css`, `internal/server/httpapi/pack_configuration_ui_browser_fixture_test.go`, `scripts/verify-pack-configurations-ui.mjs`, `docs/PACK-CONFIGURATION-WEB.md`. Exactly eight files. Existing Pack/share behavior and styles are read-only dependencies; no protocol/state changes.

**Interfaces:** Expose authenticated `packConfigurations: Client<typeof PackConfigurationService>` in `useApi()`. Mount a keyed `PackConfigurations` component below the Pack skills/shares, receiving the Pack ID and current metadata/read-error context. Preserve existing page exports and RPC behavior. Check actual Pack/config revision semantics rather than assuming an attachment replacement increments both.

- [ ] Read protocol, server transaction validation and CLI review semantics; record absence of owner Web controls with a targeted failing browser check.
- [ ] Render real snapshot revisions, ordered attachment metadata, stale/unsupported states and read errors. Preserve previously read data but mark it unverified and disable writes on failure. Missing assets must not be silently discarded.
- [ ] Provide explicit edit mode: select eligible owner assets, add/remove and accessible move-up/down controls, maximum 16, no implicit initial selection. Preserve existing unsupported entries visibly; do not silently rewrite or drop them.
- [ ] Review old and proposed ordered references, origin/owner, Pack/config revisions, empty-clear consequences and the NativeUnavailable boundary in a Radix confirmation. Cancel performs zero writes. Re-read exact metadata before a single Replace; actual CAS handles intervening asset changes.
- [ ] Distinguish known permission/validation/conflict from Unknown, cancellation after dispatch and malformed replies. Never replay mutations. Keep the draft for inspection; offer only explicit read-only reconciliation after Unknown, and require a new deliberate edit/review before any later replacement. A matching result cannot establish which attempt committed.
- [ ] Handle Pack revision changes, deletion and switch, identity/logout changes and blocked queries without submitting stale captures or stealing focus. Restore only the affected dialog trigger, falling back to a stable heading when absent/disabled.
- [ ] Production build and real TLS/Connect/SQLite fixture cover CRUD/order/clear, other-owner refusal, asset/Pack/config CAS, dropped committed reply, cancellation, stale and unsupported metadata, no body/secret RPCs and unchanged skill/share behavior.
- [ ] Add meaningful negative terminal-gate tests (nonzero/signal/timeout/missing or contradictory receipts), keyboard/layout/motion checks and safe screenshots. Freeze eight-file patch/tar/hash/evidence and limitations for independent Spec/Quality review.

## Delivery Ledger

- Management-page review and configuration-overview implementation run on disjoint write sets. Main owns integration and public delivery.
- This closes only owner Web metadata management after review. Client consumption, opt-in grants, per-target preview/apply/restore and Pack auto-application remain separate required work for GA.
