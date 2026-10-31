# Local account: one owner command

On Linux, `cognate auth run` logs in to the existing local-account service and runs **one fixed Cognate owner command**. It does not create accounts, implement OIDC, export credentials, persist sessions or qualify native profiles. The website MCP-token page is unrelated.

```sh
cognate auth run --server https://cognate.example -- mcp-assets list
cognate auth run --server https://cognate.example -- mcp-assets create --declaration-file ./draft.json
cognate auth run --server https://cognate.example -- mcp-assets create --declaration-file ./draft.json --apply
cognate auth run --server https://cognate.example -- pack configurations list --pack PACK_ID
```

Use a real controlling terminal. Both username and password are hidden; neither comes from stdin, arguments, environment or files. Enter a local username (3–64 ASCII letters/digits/`._-`) and password (8–1024 UTF-8 bytes). Password spaces are preserved. The combined human-input budget is 60 seconds, so a person may take more than the owner's existing 10-second session-input deadline: **the owner child is started only after Login and GetSession succeed**. The wrapper prefills and closes a private anonymous pipe before starting the child. The session never appears on stdout or in child argv/environment.

Only these routes are admitted: `mcp-assets list|get|read|preview|create|update|copy|delete`, `pack configurations list|replace`, `import select|status`, and `config apply --target claude.settings|codex.config`. Child `--server`, `--token-stdin` and `--owner` flags are refused, including equals forms. The wrapper supplies its canonical origin and stdin session, and supplies the authenticated owner only for native apply. All ordinary child flags, private-file rules, native qualification and local-key prerequisites remain enforced. Arbitrary programs, shell commands, nested auth, local recovery commands and future agent commands cannot be wrapped.

Mutation remains **preview by default**. `--apply` still requires the original owner's full summary and exact operation-specific confirmation on the same controlling terminal. Stdin `yes` is not approval. Native profile admission, per-slot approval, CAS, original expiry and partial-disk truth are unchanged. Local status/recover/restore/abandon/retire commands need no login and are not wrapped. Account login does not make an unsupported native target ready.

Origins use verified HTTPS, except literal loopback HTTP (`127.0.0.0/8` or `::1`). HTTP DNS names, including `localhost`, are refused; use `http://127.0.0.1:PORT`, `http://[::1]:PORT` or verified HTTPS instead. No ambient proxy/cookies/redirects or caller HTTP tracing are inherited. Auth uses only the existing typed binary Login/GetSession/Logout RPCs, 4KiB messages, 20-second requests and no automatic retry. The child receives only required locale, trust, HOME/XDG/TMP environment and a fixed PATH; auth itself does not load or create local HOME/key/configuration/cache state. Existing owner routes may use local state under their own contracts.

After child join, the wrapper attempts Logout once with a separate 5-second cleanup budget. The server's session lifetime is still **24 hours**, not a new short-lived token policy. A lost Login response, SIGKILL or failed Logout can leave a session active until its original expiry. An unconfirmed cleanup exits nonzero with “business result unchanged”; it never means rollback and must not cause business replay. A child Unknown/nonzero/partial result remains nonzero. Reconcile using the relevant read-only owner or local status command and perform a new deliberate review if needed.

SIGINT/SIGTERM cancel the run; a child has a five-second grace period before forced termination and is joined once. The wrapper cap after login is the earlier of the session expiry and five minutes; it does not extend any child deadline. Catchable input cancellation restores exact terminal settings and discards queued credential input before child confirmation. **SIGKILL during hidden input cannot guarantee terminal restoration or Logout**; restore the terminal manually (`stty sane`) if necessary. No defense against root/same-UID tracing or terminal observers is claimed. Owned buffers are cleared, but Go/protobuf/GC/TLS copies cannot be promised physically erased.

This is a Linux local-account first slice. Non-Linux execution refuses before credential/network I/O. Cross-compilation is not runtime qualification. Full cross-platform account UX, OIDC and the complete F3 goal remain open.
