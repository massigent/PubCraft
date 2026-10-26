# PubCraft

**Write your book in Markdown. Keep your files local. Export EPUB and print interiors from one project.**

PubCraft is a free, open-source writing and publishing tool with a local browser-based editor, a C command-line compiler, manuscript import, EPUB export and print-layout previews. Your work stays in ordinary Markdown files and `book.yaml`, not a proprietary cloud format.

> **Alpha software:** keep backups and work on copies of your manuscripts. Inspect every import/export. A successful build does not certify Amazon KDP, IngramSpark or other distributor compliance.

[![CI](https://github.com/massigent/PubCraft/actions/workflows/ci.yml/badge.svg)](https://github.com/massigent/PubCraft/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Latest: 1.0.0-alpha.2** — [Release notes and all downloads](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.2)

| Platform | Downloads |
| --- | --- |
| Windows x64 | [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-Setup-1.0.0-alpha.2.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-windows-x64.zip) |
| macOS Intel | [DMG app](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2.dmg) · [PKG installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2.pkg) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-macos.tar.gz) |
| Linux x64 | [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-linux-x64.tar.gz) |

[SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/checksums-sha256.txt) · [Italiano](README.it.md) · [Report a problem](https://github.com/massigent/PubCraft/issues/new?template=bug_report.md) · [Contribute](CONTRIBUTING.md)

**Before installing:** quit the previous version. Apple Silicon Macs require Rosetta for this Intel build. The macOS app is ad-hoc signed, not Developer ID signed/notarized; macOS PKG and Windows installer are not publisher-signed. OS security warnings may appear. Use the official release and verify its checksums. GitHub's “Source code” archives contain code, not ready-to-run programs.

![PubCraft editor and styled book preview](docs/media/pubcraft-editor.png)

## What's new in alpha.2?

- **Writing workspace:** chapter Corkboard/index cards, synopses, writing status, session/selection word counts, command palette and reorganized controls.
- **Scrivener import:** projects, Binder structure, supported RTF, templates and zipped projects; improved DOCX, ODT and Markdown import fidelity.
- **Safer projects:** non-destructive selection/import, protection of malformed YAML, versioned saves, cross-tab/project guards, preserved cover content and trash history.
- **Better EPUB output:** UTF-8/XML sanitization, nested images, consistent manifests, valid nested lists and persistent publication identifiers.
- **More reliable previews and diagnostics:** stable source revisions, safer UUID updates, configuration isolation and MCP/GUI coordination.

Read the [full bilingual release notes](RELEASE_NOTES_1.0.0-alpha.2.md), [changelog](CHANGELOG.md) and [validation record](docs/RELEASE_VALIDATION_1.0.0-alpha.2.md) for the detailed changes, tests and limits.

## Why PubCraft?

- **Portable projects:** plain Markdown chapters, YAML metadata and local image folders. Edit them in PubCraft or another editor.
- **Local-first:** no PubCraft account or cloud backend. External AI clients connected through MCP have their own privacy practices.
- **Import supported manuscripts:** Vellum, Scrivener, DOCX, ODT, Markdown and PDF. Conversion is not a perfect reproduction of every source feature; review the result.
- **One writing workflow:** chapter organization, notes, live manuscript preview, typography controls and explicitly generated EPUB/PDF publication previews.
- **Ebook and print:** EPUB plus print HTML/PDF with trim and bleed settings. PDF generation needs an available supported renderer; final pagination and appearance can vary.
- **Free and open source:** code under MIT; fonts and dependencies retain their own licenses.

## Getting started

Launch the installed app, choose **New Book** or open a project folder. A project contains `book.yaml`, chapter `.md`/`.markdown` files and optional `cover/` and `images/` folders. Keep your original manuscript and a separate backup.

### GUI and Watch

```sh
pubcraftgui ./my-book
pubcraftgui --watch ./my-book
```

The interface runs in your browser, served by a local process. It is not a hosted web service. The native GUI binary also supports the stdio MCP entry point, `pubcraftgui --mcp`; see [Contributing](CONTRIBUTING.md) before enabling external clients.

### CLI

```sh
pubcraft init ./my-book "My Book"
pubcraft build ./my-book ./my-book.epub --kindle
pubcraft build-print ./my-book ./print-output --trim 6x9 --pages 200 --pdf
```

### Vellum conversion

```sh
pubcraft export-md "./My Book.vellum" "./My Book-markdown"
```

Open the converted folder in PubCraft or another Markdown editor. Check chapter order, images and formatting. Other supported manuscript formats can be imported through the GUI.

## Build and test

Requirements: `make`, a C compiler, Go 1.23+; optional browser tests need Node.js and a dedicated Chromium/Brave debugging session.

```sh
make
make check
go -C src/pubcraftgui test -race -count=1 ./...
```

The explicit `-count=1` avoids cached Go test results when only the external C compiler binary changed. See [tests/README.md](tests/README.md) for browser tests and [release validation](docs/RELEASE_VALIDATION_1.0.0-alpha.2.md) for packaging checks.

`make` updates standalone binaries, **not an existing `.app` bundle**. On macOS, `make pkg-macos` assembles and ad-hoc signs the app and creates the DMG/PKG/portable archive. This is not Apple notarization. Native Linux/Windows release builds are defined in [Release builds](.github/workflows/release-build.yml); `make dist` is not a universal cross-compiler.

## Feedback and limitations

Import conversion, typography and pagination vary with source, fonts, OS and renderer. EPUB readers may override typography. The supported YAML subset is deliberately restricted for safe project updates. Native Apple Silicon and Linux ARM packages are not provided in this release.

[Issues](https://github.com/massigent/PubCraft/issues), documentation, translations and testing contributions are welcome. Include the version, OS, reproduction steps and expected/actual result. **Never attach private manuscripts, unpublished writing or copyrighted covers. Review diagnostic reports before posting: redaction can miss sensitive information.**

## License

PubCraft code is [MIT-licensed](LICENSE). See [font notices](fonts/README.md) and [third-party licenses](third-party-licenses/) for resources with separate licenses.
