# Darwin client-key core implementation plan

Fixed base: `0098d1c431b84067ffc26412cb3ec29b7aab050b`. Status: approved exact10 successor, implementation in progress; native qualification PENDING.

## Authority and scope

Extend the existing client-key-core module's Linux-first runtime boundary to Darwin without changing the algorithm, API, historical paths, explicit initialization, lost-key recovery, identity checks, locking or durability/error semantics. Windows and other platforms remain Unsupported before I/O. The original exact8 proposal SHA `ff28c57a989bbf75b75ce118365f877ba6b6853bfa452f3f3f80d9af7fbd8e72` remains REJECT. Representative review `5357b38498d488597abf8260015e011c78f519272ee991f754ea32e694460faa` and amendment `c85695c76ea49cb8e8f1e0ef5519c949d970e33c3736fe6608f0f2520e86089f` approve this exact10 design correction for DARWIN-CORE-PLAN-01; neither is native runtime acceptance.

Exact paths: move `internal/clientkey/key_linux.go` to `key_posix.go` with `linux || darwin`; narrow `key_other.go` and `key_other_test.go` to `!linux && !darwin`; add `key_posix_test.go`; update `.github/workflows/platform-primitives.yml`, this plan and `docs/REQUIREMENTS.md`; add caller guards only in `internal/cli/client_key.go` and `internal/desktop/service.go`. No root/pack/UI/install/module/generated edits or hidden build tag.

The common core must retain the exact original 491-line algorithm excluding its new build header. CLI `loadClientKey` and the init RunE must still reject non-Linux before delegating or opening output/HOME; Desktop InitializeClientKey must likewise reject before delegation. This deliberately preserves the current caller capability boundary while enabling the direct Darwin core API. It is not Darwin CLI/GUI/installation support. Other existing platform guards remain unchanged.

## Ordered implementation and verification

1. Add real common security tests first: initialization/idempotence, existing-key enrollment, lost-key and residual-state refusal, malformed marker/key preservation, no-follow/permissions/nlink/special-file refusal, cancellation, physical key/install lock contention and concurrent convergence. Use owned canonical synthetic homes, never actual accounts. Preserve existing Linux tests and the existing Darwin core oracle unchanged.
2. Move the algorithm and narrow platform selection; add the three caller guards. Compare shared body bytes to the original. Run complete Linux clientkey/CLI/Desktop regressions, vet, readonly modules and CGO0 cross-builds. Test children/readers close and join on success and failure.
3. Compile four independent private controls: implicit generation, missing-key marker bypass, nlink bypass and physical installation-lock bypass. Require ordinary bounded assertion RED after compile0, then exact restoration GREEN; compiler errors/panic/whole-test timeouts do not qualify.
4. Change both native classifier and final evidence receipt to require the unchanged core oracle PASS0 with `product-pass`, not a precondition capability or accepted Unsupported RED. Keep both native precondition PASS gates and the other five product Unsupported RED observations unchanged. Summary must not claim full installation or release readiness.
5. Run the new exact common suite on both owned distinct real APFS mounts. The test-only selector and workflow reject missing/duplicate qualification roots, skip, zero-match, extra tests and failed/unknown receipts. Capture bounded allowlisted raw JSON and summaries in a separate artifact. Also run unchanged CLI TestClientKeyEntrypointsUnsupported and Desktop TestClientKeyEntrypointsUnsupported/TestUnsupportedInstallationIsHonest on actual macOS; preserve their original assertions and zero-I/O behavior.
6. Independently review frozen exact10 source before the preview commit. After authorized main push, consume the exact-commit actual Darwin arm64/Go1.27.3/CGO0 job, source identity, two APFS fixtures, new security/caller results and real cleanup receipt. Only that evidence can qualify Darwin core. Retain any actual RED and fix its cause, never restore Unsupported expectations to manufacture green.

## Completion boundaries

No cgo or new dependency, no DB open/migration or network operation in key admission, no source algorithm fork, caller certificate or credential logging. Go1.27.3 and modern Go guidance apply. `LoadExisting` never generates a key, initialization never replaces a valid key, missing used keys require recovery, and failure clears tentative material. A source commit/cross-build alone is development preview, not native qualification. The other five Darwin products, full cross-platform CLI, complete backups and the full first release remain OPEN.
