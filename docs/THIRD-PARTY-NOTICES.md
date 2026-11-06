# Distribution license locations

## React Bits Web effects

The Web-only spotlight and mount-fade/stagger adapters reference DavidHDev/react-bits
commit `4dc7b5b7772e1217c8fb8d2a7aa074302180e5b4`. They are reduced adaptations,
not an unmodified upstream package or its complete component APIs. Provenance
and adaptation boundaries are in `web/src/components/react-bits/README.md`.

The full upstream MIT + Commons Clause license is preserved byte-for-byte at
`web/public/assets/react-bits-LICENSE.txt` (2,060 bytes), SHA256
`4bdd1fa59a6b7d5954bc3b213dbc34e455c635a94bbcecf9293553797d0fad02`.
It is **not** an unrestricted MIT grant; do not redistribute these effects as
a standalone component library. Cognate's root license does not replace it.

Vite copies the notice to `dist/assets/react-bits-LICENSE.txt`; the server embeds
that directory, including the complete text, into its executable. This keeps
the readable notice with a server binary even without companion files. It is
served anonymously at `/assets/react-bits-LICENSE.txt`, including in Docker/LPK
builds containing that executable. The real HTTP browser fixture verifies the
served bytes; `scripts/verify-react-bits.mjs` checks source and build output in
CI. This describes subsequent build delivery, not a retroactive change to
already-published 0.6.1 artifacts or a claim that a new release was published.

## Codex model metadata

The Codex model metadata and fallback prompt derive from OpenAI Codex source
`6ff1e1ca3b30fc8b914720681fcf5fe62e95f856`. Their readable distribution notice
combines the full upstream Apache License 2.0 and original NOTICE, including
the upstream Ratatui attribution. It is not merely an SPDX label or a hash.
The canonical file is
`internal/codexconfig/testdata/native-0.166.0/LICENSE` (11,558 bytes), SHA256
`85e1d58a617d42cc2095caf2a4b4bac26d3d260b4907e3e7020be545192c6aa6`.
The two upstream text sections remain byte-for-byte unchanged. The project
root `LICENSE` is separate and is not replaced by this file.

Recipients can read the combined text at these locations:

| Distribution | Location |
|---|---|
| CLI/server CI artifact, every OS/architecture | `CODEX-0.166.0-LICENSE.txt` beside the two binaries in the downloaded artifact |
| Final Docker image | `/usr/share/licenses/cognate/codex-0.166.0/LICENSE`, readable by the nonroot runtime user |
| MyGo macOS app and DMG | `Cognate.app/Contents/Resources/LICENSE` |
| MyGo Windows application and installed NSIS Setup payload | `LICENSE` beside `Cognate.exe` |
| MyGo Linux application and tar.gz | `LICENSE` beside `cognate-desktop` |
| MyGo Debian package | `/opt/cognate-desktop/LICENSE` |
| Desktop verification artifact's raw cross-builds | `cross/LICENSE` beside its binaries |

Keep the binary artifact's companion file when redistributing its binaries.
Copying a bare executable does not also copy a separate readable notice.
The container notice is inside the final image, not only in its build stage
or a CI-side companion file.

MyGo v0.7.1 uses the `resources` list in `cmd/cognate-desktop/mygo.json` to
copy the canonical file under its basename `LICENSE`. Its runtime resource
directory is `mygo.App.Path(mygo.PathResources)`: `Contents/Resources` in
macOS bundles and the executable's directory on Windows/Linux. Do not infer
that location from HOME or the working directory. Plain `go build`,
`go run`, and `go test` do not demonstrate a packaged resource; temporary
executables use the SDK's development-resource behavior instead.

CI verifies the full pinned bytes after binary artifact download, inside
each loaded final Docker image, and in actual desktop package payloads.
Desktop staging checks alone do not prove DMG, Debian, tar.gz or NSIS payloads.
Unsigned non-Linux builds without updater signing do not produce an extra
generic archive in this SDK, so Windows checks its actual Setup rather than
an assumed ZIP. Packaging checks do not establish native product or profile
qualification. Changes to the canonical text, its source provenance, or
these delivery paths require coordinated review and updated payload checks.

This notice describes the included metadata/prompt; it does not claim that
all third-party components or the official Codex executable are covered by
this one file.

## Gemini CLI 0.61.0 profile sources

The Gemini native provider profile derives from the fixed official
`@google/gemini-cli` 0.61.0 package and source commit
`bb523741c7429a44d03e964bc124c7c92df59d5f`. Its original Apache License 2.0
and complete bundled third-party notices are separate from the Codex notice:

| Canonical file under `internal/geminiconfig/testdata/native-profile-v0.61.0/` | Original package member | Bytes | SHA256 |
|---|---|---:|---|
| `LICENSE` | `LICENSE` | 11357 | `58d1e17ffe5109a7ae296caafcadfdbe6a7d176f0bc4ab01e12a689b0499d8bd` |
| `THIRD_PARTY_NOTICES` | `bundle/bundled/third_party/THIRD_PARTY_NOTICES` | 166147 | `fb2528fa004c7597e258b80c46370e27c5fe74565f6afb64228a4e8ac0e5037c` |

Both files are copied without rewriting, concatenation or newline conversion.
In particular, the original Gemini `LICENSE` has **no final newline**; adding
one changes the required bytes. The existing 449-file `package-files.sha256`
records both original package members. No Node or Gemini executable is included
by this legal-resource delivery, and no OAuth credentials are redistributed.

| Distribution | Gemini legal locations |
|---|---|
| CLI/server CI binary artifact and final CLI tar.gz/ZIP, every OS/architecture | `GEMINI-0.61.0-LICENSE.txt` and `GEMINI-0.61.0-THIRD_PARTY_NOTICES.txt` beside the binary/binaries |
| Final Docker image | `/usr/share/licenses/cognate/gemini-0.61.0/LICENSE` and `THIRD_PARTY_NOTICES`, readable by UID 10001 |
| MyGo macOS app and mounted DMG | `Cognate.app/Contents/Resources/native-profile-v0.61.0/{LICENSE,THIRD_PARTY_NOTICES}` |
| MyGo Windows application and actual NSIS Setup payload | `native-profile-v0.61.0/{LICENSE,THIRD_PARTY_NOTICES}` beside `Cognate.exe` |
| MyGo Linux application and tar.gz | `native-profile-v0.61.0/{LICENSE,THIRD_PARTY_NOTICES}` beside `cognate-desktop` |
| MyGo Debian payload | `/opt/cognate-desktop/native-profile-v0.61.0/{LICENSE,THIRD_PARTY_NOTICES}` |
| Desktop raw cross-build artifact | `cross/native-profile-v0.61.0/{LICENSE,THIRD_PARTY_NOTICES}` |

MyGo v0.7.1 supports a listed directory resource under its basename, including
nested files. The five-text-file `native-profile-v0.61.0` directory also carries
the pinned package manifest, source provenance and synthetic codec vectors. Its exact input allowlist
rejects additional files or symlinks. This distinct directory preserves the
existing Codex `LICENSE` at the resource root; the two must never overwrite one
another. Runtime lookup remains `mygo.App.Path(mygo.PathResources)`.

Workflow gates check pinned source bytes, downloaded binary artifacts, loaded
final images and each actual release/archive/installer payload. Codex checks
remain mandatory. Local resource staging is not evidence that a DMG was
mounted, NSIS payload extracted, or a final Docker image built and read. These
changes specify delivery gates for subsequent builds, not retroactive repair
of already-published assets or new native-profile/platform qualification.
