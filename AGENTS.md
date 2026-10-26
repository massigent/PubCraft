# Instructions for coding and release agents

Read this file and [docs/RELEASING.md](docs/RELEASING.md) before changing builds,
download links, release metadata or GitHub releases.

## Public checkout / privacy

- This checkout is the sanitized, public repository. Do not bulk-copy a neighboring
  development/test folder into it. Import only reviewed source changes.
- Never commit manuscripts, personal project folders, private Scrivener templates,
  videos, diagnostics/configuration, local paths, secrets, `node_modules`, build
  outputs or internal audit notes. Regression fixtures must be synthetic.
- Inspect the working tree before changes; preserve other people's work. No
  destructive `reset`, `clean`, forced push, or silent deletion of existing files.
- Executables belong in GitHub Release assets, not Git. Keep the test workspace
  separate. Old local artifacts can be backed up outside this checkout, not
  silently included in a new release.

## Non-negotiable macOS compatibility contract

**The default distributed PubCraft.app is Universal 2: native Intel x86_64 AND
Apple Silicon arm64, with no Rosetta requirement.**

This applies to EVERY executable, not just the launcher:

1. `PubCraft.app/Contents/MacOS/pubcraft-launcher` (native AppKit / CFBundleExecutable);
2. `pubcraft` (C CLI);
3. `pubcraftgui` (Go GUI);
4. `pubcraft-html2pdf` (WebKit helper);
5. any additional Mach-O helper added later.

- `make all` is a native development build. **Never publish it as the macOS
  release.** `make app` / `make pkg-macos` explicitly rebuild both architectures.
- Required macOS downloads: DMG containing the convenient app, `.app.zip`, PKG
  and portable archive. Every one must contain the same verified Universal code.
- Run `make verify-macos-release` and the two native macOS test jobs against the
  SAME uploaded artifacts. Building/cross-compiling ARM64 on Intel is not a native
  ARM test. A Rosetta run is not a native Apple Silicon test.
- Do not weaken, skip, add a bypass for, or remove the architecture/signature
  guards to make a release green. If a dependency loses an architecture, stop
  publication and fix the build or request an explicit product-policy change.
- macOS minimum: 13.0 on both architectures. `.go-version` pins the release
  toolchain. A compiler upgrade must not silently raise the deployment minimum;
  the verifier checks each slice. A policy change needs documentation/review.
- Keep the native launcher in `src/pubcraft-launcher.m`. A system prebuilt
  AppleScript applet can silently raise the ARM minimum OS (even if the app's
  Info.plist says 13.0); do not substitute it for the compiled native launcher.
- Assemble ALL resources first, sign each executable and finally the bundle,
  then verify signatures. Ad-hoc signing is NOT Developer ID signing/notarization.
  Do not describe an unsigned installer or non-notarized app as Apple-approved.

## Versions and immutable releases

- `VERSION` is the marketing/source version; `BUILD_NUMBER` is a positive,
  monotonically increasing Apple/installer build number. Increment both for a new
  release. Do not reuse an already published tag or overwrite its assets.
- Keep C, Go, NSIS and every localized welcome-version marker synchronized.
  `python3 scripts/check_release_version.py` must pass.
- Preserve historical changelog/validation/release documents. Add a new release
  document; update live README links in English and Italian.
- Source, all platform binaries and the release tag must correspond to the same
  clean commit. Go build info must show that revision and `vcs.modified=false`.
  Do not mistake copied timestamps for source provenance.

## Validation and publication gates

- Use isolated temporary HOME/configuration and synthetic content for tests.
- Run uncached Go tests (`-race -count=1`), CLI tests, `go vet`, Python release-guard
  tests and the browser suite when relevant. The Go cache does not track the C
  executable used by tests.
- Native Linux/Windows build/package tests AND macOS Intel/ARM package tests must
  pass. A green compile job with a skipped/failed native test job is not approval.
- Verify fonts, notices, package contents, architecture, signatures and SHA-256
  hashes. Cross-build alone does not certify installation or GUI/PDF behavior.
- Publish from a draft only after checks and upload verification succeed. Use
  `set -e` / checked subprocesses: a failed validation MUST prevent publication.
  Drafts may not resolve via the REST releases-by-tag endpoint; inspect the draft
  by release ID or `gh release view`, not by publishing it to make lookup work.
- Verify remote asset digests against local files before/after publication.
- Report exactly which tests ran and which were skipped. Never equate XML parsing
  with full EPUB compliance, EPUBCheck samples with retailer certification, or
  architecture inspection with actual native execution.
