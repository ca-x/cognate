# Distribution license locations

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
