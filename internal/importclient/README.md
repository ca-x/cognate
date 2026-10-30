# Authorized Magpie import client

CLI usage:

```sh
cognate import magpie --server https://hub.example --file library.json --token-stdin
cognate import magpie --server https://hub.example --file library.json --token-stdin \
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

## Three-category selective imports

```sh
cognate import select --source cc-switch --server https://hub.example \
  --file snapshot.db --token-stdin
cognate import select --source cc-switch --server https://hub.example \
  --file snapshot.db --token-stdin --options-file import-options.json
cognate import select --source cc-switch --server https://hub.example \
  --file snapshot.db --token-stdin --options-file import-options.json \
  --apply --expected-sha256 PREVIEW_SHA256 --repositories 1 --skills 1 \
  --configurations 1 --ack-notice FIRST_NOTICE_ID --ack-notice NEXT_NOTICE_ID
cognate import status --server https://hub.example \
  --operation-id ORIGINAL_OPERATION_HEX --token-stdin
```

Use `--source magpie` for `library.json`. Sources must be self-contained regular
files: cc-switch schema24 up to16MiB, Magpie up to1MiB. A live database main file
without its WAL is not a consistent export; follow `docs/CCSWITCH-SNAPSHOT.md`.
All categories default unselected. Preview shows independent 1-based indices,
opaque candidate IDs, all seven bound-skill fields, typed credential-free provider
drafts, recognized credential presence and every exclusion notice. A skill's
repository need not also appear in the source's repository-candidate list.

The optional regular options file is bounded to64KiB. Its ProtoJSON shape is
`{"options":[...]}` with existing `ImportCandidateOptions` fields: candidateId,
name, the matching claudeAuthMode/geminiAuthMode, geminiKeyTransport, or a complete
skillBinding. Unknown fields/duplicate JSON keys are rejected. Use candidate IDs
from preview, not names. No token or provider secret belongs in this file. Changing
choices requires repreview; binding a source skill never claims its locally
modified contents match the server archive. All displayed notices must be
acknowledged in their displayed order, including exclusions for unselected data.

Apply needs a matching source SHA256, explicit indices/notices and a controlling
terminal. It repeats the complete review and selected indices on that terminal;
type the exact `import OPERATION_HEX` challenge. Piped `yes`, stdin tokens, an empty
answer or a wrong operation are not approval. The current controlling-terminal
implementation is Linux-only; other platforms fail closed at approval rather than
pretending cross-compilation establishes runtime support.

The operation ID is written before the sole Commit dispatch. An uncertain result
does not replay Commit, even if the same operation object is called again. Keep
that ID and query Status using the original owner and origin. NotObserved is not
proof a concurrent submission can never complete. Status in the same live client
checks the full selection and monotonic receipt; a new process has no persisted
preview, so it reports `SELECTION_VERIFIED false` while validating receipt identity
and shape. Neither Completed nor a bound receipt means local installation or
configuration application. Partial/Failed are not whole-batch rollback.

`NewSelectionClient`, `PreviewFile`, `BeginSelection`, `CommitFile` and `Status`
form the shared client API. Client/preview/operation keep owned state behind
closure capabilities, so private struct, slice and map containers cannot expose
sessions, tickets or configuration drafts through recursive fmt/slog formatting.
They are explicitly closable. `View()` omits the ticket and returns a deep copy. BeginSelection reserves
a preview once and exposes the random operation ID without dispatch. CommitFile
rereads the source, verifies the hash, then dispatches once. Client Close cancels
and joins requests and destroys owned ticket/options references; no source bytes,
session, ticket or preview is persisted. Caller HTTP tracing/context values are
not passed into authenticated requests. Default transport has no ambient proxy or
cookie jar; an explicitly supplied HTTP transport remains caller-trusted.
Selection requests use identity encoding and independent nonqueued heavy/observer
lanes, so Status can run while Commit is waiting. Deadlines are5s for Magpie
preview,15s for snapshot preview,120s for Commit,5s for Status. Status input/output
are bounded to64/128KiB before decoding; Preview/Commit input to24MiB and preview
output to2MiB. These limits do not broaden the legacy Magpie repository API.
