# PubCraft 1.0.0-alpha.6 — stability update: section-type TOC policy, safe PDF publication

**Status: pre-release (alpha).** This is the 1.0.0-alpha.6 source; it is alpha software, not a stable or GA release. Pre-release downloads for this version are published as release assets on the [alpha.6 release page](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.6). The previous published version remains available as a fallback: [alpha.5](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.5).

Alpha.6 focuses on reinforced stability: one consistent section-type policy across the printed TOC and the EPUB nav/NCX, full CLI/GUI/`book.yaml` loading and validation parity for the table-of-contents boolean options, a PDF page-number cache that cannot silently reuse numbers from a different rendering environment, and a publication guard that protects the previously published PDF when a new build fails.

## What's changed

### Section-type/TOC policy

- Chapter `type:` values in `book.yaml` are section-type strings (including the `colophon` alias of `copyright`), not numeric indices; there are no numeric indices to be out of range. A single policy table (`TOC_POLICY` in `src/pubcraft.c`, mirrored in `src/pubcraftgui/booktypes.go`) now drives the printed TOC, EPUB nav.xhtml and toc.ncx consistently: dedication, epigraph and copyright/colophon are front matter excluded from the printed index but included in the ebook navigation, subheadings are opt-in (`toc.subheads`, default false).
- Unknown types are reported as a warning and paginated as normal chapters; contents are preserved and the value in `book.yaml` is never rewritten.

### CLI / GUI / `book.yaml` parity

- The `toc:` boolean options (`subheads`, `pagenumbers`) are loaded and validated the same way by the CLI, the GUI and `book.yaml`, with the same defaults and the same boolean syntax. Parity tests (`src/pubcraftgui/yaml_parity_test.go`) and YAML fixtures (`tests/fixtures/yaml/07..09`) lock this in.

### PDF index-page cache keyed on the environment

- The printed index measures page numbers on the real PDF and caches them. The cache is now tied to the rendering environment (renderer and page setup), so changing the environment invalidates it instead of reusing stale numbers in the table of contents.

### Publication guard protects the previous PDF

- Publishing a print PDF is now guarded: if the candidate PDF fails validation (for example the page-size check), the previously published PDF stays untouched and the build fails with an explicit message.

### Preview warns on changed sources

- The publication preview shows a warning when the Markdown/`book.yaml` sources changed since the last render, so you are never looking at an outdated preview unknowingly.

### Test hygiene and build tooling

- The browser test suite was consolidated into 19 runners with a shared cleanup helper (`tests/cleanup.cjs`), removing duplicated setup and making failures easier to reproduce.
- The AppImage workflow pins appimagetool to release 1.9.1, so Linux image builds use a fixed tool version.

## Validation performed

The alpha.6 source (commit `34f309e`) passed the full release-cycle validation, including the native multi-platform package jobs:

- Local cycle: `make check` with uncached `-race -count=1` Go tests, `go vet`, C core checks (TOC-policy table), the CLI test suite, Python release-guard tests, PDF end-to-end render tests, the full 19-runner browser suite and `check_release_version.py`.
- Native release builds and package tests (workflow run [45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137), all 5 jobs green on this exact source): Linux x64 native build/tests/packages (Ubuntu 22.04), Windows x64 native build/smoke tests/packages, one Universal 2 macOS artifact set, and two dependent native macOS jobs (Intel `macos-15-intel` + Apple Silicon `macos-15`) that verify the SAME package bytes, both CPU slices, signatures and source revision, and run native GUI/CLI/PDF checks.
- `main` CI passed on the same revision (run [45499319137](https://github.com/massigent/PubCraft/actions/runs/45499319137)).

Details: [release validation record](docs/RELEASE_VALIDATION_1.0.0-alpha.6.md). SHA-256 checksums for all release assets accompany the release.

## Honest status / limits

- This is alpha software: **not** a general-availability ("stable") release. Keep backups and work on copies; review every import/export.
- The macOS app is ad-hoc signed; it is **not** Developer ID signed and not notarized by Apple, and macOS Gatekeeper may show a false "damaged" warning on first launch (see README for the `xattr` workaround). The PKG and the Windows installer are not publisher-signed.
- EPUBCheck sample checks do not equal full EPUB compliance, and passing checks do not certify Amazon KDP, IngramSpark or other distributors.
- A few tests are skipped for environmental reasons only (the external Scrivener template fixture is not set, and the macOS-only WebKit geometry E2E is skipped on non-macOS hosts by design).
- Linux packages are tested on a real Ubuntu 22.04 host; no RPM package is provided.

---

## Italiano

# PubCraft 1.0.0-alpha.6 — aggiornamento di stabilità: politica dei tipi di sezione negli indici, pubblicazione PDF sicura

**Stato: pre-release (alfa).** Questo è il sorgente 1.0.0-alpha.6; è software alfa, non una release stabile o GA. I download pre-release di questa versione sono pubblicati come asset della [pagina release alfa 6](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.6). La versione pubblicata precedente resta disponibile come ripiego: [alfa 5](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.5).

L'alfa 6 punta a una stabilità rafforzata: un'unica politica coerente per i tipi di sezione nell'indice di stampa e nella navigazione EPUB (nav/NCX), piena parità di caricamento e validazione dei booleani dell'indice fra CLI, GUI e `book.yaml`, una cache dei numeri di pagina PDF che non può riusare numeri di un ambiente di rendering diverso, e una guardia di pubblicazione che protegge il PDF già pubblicato quando un nuovo build fallisce.

## Cosa cambia

- **Politica tipi di sezione/indice:** i valori `type:` in `book.yaml` sono stringhe di tipo di sezione (incluso l'alias `colophon` di `copyright`), non indici numerici: non esistono riferimenti numerici fuori intervallo da correggere. Un'unica tabella di politica (`TOC_POLICY` in `src/pubcraft.c`, rispecchiata in `src/pubcraftgui/booktypes.go`) governa in modo coerente l'indice di stampa, nav.xhtml e toc.ncx dell'EPUB: dedication, epigraph e copyright/colophon sono frontmatter esclusi dall'indice di stampa ma presenti nella navigazione EPUB, i sottocapi sono opt-in (`toc.subheads`, default falso).
- I tipi sconosciuti producono un avviso e sono impaginati come capitoli normali; i contenuti restano e il valore in `book.yaml` non viene mai riscritto.
- **Parità CLI/GUI/YAML:** le opzioni dell'indice accettate da CLI, GUI e `book.yaml` coincidono, con gli stessi valori predefiniti, e sono coperte da test dedicati.
- **Cache delle pagine dell'indice PDF legata all'ambiente:** cambiare renderer o formato invalida la cache invece di riusare numeri di pagina obsoleti nell'indice.
- **Guardia di pubblicazione:** se il PDF candidato fallisce la validazione (per esempio il controllo del formato pagina), il PDF pubblicato in precedenza resta intatto e la build si interrompe con un messaggio esplicito.
- **Avvisi di anteprima:** l'anteprima di pubblicazione segnala quando i sorgenti sono cambiati dall'ultimo render.
- **Igiene dei test e tooling:** suite browser consolidata in 19 runner con helper di pulizia condiviso; appimagetool fissato alla versione 1.9.1 nel workflow Linux.

## Validazione eseguita

Il sorgente alfa 6 (commit `34f309e`) ha superato l'intero ciclo di validazione della release, inclusi i job nativi multi-piattaforma dei pacchetti:

- Ciclo locale: `make check` con test Go non memorizzati in cache (`-race -count=1`), `go vet`, verifiche core C (tabella di politica dell'indice), suite CLI, test Python delle guardie di release, test E2E di rendering PDF, l'intera suite browser in 19 runner e `check_release_version.py`.
- Build e test nativi dei pacchetti (run del workflow [45497549137](https://github.com/massigent/PubCraft/actions/runs/45497549137), tutti e 5 i job verdi su questo sorgente esatto): Linux x64 nativo (Ubuntu 22.04), Windows x64 nativo con smoke test e pacchetti, un unico set di artefatti macOS Universal 2, e due job macOS nativi dipendenti (Intel `macos-15-intel` + Apple Silicon `macos-15`) che verificano gli STESSI byte dei pacchetti, entrambe le architetture, le firme e il commit sorgente, eseguendo controlli nativi GUI/CLI/PDF.
- CI su `main` superata sulla stessa revisione (run [45499319137](https://github.com/massigent/PubCraft/actions/runs/45499319137)).

Dettagli: [record di validazione](docs/RELEASE_VALIDATION_1.0.0-alpha.6.md). I checksum SHA-256 di tutti gli asset accompagnano la release.

## Stato onesto / limiti

- Software alfa: **non** è una release di disponibilità generale ("stabile"). Conserva backup e lavora su copie; controlla ogni importazione/esportazione.
- L'app macOS ha firma ad-hoc: **non** è firmata Developer ID e non è notarizzata Apple, e Gatekeeper può mostrare il falso avviso "danneggiata" al primo avvio (vedi il README per il comando `xattr`). PKG e installer Windows non sono firmati da un editore verificato.
- I controlli campione con EPUBCheck non equivalgono alla piena conformità EPUB, e il superamento non certifica Amazon KDP, IngramSpark o altri distributori.
- Alcuni test sono saltati solo per motivi ambientali (fixture esterna di template Scrivener non impostata; il test E2E WebKit della geometria, solo macOS, è saltato su host non macOS per progetto).
- I pacchetti Linux sono testati su un host reale Ubuntu 22.04; non è fornito un pacchetto RPM.
