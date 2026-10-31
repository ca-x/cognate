# Pack configuration attachment Web management

## Scope and contract

The owner control lives in `/packs`, below the existing skills and shares. It uses the authenticated production `PackConfigurationService.GetAttachments`, `ReplaceAttachments` and `ConfigurationService.ListAssets`, with the existing React/TanStack cache, Radix Select/Checkbox/Dialog and design tokens. There are no new dependencies, protocol fields, server handlers or database migrations.

This is **private server attachment metadata management**, not configuration consumption. Every target remains **NativeUnavailable**. Saving does not read a draft/body/key, change a provider, install anything, grant a device permission, expose configurations in shares, or automatically apply to an agent. The fourteen allowed schema-1 provider targets are metadata choices, not native qualifications. This does not close F5/F12, MyGo parity or GA.

## Owner workflow

1. Read the Pack and attachment/configuration revisions and ordered references. The metadata query combines an attachment snapshot and the owner's asset metadata. It never calls ReadDraft, Preview, Reveal or asset mutation RPCs. Snapshot structure, unique identities/pairs, ordinals, positive revisions and the server SHA-256 digest are checked before use.
2. Enter explicit edit mode. Nothing is preselected. Add eligible owned schema-1 providers, remove references, explicitly use an updated asset revision, or move entries with named up/down buttons. At most 16 distinct asset/target pairs are allowed. Existing stale, unsupported and missing-asset references stay visible and pinned; they are never silently upgraded or discarded. The server remains authoritative for ownership/target validation.
3. Open the Radix review to inspect origin, current owner, Pack ID, both revisions, complete old and proposed ordered references, metadata-only/NativeUnavailable boundaries and the empty-list consequences. The confirmation checkbox starts unchecked on each review. `[]` clears references only, not skills, shares or configuration assets. Cancel before dispatch sends no Replace.
4. Confirmation reauthenticates the pinned owner, rereads the exact attachment metadata (including digest and states), and issues at most one Replace with both captured CAS revisions and explicit asset revisions. There is no preview RPC and no retry policy for writes. An asset/Pack/configuration race after the reread is refused transactionally by the existing server.
5. A successful receipt must contain the correct Pack ID, exact ordered references, current states, unchanged retained attachment identities, expected metadata and a valid digest. **Both Pack revision and configuration revision increment by one** in the existing transaction. Only then is success displayed and the Pack cache invalidated. Skill/share RPC behavior remains unchanged.

## Refusal, uncertainty and cancellation

Explicit, bounded, well-formed server JSON wire refusals are distinguished from transport/decoding failures by the dedicated Pack transport, not merely by `ConnectError.code`. Permission/not-found, validation/unsupported and revision conflicts have fixed, distinct messages; server diagnostic text is never displayed. Missing/bad success snapshots, malformed error envelopes, failed reads after dispatch, lost committed replies and cancellation after dispatch are **Unknown**, never assumed rollback or success.

Unknown preserves the proposed ordered metadata for inspection, disables mutation controls and offers only explicit read-only reconciliation. A matching current snapshot cannot identify which attempt committed. Reconciliation does not resubmit, resurrect a confirmation or replace the draft; a subsequent write needs a deliberate new edit and complete new review. Known refusals also require a fresh deliberate review. Closing the busy dialog aborts waiting; it cannot undo a server commit. Pack changes, removal, read errors and identity/logout changes invalidate captured authority. Unmount aborts active work and prevents late UI updates.

Cached metadata and drafts survive read failure, but are labelled unverified and cannot authorize writes. Background rendering never focuses a control. Radix close restores only that dialog's review trigger if still enabled/present, otherwise its stable attachment heading. The review heading receives initial focus, so long content does not put focus behind a scroll boundary.

## Visual and accessibility direction

The existing precision-console design is retained: neutral layered surfaces, quiet metadata typography, cold accent for actions and positioning, and separated review/error states. Scoped CSS uses existing `--color-*` tokens and no downloaded fonts. The attachment section has one job: show authoritative status, then enable an explicit ordered edit. Existing modal/press feedback is reused; keyboard and reduced-motion input do not need animation. No decorative progress, application status or invented native capability is shown.

Verification uses actual 200% root text (computed 32px), 320/375/1440 CSS-pixel viewports, Chinese/English, light/dark/system theme, real Tab/Space/Enter/Escape, dialog focus restoration and reduced motion. Screenshots contain only synthetic owner IDs and metadata, never share tokens, sessions, bodies or credentials.

## Reproducible narrow verification

Run from the private archive root, not the concurrently modified integration checkout:

```bash
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
npm --prefix web run build
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o /tmp/pack-configuration-fixture.test
node scripts/verify-pack-configurations-ui.mjs --self-test
node --check scripts/verify-pack-configurations-ui.mjs
node scripts/verify-pack-configurations-ui.mjs \
  --fixture-bin /tmp/pack-configuration-fixture.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output /tmp/pack-configuration-browser-evidence
```

The opt-in Go fixture starts the real TLS/Connect handler, auth service, library/Pack domains and encrypted SQLite store. The private loopback control capability is never sent to the browser; readiness is mode 0600 and removed after execution. Upstream skills and accounts are synthetic. Faults surround real requests: committed response loss, malformed receipts, delay/cancel, owner refusal, missing metadata and Pack/configuration/asset races. Unsupported history is seeded in the fixture's own SQLite attachment row after an asset format change; production state/protocol code is unchanged. No secret body is requested to construct the metadata tests.

The browser terminal gate requires both a clean process exit and complete, noncontradictory receipts. Self-tests reject nonzero exits, signals, timeouts, missing and inconsistent receipts, including real abnormal Node subprocesses. A fixture PASS alone is not a browser PASS. Full-repository and cross-platform/18-target builds are intentionally not repeated.

## Recorded acceptance and author review

- The final `frozen-browser/receipts.json` records 28/28 business receipts, no failure, and fixture exit `{code: 0, signal: null}`. This includes real committed response loss, malformed success/error receipts, duplicate JSON fields, malformed error details, cancellation on both sides of dispatch, and no replay during read-only reconciliation.
- Its 36 geometry receipts cover Chinese/English, light/dark, 1440/375/320 CSS px and editor/review/keyboard-revealed controls, all at computed 32px root text. They record no page/control overflow or split prose words. System-theme changes and reduced-motion/keyboard opening are separate behavioral assertions. Technical identifiers may wrap; candidate combobox text is not part of the prose-word assertion.
- The terminal self-test rejects eight negative exit/receipt cases and five real abnormal Node subprocesses. Narrow Go logs record 59 passing tests/subtests with one opt-in fixture skip, plus 27 passing attachment-protocol tests/subtests with no skips. Neither count is a claim of full-repository qualification.
- Genuine RED precedes implementation: `red-targeted` logs a real login and Pack detail lacking owner attachment controls. Later `duplicate-red` and `details-red` reproduce committed writes wrongly classified as known refusals; the final transport rejects those ambiguous envelopes as Unknown. `layout-red-metrics-2` records narrow/200% word-splitting before the scoped layout correction. Earlier fixture-directory/login setup failures are not the targeted RED.
- Author self-review compares the exact eight-file change with `e2003855c69e2e72e05962d011faa8f788abd733`, checks the existing transaction and snapshot digest, reviews authority invalidation, single-dispatch/cancellation and read-only reconciliation, and inspects the metadata-only RPC/storage assertions and safe final screenshots. No unresolved write-set or protocol-semantic blocker was found. This is not an independent review.

## Delivery ledger and limitations

- Isolation: `mktemp` + `git archive e2003855c69e2e72e05962d011faa8f788abd733`; only the existing `web/node_modules` is linked. No shared source edits, branches, worktrees, commits or subagents.
- Exact eight files: `web/src/api.tsx`, `web/src/packs.tsx`, `web/src/pack-configurations.tsx`, `web/src/pack-configurations-i18n.ts`, `web/src/pack-configurations.css`, `internal/server/httpapi/pack_configuration_ui_browser_fixture_test.go`, `scripts/verify-pack-configurations-ui.mjs`, this document.
- RED: baseline production build and genuine TLS/SQLite browser login/Pack detail fail specifically because owner attachment management is absent. Fixture-directory privacy and login-selector setup failures are retained separately, not presented as the targeted RED.
- GREEN and later regression evidence: use the frozen delivery's command log and receipts. Intermediate failed browser runs are preserved rather than overwritten. The final receipt, not this description, is authoritative for the actual completed matrix.
- The original exact-eight author assignment forbids author-spawned children; it does not replace the separate main-thread independent review. The first independent review is recorded below rather than described as an author acceptance.
- Recorded tools: Go 1.27.3 (`CGO_ENABLED=0`), Node 26.13.0, agent-browser 0.39.1 and Google Chrome 156.0.8078.79. The Node patch version differs from the plan's 26.12.0; no dependencies or lockfiles are changed.
- Runtime qualification is local Linux/Chromium with synthetic loopback TLS and browser certificate-ignore, not public CA deployment, cross-browser, real touch/IME, human screen reader, MyGo, native configuration application, outside providers, macOS/Windows, cgo/race or 18-target evidence. Offscreen candidate selection uses actual Home/ArrowDown/Enter, not a claim that every candidate was pointer-tested.

## Main integration: PC-F1 (2026-11-01)

Independent Spec/Quality review initially **BLOCKS** on PC-F1/P2: reload, move-to-boundary and removing the focused reference lose focus to BODY in Chromium 143 and 156. Protocol/isolation checks pass but do not override that finding. The original report is `/tmp/cognate-pack-configuration-independent.kgyP2Dpb/work/REVIEW.md`, SHA256 `252db0853721959729a78e48076eda6e953e0ae789a284a0a37fd19c4851053c`.

Main first adds a permanent `--phase focus` regression and runs it against the author's frozen old production fixture. It fails with `reload lost focus after its read completed`, while the fixture itself exits cleanly. The fix keeps reload focusable with `aria-disabled` and an explicit read guard. Synchronous draft actions remember only a trigger that actually owns focus, then use the React layout lifecycle to restore the same logical reference's available action, its successor, asset selector or stable heading. External focus is not stolen; no timer, document listener, request replay or business protocol changes are added. Add/clear/discard receive the same lifecycle treatment.

Fresh production Web and Go fixture builds pass. Both Chromium 143 and 156 then pass ten focus receipts, including external-focus retention and zero replacement writes; each fixture exits `0` / no signal. Evidence is `/tmp/cognate-npx-selection.TB9Vd2tL/pack-focus-{red-156,green-143,green-156}`. These are narrow focus checks, not a rerun of the author's complete business/layout matrix.

The subsequent non-author review independently builds the candidate and passes ten focus checks in each browser, including external focus, add/clear/discard and no replacement writes. **PC-F1 CLOSED; limited Spec/Quality ACCEPT**, with the original BLOCK retained. Report `/tmp/cognate-pack-configuration-independent.kgyP2Dpb/work/REVIEW-PC-F1.md`, SHA256 `255b8423c86328d23f3f669b514ff3e3042ecc28037dfdbc90cb453547f8e9d4`. A nonblocking P3 remains: two synchronous explicit reload activations can send two read groups before React reflects fetching. The current guard must not be described as strict synchronous single-flight; no business write is observed. This is not native consumption, full F12 or GA acceptance.
