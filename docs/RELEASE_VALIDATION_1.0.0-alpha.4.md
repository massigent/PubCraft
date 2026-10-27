# Release validation — 1.0.0-alpha.4

This release re-introduces chapter reordering arrows in the sidebar and extends them to the Corkboard (Lavagna a Schede), alongside drag-and-drop enhancements. Historical alpha.3 assets/validation remain unchanged. See [RELEASING.md](RELEASING.md) for the persistent release procedure and [AGENTS.md](../AGENTS.md) for agent instructions.

## Architecture contract

The distributed app, DMG, PKG, app ZIP and portable Mac tools contain the same Universal 2 code. Both `x86_64` and `arm64` are required in:

- native `pubcraft-launcher` / CFBundleExecutable;
- C `pubcraft`;
- Go `pubcraftgui`;
- WebKit `pubcraft-html2pdf`.

Minimum OS metadata is checked per slice and in the app: macOS 13.0 on both CPUs.
The launcher is compiled from our native AppKit source, not copied from a system applet.

## Automated release guards

- `python3 scripts/check_release_version.py` verifies synchronized version markers (`1.0.0-alpha.4`, build `4`) across C, Go, NSIS, HTML and all 10 localized UI strings.
- Complete browser end-to-end regression test suite, including new `tests/test_chapter_reorder.cjs`.
- Uncached Go unit and race tests (`go -C src/pubcraftgui test -race -count=1 ./...`).
- Go static analysis (`go -C src/pubcraftgui vet ./...`).
- CLI test suite (`bash tests/test_cli.sh build/pubcraft`).
- Release guard unittest suite (`python3 -m unittest discover -s tests -p test_release_guards.py`).
- Universal 2 verification (`make verify-macos-release`).
