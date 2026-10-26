# Release validation — 1.0.0-alpha.3

This release adds Universal 2 macOS distribution to alpha.2. Historical alpha.2
assets/validation remain unchanged. See [RELEASING.md](RELEASING.md) for the
persistent release procedure and [AGENTS.md](../AGENTS.md) for agent instructions.

## Architecture contract

The distributed app, DMG, PKG, app ZIP and portable Mac tools contain the same
Universal 2 code. Both `x86_64` and `arm64` are required in:

- native `pubcraft-launcher` / CFBundleExecutable;
- C `pubcraft`;
- Go `pubcraftgui`;
- WebKit `pubcraft-html2pdf`.

Minimum OS metadata is checked per slice and in the app: macOS 13.0 on both CPUs.
The launcher is compiled from our AppKit source, not copied from a system applet
whose ARM deployment target can be newer than the advertised minimum.

## Automated release guards

- A portable Mach-O parser rejects thin, missing, duplicate, truncated, overlapping
  or inconsistent architecture slices; Apple's `lipo` independently checks them.
- Each executable and assembled app undergoes code-signature verification.
- The verifier expands app ZIP/PKG, mounts DMG read-only, extracts the portable
  archive and checks architecture, public permissions and identical executable
  hashes across all formats. Temporary mounts are detached after checks.
- DMG/PKG/app ZIP copies normalize file permissions in disposable staging, avoiding
  owner-only modes inherited from a development volume. The app remains ad-hoc
  signed; permission normalization does not rewrite signed code/data.
- Clean Go source provenance is checked independently for both GUI slices.
- Live C/Go/NSIS/HTML/localized UI versions must agree with `VERSION`; `BUILD_NUMBER`
  must be positive and increase relative to prior release history when available.
  Native release checkouts fetch full history. `.go-version` pins the release toolchain.
- Eight fast Python regression tests cover architecture-table failures, deployment
  metadata parsing (linker versions are not OS versions), version-marker mismatch
  build-number monotonicity and explicit UTF-8 source reading on Windows.

## Native macOS execution — same artifact set

The release workflow builds **one** Universal artifact set on `macos-15`, then
passes those exact files to two dependent native jobs:

| Job | Runner | Execution |
| --- | --- | --- |
| Intel | `macos-15-intel` | `x86_64` / Go `amd64` |
| Apple Silicon | `macos-15` | `arm64` / Go `arm64` |

Each job checks packages/signatures/source revision and then:

1. starts the real native `.app` launcher with isolated HOME/configuration;
2. verifies the launched GUI reports the expected native runtime architecture;
3. executes CLI book creation, EPUB generation and synthetic archive extraction;
4. checks ZIP CRCs, the first uncompressed `mimetype` entry and XML documents;
5. executes the WebKit helper's native slice and checks a nonempty PDF result;
6. runs CLI regressions, uncached Go race tests, `go vet` and Python guard tests.

The host is checked explicitly and executable slices are selected explicitly for
smoke tests. Cross-compilation, architecture listing and a Rosetta run are not
counted as native Apple Silicon execution. The two proof JSON files must report
identical executable hashes and the intended source revision before publication.

## Other checks and platforms

- Native Linux/Windows builds and package checks are retained and rerun for the
  new version. Linux covers portable/extracted DEB/AppImage binaries; Windows covers
  portable files and disposable silent-installer installation/startup.
- The existing browser regression suite has 15 runners, tested with a dedicated
  Brave/CDP profile and the real PDF-renderer adapter on macOS Intel. This browser
  suite is not claimed as full interactive coverage on all platforms.
- EPUBCheck 5.4.0 is used on the same 14 synthetic EPUB cases documented for
  alpha.2. Actual results, native workflow URL, source commit and asset hashes are
  attached in the release's machine-readable validation manifest.
- All downloadable artifacts have SHA-256 checksums; the public source checkout
  excludes private manuscripts/templates, local state and intermediate binaries.

## Limits — do not omit these from future notes

- Ad-hoc signing is not Developer ID signing or notarization. PKG and Windows
  installer are not publisher-signed. Security warnings may appear.
- Native tests run on the listed hosted OS versions, not every Mac model or macOS
  13 point release. Binary deployment compatibility is checked, not assumed.
- The WebKit helper test is a continuous PDF snapshot, not KDP multi-page print
  certification. Distributor preview/validation remains necessary.
- Linux FUSE/desktop integration and Windows interactive PDF/browser coverage are
  not exhaustive. Linux ARM packages are not included.
- EPUBCheck sample passes do not guarantee every manuscript or retailer approval.

Final publication requires the complete workflow to pass, including both native
Mac jobs; an isolated successful build job is insufficient. Exact source/build
provenance and native proof files are retained with release validation metadata.
