# Web private MCP connection configuration assets

## Scope

Open **AI configurations → Manage private MCP connection assets**, at
`/configuration?section=mcp`. This is not the website MCP management-token page.
It uses the existing owner-authenticated `MCPConfigurationService` and existing
configuration-order service; no protocol, schema, host permission, dependency,
native application, backend production implementation or credential store is added.

The editable format is the existing Claude MCP schema 1. The editor handles
ordered stdio, HTTP and SSE declarations, explicit enabled/disabled state,
ordered argument/environment/header references, and the distinction between an
absent collection and a declared empty collection. Canonical references follow
the existing RPC/CLI contract. Changing IDs or names changes references; it never
silently relocates credentials. Unsupported schemas or preserved extras stay
read-only, without conversion. Unknown schemas expose metadata rather than an
attempted payload decode.

Saving, arranging assets and logical preview do **not** connect to an MCP server,
test credentials, execute a command/plugin, select an active provider, or apply
anything to a device. Preview reports existing server planner operations and
credential readiness, including disabled declarations and insecure-HTTP warnings.
It never grants native application qualification. This page does not complete
all F12 agent/types, device operations or GA qualification.

## Owner operations and credentials

- Listing reads owner-only metadata, not all editable payloads. Opening an editor
  reads a fresh sanitized draft bound to its displayed metadata revision.
- Create/edit requires a server logical preview, followed by approval of that
  exact draft and credential-action version. Any change invalidates approval.
- **Keep** preserves the existing slot; **Replace** supplies a new value, including
  an explicitly empty value; **Clear** requires an explicit per-reference
  confirmation. Every removed old reference requires Clear, even if previously
  unpopulated. Removing the last reference is not an implicit secret deletion.
- Copy reviews the source declaration and source revision, and copies no saved
  credentials. Delete requires explicit confirmation and the displayed revision.
- Arrangement reuses the complete owner/agent/MCP partition and order CAS,
  including unknown formats without reading or converting their payloads. It
  does not change asset revisions or Pack pins.

Saved secret plaintext is never displayed or copied. New values remain only in
the current operation and request buffers; cancellation, completion, discard,
unmount and identity-generation changes clear input nodes and owned buffers.
No new local/session storage, clipboard secret export, token vault or credential
persistence is introduced. Browser/JavaScript immutable strings and runtime
copies cannot be promised cryptographic memory erasure. Do not use the website
MCP management token as an asset credential.

## Conflicts, uncertain results and lifecycle

Writes use full asset/source/order CAS and are single-attempt. There is no
automatic merge, automatic application-level retry or replay. A transport error,
invalid acknowledgement or cancellation of a submitted write may be **Unknown**:
the server can already have committed. Closing the editor does not clear that
workspace's write barrier or undo a write.

Explicit **Discard draft and read current state** discards new input and reads
metadata. It does not replay the old operation or guess an unknown create's ID.
Inspect the list/outcome, open a new operation and review a new preview before
writing again. A known CAS conflict also requires explicit readback, not silent
rebasing. A full page/session restart is not a durable operation journal: inspect
the current server state before manually initiating another write.

Dirty close/history navigation asks whether to keep editing or discard. Pending
requests can stop waiting; cancellation does not promise server rollback.
Origin/account/client generation owns requests and rejects late results. Locale
and theme changes preserve the operation rather than remounting it. Radix dialogs
keep keyboard focus, default discard/readback confirmation to the safe action,
and restore a connected trigger or workspace heading after completion/removal.
Existing semantic theme tokens and reduced-motion behavior are reused.

## Focused verification

Use a private source export with a normal `npm ci` and `npm run build` in `web`.
The resulting ignored `web/dist` is needed only in that export for Go embedding;
it is not part of the source patch/archive. No author-built distribution is
required. Go validation uses Go 1.27.3, `CGO_ENABLED=0`, no race and no full-repo
matrix:

```sh
node scripts/test-mcp-configurations-state.mjs
node scripts/test-mcp-configurations-state.mjs --red-allow-replay
node scripts/verify-mcp-configurations-ui.mjs --self-test --output /tmp/OWN-TERMINAL
CGO_ENABLED=0 go test -json -count=1 -timeout=180s \
  -run '^Test(MCP|AppConnectsMCP)' \
  ./internal/server ./internal/server/httpapi ./internal/server/configuration ./internal/cli
CGO_ENABLED=0 go vet \
  ./internal/server ./internal/server/httpapi ./internal/server/configuration ./internal/cli
CGO_ENABLED=0 go test -c -o /tmp/OWN-MCP-BROWSER.test ./internal/server
node scripts/verify-mcp-configurations-ui.mjs \
  --fixture-bin /tmp/OWN-MCP-BROWSER.test \
  --browser-bin /ABSOLUTE/agent-browser --chrome-bin /ABSOLUTE/chrome \
  --output /tmp/OWN-BROWSER
node scripts/verify-mcp-configurations-ui.mjs --zoom-only \
  --fixture-bin /tmp/OWN-MCP-BROWSER.test \
  --browser-bin /ABSOLUTE/agent-browser --chrome-bin /ABSOLUTE/chrome \
  --output /tmp/OWN-NATIVE-ZOOM
```

The `--red-allow-replay` command must exit nonzero at the behavioral Unknown
barrier assertion; it changes only the test bundler's in-memory negative control,
not product source. A compilation/setup failure is not a safety RED.

The browser harness uses unique private HOME/socket/session directories, fresh
synthetic accounts and secrets, a real App, TLS, SQLite, the real RPC/worker
paths, and a bounded loopback failure proxy. It checks actual server state rather
than just success text. The proxy's lost-after-success mode proves Unknown after
a real commit. Other cases cover late CAS, final-reference Clear, no-secret copy,
complete order, identity changes, dirty navigation, privacy and keyboard focus.
Its finally path closes the browser, requires acknowledged stop, waits for the
fixture process, and checks App.Close/Serve/worker drain receipts. Nonzero,
signal, timeout, missing stop/PASS and contradictory fixture results fail its
terminal self-tests. Do not reuse an output directory or a live browser profile.

### Zoom evidence boundary

The ordinary matrix's **200% text enlargement** sets the root font to 32px. It is
not browser zoom, DPR emulation or a substitute for native browser zoom.

The separate `--zoom-only` run changes the fresh Chrome profile's native default
zoom from 1 to 2 using `chrome.settingsPrivate` on `chrome://settings/appearance`.
CDP controls the actual window and clears device-metrics overrides. Evidence must
show unchanged 16px root font and CSS `zoom: 1`, `visualViewport.scale: 1` (no
pinch), DPR 1→2, and CSS viewport width halving for the same physical window.
At physical widths 1280 and 750, the native-zoom layout is respectively 640 and
375 CSS pixels. The focused run checks retained draft, no horizontal clipping,
reachable preview/save, actual SQLite CAS and restored focus. It does not rerun
or overwrite the original matrix. Only this run additionally allows the internal
`settings` origin; public network navigation remains blocked.

These are local Linux/Chrome built-page results, not all browsers, OSes, screen
readers, live MCP connectivity, native device application or a full F12/GA claim.
