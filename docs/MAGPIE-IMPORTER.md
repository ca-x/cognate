# Server-only Magpie library preview

The adapter itself is an internal, read-only preview. It is now exposed through
an authenticated, bounded Connect upload endpoint and a Web repository-selection
workflow, not a completed M6 implementation. The server accepts explicitly
selected Magpie `library.json` export bytes. The adapter does not search a home
directory, invoke Magpie or Git, open paths from the export, fetch repositories,
import configuration, or install skills. Clients do not load this package or
run moejs.

## Web upload and collection

On the Repositories page, select a nonempty JSON export of at most 1 MiB, then
explicitly choose Upload and preview. Selecting a file alone sends nothing.
The authenticated `skillhub.import.v1.ImportService/PreviewLibraryExport` RPC
returns repository candidates, their refs, an owner-scoped existing-record hint,
and the number of exported skill references. Preview does not collect anything;
no candidate is selected by default.

Select candidates and collect them in selection order. Each uses the existing
`LibraryService.CollectRepository` independently, with at most one browser request
in flight. Collection fetches the whole public repository archive and scans all
its skills, not just the Magpie references or locally modified skill files.
Existing entries still validate their cached snapshot, without refreshing it.
Default privacy, archive/cache limits and owner isolation are unchanged.

An explicit item validation/not-found/cache-integrity error may continue to the
next item. Cancellation, authorization, capacity, transport or unknown failures
stop subsequent dispatch. The current outcome may be uncertain: cancellation
does not undo a committed repository. Completed items remain; users may explicitly
retry uncertain entries to validate the cache and converge without duplicate rows.
Signing out or changing accounts clears candidates and selection; theme/language
changes preserve drafts. There is no persisted upload or resumable import job.

The export can contain unselected secrets: upload only to a trusted server.
The UI does not display its raw text or file name, store it in browser storage,
or place it in query keys. The server processes bytes only in request memory,
never logs or persists the export, and strips unrelated fields before moejs.
Clearing reachable byte buffers is not a promise to securely erase every Go,
browser or JavaScript copy.

Before decoding, the endpoint authenticates the session and admits at most two
requests per process and one per owner, without a queue. HTTP and Connect messages
are bounded to 2 MiB (including JSON base64 overhead); the actual export remains
limited to 1 MiB. Compressed uploads are rejected. A five-second request/read
deadline interrupts slow bodies; shutdown cancels and drains admitted requests
before library/store teardown. Protocol/parser errors use fixed safe messages.
The endpoint does not accept a server path, owner, plugin source or download URL.

## Verified source format

The adapter targets `yetone/magpie` commit
`502674abb767e48e7d48944fc4657d7549a25c2b`, verified on October 28, 2026:

- `internal/library/library.go`: `Library.Skills` is the JSON `skills` array.
  `library.json` is beside `library/` under `settings.Dir()`, not necessarily
  inside the directory containing skill files.
- `internal/library/skills.go`: each skill has `name`, optional `source`, and
  `agents`. A source has `kind` (`github`, `git`, or `folder`), optional `repo`,
  `ref`, `path`, and local-folder `dir`. A GitHub `repo` is `owner/repo`.
  Skill names identify directories beneath Magpie's library skills directory.
- `internal/library/mcp.go`: the fixture's ignored MCP entry follows the real
  `Server` JSON tags. Its command and secret-bearing environment are used only
  to test exclusion; they are never executed or returned.

The fixtures in `internal/server/importer/testdata/magpie` are synthetic,
sanitized documents constructed from those verified JSON tags, not a claimed
export from a real installed library. The implementation report records the
primary URLs and the prior Nowledge cases, including the user's missing-library
case. Older remembered filesystem locations are not used for detection.

## API

```go
func PreviewMagpie(ctx context.Context, export []byte) (MagpiePreview, error)
```

- A nil export means the authorized source is absent: return `Found == false`
  and no error. The caller can map a missing authorized export to nil without
  probing arbitrary server paths.
- A non-nil empty export, invalid JSON, or an absent `skills` member is an
  error. A JSON `skills: null` or `skills: []` is a found, empty library.
- `Skills` preserves the stored skill order and names. These are references,
  not proof that the skill directory still exists, has valid content, or is
  currently applied to any agent. No IDs, content hashes, or enabled states are
  invented from this format.
- GitHub skill sources retain their repository-relative `path` and normalized
  `repo`/`ref`. Repositories are lowercased; an uppercase `HEAD` or empty ref
  becomes an empty ref, meaning the real default branch. Lowercase `head`
  remains a distinct branch.
- `Repositories` contains unique `MagpieRepository{Repo, Ref}` pairs from
  GitHub sources, in first-encounter order. Multiple skill paths on one ref
  share a repository entry; different refs remain distinct. Entries are
  metadata candidates, not verified or automatically collected repositories.
- For `git` and `folder` sources, only `Source.Kind` is returned. Their remote
  addresses, refs, paths and local directories are deliberately omitted.
  Skills without a source still appear with their name and no source.
- `from`, `seenSkills`, and `applied` do not create repository or installed
  skill entries. Instructions, MCP configuration, agent assignments, hooks,
  hashes and other unselected fields are not imported or returned.
- The export is not mutated. Any error returns an empty preview, never partial
  results; diagnostic errors do not include export values or parser details.

`MagpieSkill`, `MagpieSource`, `MagpieRepository`, `MagpiePreview`,
`MaxMagpieExportBytes`, and `MaxMagpieSkills` are defined in
`internal/server/importer/magpie.go`. The upload RPC accepts export bytes, never
a local server filesystem path.

## Bounds and execution

The raw UTF-8 JSON export is limited to `MaxMagpieExportBytes` (1 MiB). Its
`skills` array is decoded incrementally, stopping at `MaxMagpieSkills` (1,000)
before allocating additional typed skill records. Names are at most 256 bytes
and must be safe single directory references without control/bidi characters
or traversal. GitHub repository/ref validation uses the existing repository
validator (512-byte repository input and 256-byte ref limits); repository
inputs must use `owner/repo`, not credential-bearing URLs. Repository-relative
skill paths are at most 1,024 bytes; hidden components are retained, but
absolute paths, traversal, backslashes, colons, controls/bidi characters and
noncanonical separators are rejected. Duplicate skill names, duplicate JSON
object names, invalid UTF-8 and unknown source kinds fail closed.

Go validates and removes unselected fields before handing metadata to the
embedded `magpie.mjs` module. moejs computes the repository aggregation, and
the host validates its skill output and repository set before returning.
Each invocation uses the existing isolated runtime: no filesystem, process or
network bindings; no dynamic code or module imports; a three-second runtime
deadline; 16 MiB of JavaScript memory; 1 MiB input/output and 50,000 output
nodes. Cancellation is checked before even treating nil as an absent export.

These are per-invocation bounds, not a process-wide heap limit or an OS sandbox
for hostile plugins. There is no plugin-code upload surface. The new HTTP/RPC
layer supplies authentication, pre-decode admission, bounded upload and request
context; the helper itself still supplies neither transport nor authorization.

## Verification and remaining work

```sh
export PATH="$(mise where go@1.27.3)/bin:$PATH" CGO_ENABLED=0
go test ./internal/server/importer -run Magpie -count=1
go test ./internal/server/importer ./internal/server/plugin -count=1
go vet ./internal/server/importer ./internal/server/plugin
```

The fixtures cover valid references, empty libraries and malformed input; nil
covers an absent export. Tests also cover traversal/credential rejection,
duplicate records/keys, Unicode validation, exact byte/record bounds,
cancellation, repeated previews, concurrent isolated calls and secret exclusion.

HTTP/App tests additionally cover authenticated upload, pre-decode rejection,
byte/record/encoding limits, post-read reauthentication, per-owner/global
admission, real slow-body interruption, shutdown drain and selected collection
with retry/partial-failure semantics. See `VERIFICATION.md` for the integration
and browser evidence.

Generic Git sources, local-folder contents and Magpie client configuration remain
unsupported for migration. Source-specific config schemas, encryption formats,
filesystem detection, CLI/MyGo import UI, full skill-content migration and
configuration conflict resolution need separate, source-backed work. This
workflow never uses the previously suggested arbitrary CLI fallback.
