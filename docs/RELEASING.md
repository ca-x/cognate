# Component releases

Server, CLI and native GUI have independent versions and tag triggers. Push a tag
only after its component has passed acceptance. A tag is not a declaration that
the entire Cognate product is complete.

| Tag | Checks and output |
| --- | --- |
| `server-v0.1.0-rc.1` | Full shared Go/Web/Buf checks, both Docker architecture smoke tests, then promote the tested OCI artifacts to `ghcr.io/ca-x/cognate:server-v0.1.0-rc.1`; no CLI release or GUI packaging |
| `cli-v0.1.0-rc.1` | Full shared checks, six CLI/server cross-build checks, then a CLI-only **draft** GitHub Release with six archives, bundled Codex license and `SHA256SUMS`; no Docker publication or GUI packaging |
| `gui-v0.1.0` | Existing native verification and all three OS packaging jobs, then an unsigned/ad-hoc **draft** GUI Release; no server or CLI release |

Examples describe routing; actual delivery receipts are recorded below. Server and CLI
accept `X.Y.Z`, optionally followed by `-alpha.N`, `-beta.N` or `-rc.N`, without
leading zeroes. GUI tags must exactly match `gui-v` plus the version in
`cmd/cognate-desktop/mygo.json`. Legacy `desktop-v*` and generic `v*` do not publish.
Ordinary branch pushes and pull requests retain build checks without publication.
Manual desktop runs do not publish unless `publish` is explicitly enabled and an
existing matching GUI tag is supplied.

The first three-component preview uses `server-v0.1.0-rc.2`,
`cli-v0.1.0-rc.1` and `gui-v0.1.0`. The GUI package keeps its numeric internal
version for native packaging, but its GitHub Release is a prerelease, not a GA.
The workflows first create CLI/GUI drafts; after their required checks and
downloaded asset/checksum verification, the maintainer publishes those drafts
with `prerelease=true` and without promoting them to latest. See
`PREVIEW-RELEASE.md` for release notes and `TODO.md` for deferred work.

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

`Verify published server image` is a separate read-only workflow, reusable by the
publisher or manually dispatched with the existing tag and expected index digest.
It never builds or pushes images. Each architecture is pulled using its distinct
child manifest digest from that index: pulling both platforms using one index
digest into a classic Docker image store can fail with `cannot overwrite digest`.
If a post-upload verification fails, repair or rerun this verifier, not the
publisher. Keep the failed run as evidence.

The first `server-v0.1.0-rc.1` run (41149414526, source `289dc98`) passed shared
checks and both full container smokes, uploaded both platform artifacts and index
`sha256:ac5c5721b5ad8eaa9469c14f0607ce0e7bb2b7c6c7aadf26e06fe715865f36fc`,
then failed at the second platform pull because of that local Docker store
conflict. It is not a successful public-release receipt; independent verification
of the same uploaded digest is required without rewriting the tag or image.

That independent check completed successfully in run **41150799480**, verifier
source `21bd8a0`: the unchanged index and both distinct platform manifests were
pulled, their actual architectures checked, both server binaries executed with
`--help`, and anonymous access returned the same expected index digest. This
read-only run did not rebuild or republish anything. The original publisher run
remains failed; the two receipts together document the preview's delivery.

The server preview is now publicly pullable without logging into GHCR:

```sh
docker pull ghcr.io/ca-x/cognate:server-v0.1.0-rc.1
```

For immutable deployment, use
`ghcr.io/ca-x/cognate@sha256:ac5c5721b5ad8eaa9469c14f0607ce0e7bb2b7c6c7aadf26e06fe715865f36fc`.
Image source remains `289dc98`; later verifier/docs commits are not rebuilt into
this preview. Persistent data and an external private master key remain required;
follow the server setup instructions in README and do not expose plain HTTP
directly to the Internet.

Use a new version tag for new source; do not move published tags. Same-tag runs
are serialized. Protect release tags with GitHub repository rulesets and limit
push permission to maintainers. Only the server publisher has `packages: write`;
only CLI/GUI draft publishers have `contents: write`.

Preview images retain the feature and platform limitations described in README
and the acceptance documents. Full backup/restore, the complete agent matrix and
automatic client configuration application remain separate acceptance gates.

## First three-component preview (2026-10-31)

All three source tags bind commit `2bb9b137e124a89833d3c66f1c59018d7e8e44c5`.

| Component | Delivery | Successful Actions run |
| --- | --- | --- |
| Server | `ghcr.io/ca-x/cognate:server-v0.1.0-rc.2`, Linux amd64/arm64 | `41157352875` |
| CLI | [cli-v0.1.0-rc.1](https://github.com/ca-x/cognate/releases/tag/cli-v0.1.0-rc.1), six archives plus checksums | `41157361455` |
| GUI | [gui-v0.1.0](https://github.com/ca-x/cognate/releases/tag/gui-v0.1.0), eight native packages plus checksums | `41157371791` |

The server index digest is
`sha256:52c8bdc6d3f0959d51469de730306eeddf93e3c41b59b087c2c6909bad1c622c`.
Both tested OCI architectures passed promotion and digest-specific pull/help
verification; main also fetched the anonymous index, hashed its actual bytes and
confirmed exactly those two platforms. The old rc.1 tag and image remain unchanged.

All fourteen CLI/GUI release archives/installers were downloaded and matched
their published `SHA256SUMS`. The six CLI payloads also matched the source SHA,
Go 1.27.3, CGO0 and target platform metadata, contained the exact Codex license,
and the downloaded Linux amd64 CLI ran `--help` in an isolated environment.
GUI Linux archives passed source/CGO0 checks and both Debian payload licenses
were read back; the native workflow separately checked actual DMG and NSIS
license payloads, source metadata and a Linux native window smoke.

Only after those gates were the CLI/GUI drafts made public as prereleases,
without promoting either to latest. Slow ordinary downloads were stopped only
after independently fetched range-completed bytes passed their full expected
hashes; final checksums covered every complete delivered file. The interrupted
transfer commands are not product-test failures or evidence of missing assets.
Preview packaging does not qualify macOS/Windows installation or configuration
application; see `PREVIEW-RELEASE.md` and `TODO.md`.

## LazyCat Exec distribution

The separately versioned packaging repository is
[lazycat-contrib/cognate-lzcapp](https://github.com/lazycat-contrib/cognate-lzcapp).
It pins the same upstream source and uses the reusable LazyCat Action with Go
1.27.3, Node 26.12.0 and CGO0. Only MiaoMiao is enabled; the official store is off.

Run `41159355144` built and published
`community.lazycat.app.cognate-v0.1.0-rc.2.lpk` (67,970,048 bytes), SHA256
`c1bb1b0eee244a8504bdb22bbc2401b97503e0e76a49430b65832f2f71381b63`.
The private-store response accepted application `670`, version `3752`, with the
exact GitHub Release Asset URL and same SHA256. Its GitHub Release is marked
prerelease. This proves build and store submission, not real-device installation,
storefront visibility, platform OIDC success or full-product acceptance.
The first rc.1 failed before LPK creation due to a missing root Node cache lock;
its failure and tag are retained, not rewritten as success.
