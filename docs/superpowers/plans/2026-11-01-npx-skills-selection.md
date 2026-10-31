# npx Skills Selection Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the required npx skills source to the existing server-side plugin selection import, with real owner Web and CLI entry points.

**Architecture:** Project a bounded, explicitly uploaded global lock into safe repository/skill metadata, run it through an isolated moejs module and the existing selection ticket/receipt pipeline. Add one protobuf enum value with Buf; keep existing cc-switch/Magpie semantics and database schemas unchanged. CLI/Web reuse their established explicit choices, acknowledgments and Unknown reconciliation rather than implementing another importer.

**Tech Stack:** Go 1.27.3, ent/entsqlite, server-only moejs, existing Connect/Buf, React/TanStack/Radix. `CGO_ENABLED=0`; no new dependency.

**Spec:** `docs/REQUIREMENTS.md` F6, `docs/IMPORT-SELECTION.md`, `docs/PLUGIN-IMPORT.md`. Format evidence: official `vercel-labs/skills` commit `92a9a34ac3bf88e695e468b90c7bf32f163d32c8`, `src/skill-lock.ts`; fetched read-only via GitHub API. The current global schema is version 3; input is never executed or allowed to name a server filesystem path.

## Global Constraints

- Preserve all fixed stack, repository retrieval, canonical skill, no-cgo and server-only plugin decisions. No client moejs or implicit file discovery/upload.
- Support explicitly chosen v3 global lock JSON, maximum 1 MiB and 1000 skills. Reject unknown versions, malformed/truncated/duplicate JSON and ambiguous source declarations; never copy upstream's behavior of silently wiping old locks.
- Retain the existing selection protocol's 128-candidate/16-repository batch cap. A larger supported GitHub candidate set is rejected explicitly with the safe capacity error, not truncated or allowed to fail later in response mapping. The 1000-entry source cap also covers excluded providers.
- Only `github` entries create candidates. Accept the documented `source_type` spelling and current upstream `sourceType`, rejecting disagreement. Other providers are excluded with an aggregate notice, not treated as GitHub or exposed as local paths.
- Validate canonical owner/repo and ref through existing repository code. Optional source URL must be HTTPS GitHub, without userinfo/query/fragment and consistent with the repository; no network use of arbitrary lock URLs. Skill paths are repository-relative, bounded and never interpreted as host paths.
- Lock tree hashes are not Cognate skill/archive hashes. Skills require the existing explicit owner content binding and content-not-compared acknowledgment; no fabricated imported content, downloaded skills, credentials or client configuration.
- The lock accepts folder or `SKILL.md` paths; normalize file paths to Cognate's scanned logical **directory** (`SKILL.md` at repository root becomes `.`), never append `SKILL.md` to a Cognate binding. The real SQLite/archive test must cover this boundary.
- Unknown source fields and non-content metadata must be visibly excluded or unsupported, never silently treated as a supported configuration. Raw input is private, bounded, cleared by existing lifecycles and absent from logs/caches/screenshots.
- Preview has no persistent side effect. Commit uses existing pinned source digest/ticket/owner/options, explicit selections, idempotent operation receipt and no automatic business replay after Unknown.
- Work on current main without new branch/worktree; do not touch the configuration-overview or Pack-attachment workers' write sets. No API-context or Pack-page changes.

## Task 1: Server Projection And Plugin

**Files:** Modify `internal/server/importer/selection.go`, `internal/server/importer/selection_service.go`; add `source_npx.go`, `source_npx_test.go`, `npxskills.mjs` within the same package, and a narrowly scoped npx selection service test if needed.

**Necessary state boundary:** Extend `internal/server/state/import_selection.go` and add `import_selection_npx_test.go` for source profile 3. Keep existing profiles 1/2 and the ticket format/size unchanged; npx must not borrow Magpie's sealed source identity. Give global npx exclusions their real metadata/other-provider/unknown-section notice categories.

**Host identity regression:** The existing `selection_service_test.go` canonical host tests must cover the new parser's actual compiled bytes. Add `source_npx.go` to the host identity manifest and independently recompute fixed literals for all three source bindings. cc-switch/Magpie JavaScript digests remain unchanged; their host-bound identities necessarily change, invalidating old preview tickets rather than accepting changed semantics under an old host identity. Existing operation receipts remain queryable.

- [ ] First write a valid v3 projection test using the literal new source identifier, observe the existing unsupported-source RED, then add the bounded parser and embedded server-only module.
- [ ] Cover GitHub dedup/ref/path, other-provider exclusion, both type spellings/conflict, unsupported versions, duplicate keys, malformed/credential-bearing or mismatched URLs, invalid Unicode/paths, limits and cancellation. Preserve metadata-only/explicit-binding behavior and deterministic IDs/digests.
- [ ] Extend existing source admission consistently in projection, preview and commit; keep cc-switch/Magpie adapter digests unchanged. Exercise real moejs and real SQLite owner preview/commit/status without mock successful business responses.

## Task 2: Protocol And CLI

**Files:** `proto/skillhub/import/v1/import.proto`, generated Go/TS import files; `internal/server/httpapi/imports_selection.go`, `imports_selection_options_test.go`, `imports_selection_protocol_test.go` (retain permanent enum ledger and add only value 3) and a new npx-specific HTTP test; `internal/importclient/selection.go` and its tests; `internal/cli/import_selection.go` and its tests.

- [ ] Add only `IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK = 3`, preserve existing values and methods, generate with Buf and verify both templates/parity/breaking compatibility.
- [ ] Extend bounded source mapping and client admission, including tests that used 3 as an unknown future enum. Add `--source npx-skills` without changing controlling-terminal approval, private-file, session or Unknown behavior.
- [ ] Validate real authenticated TLS preview/commit/status, cross-owner denial, source drift, cancellation and safe errors; ensure no plugin/server dependency enters CLI production closure.

## Task 3: Owner Web And Delivery

**Files:** `web/src/import-repositories.tsx`, `web/src/import-selection-form.tsx`; new local `web/src/npx-import-i18n.ts`, npx browser fixture/script if existing fixtures cannot exercise the new branch, `docs/NPX-SKILLS-IMPORT.md` and relevant progress ledgers.

- [ ] Add the new source only to the modern selection UI. Keep the legacy two-source preview route unchanged; avoid ternaries accidentally routing npx to cc-switch.
- [ ] Default all groups unselected, show version/size/GitHub-only and explicit-binding boundaries, preserve draft/owner/Unknown cleanup and no raw-input persistence. Existing mature controls and Chinese/English translations only.
- [ ] Production Web/real TLS/SQLite browser exercise file choice, preview, repository selection/commit/status and empty/excluded/invalid input. Check keyboard/focus and new labels at 320/375px with 200% root text, both themes/languages; guard screenshots against secrets and verify fixture terminal outcome.
- [ ] Freeze source identity and evidence, obtain independent Spec/Quality review, fix material findings, commit and SSH push this stage and verify remote/CI. Record actual progress in Nowledge.

## Explicit Remaining Scope

This slice does not close all F6: safe compatible write-back to a user's actual global lock, generic canonical-directory import, MyGo selection parity, third-party plugin management and the remaining source configuration matrix still need implementation. No implicit local lock write or invented installation provenance is introduced; the complete formal-release goal remains unchanged.
