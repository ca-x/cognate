# Authorized Magpie import client

CLI usage:

```sh
skillhub import magpie --server https://hub.example --file library.json --token-stdin
skillhub import magpie --server https://hub.example --file library.json --token-stdin \
  --apply --expected-sha256 SHA256_FROM_PREVIEW --select 3,1 --select 2
```

Supply the session token through stdin, never argv or an environment variable.
The first command uploads the explicitly authorized export and only previews.
It prints the raw-file SHA256, repository/ref candidates with 1-based indices,
existing-record booleans and repository/reference counts. It omits skill names,
export contents, filenames, owner IDs and credentials. Exports can contain secrets;
authorize uploads only to a trusted server.

The second command obtains a fresh preview and selects indices from that preview.
It requires a matching SHA256 and reopens and rehashes the file before the first
Collect call. `--select` follows comma/flag order, rejects duplicates and has no
default selection. Collection processes entire public repositories, including
validation of existing snapshots, without installing local skills.

MyGo can use the same package without Cobra, server packages or plugin execution:

```go
client, err := importclient.New(origin, session, httpClient)
preview, err := client.PreviewFile(ctx, authorizedPath)
candidates := preview.Candidates()
result, err := client.ApplyFile(ctx, authorizedPath, confirmedSHA256, preview, selectedIndices)
```

Handle each error before continuing. A preview is immutable and bound to its
client/session; candidate getters return copies. Read CLI credentials using
`ReadSession(io.Reader)`, bounded to 2 KiB; credentials live only in memory.
No client state or upload jobs are persisted.

`Result.Outcomes` preserves selection order. Statuses are `collected`, `failed`,
`unknown` or `unattempted`. Explicit invalid-argument, not-found,
failed-precondition and data-loss errors mark one item failed and continue;
other failures stop dispatch with the current item unknown and the remainder
unattempted. Any incomplete apply returns an error (CLI exit 1). Cancellation
before dispatch leaves entries unattempted. Completed entries remain; unknown
entries may already have committed. There is no automatic retry or rollback.

Network requests use generated Connect clients, an HTTPS origin (explicit
loopback HTTP allowed), no redirects, a same-origin transport guard, bounded
wire/decoded responses and a maximum 20-second timeout per RPC. File reads accept
only nonempty regular files up to 1 MiB. Unix opens every component using
`openat`/`O_NOFOLLOW`; Windows rejects reparse points while holding parent handles.
Other OSes fail closed. Windows/macOS paths compile; their filesystem behavior
still requires verification on those systems. Clearing reachable buffers does
not promise secure erasure of every runtime copy.
