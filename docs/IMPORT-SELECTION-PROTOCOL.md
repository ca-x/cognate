# Import selection protocol

Status: G0 protocol prerequisite, not a working import feature. The complete flow
must still implement snapshot projection, selection, authenticated receipts,
repository collection, asset transactions, and the Web confirmation flow.

The two existing import RPCs and their messages remain unchanged. Three additive
RPCs share the existing service: `PreviewImportSelection`, `CommitImportSelection`,
and `GetImportCommitStatus`. Until the authenticated controller is connected,
the guard rejects these methods before reading a body. No permissive legacy codec
may decode a new selection request.

## Field ledger

Numbers below are permanent protobuf tags. Repeated values preserve source or
explicit selection order. Singular messages have presence. Optional scalars retain
absence separately from empty, false, and zero; invalid present values are rejected,
not normalized. New request JSON rejects every explicit null, including optional
fields, messages, repeated fields, and their elements. Omitting an optional field
is distinct from supplying it. Unknown/duplicate fields, unknown enum values,
duplicate oneofs, wrong wire types, and mixed candidate options are rejected.
Protobuf omission alone does not make a required business value valid.

| Message | Tags |
| --- | --- |
| PreviewImportSelectionRequest | source=1; source_data=2 bytes, redacted; options=3 repeated ImportCandidateOptions |
| ImportCandidateOptions | candidate_id=1; name=2 optional string; auth oneof { claude_auth_mode=3, gemini_auth_mode=4 }; gemini_key_transport=5 optional enum; skill_binding=6 SkillBinding |
| PreviewImportSelectionResponse | ticket=1 bytes, redacted; expires_ms=2 int64; repositories=3 repeated ImportRepositoryCandidate; skills=4 repeated ImportSkillCandidate; configurations=5 repeated ImportConfigurationCandidate; notices=6 repeated ImportNotice |
| ImportRepositoryCandidate | id=1; hint=2 existing RepositoryCandidate; status=3 ImportCandidateStatus |
| ImportSkillCandidate | id=1; safe_name=2; repository_candidate_id=3; status=4; binding=5 SkillBinding; notices=6 repeated ImportNotice |
| SkillBinding | repository_id=1; archive_hash=2; skill_id=3; logical_path=4; content_hash=5; repository=6; branch=7 |
| ImportConfigurationCandidate | id=1; known_agent_id=2 string; kind=3 configuration.v1.AssetKind; status=4; draft=5 configuration.v1.Draft; notices=6 repeated ImportNotice; credential_slots=7 repeated ImportCredentialSlot; safe_name=8 |
| ImportCredentialSlot | slot_id=1 string; present=2 bool |
| ImportNotice | id=1; code=2 ImportNoticeCode; count=3 uint32 |
| CommitImportSelectionRequest | ticket=1 bytes, redacted; source=2; source_data=3 bytes, redacted; options=4 repeated ImportCandidateOptions; operation_id=5 bytes16; repository_ids=6 repeated string; skill_ids=7 repeated string; configuration_ids=8 repeated string; ack_notice_ids=9 repeated string |
| CommitImportSelectionResponse | receipt=1 ImportReceipt |
| GetImportCommitStatusRequest | operation_id=1 bytes16 |
| GetImportCommitStatusResponse | observation=1 ImportCommitObservation; receipt=2 ImportReceipt |
| ImportReceipt | operation_id=1 bytes16; revision=2 int64; phase=3 ImportReceiptPhase; repository_outcomes=4 repeated ImportRepositoryOutcome; selected_skill_bindings=5 repeated SkillBinding; created_assets=6 repeated configuration.v1.Asset; notices=7 repeated ImportNotice |
| ImportRepositoryOutcome | candidate_id=1; code=2 ImportRepositoryOutcomeCode; repository_id=3 optional string |

All unspecified enum values are zero and invalid as a selected business state.
`source` uses ImportSelectionSource: CCSWITCH_SCHEMA24=1, MAGPIE_LIBRARY_JSON=2.
The auth and transport enums reuse configuration.v1, including their existing
numbers; no new import-specific auth interpretation exists.

| Enum | Nonzero values |
| --- | --- |
| ImportCandidateStatus | READY=1; NEEDS_CHOICE=2; NEEDS_REPOSITORY_IMPORT=3; UNRESOLVED=4; UNSUPPORTED_AGENT=5; UNSUPPORTED_KIND=6; UNSUPPORTED_FIELD=7; NAME_CONFLICT=8 |
| ImportNoticeCode | CREDENTIALS_OMITTED=1; METADATA_EXCLUDED=2; OTHER_SOURCE_DATA_EXCLUDED=3; SOURCE_CONTENT_NOT_COMPARED=4; UNKNOWN_SOURCE_SECTION_EXCLUDED=5 |
| ImportReceiptPhase | RESERVED=1; COLLECTING=2; COMPLETED=3; PARTIAL=4; FAILED=5 |
| ImportCommitObservation | NOT_OBSERVED=1; IN_PROGRESS=2; UNKNOWN=3; TERMINAL=4 |
| ImportRepositoryOutcomeCode | COLLECTED=1; ALREADY_COLLECTED=2; NOT_ATTEMPTED=3; CANCELLED=4; AUTHORIZATION_LOST=5; SOURCE_REJECTED=6; CAPACITY_EXHAUSTED=7; UNKNOWN=8 |

## Presence and semantic validation

- Candidate IDs and notice IDs are opaque hex64 values, never raw source IDs.
  Known agent IDs are the documented fourteen IDs; unknown source strings are
  not reflected. Only the first three provider types are creatable in this slice.
- Options apply to a known candidate at most once. Configuration options permit
  explicit name and the matching agent auth/transport; skill options permit only
  a binding. Repository options, cross-kind options, and cross-agent auth fail.
- A binding includes the canonical repository/ref as well as owner-library ID,
  archive hash, skill ID, logical path and content hash. All fields are required
  together. User-selected cc-switch bindings require content-not-compared ack;
  a source hash is not a Cognate content hash. Commit revalidates all current pins.
- Ready configuration candidates have a matching secret-free typed Draft, kind,
  agent and safe_name. Other statuses have no Draft. Credential slots only expose
  recognized slot IDs and a presence bit, never values. These are not migration
  authorizations. Unsupported candidates cannot be selected.
- Notices have a stable ID, closed code and positive count. Acknowledgements name
  exactly the required exclusions for this preview; no arbitrary warning text.
- Ticket is exactly 290 bytes, expires_ms is Unix milliseconds, lifetime five
  minutes. It binds current owner, configured canonical origin, snapshot digest,
  projection, actual adapter plus host-rule identity, resolution, and options.
- Commit resends the same raw snapshot, ticket and options. It chooses at most
  16 repositories, 128 skills and 32 configurations, with no duplicate IDs. An
  all-empty selection is invalid. Order is bound into the request digest.
- Status NOT_OBSERVED has no receipt. IN_PROGRESS and UNKNOWN require a matching
  nonterminal receipt; TERMINAL requires COMPLETED/PARTIAL/FAILED. Reserved and
  Collecting never imply success; Unknown never causes automatic replay.
- Repository outcomes cover each selected repository in order. Only COLLECTED
  and ALREADY_COLLECTED have repository_id. A source failure is a fixed code,
  never an upstream error string. Completed requires all repository successes
  and the final asset/reference transaction. Partial is not rollback.

## Storage prerequisite

Only ImportReceipt is added, with a required immutable user FK and cascade delete.
Unique keys: (user_id, operation_id), (user_id, ticket_id). Bytes operation_id,
ticket_id and execution_id have length16; request_digest and origin_digest length32.
Identity and digests are immutable; execution_id is immutable and never transferred
to a restart. Revision is positive (default1). Phase is reserved/collecting/completed/
partial/failed, without a successful default. created_at is immutable, updated_at
is required. Ciphertext is nonempty and Sensitive; fixed immutable charge=131072.
No source, ticket, Draft, credential or preview is persisted. Runtime quotas and
AEAD AAD enforcement belong to the subsequent state transaction implementation,
not to the existence of this schema.

## Limits and compatibility

Preview/Commit wire limit24MiB, decoded source16MiB (Magpie1MiB); preview response
2MiB. Status request64KiB, response128KiB. Identity encoding only; authentication
precedes body reads. Preview deadlines5s for Magpie/15s for snapshot, Commit120s,
Status5s. New strict codec dispatches by exact message type; old unknown-field
behavior and old null semantics are unchanged. Standard Go/Connect and TS Buf
generation must change only the three import outputs. No configuration schema
change is necessary. Existing descriptor compatibility and regenerated equality
are prerequisites, not evidence of a working end-to-end selection flow.
