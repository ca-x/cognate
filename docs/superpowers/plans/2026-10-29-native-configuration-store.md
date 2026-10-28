# Native configuration store implementation plan

**Authority:** `2026-10-29-native-consumer-protocol.md` §§3–5/9, SHA256 `472423501c33943de1e4bc0380ca9c0ef982559a5d69e7787b3c67c2fd1dbf29`; main + Gauss accept Task2C proposal `a9feb58da1df81d1d679b708c71de0b9360606687c170df711a1c57cf6fa1a50`. Implementation authorization is for the sixteen handwritten paths listed below plus normal ent generation, this plan and a verified scoped update to `docs/CONFIGURATION.md`. No stage/commit/push.

## Frozen boundary

Two new tables (Preparation/Grant), five new CASCADE FKs; four native tables/nine FKs total. No asset/registry FK, audit table, ordinary CRUD/proof/wire/module changes, HTTP/App/MCP/GUI/sourceworker integration, or local apply. State holds existing AEAD/keyID, never master or dependencies on client/gen/configuration/moejs. Bridge is std-only and builds canonical signed fields itself; evidence is compared against authoritative snapshots again in the final transaction. Proof means possession, not attestation.

| Table | Max row KiB | Parent rows/KiB | Owner rows/KiB | Instance rows/KiB |
| --- | ---: | ---: | ---: | ---: |
| Challenge | 4 | 8/32 | 32/128 | 128/512 |
| Child | 4 | 16/64 | 32/128 | 128/512 |
| Preparation | 16 | 16/256 | 32/512 | 128/2048 |
| Grant | 4 | 16/64 | 32/128 | 128/512 |
| Combined | — | 56/416 | 128/896 | 512/3584 |

Charge is 512 plus every persisted variable byte, including duplicate copies and AEAD overhead; terminal/expired rows count until real deletion. All admissions, including Begin/Bind, authenticate before checking the complete four-table candidate post-state in one transaction. Authorize charges preparation replacement plus grant insertion together. This bounds logical native records, not SQLite/WAL/disk/RAM. Old two-table writers must stop before enabling the new flow.

GC roots remain used/expired challenges or expired children. Count distinct descendant row IDs across both grant cascade paths before deletion; per pass ≤64 roots/256 rows/1MiB, with cooperative 50ms budget. A single oversized root fails closed; insufficient remaining pass capacity stops. Live-child terminal records are retained. Admission gets at most one prune pass; real Logout DELETE bypasses these GC/quota limits. Scheduled 30s lifecycle invocation remains Task3.

## Execution checkpoints

- [ ] **C2a, indivisible schema + quota + GC:** add `internal/server/ent/schema/{nativeapplypreparation,nativematerializationgrant}.go`; modify `schema/{nativeconsumersession,user}.go` reverse edges; normal generated ent. Add `internal/server/state/native_configuration{,_test}.go`; modify `native_consumer.go`, `native_consumer_internal_test.go`. TDD real SQLite nine FKs, consumed-chain RevokeSession/rollback/isolation, four-table rows/bytes at all three scopes, replacement charge, multi-Store last slot, bounded distinct closure; mutation controls for missing FK/table/descendants. Not complete with schema alone.
- [ ] **C2b, authoritative bridge + Prepare/Authorize:** add `internal/nativesessionauth/native_apply{,_test}.go`, `internal/server/auth/native_configuration{,_test}.go`, `internal/server/configuration/native_consumer{,_test}.go`; modify `internal/server/state/native_auth_seam{,_test}.go` and continue the new state files. Real owner MAC/Bind/Ed25519, test-only proof/vector parity, direct-factory bypass rejection. Prepare fixes revision/unique safe patch before commitment; Authorize binds full intent/consent, immutable binding and unique grant atomically. Immutable registry/moejs runs only outside transactions; transaction callbacks are bounded/pure. Native-login binds without grant.
- [ ] **C2c, Consume/Status:** continue those files. Writer gate then exact parent/native/owner/key, same-row asset metadata+ciphertext/readiness/revision and adapter recheck, final fresh clock then consumed CAS/COMMIT. Credentials share asset envelope CAS revision, no independent credential version. API key and auth token each return the real selected slot once; Status never returns secret/token/hash/patch. Real COMMIT-then-error/withheld, revocation/update orderings, double consume, cancellation and expiry tests; each guard gets a behavioral negative control.

Only known rollback BUSY may retry with a fresh transaction/evidence/accounting. Clear scratch on failed/unknown outcomes, never replay grant/secret responses. Fresh ctx gate after confirmed COMMIT withholds canceled publication but preserves durable state; check→driver commit/return is not atomic. Go strings/copies/GC prevent guaranteed erasure. Double-opaque wrappers protect all fmt verbs/private containers/real slog; raw generated DTOs remain unsafe to log.

All fixtures use synthetic keys, isolated HOME/TempDir SQLite, Go1.27.3 and CGO_ENABLED=0 only (no race/CGO1). Focused RED→GREEN, repeated tests/vet, behavioral mutations, then isolated candidate manifest/base/tree and report for independent acceptance. Full release/native apply remains pending. `docs/CONFIGURATION.md` changes only after actual verification; scope expansion is reported before touching files.

**Author checkpoint:** C2a/C2b/C2c are implemented and focused-verified, including actual COMMIT-then-error and COMMIT-then-cancel oracles, both real secret slots, native-login, and eleven behavioral negative controls. Initial identity time is sampled after writer acquisition; the final commit clock gate remains separate. Late callback expiry is committed as a sticky expired grant without publishing materialization. Boxes remain independent-acceptance gates, not an assertion that review or endpoint delivery has occurred. Final evidence/manifest is handed off separately from fixed base `4f9b529222feb7f86627446820d76e3d3c44037e`; concurrent B1a changes are not included and native maintenance admission remains a later handoff.
