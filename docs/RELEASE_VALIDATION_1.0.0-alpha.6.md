# Release validation — 1.0.0-alpha.6 (validated pre-release)

**Status: validated alpha pre-release, not GA.** The alpha.6 sources (commit `34f309ef147201119041c92b89e4daf51d82b2e2`) passed the full local cycle and all 5 native release-build/package jobs. Tag, draft release and final asset upload/publication are the next cycle step and are not yet done; this record describes results on that exact source revision. Historical validation records (alpha.2–alpha.5) are unchanged.

## Source revision and provenance

- Source: branch `release/1.0.0-alpha.6`, commit `34f309e` — clean tree at commit time.
- Native release builds/packages: workflow run [45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137), `head_sha` = `34f309e…`, all 5 jobs succeeded:
  - "Build one Universal 2 macOS artifact set" (`macos-15`) — 1m56s;
  - "Linux x64 build, test and packages" (`ubuntu-22.04`) — 2m01s;
  - "Windows x64 build, smoke tests and packages" — 1m54s;
  - "Test Universal packages natively (amd64)" (`macos-15-intel`) — 3m31s;
  - "Test Universal packages natively (arm64)" (`macos-15`) — 1m59s.
- The native macOS jobs download the uploaded `release-macos-universal` artifact, verify the SAME package bytes across ZIP/DMG/PKG/portable formats with `tests/check_packages.py` and `scripts/check_macos_universal.py` (`--expected-source $GITHUB_SHA`, native architecture `--smoke`), run `tests/test_cli.sh`, uncached `go test -race -count=1` and `go vet` on the native binaries, and emit a per-slice native proof report (`macos-native-amd64.json` / `macos-native-arm64.json`, uploaded as `macos-native-proof-*` artifacts).
- `main` CI on the same revision: run [45499319137](https://github.com/massigent/PubCraft/actions/runs/45499319137), "Build & Test (Ubuntu Linux)" succeeded.

## Local cycle (development host, Linux)

Run while preparing the commit, on the working tree that then became `34f309e` (these were working-tree runs before the commit, not runs from a fresh clean checkout):

- `make check` — C core checks (`check_core`, including the TOC-policy table: 9 declared section types plus the `colophon` alias, 0 failures), the CLI test suite (`tests/test_cli.sh`) and the **uncached** Go test suite (`go test -race -count=1` over `src/pubcraftgui`). All Go tests passed.
- `go vet ./...` — clean (no findings).
- PDF end-to-end render tests (`go test -race -count=1 -run '.*E2E'`): real print-TOC page-number E2E and Chromium-with-bleed geometry E2E passed.
- Python release-guard tests (`python3 -m unittest discover -s tests -p test_release_guards.py`): 8/8 OK.
- Browser suite: all 19 consolidated runners passed, including the shared cleanup helper (`tests/cleanup.cjs`) and the vault-sync suite, with no residual processes.
- `python3 scripts/check_release_version.py` — passed (VERSION, BUILD_NUMBER and all C/Go/NSIS/welcome markers synchronized).

## Skipped tests (exact list, all environmental, none failing)

- `TestImportScrivenerRealTemplate`, `TestInspectTemplateClean` — require the external Scrivener template fixture (`PUBCRAFT_SCRIVENER_TEMPLATE_FIXTURE` not set in the isolated environment).
- `TestPrintGeometryRejectsWebKitSnapshotE2E` — macOS-only WebKit snapshot-geometry E2E, skipped on non-macOS hosts by design.

Compiler `snprintf` format-truncation warnings in the C sources are known non-fatal build warnings, not skipped tests and not failures.

## Final native gates and provenance

- All 5 release-build jobs verified the same source revision and package bytes as described above; the two native macOS proof reports record the per-slice native execution results against the uploaded Universal artifact set.
- The SHA-256 checksum manifest for every downloadable package accompanies the release assets together with this validation record; the manifest never includes its own digest.
- Digests of final release assets are verified remotely against local files before/after publication; no hash of this record's own commit is derived or guessed — the record cites the existing CI `head_sha` (`34f309e…`).

## Amendment — Linux static-ELF portability fix (supersedes the Linux preflight above)

A pre-publication review blocked alpha.6: in the Linux job of run [45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137), `make all CC=musl-gcc` built the Go GUI with CGO enabled, producing a dynamically linked ELF (`PT_INTERP /lib/ld-musl-x86_64.so.1`, musl `DT_NEEDED`) that cannot run on stock Debian/Ubuntu hosts.

- **Fix:** the release workflow now applies `CGO_ENABLED=0` **only** to the Linux `make all` release command (the `go test -race` step keeps its default environment because `-race` requires CGO); the CLI stays built with `-static`.
- **New fail-closed guard:** `tests/check_linux_static_elf.py` (checked `readelf` subprocess calls, assert-based, no masking pipes) rejects any Linux release executable with `PT_INTERP` or `DT_NEEDED` and verifies the ELF machine type; it runs before any smoke test or packaging, on the freshly built binaries and again on the unpacked portable archive.
- **Reclassification:** the Linux job of run [45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137) (and any artifacts built from `51f48a6` or earlier with the dynamic GUI) is a **preflight**, not final validation. The uploaded artifacts of that run are invalidated for publication.
- **Authoritative record:** final validation for alpha.6 is the first full workflow run on the fixed revision; its uploaded SHA-256 manifest of final assets is authoritative. This record does **not** claim that the earlier packages are portable.
- The macOS architecture/signature guards, minimum-OS policy and native macOS jobs were not modified.

## Not yet done for alpha.6 (next cycle steps)

- Annotated tag `v1.0.0-alpha.6`, draft release creation with assets, remote digest verification and publication — not started as of this record.
- A fresh full workflow run on the fixed revision (with the Linux static-ELF guard green) is required before any draft/publication step.

## Honest limitations

- Validation results in alpha.2–alpha.5 records were obtained on those sources, not on the alpha.6 code.
- No claim of GA/stable, distributor (KDP/IngramSpark) compliance, Developer ID signing or Apple notarization is made. Ad-hoc macOS signing and unsigned Windows installers are documented. No RPM package is provided; Linux packages are tested on a real Ubuntu 22.04 CI host.

## Architecture contract (unchanged)

The Universal 2 macOS contract, architecture/signature guards and all release policies in `AGENTS.md` and `docs/RELEASING.md` remain fully in force and have not been modified by this record.
