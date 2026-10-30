# Private MCP connection assets: CLI

`cognate mcp-assets` manages the current authenticated owner's MCP **connection configuration assets**. It does not manage the website's MCP access tokens, launch an MCP server, execute its command, load the server planner locally, install configuration, or apply anything to a native client. The first target is Claude (`claude`, schema1). Other agents, full cross-platform CLI interaction and production native application remain OPEN; this is not all14-agent completion.

The interactive CLI first slice is Linux. Other operating systems fail closed for controlled file/terminal I/O; cross-compilation is not execution or qualification. The client uses the eight dedicated MCP configuration RPCs and existing `AuthService.GetSession` for private owner binding. It never logs in or creates a new session automatically.

## Session and origin

Every command requires `--server ORIGIN --token-stdin`. Supply an existing session through a bounded **non-TTY stdin pipe**, never an argv value, environment variable or discovered configuration file. Stdin is only the session, not approval or a declaration. Use trusted HTTPS in production; explicit loopback HTTP is permitted for local service use. Origins cannot include credentials, query, fragment or path. There is no certificate-verification bypass, automatic redirect, proxy-from-environment, token persistence or automatic mutation retry.

`list`, `get --id ID` and `read --id ID` are read-only. Default output is bounded metadata; `read` additionally reports refs/presence/readiness, not command/URL, replacement values or stored credentials. Unknown asset schemas remain metadata-only/read-only. The owner ID and token are not printed: review identifies only the “current authenticated session”.

```text
cognate mcp-assets list --server https://cognate.example --token-stdin
cognate mcp-assets get --server https://cognate.example --token-stdin --id ASSET_ID
cognate mcp-assets read --server https://cognate.example --token-stdin --id ASSET_ID
```

There is **no document-out or secret export** in this slice. The eight RPCs never return stored credential values. A CLI flag cannot add a secret-export capability that the server does not expose.

## Explicit files, presence and ordering

`--declaration-file` is one Draft JSON object. `--secrets-file` is a separate array of SecretMutation objects; omission means an empty action list, not implicit clearing or reimporting credentials. Neither file is executed. No path indirection, shell expansion inside a document, environment lookup or credential discovery occurs.

Example declaration:

```json
{
  "name": "tools",
  "agentId": "claude",
  "document": {
    "servers": [
      {
        "id": "tools",
        "name": "tools",
        "enabled": false,
        "stdio": {
          "command": "tool",
          "args": {"values": []},
          "env": {"values": [{"name": "API_KEY", "slotRef": "env/tools/API_KEY"}]}
        }
      }
    ]
  }
}
```

Exactly one of `stdio`, `http` or explicit legacy `sse` is required per server. Remote declarations use `url` plus optional `headers` with named refs. Query strings, embedded URL credentials and fragments are not supported. Commands/URLs are declaration data, not credential containers: never put secrets into nominally nonsecret names or command text. The complete noncredential declaration is deliberately shown on the private controlling terminal before mutation.

`enabled` must be explicit, including false. Server/arg/env/header order is preserved. Absent collection wrappers remain absent; present `{}` and `{"values":[]}` are present-empty. No trimming, sorting, inferred IDs, enabled defaults, inferred transport, or silently dropped unknown fields. IDs/names and slot purposes must match the declared canonical namespace.

Secret action examples (synthetic values only):

```json
[
  {"slotId": "env/tools/API_KEY", "keep": {}}
]
```

REPLACE uses canonical base64 bytes, for example `"replace":""` is a present **empty replacement**, not CLEAR. CLEAR is `"clear":{"confirmed":true}`. Every action has exactly one variant. All arg/env/header values are opaque slots, including values a user considers nonsecret; no inline-value fallback.

Updates validate the old/new ref union. KEEP/REPLACE can target only new refs. Every old-only ref removed by deleting an arg/env/header/server or switching transport requires an explicit confirmed CLEAR in that same full-revision operation. Missing clears, wrong-purpose/foreign refs and duplicate actions are refused; deleting an asset itself is a separate explicitly approved operation. The server remains authoritative for owner, schema, final merged payload and CAS.

Files are opened once, without following symlink components, and must be bounded regular files. Secret files must belong to the effective user and have mode0600 or stricter without executable/group/other permissions. Files are never reread after approval: the operation owns its immutable snapshot. Raw declaration<=512KiB, decoded document<=64KiB, raw mutation array<=1536KiB; at most32 servers,512 new refs and1024 old/new actions,8KiB per replacement and128KiB aggregate. The **combined actual wire request** must fit2MiB, and the server's encoded stored payload limit256KiB is independent. Separate file maxima do not guarantee combined admission. Malformed/unknown/duplicate/null/oneof/base64/overflow inputs are refused with fixed safe diagnostics.

## Preview and explicit mutation

`preview` only returns refs-only logical operations/readiness/warnings. `can_apply=false` and `applied=false` are required; preview never claims native qualification or writes assets.

```text
cognate mcp-assets preview --server https://cognate.example --token-stdin --declaration-file draft.json --secrets-file actions.json
cognate mcp-assets create --server https://cognate.example --token-stdin --declaration-file draft.json --secrets-file actions.json
cognate mcp-assets update --server https://cognate.example --token-stdin --id ASSET_ID --expected-revision 7 --declaration-file draft.json --secrets-file actions.json
cognate mcp-assets copy --server https://cognate.example --token-stdin --id ASSET_ID --expected-source-revision 7 --name "Copy name"
cognate mcp-assets delete --server https://cognate.example --token-stdin --id ASSET_ID --expected-revision 7
```

These mutation commands **default to no write**. Add `--apply` only after choosing the operation. A real controlling terminal then receives the complete noncredential declaration/optional-presence/order/refs, KEEP/REPLACE/CLEAR actions and presence, source metadata/CAS, logical preview and warnings. Replacement values/base64/digests are never displayed. Copy explicitly leaves all destination credentials absent and never copies key bytes. Preserved unknown extras are read-only and cannot be silently copied into editable declarations. Deletion retains server-side in-use/Pack constraints.

After the complete bounded review is written successfully, type the exact prompted phrase: `mcp create`, or `mcp update ID REV`, `mcp copy ID REV`, `mcp delete ID REV`. Enter alone, `yes` on stdin, absence of a controlling terminal and unknown approval flags cannot approve. Output failure before submission means no mutation. No unattended `--yes` option exists. Supported stdout uses the existing bounded Linux pipe/TTY helpers; unsupported output handles fail closed rather than enabling an unbounded writer.

Each request has a20-second ceiling, token input10seconds, review60seconds and output5seconds per existing bounded I/O helper; complete review is capped at4MiB. Client Close cancels/drains active calls and callbacks and clears owned input/replacement buffers. Immutable Go string heap erasure is not promised. Cancellation/Close cannot roll back an already committed server write.

## Unknown result: never business-replay

An operation is single-submission. Lost/canceled/malformed responses may mean the write committed; neither a transport failure nor a failed result display proves rollback. The current client blocks further mutations after uncertainty, while read-only reconciliation remains available. The application never automatically resubmits. An HTTP stack's physical delivery attempts are not interchangeable with generated-client application invocation or actual SQLite successful CAS counts.

Reconcile manually with `list`, `get` and `read` under the same authenticated owner. This ledger has **no Status RPC, durable operation receipt or create idempotency key**. A name match or a missing list row is not proof of the original create's outcome. Do not rerun create/update/copy/delete blindly; stale revisions need a new explicit read/review, not a replay. A result-output failure after mutation is reported as uncertainty, not zero writes.

File descriptor close errors in the current file-input and command-output cleanup paths are ignored. This slice does not provide checked-close or durable-output guarantees; successful writes and cleanup/drain are not evidence of successful descriptor close. No credential-export file is created.

Final production acceptance requires the frozen actual backend's TLS/App/auth/SQLite/server-only planner gates and real Linux PTY tests; generated protocol or mapping-only tests do not substitute for those gates.
