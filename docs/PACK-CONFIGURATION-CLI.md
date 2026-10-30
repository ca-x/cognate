# Pack configuration attachment management

`cognate pack configurations` manages **owner-only server metadata**, not subscriptions, device consumption or configuration application. Supply a current authenticated owner session through bounded non-TTY stdin, never a Pack share token, command-line token or environment credential.

```
session-producer | cognate pack configurations list --server https://cognate.example --pack PACK_ID --token-stdin
session-producer | cognate pack configurations replace --server https://cognate.example --pack PACK_ID --token-stdin --references-file ./references.json --expected-pack-revision 4 --expected-configuration-revision 2
```

`session-producer` stands for your existing private session source; it is not a new login command or a literal credential. Do not put sessions into shell arguments/history. The stdin stream must finish; arbitrary readers/TTY session input and blocking file redirection are not supported by the existing CLI bounded-I/O contract.

## Ordered references and preview

The references file must be an explicit owned regular file, not a symlink (including a symlink parent), hardlink, FIFO, directory, group/world-writable file or stdin alias. Linux physical nofollow input is supported; unsupported platform file operations fail closed. The file is at most64KiB and contains one strict JSON array of at most16 entries:

```json
[
  {"assetId":"ASSET_ID_FROM_OWNER_METADATA","expectedAssetRevision":3,"targetId":"codex.provider.user"}
]
```

Replace the illustrative asset ID with a real canonical26-character server ID. Revisions are positive JSON integers (not strings, fractions, exponents or overflowing numbers). All three fields are required; null, unknown/duplicate fields, duplicate asset/target pairs and trailing JSON are rejected. Array order is preserved. **`[]` explicitly clears the entire attachment set.** This does not remove Pack skills or delete configuration assets.

Without `--apply`, replace reads current owner metadata, verifies the two supplied CAS revisions and prints the complete old set and proposed ordered reference set. It sends no Replace RPC. There is **no server preview RPC**: new asset ownership, current asset revisions and target support are **NOT server-validated by this preview**. Those checks happen atomically only during Replace. A concurrent Pack/attachment/asset change can still refuse submission.

## Explicit management mutation

Add `--apply` to the same command only when ready to replace metadata. The CLI repeats the entire review on one actual controlling terminal, including origin/current-session scope, Pack and configuration revisions, old metadata and new ordered refs, and the warning about server validation. It then asks for an exact operation-specific `REPLACE ...` phrase on that same terminal. `yes`, stdin/non-TTY approval, a missing terminal or incomplete/blocked review do not authorize mutation. Session stdin and redirected stdout previews are never approval input; complete review/confirmation must still occur on the controlling terminal.

After confirmation, the client reauthenticates the pinned owner and rereads the exact metadata snapshot before a single dual-CAS Replace. A successful response is checked against ordered refs and expected revision increments; attachment identities are server metadata, not filesystem paths. The acknowledgement means **server attachment metadata replaced only**.

All targets, including all fourteen first-release agent targets, remain **NativeUnavailable** in this feature. No keys, configuration bodies, native proof, agent files, device grants or installation are read/applied. Nothing here enables a NativeProfile, consumes a configuration or turns metadata into an automatic application. This is not a reduced first-release agent promise.

## Failure and reconciliation

Known owner denial, CAS conflict and unsupported target stay distinct safe errors. No server diagnostic body/session is printed. A lost response, cancellation after mutation dispatch or malformed success is **Unknown**, not rollback or success. The same client then blocks all further mutations but allows read-only Get. No automatic business replay occurs. Use `pack configurations list` to inspect current owner metadata; matching metadata alone cannot prove which unknown attempt committed. Any later replacement requires a deliberate new review and current CAS values, not replaying the old confirmation.

Only explicit server wire errors qualify as known mutation refusals. Local decoding, framing and response-size failures cannot prove a rejection, even if their Connect code resembles an input error. The actual CLI entry point uses owned bounded output handles, checks output completion (including help), and exits nonzero on unconfirmed output. Error diagnostics contain fixed safe text and have a best-effort200ms budget; closed or full stderr may omit them. Borrowed descriptor flags are not changed. Cancellation interrupts blocked output; the overall command budget is120seconds.

HTTPS verifies trusted CA/hostname normally; only explicit loopback origins may use HTTP. Client transport owns its policy: redirects, proxies, cookies, ambient credentials and caller context values are not inherited. RPCs are unary, bounded64KiB (Auth4096) with twenty-second operations; cancellation/Close join active work. The production dependency closure excludes server/moejs. The interactive CLI/private-file runtime boundary is Linux; cross-compilation alone is not non-Linux runtime qualification. Existing Pack subscribe/list/sync/watch/remove behavior is unchanged.
