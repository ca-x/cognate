# MyGo: private MCP connection assets

Open **AI 配置 → MCP 连接配置** in the native MyGo desktop. This workspace manages the signed-in owner's MCP connection configuration assets, not the website's MCP management tokens. Claude schema1 is the editable target. It never starts an MCP process, runs a declaration's command, contacts its URL, loads a local plugin, initializes a client key, or applies configuration to a native application.

The workspace shares the existing account/session and validated origin. Load is explicit; no token discovery, automatic login, mutation retry, redirect bypass or environment credential fallback is added. The English/中文 switch changes MCP workspace copy without changing the draft. The outer application shell retains its existing Chinese copy.

## Edit a connection

1. Sign in on the account page, return to this workspace, and choose **载入 / 核实 MCP 资产**. Select an actual asset to inspect its ID, agent, schema and revision, or create a new draft.
2. Enter the asset name. Add up to32 explicitly named/identified servers; choose stdio, HTTP or legacy SSE, and explicitly enable or disable each declaration. Server up/down controls change declaration order only, not the independent asset-order workspace.
3. For stdio, enter the nonsecret command and optionally enable the args/env collection wrappers. For HTTP/SSE, enter the nonsecret URL and optionally enable headers. An absent wrapper differs from a present-empty wrapper. URLs containing userinfo, query or fragment are rejected, never silently stripped.
4. Add, select, edit, move or remove ordered argument/env/header items. Argument IDs, environment names and header names determine canonical `arg/SERVER/ID`, `env/SERVER/NAME`, and `header/SERVER/Name` refs. The ref is displayed, not a separately editable alias. All values are opaque credential slots, even values considered nonsecret by their user. There is no arbitrary JSON editor or inline-value bypass.
5. Select a credential ref. KEEP preserves its current presence, REPLACE supplies a new memory-only password value, and CLEAR requires its own explicit confirmation. An explicitly chosen empty REPLACE remains a present credential, not a deletion. A removed or renamed original ref stays visible and requires confirmed CLEAR in the same full-revision update. Scratch refs that were never persisted can be discarded.

Commands, URLs, names and IDs are deliberately visible declaration data, not secret containers. Do not put credentials into these fields. Limits reuse `mcpclient`: at most32 servers,64 args/env and32 headers per server,512 desired refs,1024 old/new actions,64KiB encoded declaration,8KiB per replacement and128KiB aggregate; the existing wire and stored payload limits also apply. Invalid input is refused with safe fixed messages, without trimming, sorting, inferred identifiers or silently dropping extensions.

## Preview and write

**仅逻辑预览** sends a read-only logical preview. `can_apply=false` and `applied=false` are mandatory; this is not MCP connectivity or native qualification. **审阅服务端保存…** prepares create/update. A clean persisted asset also offers a separately named keyless copy and explicit deletion. The copy never contains credential bytes. Deletion retains the backend's in-use/Pack constraints.

The native review shows the full bounded noncredential declaration, presence, order, refs, actions, current-session scope, exact asset/revision and logical preview. Approve each actual REPLACE/CLEAR slot, then enter the exact visible `mcp create` or `mcp ACTION ID REV` phrase. The final button consumes this prepared operation once. Enter alone, a generic “yes”, a stale revision or missing slot approval cannot submit. Escape or cancel closes the actual pending capability, not just the dialog. Content scrolls while confirmation/cancel/submit remain outside that scroll in short windows.

New values clear from editor state immediately when preview/preparation snapshots them, on draft-switch requests, workspace transitions, cancellation, owner/origin changes and shutdown. Reusing a cleared REPLACE requires re-entry or explicitly choosing REPLACE again to permit an empty value. Credential widget identities are isolated per slot and cleanup epoch so undo cannot recover another slot or a cleared replacement. The input/operation owns independent replacement byte buffers and clears them on Close. This does not promise erasure of immutable Go strings, framework/OS heap copies, the clipboard or process memory.

## CAS, uncertainty and read-only state

Writes use the full asset revision, including credential actions. A late competing update is not overwritten. Unknown schemas are metadata-only/read-only. Preserved extras or details that the existing client cannot validate are also displayed only as authenticated metadata; Invalid/Conflict detail responses may fall back to a fresh metadata read, never to an editable draft. Preparation independently re-reads supported details; an invalid/unsupported view cannot authorize copy, update or deletion in this workspace. No server/protocol validation is weakened.

After any submit attempt, the current form is blocked. Lost/canceled/malformed responses can mean a SQLite commit already happened. Unknown is not rollback. Explicitly load to reconcile: loading discards the attempted draft and never replays it. This protocol has no durable outcome receipt, Status RPC or create idempotency key; a matching name or absent list row is not proof of the earlier outcome. Any later operation requires a new deliberate read/draft/review, not blind resubmission.

Changing owner/origin or closing the application cancels and joins owned client work. Shared desktop generations reject stale queued results, including otherwise successful reads. Unsaved nonsecret drafts require an explicit discard before navigation or workspace switching; attempting to leave clears new secrets even if the user keeps the draft.

## Qualification boundary

Directed tests cover typed input ownership/budgets, actual TLS/App/auth/SQLite CRUD and CAS, committed-response loss, owner isolation, unknown schemas/extensions, cancellation/drain, stale delivery, and native MyGo mouse/keyboard forms and reviews. UI probes exercise English/Chinese, light/dark, reduced motion/high contrast, password masking/undo isolation and short windows. Linux native-window smoke is separately recorded with a private HOME and synthetic inputs. It is not authenticated human/GPU or macOS/Windows runtime acceptance. This workspace is not complete F12, all-agent completion or GA. CLI file-input semantics remain documented in `MCP-CONFIGURATION-CLI.md`.
