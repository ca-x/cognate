# Public catalog and owner categories

The backend implements REQUIREMENTS F8/F9 through the frozen
`proto/skillhub/catalog/v1/catalog.proto`. Anonymous distribution is separate
from private Pack sharing. A missing or empty Pack token never grants public
access through a private Pack endpoint.

## Integration

Construct one public service with the existing store and verified library:

```go
catalog, err := publiccatalog.NewService(store, collection)
```

The constructor accepts `*state.Store` and `packs.ArchiveSource`; the existing
`*library.Service` implements that source. It returns `(*publiccatalog.Service,
error)` and does not need a signing key or token. Pass the service as
`httpapi.Options.PublicCatalog`, together with the existing `Options.Store`.

`CategoryService` and `PublicationService` register when `Options.Store` is set.
Every owner method authenticates an existing Web session. The explicit
`PublicCatalogService` registers when `Options.PublicCatalog` is set and needs
no session, share token, or MCP token.

The public service implements
`BeginRequest(context.Context) (context.Context, func(), error)` and `Close()
error`. The completion callback is idempotent. Public HTTP routes use the
existing distribution request/deadline wrappers, including body-read and
socket-write deadlines and keep-alive cleanup. Close the public service before
closing its library or store. Close cancels and waits for in-flight requests;
subsequent service calls are unavailable. App wiring belongs to the main task.

## Categories

- Each owner can create at most 100 categories. Names must be valid UTF-8,
  nonblank, and at most 80 UTF-8 bytes, including after normalization.
  Control characters are rejected. Whitespace collapses, Unicode NFKC
  normalization, and Unicode case folding define owner-local uniqueness.
- Categories start at revision 1. Updates and deletes require the current
  `expected_revision`. Successful updates increment the revision. Duplicate
  normalized names are rejected, but another owner may use the same name.
- A skill can have at most eight distinct category IDs. Owner, collected skill,
  and all categories are validated in the same transaction. Invalid replacement
  selections leave the existing associations intact.
- Owner counts include that owner's collected skills. Anonymous category DTOs
  contain only opaque ID, display name, and the number of published skills.
  Categories used only by private skills do not appear in anonymous results.
- Deletion removes category associations only. It does not remove a collected
  repository, skill, archive, Pack item, or installation, and it does not change
  source files or logical content hashes. Uncategorized public skills remain
  browsable and downloadable.

## Explicit publication

Each collected repository starts unpublished at revision 1, including collections
from public GitHub repositories and selected Lists. A repository's GitHub
visibility is not publication consent.

`GetPublication` and `SetPublication` operate only on the authenticated owner's
collected repository ID. Enabling publication requires
`confirm_full_contents=true`. The publishing UI must explain that all distributable
skill-directory contents, including hidden, binary, and executable files, become
available anonymously. Client configuration, credentials, and unrelated files
outside the selected skill directory are not included.

Updates use `expected_revision` CAS. A visibility change increments the revision;
an accepted same-value update leaves it unchanged. Requests setting `published`
to true still require confirmation. Unpublishing invalidates the current
download revision. Republishing increments it again and does not revive an old
download request. Visibility and revocation are read from the database, not a
service cache.

## Anonymous queries

- `ListSkills` accepts `query`, `category_id`, `limit`, and `cursor`. Search covers
  skill name, description, and repository name. Query size is at most 128 UTF-8
  bytes; malformed UTF-8 and control characters are rejected. The default page
  size is 24 and the maximum is 100.
- Public `ListCategories` accepts `limit` and `cursor`, defaults to 64, and has
  the same maximum of 100. Only categories associated with published skills are
  returned; counts do not include private skills.
- Both lists use bounded keyset queries ordered by immutable opaque public ID,
  fetching at most one extra row. Cursors are versioned and bound to the list
  kind and active search/filter. Invalid encoding, oversized cursors, and
  incompatible filters are rejected. Limits above 100 are rejected, not clamped.
- `GetSkill` returns only the frozen public DTO: portable slug, source name,
  description, content hash, opaque skill/repository IDs, repository name,
  repository-relative skill path, branch, publication revision, and public
  categories. It does not expose user identity, credentials, private Pack data,
  archive cache hashes, client configuration, or server filesystem paths.
- Missing and unpublished skill IDs both return `not_found: public skill not
  found` for details and downloads. Public responses use the existing `no-store`
  response policy.

Keyset pages are not a persistent catalog snapshot. Concurrent publication or
category changes can affect subsequent pages. Clients should restart pagination
when changing search or category filters.

## Verified anonymous downloads

`DownloadSkill` requires the exact `skill_id`, `content_hash`, and
`publication_revision` returned by the catalog. It has no token field.

The service resolves archive hash and relative skill path from collected state,
then calls the existing `Library.WriteSkillArchive`. That path verifies the
cached repository object and logical skill hash before producing the existing
deterministic `skill/`-rooted tar.gz format. Its archive protections and executable
mode handling remain unchanged. No download fetches GitHub or accepts a caller's
filesystem path. Missing or inconsistent cached sources fail closed rather than
refetching or emitting a successful final frame.

The stream uses the existing `packs.Chunk` and frozen
`skillhub.pack.v1.DownloadSkillResponse` wire shape:

- Data chunks contain at most 64 KiB, with contiguous byte offsets starting at 0.
- The sole successful final frame has no data, total-byte offset, the transfer
  SHA-256 of the tar.gz bytes, and the expected logical content hash.
- Publication, skill identity, hash, and revision are checked at entry and before
  every data chunk and final frame. Revocation, content/revision mismatch, source
  errors, cancellation, or failed sends terminate without a successful final.
- The public service permits two simultaneous downloads globally per service
  instance. An extra download receives `resource_exhausted`. It also enforces
  `repository.MaxSkillArchiveBytes` (81 MiB) and a 20-second request bound,
  shortened by a caller or Connect deadline.

Revocation cannot retract bytes already sent. A client must reject incomplete
streams and verify offsets, transfer SHA-256, the final logical hash, and the
unpacked directory's logical hash before installation.

## Error mapping

Malformed names, selections, revisions, queries, cursors, and limits return
`invalid_argument`; duplicate category names return `already_exists`; stale CAS
or download revisions return `aborted`; publication without full-contents consent
returns `failed_precondition`. Foreign owner resources return `not_found`.
Capacity exhaustion returns `resource_exhausted`, closed services return
`unavailable`, and missing cached source objects return `data_loss`. Existing
authentication, cancellation, deadline, and archive verification mappings apply.

## Verification

```sh
CGO_ENABLED=0 go test ./internal/server/... -count=1
CGO_ENABLED=0 go vet ./internal/server/state ./internal/server/publiccatalog ./internal/server/httpapi
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./internal/server/publiccatalog ./internal/server/httpapi
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./internal/server/publiccatalog ./internal/server/httpapi
```

Tests use real ent/SQLite stores and actual Connect handlers. They cover owner
isolation, Unicode normalization, CAS, bounds, private counts, pagination,
committed revocation across separately opened stores, verified binary/hidden/
executable archive transfer, deterministic bytes and logical hash, revocation
between real HTTP chunks, failed source verification, bounded concurrent
downloads, cancellation, Close waiting, short transport deadlines, incomplete
request bodies, blocked socket writes, and keep-alive reuse.

The task report and baseline-based patch are in
`.superpowers/sdd/2026-10-28-client-subscriptions/`. CLI installation, Web behavior,
and MCP tool expansion are owned by separate tasks. No race-detector or foreign
platform runtime result is claimed by these no-cgo checks.
