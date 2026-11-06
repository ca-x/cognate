# Component releases

## 2026-11-06 OpenCode import update

Published component set: Server/LazyCat 0.6.0, CLI 0.3.0 and native GUI 0.1.4.
All source tags bind `ed00481e84fb29fce6d5a50a2140875939911823`.
See `RELEASE-v0.6.0.md` and `RELEASE-v0.6.0-VERIFICATION.md` for complete downloaded
asset checks, Docker digests, MiaoMiao acceptance and the retained failed Darwin
observation. Upgrade import clients together: old clients can reject an entire
snapshot containing a READY OpenCode candidate; no capability negotiation exists.
This controlled v1 import update does not finish the broader product backlog.

## Server 0.3.0

`server-v0.3.0` fixes source `20c7a193a9582cd14cf9d29cbbbdcfbf414e54ec`
and distributes explicit Linux legacy archive repair through the amd64/arm64
Docker image. LazyCat Exec `0.3.0` pins the same source and is accepted by
MiaoMiao only; storefront activation and device upgrade are not confirmed.
CLI remains `0.2.0` and Desktop remains `0.1.3`: no unaccepted MyGo, Pack or
restore changes are included. See `RELEASE-v0.3.0.md` and
`RELEASE-v0.3.0-VERIFICATION.md`. Previous immutable tags are unchanged.

## 2026-11-01 follow-up distribution

The published follow-up component set is `server-v0.2.0`, `cli-v0.2.0` and
`gui-v0.1.3`, with separately versioned LazyCat Exec `0.2.0`. Scope and upgrade
limitations are in `RELEASE-v0.2.0.md`; complete downloaded assets, image
verification and MiaoMiao acceptance are recorded in
`RELEASE-v0.2.0-VERIFICATION.md`. These are new immutable tags; prior tags remain
unchanged. In-progress restore, MyGo native provider application and Pack
automatic configuration are not included. The failed Darwin observation is
retained and does not acquire runtime qualification from this distribution.

Server, CLI and native GUI have independent versions and tag triggers. Push a tag
only after its component has passed acceptance. A tag is not a declaration that
the entire Cognate product is complete.

The 2026-11-01 stable component release is described in `RELEASE-v0.1.0.md`:
`server-v0.1.0`, `cli-v0.1.0`, and `gui-v0.1.2`. Published preview tags remain
unchanged. Stable distribution does not waive the documented feature, platform,
backup/restore, signing or complete-product acceptance gates. Publish CLI/GUI
drafts only after verifying the delivered archives and checksums; replace draft
notes with the component's supported scope and known limitations before publication.

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

A non-publishing desktop dispatch still runs verification and all three OS
packaging jobs. The final `package-check` requires both verification and the
packaging matrix to succeed; skipped packaging is not a successful packaging
receipt. The publication job is separate and remains skipped for these runs.
Inspect individual job results, not just the workflow's overall green status.

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

## Gemini legal delivery gate

Every subsequent build that includes the Gemini native profile must distribute
the unchanged original Gemini CLI 0.61.0 `LICENSE` and complete
`THIRD_PARTY_NOTICES` alongside the existing Codex combined license/notice.
Exact source hashes, byte counts and recipient paths are in
`THIRD-PARTY-NOTICES.md`. Gemini's original `LICENSE` has no EOF newline; do not
normalize it on checkout, concatenate it with Codex text, or replace it with a
summary/SPDX identifier.

The build workflow checks both files after binary artifact download and inside
each loaded final Docker image as the unprivileged runtime user. The CLI release
workflow includes them in every tar.gz/ZIP and reopens each final archive to
verify exact regular members and bytes before any draft is created. Server
promotion continues to use the tested OCI archives without rebuilding.

Desktop packaging uses the SDK's directory-resource support, retaining Codex's
root `LICENSE` and Gemini's separate `native-profile-v0.61.0` directory. Both
architectures must pass staged-resource checks **and** actual tar.gz/Debian,
mounted DMG or extracted NSIS Setup legal checks on the corresponding runners.
The Gemini input directory is restricted to its five pinned textual resources;
Node/Gemini executables are not added to any distribution by this change.

Do not mark those platform gates passed from local staging, plain Go builds or
source checks. A missing notice, symlink, duplicate archive legal member, changed
byte/newline or missing installer fails the gate. Prior release receipts remain
historical and are not evidence that this new legal delivery was present.

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
