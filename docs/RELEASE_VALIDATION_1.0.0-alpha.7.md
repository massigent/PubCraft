# Release validation — 1.0.0-alpha.7 (validated pre-release)

**Status: validated alpha pre-release, not GA.** The alpha.7 sources (branch `release/1.0.0-alpha.7`, commit `466cdd3a635ef692203aa1b8fdf55a366ecb1b10`, clean tree at commit time) passed the full local cycle and all 5 native release-build/package jobs. Tag, draft release and final asset upload/publication are the next cycle step and are not yet done; this record describes results on that exact source revision. Historical validation records (alpha.2–alpha.6) are unchanged.

## Source revision and provenance

- Source: branch `release/1.0.0-alpha.7`, commit `466cdd3` — clean tree at commit time.
- Native release builds/packages: workflow run [45652250381](https://github.com/massigent/PubCraft/actions/runs/45652250381), `head_sha` = `466cdd3…`, all 5 jobs succeeded:
  - "Build one Universal 2 macOS artifact set" (`macos-15`) — 2m01s;
  - "Linux x64 build, test and packages" (`ubuntu-22.04`) — 2m15s;
  - "Windows x64 build, smoke tests and packages" (`windows-latest`) — 1m17s;
  - "Test Universal packages natively (amd64)" (`macos-15-intel`) — 5m27s;
  - "Test Universal packages natively (arm64)" (`macos-15`) — 2m00s.
- The native macOS jobs download the uploaded `release-macos-universal` artifact, verify the SAME package bytes across ZIP/DMG/PKG/portable formats with `tests/check_packages.py` and `scripts/check_macos_universal.py` (`--expected-source $GITHUB_SHA`, native architecture `--smoke`), run `tests/test_cli.sh`, uncached `go test -race -count=1` and `go vet` on the native binaries, and emit a per-slice native proof report (`macos-native-amd64.json` / `macos-native-arm64.json`, uploaded as `macos-native-proof-*` artifacts).
- `main` does not yet contain the alpha.7 version bump: the fast-forward merge of this branch into `main` (and the `main` CI run on the same revision) is part of the publication step and has not been performed yet.

## Local cycle (development host, Linux x64)

Run while preparing the commit, on the working tree that then became `466cdd3`:

- `gcc -std=c11 -Wall -Wextra -O2 -fsyntax-only` on the C sources — clean (the pre-existing `-Wformat-truncation` notes in `copy_fonts` remain known non-fatal warnings).
- C internal checks (`tests/check_core.c`): the three suites — TOC policy, `roundtrip-guards`, `loader-unescape` — all at 0 failures.
- CLI shell suite (`tests/test_cli.sh`, 15 tests) — passed.
- `go vet ./...` — clean on Linux and Windows.
- Uncached Go suite (`go test -race -count=1` over `src/pubcraftgui`) — passed, with the PDF end-to-end tests SKIPPED because Chromium is absent on that host (`TestRenderPrintCoreTocNumbersE2E`, `TestPrintGeometryRejectsWebKitSnapshotE2E`, `TestPrintGeometryAcceptsChromiumWithBleedE2E`).
- Cross-compilation for linux/amd64, windows/amd64, darwin/amd64 and darwin/arm64 — compile checks only.
- Python release-guard tests (`python3 -m unittest discover -s tests -p test_release_guards.py`) — OK.
- `python3 scripts/check_release_version.py` — PASS (VERSION, BUILD_NUMBER and all C/Go/NSIS/welcome markers synchronized).
- `node --check` on `web/static/app.js` and `web/static/watch.js` — clean.
- Manual end-to-end round-trip smoke on a real vault: escaped lines render as plain paragraphs in the EPUB.

## Skipped tests (exact list, all environmental, none failing)

- The full browser suite — requires a Chromium instance with remote debugging on port 9222, which is absent on the preparation host.
- The three PDF end-to-end Go tests (`TestRenderPrintCoreTocNumbersE2E`, `TestPrintGeometryRejectsWebKitSnapshotE2E`, `TestPrintGeometryAcceptsChromiumWithBleedE2E`) — same reason (no Chromium).
- Native Windows/macOS execution — performed by CI only, not on the Linux preparation host.
- `tests/check_packages.py` on locally built artifacts — the packages are built in CI; the workflow runs the same check on the real packages.
- `TestImportScrivenerRealTemplate`, `TestInspectTemplateClean` — require the external Scrivener template fixture (`PUBCRAFT_SCRIVENER_TEMPLATE_FIXTURE` not set in the isolated environment).

## Final native gates and provenance

- All 5 release-build jobs verified the same source revision (`466cdd3…`) and the same Universal package bytes; the two native macOS proof reports record the per-slice native execution results against the uploaded Universal artifact set.
- The SHA-256 checksum manifest for every downloadable package accompanies the release assets together with this validation record; the manifest never includes its own digest.
- Digests of final release assets must be verified remotely against local files before/after publication; no hash is derived or guessed — this record cites the existing CI `head_sha` and the run link above.
- The Universal 2 macOS contract, architecture/signature guards and minimum-OS policy in `AGENTS.md` and `docs/RELEASING.md` were not modified by this round.

## Not yet done for alpha.7 (next cycle steps)

- Fast-forward/merge of `release/1.0.0-alpha.7` into `main` and the `main` CI run on that revision.
- Annotated tag `v1.0.0-alpha.7`, draft release creation with the workflow artifacts of run 45652250381, remote digest verification and publication.

## Honest limitations

- Validation results in alpha.2–alpha.6 records were obtained on those sources, not on the alpha.7 code.
- No claim of GA/stable, distributor (KDP/IngramSpark) compliance, Developer ID signing or Apple notarization is made. Ad-hoc macOS signing and unsigned Windows installers are documented. No RPM package is provided; Linux packages are tested on a real Ubuntu 22.04 CI host.
- The browser suite and the PDF end-to-end tests were not run on the preparation host (environment), and native Windows/macOS behaviour is asserted through the CI native jobs only.
- The macOS launcher `openFiles:` ordering and `cmd.exe` quoting for exotic paths remain flagged for native verification on macOS/Windows hosts (no known regression).
