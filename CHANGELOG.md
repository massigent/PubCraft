# Changelog

PubCraft is pre-release software. Entries describe changes relative to the previous public tag, not a promise of distributor certification or perfect conversion of every manuscript.

## [1.0.0-alpha.7]

### Fixed — CLI (`src/pubcraft.c`)

- **EPUB `dcterms:modified`:** the OPF metadata timestamp is now the actual
  UTC build time instead of a hard-coded date. EPUB 3 requires the date and
  time of the last modification; previously every book of every build claimed
  the same fixed value.
- **Bounded KDP gutter convergence:** the printed-PDF path (`build-print`/
  `export-print` with `--pdf`) recalculates margins when the real page count
  crosses a KDP gutter band. The old implementation recalculated with no
  bound, so a book sitting on a band boundary could oscillate and rebuild
  forever. There is now an explicit recalculation budget; when it is exhausted
  the build stops with a clear warning suggesting `--pages`.
- **Faithful `export-md` round-trip:** paragraph lines that merely *begin*
  with Markdown structure (`>`, `- `/`* `/`+ `, numbered items, `|`, fenced
  code, headings, images) used to be re-interpreted as structure on re-import,
  silently turning prose into block quotes, lists or code blocks. The writer
  now escapes all such lines and the loader treats an escaped line as plain
  text (no re-parsing). Old `\#`/`\!` files remain compatible. Covered by a
  new end-to-end loader test in `tests/check_core.c` (`loader-unescape`)
  plus unit checks for the escape predicate (`roundtrip-guards`).
- **Front matter delimiters:** a closing front-matter line made of more than
  three dashes (e.g. `----`) no longer corrupts the chapter body (the leftover
  dash used to turn the first body line into a list item). The closing line
  is now recognised as a run of dashes with optional surrounding whitespace.
- **Chapter-name validation:** `is_safe_chapter_file` rejected any file name
  containing `..` anywhere (e.g. `Part..1.md`). Separators and leading dots
  are already rejected, so no path component can ever be `..`; legitimate
  double-dot names are accepted again, with the safety chain documented
  inline and unit-tested.
- **`cmd_list` on Windows:** the archive listing now uses PowerShell/.NET
  (`System.IO.Compression.ZipFile`) like the rest of the Windows code path,
  instead of shelling out to a Unix `unzip` that does not exist there.
- **Boolean `bleed:` option:** `book.yaml` `bleed:` now uses the same tolerant
  boolean grammar as `toc.subheads`/`toc.pagenumbers` (`true/false`, `yes/no`,
  `on/off`, `1/0`), and an invalid value fails the build with an explicit
  error instead of silently defaulting to `false`.
- **`--option=value` print flags:** `--bleed`, `--no-bleed`, `--pdf` and
  `--no-toc-pagenumbers` now accept the `--flag=value` form, consistently with
  `--trim`/`--pages`/`--toc-pagemap`.
- **Hardened binary-plist parser:** the `0xA`/`0xD` (array/dictionary) node
  allocations are now checked before writing; a malformed third-party
  archive degrades to a null node instead of risking a crash.

### Fixed — GUI (`src/pubcraftgui`)

- **Process existence on Windows:** `processExists` relied on
  `Process.Signal(syscall.Signal(0))`, which always fails on Windows. Among
  other things this broke the single-instance launcher (a live instance was
  always considered dead) and made the preview-directory janitor delete the
  preview folders of *other running instances*. Windows now probes with
  `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` via the standard library,
  treating access-denied as "exists" (`process_windows.go`, build-tagged;
  the Unix implementation is unchanged in `process_unix.go` with a unit test).
- **Project alignment for chapter reordering:** `apiReorder` now honours the
  `X-PubCraft-Vault` header (already sent by the web client for every
  mutation) and rejects with 409 a reorder request that belongs to another
  open project, like `apiSave` and `apiBuild` already did. Clients without the
  header keep working.
- **CSRF coverage:** the security middleware now also requires POST + JSON on
  `/api/publication-preview`, the recents/workspace mutation endpoints, and
  guards the only GET endpoint with side effects (`/api/watch/pdf?rebuild=1`)
  against cross-site embedding via the `Sec-Fetch-Site` header. Requests
  without the header (non-browser tools) remain allowed; the limitation is
  documented inline. New tests cover both behaviours
  (`endpoint_hardening_test.go`).
- **Recent-projects hygiene:** the recents filter no longer excludes paths via
  hard-coded test-specific patterns (`/var/folders/`, `pubcraft-tests-`, …)
  which could also exclude real user vaults; it now skips only vaults inside
  the system temporary directory plus the explicit `PUBCRAFT_TESTING` gate.
- **Build timeout:** CLI invocations now get a 5-minute budget (configurable
  via `runToolTimeout`) instead of a fixed 120 s, so large illustrated books
  on slow disks no longer fail mid-build.

### Validation of this round

All changes passed, on Linux x64: `gcc -Wall -Wextra -fsyntax-only`, the C
internal checks (`tests/check_core.c`: TOC policy, round-trip guards,
loader unescape), the CLI shell suite (`tests/test_cli.sh`), `go vet` on
Linux and Windows, the uncached Go suite (`go test -race -count=1`),
cross-compilation for linux/amd64, windows/amd64, darwin/amd64 and
darwin/arm64 (compile checks only), the Python release-guard tests and
`check_release_version.py`, plus a manual end-to-end round-trip smoke on a
real vault. Not executed here by design: the full browser suite (requires a
Chromium remote-debugging instance), native Windows/macOS runs, and
`check_packages.py` (release artifacts). The macOS launcher `openFiles:`
ordering and `cmd.exe` quoting for exotic paths remain flagged for native
verification on macOS/Windows.

## [1.0.0-alpha.6]

### Changed — improved stability

- **Section-type/TOC policy:** chapter `type:` values (strings, including the
  `colophon` alias) now drive one consistent TOC inclusion table across the
  printed index, EPUB nav.xhtml and toc.ncx; unknown types produce a warning
  and are paginated as normal chapters, and `book.yaml` is never rewritten.
  CLI and GUI share the same parser and validation (see
  `src/pubcraftgui/booktypes.go`, `pagemap.go`, `toc_policy_test.go`).
- **CLI/GUI/YAML parity:** the `toc:` boolean options (`subheads`,
  `pagenumbers`) are loaded and validated identically in the CLI, the GUI and
  the `book.yaml` schema, with the same defaults and tests
  (`src/pubcraftgui/yaml_parity_test.go`, fixtures `tests/fixtures/yaml/07..09`).
- **PDF index-page cache identity:** the page-number cache used by the printed
  index is keyed on the rendering environment, so a changed renderer or page
  setup cannot reuse stale page numbers from a previous build.
- **Publication guard:** a failed candidate PDF build keeps the previously
  published PDF untouched (`src/pubcraftgui/printguard.go`).
- **Preview freshness:** the publication preview warns when source files have
  changed since the last render.
- **Test hygiene:** the browser suite now uses 19 consolidated runners with a
  shared cleanup helper (`tests/cleanup.cjs`).
- **AppImage tooling:** appimagetool is pinned to release 1.9.1 in the build
  workflow.
- **Linux static-ELF portability fix:** the Linux release build now compiles
  the GUI with `CGO_ENABLED=0` (scoped to the `make all` release command, not
  to the `go test -race` step, which requires CGO). Both Linux executables
  (CLI and GUI) are fully static ELF with no ELF interpreter and no
  `DT_NEEDED` dependencies; a fail-closed `tests/check_linux_static_elf.py`
  guard (checked `readelf` calls) verifies this before any smoke test or
  packaging, in the build job and again on the unpacked portable archive.
  Linux CLI and GUI run without any musl loader or additional runtime; the
  optional `zip`/`unzip` helpers and a browser runtime for PDF export remain
  external requirements.
- **Validation record amendment:** the earlier Linux CI run on `34f309e` is
  reclassified as a preflight; the final asset manifest of the published
  release is authoritative.

### Honest status

This is a validated pre-release (alpha), not a GA/stable release and not a claim of distributor
compliance. The alpha.6 sources (commit `34f309e`) passed the full local cycle (`make check`
with uncached Go tests `-race -count=1`, `go vet`, C core checks, CLI suite, Python
release-guard tests, PDF E2E render tests, the 19-runner browser suite and
`check_release_version.py`) and all 5 native release-build/package jobs (run
[45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137): Linux x64 on
Ubuntu 22.04, Windows x64 native, one Universal 2 macOS artifact set and two dependent native
macOS jobs on Intel and Apple Silicon verifying the same package bytes, both slices,
signatures and source revision). `main` CI passed on the same revision
([45499319137](https://github.com/massigent/PubCraft/actions/runs/45499319137)).
A few tests are skipped for environmental reasons only (external Scrivener template fixture,
and the macOS-only WebKit geometry E2E test, skipped on non-macOS hosts by design).

The macOS app is ad-hoc signed, not Developer ID signed/notarized; PKG and Windows installers
are not publisher-signed. No RPM package is provided. Details:
[bilingual alpha.6 notes](RELEASE_NOTES_1.0.0-alpha.6.md) and the
[validation record](docs/RELEASE_VALIDATION_1.0.0-alpha.6.md).

## [1.0.0-alpha.5]

### Fixed

- **Lavagna a Schede:** chapter cards can now be dragged anywhere on the board.
  The previous HTML5 drag-and-drop only accepted a drop released exactly on top
  of another card, so dropping on empty board space or in the gap between cards
  silently did nothing and the card snapped back. Dragging now uses Pointer
  Events: the card follows the cursor, a vertical indicator shows the insertion
  point, drops work on cards, gaps, empty space and below the last row, the
  board auto-scrolls near the edges, `Esc` cancels, and filtered/searched views
  keep the correct saved order.
- **Regression test honesty:** `tests/test_chapter_reorder.cjs` no longer calls
  `window.reorderChaptersByDrop()` directly; it performs real mouse drags and
  fails against the previous implementation.

### Added

- **Folder ↔ `book.yaml` synchronization (non-destructive).** `.md` files added
  to the book folder outside PubCraft are appended to `book.yaml` automatically
  (title from front-matter or first heading, natural sort), both when a book is
  opened and while the app is running; the chapter list refreshes without a
  reload. Files renamed outside PubCraft are re-linked in place by their
  front-matter `id`, so no duplicate chapter is created. Chapters whose file
  disappeared stay in the list, flagged as `missing` in the sidebar and
  Corkboard, and can be dropped from `book.yaml` only on explicit request
  (`/api/forget-chapter`, no file is deleted).
- New browser suite `tests/test_vault_sync.cjs` and Go tests
  (`src/pubcraftgui/vault_sync_test.go`) covering append, rename by `id`,
  missing-file marking, reserved/hidden/nested file exclusion, idempotency and
  orphan-entry removal.

Details: [bilingual alpha.5 notes](RELEASE_NOTES_1.0.0-alpha.5.md).

## [1.0.0-alpha.4]

- Re-introduced and polished chapter movement controls across all views.
- Dedicated up/down arrow buttons in the sidebar chapter list with disabled states for boundaries.
- New previous/next arrow buttons on Corkboard cards (Lavagna a Schede) for instant repositioning.
- Smooth HTML5 drag-and-drop reordering on both sidebar items and Corkboard cards, with insertion edge indicators (`drag-over-top`/`drag-over-bottom` in sidebar, `drag-over-left`/`drag-over-right` in Corkboard).
- Drag completion guard prevents accidental chapter opening and editor switching during Corkboard card repositioning.
- Immediate UI synchronization and server persistence to `book.yaml` via `/api/reorder`.
- Complete localization across all 10 supported languages for chapter movement controls.
- New browser regression test suite (`tests/test_chapter_reorder.cjs`) validating movement in sidebar, corkboard and disk persistence.

Details: [bilingual alpha.4 notes](RELEASE_NOTES_1.0.0-alpha.4.md).

## [1.0.0-alpha.3]

- Universal 2 `PubCraft.app` and portable Mac executables: native Intel and Apple
  Silicon, without Rosetta; macOS 13.0 minimum on both architectures.
- Native AppKit launcher replaces a system prebuilt AppleScript applet with a
  potentially newer ARM deployment target; ordinary launch and drag/open remain.
- Direct `.app.zip`, plus DMG/PKG/portable tools; public staged app permissions.
- The same artifact set is tested natively on Intel and Apple Silicon, including
  real app startup, GUI runtime architecture, CLI and WebKit helper execution.
- Automated guards for every executable's CPU slices, signatures, minimum OS,
  version markers, source provenance, cross-format hashes and public permissions.
- Persistent `AGENTS.md` and `docs/RELEASING.md`; pinned release Go toolchain,
  increasing `BUILD_NUMBER` and UTF-8-safe version checks on Windows.
- Windows/Linux packages rebuilt for the new version. Historical releases remain
  unchanged; all alpha.2 writing/import/safety improvements are included.

Details: [bilingual alpha.3 notes](RELEASE_NOTES_1.0.0-alpha.3.md).

## [1.0.0-alpha.2]

Full details, downloads and Italian summary: [release notes](RELEASE_NOTES_1.0.0-alpha.2.md).

### Added

- Scrivener project/template/ZIP import, Binder hierarchy and supported RTF/image conversion.
- Chapter Corkboard/index cards, synopses, writing-status controls and editorial section templates.
- Chapter/book/selection/session word-count controls and a command palette.
- Native Linux/Windows release-build and package smoke-test automation; clean, temporary packaging stages.
- Explicit release version reporting, package checksums and a documented validation record.

### Improved

- Writing workspace menus, sidebar, responsive controls, themes and localized guidance.
- DOCX/ODT whitespace, break and revision handling; Markdown fenced-code and image imports.
- Notebook saves and note/chapter navigation, GUI/MCP active-project coordination and diagnostics.
- EPUB typography presets, language handling, nested images, image manifest/resource selection and list rendering.
- Publication-preview source-revision tracking and stable book/publication identifiers.

### Fixed — data safety

- Existing invalid YAML is no longer silently regenerated; selection/import rejects it without replacing metadata.
- Selecting existing chapters preserves the full book, chapter IDs, titles, types and order; new files are merged without removing chapters.
- Imports do not reuse pre-existing destination folders; repeated deletions do not overwrite prior trash entries.
- Custom cover-chapter content is preserved; duplicate image copies are avoided when appropriate.
- Missing/stale base versions no longer silently overwrite existing notes; project changes respect unsaved edits and stale-project contexts.
- Project containment checks cover relative paths and symbolic-link escapes in Go and C.

### Fixed — content and output

- Accented/non-ASCII title capitalization; consistent Markdown and uppercase note extensions.
- CRLF/BOM/frontmatter handling, YAML quoting, synopsis escapes and word-count consistency.
- Deleted DOCX text/field instructions, ODT spacing and chapter splits inside Markdown code fences.
- Invalid XML controls/UTF-8 in EPUB output; nested image paths, zero-byte directory copies and unmanifested orphan images.
- Two-/four-space nested lists, mixed list types, lazy continuations and paragraphs following a blank line.
- New UUID format, legacy publication-ID policy and safe persistence of absent/empty/null/quoted UUIDs.
- Quoted YAML UUID keys, supported literal block identifiers and explicit non-destructive rejection of unsupported update forms.
- Broken rename/palette/Corkboard actions and invalid typography choices.

### Fixed — platform and packaging

- Windows native path resolution and EPUB ZIP creation without Unix shell tools; built-in PowerShell/.NET archive extraction.
- NSIS installation/upgrade writes the current executables under their final names.
- macOS app signatures are applied after all binaries/resources are assembled and then verified.
- Bounded/validated cover copies, proper reveal-error reporting, configuration-directory isolation, Unicode-safe note previews and preview-cache limits.
- Optional external Scrivener tests use disposable fixture copies instead of developer-specific paths.

## [1.0.0-alpha.1]

Initial public alpha: local Markdown editor, CLI, Vellum/DOCX import, EPUB/print exports, notebook, recent-book launcher, local previews, MCP bridge, font resources and platform packages.

Historical notes: [1.0.0-alpha.1](RELEASE_NOTES_1.0.0-alpha.1.md).

[1.0.0-alpha.4]: https://github.com/massigent/PubCraft/compare/v1.0.0-alpha.3...v1.0.0-alpha.4
[1.0.0-alpha.3]: https://github.com/massigent/PubCraft/compare/v1.0.0-alpha.2...v1.0.0-alpha.3
[1.0.0-alpha.2]: https://github.com/massigent/PubCraft/compare/v1.0.0-alpha.1...v1.0.0-alpha.2
[1.0.0-alpha.1]: https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.1
