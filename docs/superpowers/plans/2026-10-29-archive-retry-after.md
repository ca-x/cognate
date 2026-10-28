# GitHub Archive Retry-After Admission

Requirement: respect GitHub archive 429 responses without retrying inside Fetch. The existing public ArchiveFetcher reports the error but ignores a declared retry window; a short automatic source interval can therefore issue another request too early.

The agreed test seam is the public `NewArchiveFetcher(...).Fetch` interface with actual HTTP response objects and transport-call counts. Tests exercise delta-seconds, HTTP dates, exact expiry, cross-repository sharing, cancellation priority, independent instances and concurrent admitted replies. No state/worker timing or private helper is the correctness oracle.

- On 429 with a usable positive Retry-After, record the greatest absolute deadline observed by that fetcher. Reject later admissions locally with the existing fixed ErrArchiveRateLimited, without suspending in a cooldown wait, issuing HTTP or creating a background goroutine; callers release their existing IO slots immediately on return.
- Missing, malformed, zero or past values do not introduce a cooldown and never clear an existing one. A confirmed all-decimal positive duration beyond representable range fails closed on that instance until a fresh fetcher is constructed; arbitrary malformed text is not treated as overflow.
- Check cancellation before cooldown. Already admitted/in-flight requests are not canceled; check-to-network-send is not atomic. This is process-local state on the existing shared codeload fetcher, not distributed or cross-process rate limiting. No raw header, URL or upstream body enters an error/log.
- Keep the public API, archive extraction, private fetching boundary, ordinary retry policy and dependencies unchanged. Handwritten runtime scope is `internal/repository/archive.go` plus new `archive_cooldown.go`/`archive_cooldown_test.go`, and this plan. Independent scoped review and clean CGO0 verification precede commit/SSH push.

Sequence: reproduce an immediate second Fetch despite Retry-After on the old implementation; implement the smallest shared admission gate; extend behavior cases and semantic negative controls; verify repository and source-refresh seams, full clean Go/Web checks, cross builds and exact-tree independent acceptance.
