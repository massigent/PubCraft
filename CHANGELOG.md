# Changelog

PubCraft is pre-release software. Entries describe changes relative to the previous public tag, not a promise of distributor certification or perfect conversion of every manuscript.

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

[1.0.0-alpha.3]: https://github.com/massigent/PubCraft/compare/v1.0.0-alpha.2...v1.0.0-alpha.3
[1.0.0-alpha.2]: https://github.com/massigent/PubCraft/compare/v1.0.0-alpha.1...v1.0.0-alpha.2
[1.0.0-alpha.1]: https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.1
