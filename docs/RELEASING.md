# Release playbook (maintainers and agents)

This is the persistent procedure for future pushes/releases. Read [AGENTS.md](../AGENTS.md)
first. A push is a source update, **not** permission to reuse old executable files.

## 1. Prepare a clean source revision

1. Inspect `git status`, remotes and current tags. Preserve existing user changes.
2. Work in a `release/...` branch of this public checkout. Review individual source
   changes from any test workspace; exclude personal projects and generated files.
3. Increment `VERSION` and the monotonically increasing `BUILD_NUMBER`. Update:
   - C `VERSION` in `src/pubcraft.c`;
   - Go `appVersion` in `src/pubcraftgui/version.go`;
   - NSIS `PUBCRAFT_VERSION`;
   - welcome HTML and all localized `welcome_version` strings.
4. Run `python3 scripts/check_release_version.py`. Add bilingual release notes and
   a new validation record; update both live READMEs and changelog. Historical
   release notes/tags/assets are immutable.
5. Commit source before final builds. Release binaries must embed that clean
   revision; final source/tag and build-provenance records must agree.

## 2. Build the convenient Mac app — always Universal

On a Mac with an Apple SDK, codesign tools, Python 3.12+ and the Go version in
`.go-version`:

```sh
make pkg-macos
make verify-macos-release
```

This rebuilds the C CLI and WebKit helper with `x86_64` and `arm64`, builds both Go
GUI slices and combines them with `lipo`. The native AppKit launcher is compiled
for both architectures too (not copied from a system AppleScript applet). It then
assembles `PubCraft.app`, sets
both minimum-OS declarations to 13.0, signs after assembly, and creates:

- `PubCraft-<version>.dmg` — app + Applications shortcut;
- `PubCraft-<version>-macos-universal.app.zip` — directly extractable app;
- `PubCraft-<version>.pkg` — app installer;
- `PubCraft-<version>-macos.tar.gz` — portable CLI/GUI/helper/resources.

`make all` is useful for development but builds for the host only. It must never
be substituted for `make pkg-macos`. Merely making the AppleScript launcher
Universal does not make the Go GUI, CLI or helper Universal. A system applet can
also embed an ARM minimum OS newer than our macOS 13 contract; keep the native
launcher and check every slice's deployment metadata.

The verifier rejects missing architectures, invalid signatures, an incorrect
version/minimum OS, different executables between formats, or an unexpected
source revision. The archive checker also parses Mach-O headers without requiring
Mac tools, so a thin macOS portable archive is rejected even on Linux/Windows.

## 3. Native checks of the exact same artifacts

Push the release branch to trigger `.github/workflows/release-build.yml`:

- Linux builds/tests native binaries and checks portable/DEB/AppImage contents.
- Windows builds native C/Go, checks export/startup, portable files and a disposable
  silent installer installation.
- The macOS build job creates **one** Universal artifact set.
- Two dependent jobs download that exact set: one Intel Mac, one Apple Silicon
  Mac. They check all formats, source revision, both slices, code signatures,
  native GUI architecture and CLI/PDF-helper execution. They must not independently
  rebuild and test unrelated local packages instead.

All jobs must succeed. If either native Mac job is skipped, unavailable, emulated
or fails, leave the release unpublished. Update runner labels when necessary,
without weakening the explicit host/architecture checks. The documented labels
are `macos-15-intel` (Intel) and `macos-15` (Apple Silicon); never infer architecture
from the word `macos-latest`.

Other required checks:

```sh
make check
go -C src/pubcraftgui vet ./...
python3 -m unittest discover -s tests -p test_release_guards.py
node --check src/pubcraftgui/web/static/app.js
node --check src/pubcraftgui/web/static/watch.js
# Dedicated profile and browser test renderer; see tests/README.md:
BROWSER_CDP_URL=http://127.0.0.1:9222 CHROMIUM_BIN="$PWD/tests/brave_render.sh" make check-browser
```

Use `-count=1` when Go tests depend on a changed C binary. Use synthetic books and
isolated configuration. Optional private template tests are opt-in and must never
supply fixtures to the repository or release archive.

## 4. Stage and verify downloads

- Download only successful workflow artifacts whose `head_sha` matches the
  intended source revision. Restore executable permissions after Actions artifact
  extraction where needed; the inner app ZIP/tar preserve their permissions.
- Keep only current-version artifacts in the release staging area. Back up old
  outputs elsewhere rather than mixing versions.
- Run `tests/check_packages.py` and the Universal verifier; check package formats
  and inspect version/source metadata. Filesystem timestamps can change after a
  branch checkout: use verified build provenance, never `touch` an old binary to
  make it appear current.
- Generate SHA-256 checksums for every downloadable package and the validation
  manifest. Do not include the checksum file in its own digest list.
- Check EPUB output with EPUBCheck when available and report its actual version,
  sample count and results. This is not distributor certification.

## 5. Publish safely

1. Fast-forward/merge only reviewed, green source to `main`; wait for its CI.
2. Create a NEW annotated release tag at that same source revision.
3. Create a draft pre-release with complete notes and current assets. Convert
   relative documentation links to explicit tagged GitHub URLs in the release body.
4. Inspect the draft using `gh release view` or its numeric REST ID. Releases-by-tag
   can return 404 for a draft; that is not a reason to publish before validation.
5. Verify names, sizes and remote SHA-256 digests against local files. Only then
   publish. Every command must stop on failure (`set -e`, `check=True`).
6. Recheck public download links, digests, release notes and a clean working tree.

Do not silently change a published release to add a different CPU implementation.
Use a new version/tag so users can identify exactly what they downloaded.

## Signing and compatibility disclosures

The default build is ad-hoc signed, not Developer ID signed/notarized. It may show
macOS security warnings. PKG and Windows installer are unsigned unless the owner
supplies real signing credentials. Never invent signing identities or disable OS
security controls. Universal 2 removes the Rosetta requirement; it does not provide
notarization or certify the oldest supported OS/each device model.
