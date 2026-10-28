# Public Source Refresh: Owner RPC

## Scope And Dependencies

Expose the existing owner's bounded source-refresh core through the current LibraryService. This slice requires the separately reviewed library/state refresh implementation. It does not change Collect idempotence, register a public write route, add an MCP tool, implement private fetching or claim scheduled source updates.

## Contract

- Add `RefreshRepository` to the existing `skillhub.library.v1.LibraryService`. Request fields 1/2/3 are repository ID, expected archive hash, expected publication revision. Response fields 1–6 are repository, changed, added, updated, removed, affected Pack IDs/revisions. Add Repository field 8 for publication revision; preserve every existing field/procedure and ordinary Web generation.
- Authenticate before any refresh work. Reject malformed IDs/hash/nonpositive revision with a fixed InvalidArgument; stale versions return Aborted, changed published content/referenced removal/alias divergence return FailedPrecondition. Wrong owner/missing source remain indistinguishable NotFound. Never return raw fetch/database errors.
- Source identity comes only from the stored repository; callers cannot upload hash/skill lists, choose arbitrary URLs or bypass publication confirmation. Reuse the existing 4 KiB request limit and service deadline/cancellation/IO limits.
- Get/List/Collect carry the publication revision from their existing coherent state read. The successful Refresh response is the committed result, not a second post-commit read that could race.

## Implementation And Verification

1. Add real Connect HTTP tests using synthetic upstream archives/owners: authentication and cross-owner no-fetch, invalid/stale no-fetch, successful content replacement with stable skill IDs and one Pack revision bump, no-op, publication refusal, fetch failure preservation, canceled request and fixed error privacy.
2. Add proto fields/procedure, run normal Buf Go and Web generators, implement handler and explicit result/error mapping. Do not edit generated files manually or change native Web exclusion.
3. Verify target tests before/after implementation, Buf lint/breaking/full parity, ordinary Web build, clean full Go tests/vet with Go 1.27.3 / CGO0, then independent review of exact core+RPC integration before commit/SSH push.

## Main Write Set

`proto/skillhub/library/v1/library.proto`, its normal Go/Connect/TS generated outputs, `internal/server/httpapi/library.go`, new `library_refresh.go`/`library_refresh_test.go` in that directory, `docs/REPOSITORY-LIBRARY.md`, and this plan. The core author owns state/library/public-catalog implementation files. No ent/App/scheduler/MCP/new native endpoint or unrelated UI files.
