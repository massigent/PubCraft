# Release validation — 1.0.0-alpha.6 (local candidate cycle completed, publication pending)

**Status: LOCAL VALIDATION RECORD — alpha.6 is still NOT a published release.** The full local build/test cycle for the alpha.6 sources has passed on the development machine. No official alpha.6 package, tag, draft release or published asset exists yet; the newest published release remains 1.0.0-alpha.5. Historical validation records (alpha.2–alpha.5) are unchanged.

## Local cycle passed on branch `release/1.0.0-alpha.6`

All commands below are the generic release-cycle commands; they were run from a clean checkout of this branch with isolated temporary test workspaces and synthetic content. Every step exited successfully.

- `make check` — C core checks (`check_core`, including the TOC-policy table: 9 declared section types plus the `colophon` alias, 0 failures), the CLI test suite (`tests/test_cli.sh`) and the **uncached** Go test suite (`go test -race -count=1` over `src/pubcraftgui`). All Go tests passed.
- `go vet ./...` — clean (no findings).
- PDF end-to-end render tests (`go test -race -count=1 -run '.*E2E'`): real print-TOC page-number E2E and Chromium-with-bleed geometry E2E passed.
- Python release-guard tests (`python3 -m unittest discover -s tests -p test_release_guards.py`): 8/8 OK.
- Browser suite: all 19 consolidated runners passed, including the new shared cleanup helper (`tests/cleanup.cjs`) and the vault-sync suite, with no residual processes.
- `python3 scripts/check_release_version.py` — passed (VERSION, BUILD_NUMBER and all C/Go/NSIS/welcome markers synchronized).

## Skipped tests (all environmental, none failing)

- Go tests that require an external Scrivener template fixture (`PUBCRAFT_SCRIVENER_TEMPLATE_FIXTURE` not set in the isolated environment): `TestImportScrivenerRealTemplate`, `TestInspectTemplateClean`.
- Go E2E tests that require an optional local renderer/helper not present in the isolated environment; in the dedicated PDF E2E run, the macOS-only WebKit snapshot-geometry test is skipped on non-macOS hosts by design (`TestPrintGeometryRejectsWebKitSnapshotE2E`).
- The compiler reports known non-fatal `snprintf` format-truncation warnings in the C sources; they are pre-existing and do not fail the build.

## Not yet done for alpha.6 (must pass before publication)

- Native Linux and Windows build/package tests (installers, portable archives).
- Universal 2 macOS build (`make pkg-macos`), `make verify-macos-release`, and the two native macOS test jobs (Intel + Apple Silicon) against the same uploaded artifacts. Native macOS package tests are pending for this version.
- `tests/check_packages.py`, SHA-256 manifest, draft release inspection and remote digest verification.
- Publication of tag, release and assets — not started.

## Corrected statement about builds

An alpha.6 GUI build was produced locally for testing during this cycle, so it is not true that no alpha.6 build exists at all. However, **no official alpha.6 package, tag, release draft or published asset exists**; nothing has been published and README download links intentionally still point to alpha.5.

## Honest limitations

- Validation results in alpha.2–alpha.5 records were obtained on those sources, not on the alpha.6 code. Earlier test runs in this session were performed on the alpha.5 working tree before the version bump; the runs recorded here are the first complete cycle on the alpha.6 versioned sources.
- Published alpha.5 Windows packages passed native Windows CI; the Windows packages built locally during this session (pre-bump) were only cross-build-checked. Native CI package jobs for alpha.6 are pending.
- No claim of GA/stable, distributor (KDP/IngramSpark) compliance, Developer ID signing or Apple notarization is made. No RPM package is promised.

## Architecture contract (unchanged)

The Universal 2 macOS contract, architecture/signature guards and all release policies in `AGENTS.md` and `docs/RELEASING.md` remain fully in force and have not been modified by this record.
