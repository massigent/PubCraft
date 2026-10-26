# PubCraft 1.0.0-alpha.2

A substantial writing-workspace, import, EPUB and project-safety update over **1.0.0-alpha.1**. This remains a **pre-release**: keep backups and use a copy of your manuscript when evaluating it.

## Download / Scarica

- **Windows x64:** [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-Setup-1.0.0-alpha.2.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-windows-x64.zip)
- **macOS Intel:** [App disk image](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2.dmg) · [Installer package](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2.pkg) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-macos.tar.gz)
- **Linux x64:** [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-x86_64.AppImage) · [Debian/Ubuntu package](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/PubCraft-1.0.0-alpha.2-linux-x64.tar.gz)
- [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.2/checksums-sha256.txt)

Use these release assets, not GitHub's separate source-code archives. Quit the old application before replacing it. Project files remain ordinary Markdown and YAML; no migration of existing chapter IDs is required.

## New and improved writing workspace

- **Corkboard/index cards** for reviewing chapters, synopses and writing status, with chapter reordering and creation.
- **Chapter synopses and status** in the sidebar and document controls; in-place chapter and book renaming.
- **Word counts** for chapters, the whole book, selected text and writing-session progress; consistent counts between the sidebar and print estimates.
- **Command palette** for finding chapters, notes and commands; corrected command actions and navigation.
- Reorganized book, export, typography and settings menus, a collapsible sidebar, improved responsive controls and theme handling.
- Improvements to the existing notebook, focus-writing, welcome/recent-books, tutorial and localized interfaces, including safer saves when switching notes or projects.
- Added editorial section templates for copyright/colophon, dedication, epigraph, prologue, epilogue, preface, acknowledgments and about-the-author sections.

## Imports and manuscript fidelity

- **Scrivener import:** `.scriv`, `.scrivx`, `.scrivtemplate` and zipped projects, Binder hierarchy, supported RTF formatting, chapter order and image conversion. Scrivener formatting markers are cleaned from imported text.
- DOCX handling now preserves supported line breaks/tabs and excludes deleted revision text and field instructions from the manuscript body.
- ODT handling improves explicit spaces, tabs and line breaks.
- Markdown import recognizes fenced code blocks instead of turning `# comments` inside code into spurious chapters; local image references are copied/resolved for the imported project.
- Imported titles beginning with accented/non-ASCII characters are no longer damaged by byte-based capitalization.
- `.md` and `.markdown` chapter extensions, including uppercase variants, are handled consistently. Uppercase `.MD` notes can be opened and deleted.
- Vellum conversion and other import formats remain conversions, not exact reproductions: inspect chapters, images and formatting before relying on the result.

## Protecting your work

- An existing malformed `book.yaml` is **not silently regenerated**. The application reports the problem without replacing the user's metadata.
- Selecting files in an existing book no longer removes unselected chapters or replaces their IDs, titles, types or order. Newly selected files are appended; an unchanged selection does not rewrite the YAML.
- Imports allocate a new numbered destination instead of overwriting a pre-existing folder without `book.yaml`.
- Repeated deletion of chapters/covers preserves previous copies in the local trash. Using an EPUB cover preserves custom cover-chapter content and avoids unnecessary duplicate images.
- Chapter and note saves use content versions to detect conflicts; an existing note requires a base version unless an explicit force operation is requested.
- Project changes are guarded when clients have unsaved edits. Project-context checks and serialized mutations prevent stale-tab/cross-book operations, including unsafe MCP-driven changes.
- Chapter/note paths and symbolic links are checked for containment; the Go application and C CLI reject references escaping the project.
- Fixed YAML quoting, frontmatter detection and escaped synopsis text. First-line BOM/CRLF handling no longer causes metadata to appear in the published manuscript.

## EPUB, typography and print

- Strict UTF-8/XML output policy covers text, metadata, captions and generated attributes: invalid byte sequences are replaced, forbidden XML controls removed, and a warning is emitted rather than silently producing malformed XML.
- Referenced images in nested folders retain their paths. Directory entries are no longer copied as zero-byte files, and unused images are not packaged outside the OPF manifest.
- Correct list nesting for two-/four-space indentation, ordered/unordered combinations, lazy continuations and paragraphs following a blank line.
- Stable, canonical UUIDs for new books; existing internal IDs are preserved. Legacy publication identifiers are emitted truthfully, without an invalid `urn:uuid:` prefix.
- UUID persistence handles absent/empty/null values, quoted keys, safe legacy duplicates, comments, CRLF and explicit document terminators without blind appends. Supported literal `|-` identifiers remain unchanged; unsupported update forms fail before writing.
- Additional EPUB preset coverage, corrected small-caps styling and safer language/style handling.
- More reliable publication-preview revision tracking: persisting a missing identifier no longer produces a spurious “sources changed” warning. Real EPUB/PDF previews remain explicitly generated, not rebuilt on every keystroke.
- Existing trim/bleed, print-preview and Watch workflows gain the project-safety and revision fixes described above.

## Diagnostics, MCP and release hygiene

- Diagnostics honor `PUBCRAFT_CONFIG_DIR`, use the current release version and preserve isolation in tests. File-opening/reveal failures are reported rather than always returning success.
- Cover uploads have type/size checks and streaming copies instead of unbounded whole-file reads.
- Note previews truncate at Unicode character boundaries; empty note lists are arrays, not `null`. Publication-preview artifacts have a global retention cap.
- MCP status/schema and GUI-bridge handling are aligned with overwrite/conflict rules and active-project protection.
- Release builds use a separate clean source checkout: private manuscripts, local templates, internal audit notes, generated projects, dependencies and machine-specific executables are not included in the source repository.
- macOS app resources are assembled **before** final ad-hoc signing and signature verification. Ad-hoc signing does **not** identify a verified developer or provide Apple notarization.
- Native Linux/Windows release jobs build current C and Go binaries and exercise packaged CLI/GUI startup. Windows compatibility no longer depends on Unix path resolution or a Unix `zip` command for EPUB creation.

## Validation and limits

See [release validation](docs/RELEASE_VALIDATION_1.0.0-alpha.2.md) for the exact checks and platform limits. Regression coverage includes project isolation, concurrent saves, selection/import safety, UUID stability, XML output, nested lists and browser workflows. EPUBCheck 5.4.0 is used on a representative set of synthetic EPUBs; this does **not** certify every possible manuscript or distributor-specific requirements.

- This is still **alpha software**. Use backups and inspect every import/export.
- macOS builds target **Intel/x86_64, macOS 13 Ventura or newer**; Apple Silicon also requires Rosetta. The app is ad-hoc signed, not Developer ID signed/notarized; the PKG and Windows installer are not publisher-signed.
- Linux PDF export needs a supported browser/renderer. Windows PDF/print behavior depends on the installed renderer; native build/startup checks are not exhaustive interactive testing on every supported OS.
- Native Apple Silicon and Linux ARM release packages are not included.
- The C YAML reader is a supported-subset implementation, not a complete YAML parser. Unsupported UUID update forms are rejected without altering the project rather than guessed or rewritten destructively.
- Always inspect EPUB/PDF in your distributor's previewer. No claim of Amazon KDP, IngramSpark or other retailer certification is made.

Report problems at [GitHub Issues](https://github.com/massigent/PubCraft/issues) with the version, OS, exact steps and a minimal synthetic example. Review diagnostic reports before posting: automatic redaction is not a guarantee. Never attach private manuscripts or copyrighted covers.

---

## Italiano — cosa cambia

Questa seconda alfa contiene un aggiornamento importante rispetto alla prima versione pubblica.

### Scrittura e organizzazione

- Vista a **schede/Corkboard**, sinossi e stato dei capitoli, riordino e creazione delle sezioni.
- Conteggi di parole per capitolo, libro, selezione e sessione; rinomina diretta di libro/capitoli; palette di ricerca e comandi.
- Menu riorganizzati per libro, esportazione, tipografia e impostazioni; barra laterale richiudibile e miglioramenti a temi, layout adattivo, note, guida e interfacce localizzate.
- Template editoriali per le principali sezioni iniziali e finali del libro.

### Importazioni

- Supporto Scrivener per progetti, Binder, RTF, template e archivi ZIP; pulizia dei marcatori di formattazione.
- Migliore fedeltà DOCX/ODT per spazi, tab e interruzioni; esclusione di testo eliminato e istruzioni dei campi Word.
- Blocchi di codice Markdown preservati e immagini locali risolte/copiate; titoli accentati ed estensioni Markdown/MD gestiti correttamente.

### Protezione dei manoscritti

- Nessuna rigenerazione silenziosa di un `book.yaml` esistente ma invalido.
- Selezionare un capitolo non elimina più gli altri dal libro e non cambia i loro ID/metadati.
- Importazioni in nuove cartelle numerate, cestino senza sovrascritture, copertine personalizzate preservate.
- Controlli di versione per salvataggi di capitoli/note, blocchi sui cambi progetto con modifiche pendenti e protezione da operazioni riferite al libro sbagliato.
- Controllo dei percorsi e dei collegamenti simbolici, anche nella CLI; quoting YAML, frontmatter, BOM/CRLF e sinossi corretti.

### Pubblicazione e affidabilità

- EPUB con sanitizzazione XML/UTF-8, immagini annidate e manifest coerenti, liste correttamente annidate e identificatori stabili.
- Gestione UUID sicura per chiavi quotate, valori vuoti/null e legacy; rifiuto esplicito delle forme non supportate, senza danneggiare il file.
- Miglioramenti tipografici, alle anteprime reali EPUB/PDF, alla diagnostica e al bridge MCP.
- Pacchetti ricostruiti dai sorgenti puliti e controlli nativi Linux/Windows; bundle macOS firmato ad-hoc dopo l'assemblaggio completo.

**Resta una versione alfa:** conserva backup, lavora su copie e verifica sempre i risultati con il servizio di pubblicazione scelto. La build Intel richiede macOS 13 Ventura o successivo (su Apple Silicon anche Rosetta). La firma ad-hoc macOS non è notarizzazione Apple; installer macOS/Windows non firmati da un editore verificato possono mostrare avvisi di sicurezza. I limiti e i test effettivamente eseguiti sono riportati nel documento di validazione collegato sopra.
