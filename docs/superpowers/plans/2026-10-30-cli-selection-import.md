# CLI Selective Import Implementation Plan

**Goal:** Let CLI and native clients consume the existing three-category import
protocol without executing plugins or persisting source data or tickets.

**Spec:** `docs/IMPORT-SELECTION-PROTOCOL.md`, `docs/IMPORT-SELECTION.md` and
REQUIREMENTS F6/F12. This slice does not migrate source secrets, copy local skill
modifications, apply client configuration, or close the fourteen-agent gate.

**Architecture:** Extend the existing importclient package with an independent
selection client. A preview owns a private copy of its ticket/options and source
hash, but not source bytes. Selection defaults empty. Commit rereads the regular
file and verifies its hash before its sole dispatch. It returns a random operation
ID before dispatch; an ambiguous response can only be followed with Status.

## Constraints
- Reuse generated Connect messages, no new protocol/dependency/server plugin code.
- HTTPS or explicit loopback HTTP, no redirects, bounded requests/responses and
  deadlines; cc-switch source at most 16 MiB, Magpie at most 1 MiB.
- Session only stdin/in-memory. Format/debug/JSON of client and preview must not
  expose session, ticket, options or private source data. Clear owned buffers.
- Preview/Commit bind one client instance/session, source, source hash, options,
  candidate order and notice IDs; caller mutation must not change the binding.
- Only READY candidates selected; reject duplicate/out-of-range indices and an
  all-three-empty selection; individual groups may be empty. Reject
  expired/closed/used previews and missing explicit notice acknowledgements.
- Reuse a dispatched preview is forbidden, including after an unknown outcome.
  Status is read-only and cannot implicitly reupload, commit or install.
- Source repository candidates and skills are independent: an empty source
  repository-candidate ID is allowed, but a nonempty ID must resolve in this
  preview. READY still requires the exact complete current-owner skill binding.
- Separate nonqueued heavy/observer lanes allow Status during Commit. Preserve
  identity encoding, Preview Magpie5s/snapshot15s, Commit120s, Status5s;
  Status wire request64KiB/response128KiB, Preview/Commit send24MiB and Preview
  response2MiB, bounded before aggregation/decoding. Do not broaden legacy APIs.
- Validate response identity, enum/presence/shape and all returned receipt pins;
  receipt consistency is not proof of target installation.
- Synthetic HOME/files/SQLite/loopback only; old Magpie consumer unchanged.

## Tasks
- [ ] Write selection client tests: seven nonempty combinations, exact request
  source/options/IDs, default none and notice rejection before Commit.
- [ ] Observe ordinary behavior RED with compiling stubs; implement bounded file
  and transport adapters and immutable preview/selection validation.
- [ ] Implement one-shot Commit and Status. Test lost response, foreign client,
  mutation, expired preview, changed file, cancellation and redacted errors.
- [ ] Add CLI selection/status commands with explicit file/hash/indices/notices,
  no default selection and operation-ID output before dispatch.
- [ ] Test actual command and actual TLS/Store roundtrip; malformed/mismatched
  receipt responses must remain unknown, never successful or automatically retried.
- [ ] Run focused tests/vet/old-client regression/CGO0 cross-build, non-author
  review, then commit/push. Keep original RED and all unproved gates OPEN.
