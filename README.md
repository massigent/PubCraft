# PubCraft

**Write your book in Markdown. Keep your files local. Export EPUB and print interiors from one project.**

PubCraft is a free, open-source writing and publishing tool with a local browser-based editor, a C command-line compiler, manuscript import, EPUB export and print previews. Your work remains ordinary Markdown and `book.yaml`.

> **Alpha software:** keep backups and use copies of your manuscripts. Review every import/export. A successful build does not certify Amazon KDP, IngramSpark or other distributor compliance.

[![CI](https://github.com/massigent/PubCraft/actions/workflows/ci.yml/badge.svg)](https://github.com/massigent/PubCraft/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Latest: 1.0.0-alpha.3 — Universal Mac app, native on Intel and Apple Silicon.**

### Download

| Platform | Downloads |
| --- | --- |
| **macOS Universal (Intel + Apple Silicon)** | **[PubCraft.app ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos-universal.app.zip)** · [DMG app](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.dmg) · [PKG installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.pkg) · [Portable tools](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos.tar.gz) |
| Windows x64 | [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-Setup-1.0.0-alpha.3.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-windows-x64.zip) |
| Linux x64 | [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-x64.tar.gz) |

[Release notes and all files](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.3) · [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/checksums-sha256.txt) · [Italiano](README.it.md) · [Report a problem](https://github.com/massigent/PubCraft/issues/new?template=bug_report.md)

**Mac installation:** extract the app ZIP and drag `PubCraft.app` into Applications, or use the DMG as usual. **macOS 13 Ventura or newer; no Rosetta required.** The launcher, CLI, GUI and PDF helper all include both CPU architectures. Quit the old app before replacing it.

> **macOS Security Notice (Gatekeeper / "Move to Trash"):**  
> PubCraft is a free open-source project with an ad-hoc code signature and is not enrolled in the paid Apple Developer Program ($99/year) or notarized by Apple. On first launch, macOS (especially Sonoma and Sequoia) may block the application with a false-alarm dialog: *"PubCraft is damaged and cannot be opened. You should move it to the Trash."*  
> **The application is intact and not damaged.** To clear the quarantine flag set by macOS on downloaded files and open it normally:
> 1. Drag `PubCraft.app` to your `/Applications` folder.
> 2. Open **Terminal** and run:
>    ```sh
>    xattr -cr /Applications/PubCraft.app
>    ```
> 3. Launch PubCraft normally with a double-click. *(Alternatively, if shown the standard Gatekeeper prompt, you can open System Settings → Privacy & Security and click "Open Anyway").*

The app is ad-hoc signed, not Developer ID signed/notarized; PKG and Windows installer are not publisher-signed. Use the official release and verify its checksums. GitHub's “Source code” downloads are not ready-to-run apps.

![PubCraft editor and styled book preview](docs/media/pubcraft-editor.png)

## What's new?

**Alpha.3:** a genuinely Universal 2 Mac app and portable tools; direct `.app.zip`; native launcher with a controlled minimum OS; automated architecture, signature, permission, version and source-provenance checks. The same artifact set is tested on Intel and Apple Silicon. Persistent [agent instructions](AGENTS.md) and a [release playbook](docs/RELEASING.md) prevent a future Intel-only release from being mistaken for Universal.

**Included from alpha.2:** Corkboard/index cards, synopses, writing status, selection/session word counts, command palette, Scrivener import and improved DOCX/ODT/Markdown fidelity. Safer project selection, versioned saves, protected metadata/covers/trash, XML/UTF-8 sanitization, nested images/lists and stable publication identifiers.

See the [alpha.3 notes](RELEASE_NOTES_1.0.0-alpha.3.md), [full alpha.2 changes](RELEASE_NOTES_1.0.0-alpha.2.md), [changelog](CHANGELOG.md) and [validation record](docs/RELEASE_VALIDATION_1.0.0-alpha.3.md).

## Why PubCraft?

- **Portable projects:** Markdown chapters, YAML metadata and local images, editable in PubCraft or another editor.
- **Local-first:** no PubCraft account or cloud backend. External AI clients connected through MCP have their own privacy practices.
- **Supported imports:** Vellum, Scrivener, DOCX, ODT, Markdown and PDF. Conversion is not an exact reproduction of every source feature; inspect the result.
- **Writing and publishing:** chapter organization, notes, manuscript preview, typography controls and explicitly generated EPUB/PDF publication previews.
- **Print:** HTML/PDF with trim and bleed controls. PDF generation requires an available supported renderer; pagination and appearance can vary.
- **Free/open source:** MIT code; fonts/dependencies retain separate licenses.

## Getting started

Launch the app and choose **New Book** or open a project folder. A book contains `book.yaml`, `.md`/`.markdown` chapters and optional `cover/` and `images/` folders. Keep the original and a separate backup.

```sh
# GUI and live Watch
pubcraftgui ./my-book
pubcraftgui --watch ./my-book

# CLI
pubcraft init ./my-book "My Book"
pubcraft build ./my-book ./my-book.epub --kindle
pubcraft build-print ./my-book ./print-output --trim 6x9 --pages 200 --pdf

# Vellum conversion
pubcraft export-md "./My Book.vellum" "./My Book-markdown"
```

The browser UI is served by a local process, not a hosted service. Other manuscript formats can be imported through the GUI. The GUI binary also exposes stdio MCP through `pubcraftgui --mcp`.

## Build and test

Source minimum: `make`, a C compiler, Go 1.23+ and Python. Release builds use the pinned `.go-version`; Universal Mac packaging needs macOS SDK/signing tools and Python 3.12+.

```sh
make                       # native development tools, NOT a Mac release
make check                 # CLI, uncached Go race tests and release-guard tests
go -C src/pubcraftgui vet ./...
make pkg-macos             # Universal app + ZIP/DMG/PKG/portable tools
make verify-macos-release  # all formats, both slices, signatures and clean source
```

Read [AGENTS.md](AGENTS.md) and [docs/RELEASING.md](docs/RELEASING.md) before changing or publishing releases. Never reuse old executables or substitute native `make all` outputs for the Universal app. Native Intel/Apple Silicon, Linux and Windows checks live in [Release builds](.github/workflows/release-build.yml). See [tests/README.md](tests/README.md) for browser tests. Cross-compilation alone is not native execution or installation testing.

## Limits and feedback

Import conversion, typography and pagination depend on input, fonts, OS and renderer; EPUB readers may override typography. The WebKit helper produces a continuous snapshot, not certified multi-page KDP layout. Unsupported YAML update forms fail without changing the project. Linux ARM packages are not included.

[Issues](https://github.com/massigent/PubCraft/issues), documentation, translations and testing contributions are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Include version, OS and reproduction steps. **Never attach private manuscripts or copyrighted covers. Review diagnostic reports before sharing: automatic redaction is not a guarantee.**

## License

Code: [MIT](LICENSE). See [font notices](fonts/README.md) and [third-party licenses](third-party-licenses/) for resources with separate licenses.
