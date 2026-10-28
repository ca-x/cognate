# Source Refresh: Server CLI Interval

Wire the independently implemented public-source refresh worker's `server.Config.SourceRefreshInterval` to a real `--source-refresh-interval` duration flag. Default 15 minutes, zero disables, and positive supported values are validated by the worker/App configuration. This interval is a bounded batch cadence, not a promise that all repositories refresh every interval. Keep the existing Lists interval independent.

Write set: `internal/servercli/root.go`, `internal/servercli/root_test.go` and this plan. The worker author owns Config/App, lifecycle, state gates and operational documentation. Do not commit this CLI slice before its Config dependency exists and the integrated worker is independently accepted.

TDD: verify the missing flag on an isolated committed baseline fails behaviorally, then check default, disabled and explicit durations, invalid-duration rejection and separation from the Lists flag. Run focused CLI tests and the combined clean Web/Go checks after the worker freeze. Do not read a real master key, start a production server or fetch GitHub to test flag parsing.
