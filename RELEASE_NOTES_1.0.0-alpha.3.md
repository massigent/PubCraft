# PubCraft 1.0.0-alpha.3 — Universal Mac app

**One convenient `PubCraft.app`, native on Intel and Apple Silicon. No Rosetta required.**

This pre-release adds Universal 2 macOS distribution and durable release safeguards. It includes all writing, import, project-safety and EPUB improvements from [alpha.2](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.2): Scrivener import, chapter cards/synopses/status, command palette, safer saves/imports, valid XML/UTF-8 output, nested images/lists and stable publication identifiers.

## Download / Scarica

### macOS — Intel + Apple Silicon, macOS 13 Ventura or newer

- **[Download PubCraft.app ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos-universal.app.zip)** — extract the archive and move `PubCraft.app` to Applications.
- **[DMG](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.dmg)** — open and drag the same Universal app to Applications.
- [PKG installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.pkg)
- [Portable CLI/GUI/helper archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos.tar.gz)

### Other platforms

- **Windows x64:** [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-Setup-1.0.0-alpha.3.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-windows-x64.zip)
- **Linux x64:** [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-x64.tar.gz)
- [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/checksums-sha256.txt)

Quit the old app before replacing it and keep a manuscript backup. Existing projects do not need a chapter-ID migration. Historical tags/releases are left unchanged, so the previous Intel-only build remains identifiable as alpha.2.

## What's changed

### Native Apple Silicon, without sacrificing Intel

- All four app components contain **both `arm64` and `x86_64`**: the native launcher, C CLI, Go GUI and WebKit PDF helper. The portable Mac executables are Universal too.
- A small native AppKit launcher replaces the prebuilt system AppleScript applet. It preserves ordinary launch and opening a dragged project/manuscript, passes paths as task arguments rather than shell text, and is compiled for the same minimum OS as the rest of the app.
- This avoids a hidden compatibility trap: an ARM slice in a system applet can require a newer macOS even when the app's Info.plist declares 13.0.
- App copies are staged with public-readable/executable permissions before DMG/PKG/ZIP creation, rather than inheriting owner-only modes from a development volume.
- The `.app.zip` provides the app directly; DMG, PKG and portable archive remain available.

### Safeguards for every future release

- **[AGENTS.md](https://github.com/massigent/PubCraft/blob/v1.0.0-alpha.3/AGENTS.md)** and **[release playbook](https://github.com/massigent/PubCraft/blob/v1.0.0-alpha.3/docs/RELEASING.md)** record the public/test boundary, Universal requirement, versioning, signing order, validation and publication gates.
- `make app` / `make pkg-macos` always rebuild both CPU architectures. A native development `make all` is explicitly not a Mac release build.
- Automated checks reject missing/incorrect architecture slices in any app executable or portable Mac binary, invalid signatures, an unexpectedly raised minimum OS, inconsistent executable bytes between formats, inaccessible distributed permissions and stale source provenance.
- One Universal artifact set is built and then downloaded by **two native jobs**, Intel and Apple Silicon. They test the exact same bytes, including real app launch, GUI runtime architecture, CLI EPUB/archive operations and native PDF-helper execution. Cross-compilation is not reported as native testing.
- `VERSION`, `BUILD_NUMBER` and live C/Go/installer/localized UI markers are checked for consistency. The release Go toolchain is pinned; the numeric build number is checked against prior release history when available.
- Historical release notes, tags and assets are not overwritten. Windows/Linux builds retain their native package checks and are rebuilt for the new release version.

## Validation and limits

See the [alpha.3 validation record](https://github.com/massigent/PubCraft/blob/v1.0.0-alpha.3/docs/RELEASE_VALIDATION_1.0.0-alpha.3.md) and attached machine-readable validation manifest for actual checks and workflow evidence.

- **Still alpha software:** use copies/backups and inspect imports and final exports.
- **Universal does not mean notarized.** The app is ad-hoc signed, not Developer ID signed/notarized. PKG and Windows installer are not publisher-signed; OS security warnings may appear.
- Minimum macOS is 13.0. Native tests run on the named CI OS versions, not every older OS/device model. Universal code removes the Rosetta requirement, not all possible OS/renderer differences.
- The WebKit helper's test is a continuous PDF snapshot, not certification of multi-page print pagination. Distributor-specific EPUB/PDF approval still requires the distributor's previewer.
- Linux ARM packages are not added by this update.

Report problems with a minimal synthetic project at [GitHub Issues](https://github.com/massigent/PubCraft/issues). Review diagnostic reports before sharing; never attach private manuscripts or copyrighted covers.

---

## Italiano

**Ora c'è una sola `PubCraft.app` nativa sia per Mac Intel sia per Apple Silicon, senza Rosetta.** Richiede macOS 13 Ventura o successivo.

- Scarica il **ZIP dell'app**, estrailo e sposta `PubCraft.app` in Applicazioni; oppure apri il **DMG** e trascina l'app come prima.
- Launcher, compilatore C, interfaccia Go e helper PDF contengono entrambi i tipi di processore: non è soltanto l'icona/launcher a essere universale.
- Il launcher nativo mantiene avvio e apertura dei file trascinati, evitando che un applet di sistema imponga di nascosto un macOS più recente sulla parte ARM.
- `AGENTS.md`, guida di rilascio e controlli automatici spiegano ai futuri agenti come lavorare nel repository pulito, costruire l'app universale, verificare le architetture, firmare dopo l'assemblaggio e pubblicare soltanto artefatti verificati dello stesso commit.
- I medesimi pacchetti vengono controllati su Mac Intel e Apple Silicon; sono controllati anche permessi, versioni e coerenza fra DMG, PKG, ZIP e archivio portatile.
- Sono incluse tutte le aggiunte/correzioni della seconda alfa; i pacchetti Windows/Linux sono ricostruiti con la nuova versione. Le release precedenti rimangono intatte.

Resta una **versione alfa non notarizzata**: conserva backup e verifica i tuoi file prima della pubblicazione. La compatibilità nativa Apple Silicon non equivale a certificazione Apple o KDP.
