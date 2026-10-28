# Server-only Magpie library preview

This is an internal, read-only preview, not an import endpoint or a completed
M6 implementation. The server accepts already-authorized Magpie `library.json`
export bytes. It does not search a home directory, invoke Magpie or Git, open
paths from the export, fetch repositories, import configuration, or install
skills. Clients do not load this package or run moejs.

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
`internal/server/importer/magpie.go`. No public RPC accepts this helper's input
or a local server filesystem path.

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
for hostile plugins. There is no new plugin-upload surface. A future upload or
request layer must bound bytes before constructing its input slice, authenticate
ownership, limit request concurrency and pass a request context. This helper
does not provide transport or authorization.

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

Generic Git sources, local-folder contents and Magpie client configuration
remain unsupported for migration. No source-specific config schema, encryption
format, upload authorization flow, filesystem detection, server-state ingestion,
HTTP/App integration, client integration or conflict resolution is introduced.
Those need separate, explicitly source-backed work. In particular, this preview
does not use the previously suggested arbitrary CLI fallback.
