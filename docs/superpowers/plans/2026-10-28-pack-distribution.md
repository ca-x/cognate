# Pack Distribution Implementation Plan

> **For agentic workers:** Execute inline with `executing-plans`; the existing independent user representative reviews security-sensitive changes.

**Goal:** Make collected skills usable as cross-repository packs with owner management, signed/revocable/expiring sharing, manifests, and server-only binary delivery. The full first-release goal, client synchronization, configuration attachments, backups, and native GUI remain required.

**Architecture:** Normalized ent pack/item/share records use user ownership checks and revision CAS. Share credentials are opaque random capabilities authenticated with a domain-separated HKDF/HMAC key and stored only as hashes. Distribution resolves membership and the current immutable repository snapshot, then streams a bounded deterministic skill archive through Connect; no client request selects server filesystem paths or upstream URLs.

**Tech Stack:** Go 1.27.3 without cgo, ent/entsqlite, Afero, Echo/Connect/Buf, React/TanStack Query.

**Spec:** `docs/REQUIREMENTS.md` F5–F7, §5–7.

## Global Constraints
- Respect existing uncommitted work; do not commit, branch, or publish this partial milestone.
- Plugins run only on the server. Sharing and downloading must never forward tokens to GitHub or include them in URLs/logs.
- Pack mutations are owner-authenticated. Distribution uses a separate share Bearer credential and never accepts a local path.
- Bounded requests, streams, metadata, archive expansion, concurrency and shutdown are required; no cgo/race dependency.

## Review Focus
- A user must not compose or manage another user's skills, packs, or shares, including guessed IDs and concurrent mutations.
- Revoked/expired/forged shares and removed or changed skills must not authorize a new resource transfer; in-flight bytes already sent cannot be recalled.
- Same slug/hash may be deduplicated; different content under the same portable slug requires an explicit alias, not silent overwrite.
- Binary files, executable bits, hidden paths and content hashes must survive the deterministic archive representation; path traversal, malformed/truncated transfer and wrong offsets/hashes must fail.
- Raw shares are returned only on creation and kept out of browser URL/storage/query cache, formatting, diagnostics, and subsequent lists.

## Tasks
- [x] State: write red tests for pack CRUD, user isolation, CAS, slug conflicts/dedup and share lifecycle; add ent schemas/atomic methods and pass them.
- [x] Sharing: implement domain-separated signing, redacting credentials, persistent hash/expiry/revocation, and manifest construction; test forgery, expiry, revocation, ownership and stable manifests.
- [x] Resources: add bounded deterministic skill archive writer, authenticated membership/hash resolution and synchronous Connect chunk streaming with offsets/final digest; test binary round trip, cancellation and mid-stream revocation.
- [x] Web: add pack selection across collected repositories, aliases, edit/delete, share creation/list/revoke and one-time credential display; preserve the established visual/accessibility/motion system.
- [x] Verify: focused then full no-cgo tests/vet, Buf generated comparisons, Web build/audit, cross builds, browser flow and independent representative review. Record current limitations and continuation in docs/Nowledge.

## Execution Ledger
- 2026-10-28: prior turn is progress: public repository acquisition, persistent content and actual Web skill browsing are present and verified; the program is not released. This plan advances F5 rather than polishing that completed subset.
- Implementation starts locally with schema/state because all later steps depend on its transaction/ownership contract. Independent representative reviews adversarial token/stream cases in parallel, then receives completed code for replay.
- Implemented the state, signing/manifest, bounded deterministic archive, Connect/Buf, and Web pack paths. State and service/RPC tests started red on missing implementations, then passed. A duplicate token digest in the initial lifecycle fixture was corrected to use a fresh nonce/digest instead of weakening the unique constraint.
- Browser verification: two real public branch snapshots, 3 selections → 2 manifest items, transfer gzip/offset/final/SHA256, live revision following, stale revision rejected without frames, concurrent-edit input preservation, revoke/delete → 401, and 320/390px axe A/AA 0 violations/0 incomplete.
- First independent review: 256 maximum-field JSON create/update/manifest, cross-Store CAS/fault rollback, durable signing/revocation, selected archive/hash, 20-second real backpressure cutoff and App.Close passed. Two P2 findings remain under repair: authorization query crosses expiry; protocol-derived short Connect deadline is not bound to the response write deadline. Do not mark this plan accepted until red→green regressions and representative replay are complete.
- Both representative regressions were retained and independently observed red locally, then green after query-return expiry rechecks and protocol-context read/write deadline binding. HTTP callbacks remain installed until the whole response/trailers finish. Follow-up representative replay includes half-body shutdown and normal deadline-bearing keep-alive requests; acceptance is still pending that replay.
- Lifecycle replay added a third red→green fix: normal protocol-context cancellation poisoned a reused connection (second response 503). Centralized deadline coordination restores the outer deadline on normal completion, while real expiry or outer cancellation always wins under a mutex. The retained half-body, keep-alive, HTTP/1 short deadline and HTTP/2 flow-control expiry/Close cases passed five local repetitions; representative final replay remains the last gate.
- Final representative replay passed: expiry and short-deadline regressions, partial-body shutdown and HTTP/2 flow-control expiry/Close each passed ten repetitions; the final keep-alive replay passed ten repetitions in 1.403s. All three red→green regressions remain in the tree. No blocking finding remains for this scoped server/Web plan.
- Final local verification exited 0: all no-cgo Go tests, vet, module verify/tidy diff, Buf lint and Go/TS generation comparison, Web production build and production dependency audit (zero vulnerabilities), and CLI/server Linux/macOS/Windows × amd64/arm64 cross builds. QA browser/server and temporary credentials/data/build artifacts were cleaned up.
- This sub-plan is complete, not the full F5 or M4 milestone: client subscriptions/install/sync and cross-pack deduplication remain. Native GUI, real external authorization, Docker/remote CI and native non-Linux execution remain unverified.
