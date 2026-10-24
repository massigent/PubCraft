# PubCraft

**Write your book in Markdown. Keep your files local. Export EPUB and print interiors from one project.**

PubCraft is a free, open-source book-writing and publishing tool for authors who want a practical writing workspace and portable manuscript files—not another format they cannot easily take with them. It includes a local desktop-style editor, a command-line interface, EPUB export, print-layout tools, and import support for Vellum projects.

> **Alpha software:** use copies of your manuscripts and keep backups. Review every import and exported file before publication. Print and EPUB output is not certified by Amazon KDP or any other distributor.

[![CI](https://github.com/massigent/PubCraft/actions/workflows/ci.yml/badge.svg)](https://github.com/massigent/PubCraft/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![PubCraft writing a sample chapter in Markdown beside its styled book preview](docs/media/pubcraft-editor.png)

**Download now:** [Windows installer (.exe)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-Setup-1.0.0-alpha.1.exe) · [macOS app (.dmg)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1.dmg) · [Linux AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1-x86_64.AppImage) · [All downloads](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.1)

[Report a problem](https://github.com/massigent/PubCraft/issues/new?template=bug_report.md) · [Contribute](CONTRIBUTING.md) · [Italiano](README.it.md)

## Why PubCraft?

You may have a book in Vellum and want to keep editing it without depending on Vellum forever. You may want to write on Windows or Linux, keep your manuscript in Markdown, or simply have a local tool that takes you from draft to ebook and print layout. PubCraft is being built for those needs.

- **Keep your work portable:** chapters and book metadata are stored as ordinary Markdown files and `book.yaml` in a project folder.
- **Work locally:** PubCraft runs on your computer and does not require a PubCraft cloud account. You choose where your project files live. If you connect an external AI client through MCP, that service has its own privacy practices.
- **Continue from a Vellum project:** import supported `.vellum` projects and convert their content into an editable Markdown project. Conversion is not guaranteed to preserve every detail; inspect the result.
- **Prepare ebook and print editions:** export EPUB and generate print-layout output with trim-size and bleed settings. PDF creation depends on an available supported renderer.
- **Use the workflow that suits you:** write in the built-in editor, use the CLI, or work with the Markdown files in another editor.
- **Free and open source:** PubCraft's code is MIT-licensed. Included fonts and dependencies have their own licenses; see [third-party notices](third-party-licenses/) and [font licenses](fonts/README.md).

## Downloads

Choose the download for your computer:

- **Windows 64-bit:** [Download installer (.exe)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-Setup-1.0.0-alpha.1.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1-windows-x64.zip)
- **macOS Intel:** [Download app disk image (.dmg)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1.dmg) · [Installer package (.pkg)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1.pkg)
- **Linux 64-bit:** [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1-x86_64.AppImage) · [Debian/Ubuntu package (.deb)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1-linux-amd64.deb) · [Portable archive (.tar.gz)](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/PubCraft-1.0.0-alpha.1-linux-x64.tar.gz)

[All release notes and files](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.1) · [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.1/checksums-sha256.txt). GitHub's separate “Source code” archives contain code, not ready-to-run apps.

This alpha targets macOS x86_64, Windows x64, and Linux x64. Apple Silicon users may need Rosetta; native arm64 macOS is not included. macOS may warn that the app is from an unidentified developer because this build is not notarized. Alpha packages and platform testing may differ; read the release notes. Linux PDF export may require Chromium or another supported renderer.

## Getting started

A PubCraft project is a folder containing `book.yaml` and Markdown chapters, with optional `cover/` and `images/` folders.

### Open the graphical editor

```sh
pubcraftgui ./my-book
```

The editor opens in your browser but is served by a local process on your computer. To open the live Watch view for a project:

```sh
pubcraftgui --watch ./my-book
```

### Create and export from the command line

```sh
pubcraft init ./my-book "My Book"
pubcraft build ./my-book ./my-book.epub --kindle
pubcraft build-print ./my-book ./print-output --trim 6x9 --pages 200 --pdf
```

PDF export requires a supported renderer. Always inspect the resulting EPUB/PDF with the preview tools provided by your distributor before uploading or printing.

### Import a Vellum project

```sh
pubcraft export-md "./My Book.vellum" "./My Book-markdown"
```

Open the resulting folder in PubCraft or a Markdown editor. Import support aims to make your content easier to continue working with—not to reproduce every Vellum layout option exactly. Check chapter order, images, metadata, and formatting after conversion. You do not need to keep using Vellum to edit the resulting Markdown project.

The GUI also supports importing `.docx` manuscripts.

## Build from source

Requirements: `make`, a C compiler, and Go 1.23 or newer. Optional browser tests need Node.js, the test dependencies, and a Chromium/Brave browser with remote debugging enabled.

```sh
make
make check
```

See [tests/README.md](tests/README.md) for browser tests and platform packaging. Packaging an operating-system installer requires the corresponding platform tools; `make dist` is not a universal cross-compiler.

## Alpha limitations

PubCraft is actively being tested. Import conversion, pagination, typography, and PDF appearance can vary with source files, fonts, operating system, and renderer. A successful export does not certify a file for KDP, IngramSpark, or another service. Keep the original project and a backup, verify the generated files independently, and report issues with a small sample that contains no private manuscript text.

## Feedback and collaboration

Feedback, bug reports, documentation improvements, translations, testing, and code contributions are welcome—even if you are not a developer. Start with [GitHub Issues](https://github.com/massigent/PubCraft/issues) or read [CONTRIBUTING.md](CONTRIBUTING.md). Please include your PubCraft version, operating system, exact steps, and expected/actual result. **Do not post private manuscripts, unpublished writing, or copyrighted covers.**

PubCraft began as a tool for the author's own writing workflow. It is shared freely in the hope that other writers find it useful and help shape what it becomes.

## License

PubCraft source code is distributed under the [MIT License](LICENSE). Fonts and other third-party components retain their respective licenses; see the notices shipped with the project.
