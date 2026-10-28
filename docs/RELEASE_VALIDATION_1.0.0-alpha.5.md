# Release validation — 1.0.0-alpha.5

This release fixes Corkboard card dragging (Pointer Events instead of HTML5 drag-and-drop) and adds non-destructive synchronization between the book folder and `book.yaml`. Historical alpha.4 assets and validation remain unchanged. See [RELEASING.md](RELEASING.md) for the persistent release procedure and [AGENTS.md](../AGENTS.md) for agent instructions.

## Architecture contract

The distributed app, DMG, PKG, app ZIP and portable Mac tools contain the same Universal 2 code. Both `x86_64` and `arm64` are required in:

- native `pubcraft-launcher` / CFBundleExecutable;
- C `pubcraft`;
- Go `pubcraftgui`;
- WebKit `pubcraft-html2pdf`.

Minimum OS metadata is checked per slice and in the app: macOS 13.0 on both CPUs.
The launcher is compiled from our native AppKit source, not copied from a system applet.

## Behaviour covered by this release

- Corkboard drag: drops accepted on cards, gaps, empty board space and below the last
  row; insertion indicator; auto-scroll; `Esc` cancellation; correct order with
  status filters or search active; no accidental chapter opening.
- Folder synchronization: new `.md` appended, renames re-linked by front-matter `id`,
  missing files flagged but never dropped automatically, orphan entries removed only
  on explicit request, invalid `book.yaml` never regenerated, serialized writes.

## Automated release guards

- `python3 scripts/check_release_version.py` verifies synchronized version markers (`1.0.0-alpha.5`, build `5`) across C, Go, NSIS, HTML and all 10 localized UI strings.
- Uncached Go unit and race tests (`go -C src/pubcraftgui test -race -count=1 ./...`), including the new `src/pubcraftgui/vault_sync_test.go`.
- Go static analysis (`go -C src/pubcraftgui vet ./...`).
- CLI test suite (`bash tests/test_cli.sh build/pubcraft`).
- Release guard unittest suite (`python3 -m unittest discover -s tests -p test_release_guards.py`).
- JavaScript syntax checks (`node --check` on `app.js` and `watch.js`).
- Complete browser end-to-end regression suite, including real mouse drags in `tests/test_chapter_reorder.cjs` and the new `tests/test_vault_sync.cjs`.
- Universal 2 verification (`make verify-macos-release`) and package content checks (`tests/check_packages.py`).
- Native execution of the same artifact set on GitHub-hosted Intel (`macos-15-intel`) and Apple Silicon (`macos-15`) runners, plus native Linux and Windows build/package jobs in `release-build.yml`.
