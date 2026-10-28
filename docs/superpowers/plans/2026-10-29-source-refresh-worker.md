# Public-source refresh worker execution plan

Base: `a895cfd3a5665387a53c00907e5e256352241b8c`. Implements the approved `b9715a35` worker contract, not private-source S1 or all of F5.

## Boundaries
- Exactly the ten source/test paths from the approved proposal, plus this plan and `docs/SOURCE-REFRESH.md`; no frozen core/RPC, schema, generated, scheduler, CLI or UI changes.
- Main owns `internal/servercli/root.go`/tests and the eventual `--source-refresh-interval` flag. Keep `server.Config.SourceRefreshInterval *time.Duration`: nil defaults to 15 minutes, zero disables, positive 1 second–24 hours.
- Reuse the existing filelock implementation at the trusted data directory's `.source-refresh.lock`. No new lock implementation or database schema.
- First periodic run waits for the interval; a tick is one bounded batch, not an all-source freshness SLA. Cursor progress is bounded and volatile; restart rescans.

## Tasks and acceptance
1. State targets/tickets: real SQLite RED→GREEN for manual/plugin sources without credentials, opaque issuer binding, owner/slot pagination, original tuple CAS and star authorization. No exported context factory or owner-only grants.
2. Transaction gate: only private source context changes `catalogTransaction`. Every retry creates a new transaction and rereads authorization, retaining the original ticket/candidate. Star locks the current credential generation/selection sequence before the core CAS. Commit rechecks persisted authorization in that serialized transaction and reads a fresh clock. Preserve normal/MCP writes without a source context.
3. Automatic library entry: share existing lifetime/slots/fetcher/cache; precheck ticket, actual public Fetch, post-Fetch authorization, cache persistence, private-context state apply. Never invoke bare manual Refresh after only a worker-side check.
4. Worker: bounded owner/slot batches, progress on individual failures, no overlapping runs, shared data-directory lock, cancellation and drain. Published changes and source failures preserve old state; no auto-rebase or implicit unpublish.
5. App: minimal configuration/construction/cleanup/start/stop hooks, workers drained before dependent services/storage. Unit tests must not fetch GitHub.
6. Real vertical integration: synthetic HTTP public archive A→B, metadata unchanged, two overlapping Packs, real Connect manifest/download and automatic subscription Watch, one changed installation/download. Isolated temporary home only.
7. Mutation controls, targeted CGO0 test/vet/stress, exact manifest and clean tree for independent review. No stage, commit or push.

## Security/limitations
- Tokens never enter tickets, contexts, worker logs, client traffic, or public responses. The star token is only decrypted within existing state authorization helpers.
- Credential/selection writes serialize with the source transaction. The final check and driver's COMMIT are not one atomic operation; cancellation, driver errors and lost replies may leave an unknown outcome. Read back the tuple, but never automatically rebase/replay the fetched candidate.
- Only public unauthenticated codeload; private sources fail closed. Existing cache budget includes retained old/orphan objects, with no new garbage collection.
- Source `Manual=true` includes current explicit plugin collection, which has no separate provenance schema. Star-only records require current ready credentials and fresh selected public memberships; no fallback to manual after revocation.

Author evidence and task progress live in the ignored release worker report. Independent acceptance belongs to the reviewer of the final combined worker/CLI tree.
