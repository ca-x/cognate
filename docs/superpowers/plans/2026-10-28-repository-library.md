# Repository Library Implementation Plan

> **For agentic workers:** Execute inline using `executing-plans`; the existing independent user representative reviews the completed change.

**Goal:** Connect F2 public GitHub repository collection to the Web workspace, persisting a validated whole-repository archive and its scanned skills. This is one step toward the full first release, not a reduction of its scope.

**Architecture:** Fetch only from GitHub's fixed archive host, bound compressed/expanded sizes, inspect an isolated Afero filesystem, then publish an immutable content-addressed archive before an ent transaction inserts repository and skill metadata. A failed fetch/scan cannot create a collected repository. Connect authenticates every operation; React distinguishes collected content from Lists metadata.

**Tech Stack:** Go 1.27.3, no cgo, ent/entsqlite, Afero, Echo/Connect/Buf, React/TanStack Query.

**Spec:** `docs/REQUIREMENTS.md` F2/F5/F7 and §5.

## Global Constraints
- User repository selection is an owner/name or GitHub HTTPS URL and an optional branch, never an arbitrary server filesystem path.
- Only server code fetches archives; clients never receive GitHub credentials or execute import plugins.
- No commits, branches, or publication in this change; preserve the existing uncommitted workspace.
- Start with public archives. Private repository acquisition and Lists acquisition workers remain explicit follow-up work.

## Review Focus
- Unsafe archives: traversal, duplicate entries, links, truncated gzip, excessive entries or expansion must not escape the isolated filesystem or create metadata.
- Cancellation and shutdown: in-flight network work must cancel and finish before storage closes.
- Ownership: one account cannot view another account's collected repositories or skills, including guessed IDs.
- Repeated collection: owner/name casing and default HEAD normalize to the same repository; no duplicate skills are inserted.
- UI: failed validation keeps input and shows an actionable error; pending work is visible; mobile and keyboard users can browse the real skill list.

## Tasks
- [x] Archive pipeline: write failing validation/limits/network tests; implement fixed-host retrieval and bounded Afero extraction, metadata scanning, and per-skill hashes; pass targeted tests.
- [x] Persistence: add ent repository/skill schemas and a confined Afero archive cache; test account isolation, restart persistence, duplicates, no empty repositories, and canceled writes; pass targeted tests.
- [x] Connect: add Buf-managed collection/list/detail RPCs; wire lifecycle and authenticate handlers; test validation before insertion and cross-account isolation.
- [x] Web: add a real collection form, collected repository/skill browser, and separate Lists discovery metadata; preserve the current visual system, labels, live feedback, and reduced-motion behavior.
- [x] Verify: regenerate and compare schemas, build Web, run focused then full Go tests/vet, browser exercise the flow, and request independent representative review; record remaining release gaps.

## Execution Ledger
- 2026-10-28: inspected current Web routes and requirements. Existing pages are login, sources/Lists, repository metadata, and account/OIDC. MyGo GUI and pack/install/import/backup UIs are not complete.
- Nowledge CLI search: query `SkillHub 续接 GitHub 设备授权 OIDC 登录绑定`, normal request/server mode `fast_bm25_vector`, configured scope `default`, filter `-n 2`. Rank 1 `99073a0e-6328-40bf-9f97-211e1222fc71` (SkillHub 续接：GitHub 设备授权与 OIDC 登录绑定已接通), score 0.99; rank 2 `d88aa953-3f10-4a7b-a155-cf853c79fd86` (SkillHub M3 续接：GitHub device flow 与 Lists 同步内核), score 0.8204431700416789. No graph opened: CLI cannot confirm browser owner/space authorization.
- Archive and state tests first failed for absent behavior, then passed. A real GitHub archive exposed the `pax_global_header` compatibility issue; a regression failed before accepting only GitHub's comment header. Nonzero tar/gzip tails now fail inspection instead of being discarded.
- Browser collection of `vercel-labs/agent-skills` produced 9 real skills; a no-skill public archive was rejected without adding a record or clearing the form. Repeated case/HEAD collection kept one repository; server restart retained its data. Skill name/description/path filtering works.
- Browser inspection found squeezed Chinese mobile navigation; reduced padding and a narrow-screen wrap preserve 44px targets. 320/390px had no horizontal overflow; axe A/AA reported zero violations and zero incomplete checks. A corrupted QA archive showed `data_loss` and no skill cards; restoring it and retrying restored 9 cards.
- Independent review found malformed global PAX acceptance, missing archive success, unconstrained abandoned cache growth, a different-hash concurrent return bypass, and mismatched shared budgets. Added red→green regressions, final-record verification, bounded shared storage, and persisted budget fencing. Final representative replay passed the differing-hash race three times and real child-process budget races five times; no new reproducible issue in that targeted scope. This is not whole-product acceptance.
- Ruling: preserve safe crash orphans rather than delete potentially referenced content; include them in the persistent byte budget. Automatic GC/repair is later work, not claimed here. The first initialized budget binds the cache directory; online budget changes are not implemented.
- `crapper`, `dryer`, and `mutator` are unavailable; no output from those tools is claimed. A small 10-skill/binary benchmark measured about 0.40ms/op and 1.53MB/op on this machine; this is not a large-repository load test.
- Final local Web build/audit, Go test/vet/mod verification, Buf lint and Go/TS generated comparisons, and CLI/server Linux/macOS/Windows amd64/arm64 no-cgo builds passed. Latest browser also collected explicit `main` as a separate snapshot and retained 9 skills, with 320/390px axe A/AA zero violations/incomplete checks. Docker, remote CI, non-Linux native execution and full release acceptance remain unverified.
- Follow-up memory duplicate check used nmem CLI query `SkillHub 公开仓库收录 Web 技能浏览 F2`, normal request/server mode `fast_bm25_vector`, scope `default`, filter `-n 2`. Rank 1 `d997ef30-2bb3-4b00-a97c-1cf71e743185` (SkillHub 需求文档全文(跨机器版,交付Codex)), score 0.9785840210020279; rank 2 `99073a0e-6328-40bf-9f97-211e1222fc71` (SkillHub 续接：GitHub 设备授权与 OIDC 登录绑定已接通), score 0.8206400852761087. No duplicate of this new delivery; no graph opened for the same CLI browser identity limitation.
