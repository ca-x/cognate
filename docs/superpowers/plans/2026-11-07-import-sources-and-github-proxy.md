# Import source registration and GitHub forward proxy

## Goal and evidence

The user's real encrypted Magpie export decrypts successfully. Its safe projection
contains 14 repository references and 35 GitHub skills. On an empty library all
35 skills require an existing verified repository, so none can be selected.
Repository commits currently download and scan before writing their first record.
Neither the export nor any decrypted user content is a test fixture or Git input.

## Contract

- Keep a saved repository source and a requested skill reference separate from a
  collected repository/skill. Unfetched content has no archive/content hash and
  must not be published, installed or attached to a Pack as verified content.
- The Web import opts into `register_sources` for Magpie and cc-switch. The option
  participates in the sealed preview digest; an old client retaining the default
  continues to use the original collect-and-verify protocol.
- In this mode an uncollected GitHub skill is selectable as a source reference,
  including a repository-root reference. Selecting a skill also saves its source.
  Repository-only imports do not download anything. Persist sources, references,
  configuration assets and the terminal receipt in the same short transaction.
- Sources are private, owner-scoped, bounded and idempotent by repository/branch
  and skill name/path. List and delete them independently of verified contents.
  The library presents saved sources and an explicit, retryable synchronization
  action using the existing whole-repository download and SKILL.md scan.
- Preserve verified bindings, stale-preview checks, notice acknowledgements,
  terminal receipt reconciliation and the no-blind-replay rule.
- GitHub fetching supports explicit HTTP/HTTPS/SOCKS5/SOCKS5h forward proxies.
  Keep the GitHub destination and verified TLS intact; do not add download mirror
  URL rewrites. An empty setting retains the standard environment proxy behavior.
  Admin settings are encrypted, do not echo credentials, and apply without restart.
  Only GitHub traffic is affected, not OIDC, backup storage or arbitrary endpoints.

## Sequence and verification

1. Add failing synthetic cold-library import/source persistence tests.
2. Add ent source/reference entities and additive Buf-managed RPC fields/methods.
3. Implement atomic reference registration and preserve legacy import behavior.
4. Expose saved sources and selectable references through the existing React,
   TanStack and Radix UI; retain bilingual, keyboard and reduced-motion behavior.
5. Integrate the independently tested forward-proxy transport and admin settings.
6. Re-run the private export through isolated local HTTP/SQLite and browser flows;
   verify 14 sources / 35 references, no external requests and restart persistence.
7. Run targeted and broader Go/Web/Buf tests and CGO-disabled cross-builds, review
   the diff for user data/secrets, then commit and SSH-push the completed stage.

This change does not create a new release tag without a new release instruction.
