# Component releases

Server, CLI and native GUI have independent versions and tag triggers. Push a tag
only after its component has passed acceptance. A tag is not a declaration that
the entire Cognate product is complete.

| Tag | Checks and output |
| --- | --- |
| `server-v0.1.0-rc.1` | Full shared Go/Web/Buf checks, both Docker architecture smoke tests, then promote the tested OCI artifacts to `ghcr.io/ca-x/cognate:server-v0.1.0-rc.1`; no CLI release or GUI packaging |
| `cli-v0.1.0-rc.1` | Full shared checks, six CLI/server cross-build checks, then a CLI-only **draft** GitHub Release with six archives, bundled Codex license and `SHA256SUMS`; no Docker publication or GUI packaging |
| `gui-v0.1.0` | Existing native verification and all three OS packaging jobs, then an unsigned/ad-hoc **draft** GUI Release; no server or CLI release |

Examples are naming conventions, not already published versions. Server and CLI
accept `X.Y.Z`, optionally followed by `-alpha.N`, `-beta.N` or `-rc.N`, without
leading zeroes. GUI tags must exactly match `gui-v` plus the version in
`cmd/cognate-desktop/mygo.json`. Legacy `desktop-v*` and generic `v*` do not publish.
Ordinary branch pushes and pull requests retain build checks without publication.
Manual desktop runs do not publish unless `publish` is explicitly enabled and an
existing matching GUI tag is supplied.

## Server image

The server release reuses the same workflow as Build. Only after both tested OCI
artifacts pass does the publisher copy them without rebuilding. Images have
`linux/amd64` and `linux/arm64` variants. The workflow reads back the published
manifest, pulls by digest, runs both variants with `--help`, and checks anonymous
registry access. The Actions summary records the digest for deployment pinning.
No `latest`, stable alias, GitHub server Release or GUI/CLI tag is created.

GHCR packages may initially be private. A failed anonymous access check means
the upload may exist but public distribution is **not verified**. The package
owner must make it public in GitHub package settings and verify anonymous access;
do not blindly repeat the publishing job. Likewise, a failed post-upload check
does not mean the registry write was rolled back. Inspect remote state first.

Use a new version tag for new source; do not move published tags. Same-tag runs
are serialized. Protect release tags with GitHub repository rulesets and limit
push permission to maintainers. Only the server publisher has `packages: write`;
only CLI/GUI draft publishers have `contents: write`.

Preview images retain the feature and platform limitations described in README
and the acceptance documents. Full backup/restore, the complete agent matrix and
automatic client configuration application remain separate acceptance gates.
