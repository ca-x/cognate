# Configuration workspace UI — author verification

Date: 2026-11-01 (task date). Base: `e2003855c69e2e72e05962d011faa8f788abd733`.
This is the overview/shared editor shell slice, not Task4, independent acceptance, native qualification or GA.

## Scope and safety

Exactly eight source files: `web/src/configuration.tsx`, `web/src/configuration-order.tsx`, `web/src/configuration-order-i18n.ts`, `web/src/configuration-ui.css`, `web/src/configuration-ui-i18n.ts`, `internal/server/httpapi/configuration_ui_browser_fixture_test.go`, `scripts/verify-configuration-ui.mjs`, and this document. Work occurs in a private archive, without branches, worktrees, commits, child agents or main-tree writes.

Provider forms, state/codec modules, RPCs, shared locales/styles, dependencies and production Go remain unchanged. React/TanStack, existing Button/Input/Checkbox and Radix Dialog/Select remain the interaction foundations. The server-asset UI is not MyGo, does not apply configuration to devices and adds no native/import/MCP asset claims.

| Before | After | Why |
| --- | --- | --- |
| Flat metadata list | Agent groups, actual counts, format-support labels and separate destructive actions | Scan saved assets without implying current provider/device state |
| Dialog initially focuses language selector | Stable editor title receives initial focus | Long forms start at their actual context, including at 200% text |
| List-query failure could leave an existing editor writable | Fresh successful metadata gates writes, with explicit retained-draft status | Cached data does not authorize stale changes |
| Reordering always refocused a row after awaiting the server | Row focus is restored only while that subtree still owns focus | Background responses cannot steal another control's focus |
| Order reload replaced its focused subtree | Focus moves to the stable order title before explicit reload | No disappearing-trigger dead end |
| Narrow layouts split complete English words | Scoped wrapping, padding and checkbox-label reflow | Root text scaling must preserve complete words and reachable actions |

The list reads metadata only. Credential readiness appears only from an actual redacted draft response and is explicitly not a connectivity, native eligibility or applied-state check. Unknown agent/schema assets retain metadata-only browsing, ordering and explicit CAS deletion; preserved-unknown typed projections retain their existing provider read-only rules. Saving and copying do not enable a provider.

CAS, owner isolation, credential Keep/Replace/Clear, copy-without-secrets and Unknown classification remain in the existing domain/state code. No write is retried by this slice. Reconciliation requires explicit discard confirmation and a fresh read, including Claude. Metadata failure does not remount the open non-secret draft. Theme/system changes preserve it; language changes keep the existing secret-clearing/cancellation contract.

Motion is intentionally inherited, not duplicated: pointer press feedback (150ms transform) and occasional Radix entrance (200ms opacity/transform) use existing tokens. No layout animation, list shimmer, decorative loop, exit delay or motion library is added. Keyboard actions remain immediate; page-scoped reduced-motion also removes transitions from shell descendants. Focus recovery uses actual subtree ownership, never a response-driven unconditional focus jump.

## Reproduce

### Independent focus-fix1 closure

On 2026-11-01, the independent follow-up closed F1 and F2 for the frozen exact8
candidate. Both Chromium 143 and 156 reproduced the old candidate's 15 focus
failures and passed all 31 follow-up checks on independently rebuilt new bytes.
The unchanged original probe, 156's 80-layout smoke, external-focus/Unknown
checks, and seven terminal self-tests also passed within the reported scope.
The original BLOCK remains historical evidence, and the OpenCode ordering-copy
P3 remains open outside these provider-form changes.

Report: `/tmp/newton-exact8-review-Jy2tGoJj/FOCUS-FIX1-REVIEW.md`, SHA256
`b31bac8bac0915847d3c3dd1fa9e2d49375c21f774a7f0c100902ab2e9405182`.
Mainline integration initially matched all eight source hashes; this closure
note is a separate documentation delta. This is not whole-product UI, native
configuration-application, or GA acceptance.

### Focus fix 1 — incremental review

Laplace's original independent review BLOCK is retained, with F1 (pending save loses focus) and F2 (cancel reconciliation leaves BODY). This revision is an author fix for incremental non-author review, not an independent closure.

The five unchanged provider forms own native disabled/submitting behavior; changing those buttons to aria-disabled would require expanding the agreed write set. This shared shell instead synchronously hands a genuinely focused submitting form to its stable editor title before pending disables its controls. It does not rewrite DOM disabled state or replace provider actions. An explicit synchronous saving guard supplements the existing request single-flight/CAS/Unknown gates. A focus already outside the submitting form is not moved. No new production timer, global listener, dependency or retry is added.

Reconciliation uses a real Radix DialogTrigger, an explicit cancel focus target, and public close-autofocus events. Cancellation restores its original connected trigger only when the closing confirmation owned focus and no other connected control has since acquired it. A removed trigger falls back to the stable editor title.

The persistent `--focus-regression` round tests all five providers: actual pending transport, one real SQLite save despite repeated submission attempts, real external CAS conflict, Space-open with Escape/Enter cancel plus 250ms stability, retained draft, explicit latest-draft reconciliation, and external focus guards. Both Chromium 143 and 156 are checked. The fix runs only this round and the existing `--matrix-smoke`; historical 960-layout evidence remains historical and is not rerun or relabeled. The OpenCode ordering copy finding remains outside exact8 and unchanged.

```sh
node scripts/verify-configuration-ui.mjs --focus-regression \
  --fixture-bin /tmp/configuration-ui.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output /tmp/configuration-ui-focus-143
```

Repeat with `/opt/google/chrome/chrome` and a separate output directory for 156. Before the production fix, this same regression fails F1 on all five branches and F2 on both cancel paths in both browsers; successful fixture termination cannot turn that product RED into PASS.

The 143 smoke also exposed an existing offscreen pointer-actuation issue before either new focus path runs: the same click failed to focus Codex's input on both the prior and fixed UI, and an offscreen row click later failed to open its dialog. Those failed receipts are retained, not labeled product PASS or silently retried. The runner now explicitly focuses and asserts the intended input before real Control+A/Backspace, then still asserts the empty value and real form validation. Row buttons are focused into view before the same real pointer click. This changes only test preparation, not form state, a production control or protocol behavior; it does not claim that the old unprepared pointer path is fixed.

No installation is required. A private archive may symlink the baseline's existing `web/node_modules`.

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
export CGO_ENABLED=0
npm --prefix web run build
go test ./internal/server/httpapi -run '^TestConfigurationUIFixture' -count=1
go test -c ./internal/server/httpapi -o /tmp/configuration-ui.test
node scripts/verify-configuration-ui.mjs --self-test --output /tmp/configuration-ui-terminal
node scripts/verify-configuration-ui.mjs \
  --fixture-bin /tmp/configuration-ui.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output /tmp/configuration-ui-evidence
```

`--skip-matrix` runs the targeted business/focus round. `--matrix-smoke` additionally checks both languages, both actual system schemes and both motion settings at 320px/200% (80 layouts); it is not a substitute for the complete matrix. `--red` stops after the initial-focus regression assertion; compiling baseline production Web plus only the new fixture reproduces the original failure. The runner's `finally` block always evaluates terminal status even in that early-return mode.

The fixture serves the actual production `web/dist` through real TLS Connect handlers, authentication and physical encrypted SQLite. It does not replace successful business responses. Faults delay/refuse transport or execute the real handler successfully before deliberately dropping its reply. Synthetic credentials exist only in memory and encrypted database state; readiness contains origins and a private control capability, not a password or session. Private control requests reject missing capability, Origin and query strings. The runner never prints response bodies, credentials or browser command arguments.

The terminal gate requires zero exit, no signal, an explicit stop receipt, the exact Go test PASS and no contradictory FAIL/panic/race receipt. Persistent self-tests use real positive/nonzero/signal/missing-stop/missing-PASS/contradictory/timeout child processes. A timeout kills the fixture and fails the run; a partial browser pass cannot override termination failure.

## Evidence contract

The delivered report identifies the original initial-focus RED, subsequent real layout REDs, final production build, targeted Go results, terminal self-tests, receipts and safe screenshots. The final browser matrix covers Chinese/English × light/dark/system (both actual system schemes) × 320/375/1440 CSS px × 16/32px actual root font × both reduced-motion settings. It checks overview, expanded boundaries, all five supported provider editors, ordering, copy and delete shells. It verifies complete English words, text-range bounds and each surface's own scroll width, not only document overflow. Actual document language and motion media state are asserted. Tab/Space/Enter/Escape are real browser keys, and narrow 200% reduced-motion editors tab to the save action and verify its visible bounds.

Business evidence separately exercises all five provider branches at open/edit validation; create, edit, copy, delete, complete-group ordering, cancel recovery, failed-query draft retention/write blocking, CAS conflict, committed-but-lost response and explicit no-replay reconciliation. Targeted Go evidence verifies real owner isolation and credential Replace/Keep/Clear/copy behavior. These are distinct from claims about every optional provider field or native consumption.

Before every screenshot, including failures, the runner refuses nonempty password inputs. No real account, provider key, token, raw production log, readiness file, database, HOME profile or fixture executable belongs in the evidence tar. Only synthetic non-secret screenshots, structured receipts and sanitized test/build output are delivered.

## Boundaries and findings

- Author self-review is not a non-author Spec/Quality decision. The main thread must review/integrate the frozen patch independently.
- The existing OpenCode form still says provider-list persistent ordering awaits a backend slice, even though shared ordering exists. This specific stale copy is inside `configuration-opencode-form.tsx`, outside the agreed write set. It was reported; it is not hidden with CSS or edited here.
- Provider-specific optional/nested editing, raw-format parity, every credential mode in every browser, all fourteen agents, native applications/MyGo/IME, Pack consumption/imports, external endpoints, human screen-reader review and other browser engines remain separate gates. Passing this shell matrix does not close them.
- The fixture exercises production HTTP/domain/store business, not the full server App worker/startup lifecycle. The report separately lists the targeted existing order lifecycle test; it does not establish full-App lifecycle coverage for this new fixture.

Final review: author self-review (no child agents permitted). The delivery report supplies actual counts and hashes rather than treating this document as a passing receipt.
